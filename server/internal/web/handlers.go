package web

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/renderer/html"

	"boussole/internal/ai"
	"boussole/internal/store"
)

//go:embed templates/*.html static
var templatesFS embed.FS

type Server struct {
	store       *store.Store
	homeTmpl    *template.Template
	listTmpl    *template.Template
	detTmpl     *template.Template
	mapTmpl     *template.Template
	chatTmpl    *template.Template
	chatPubTmpl *template.Template
	resTmpl     *template.Template
	liensTmpl   *template.Template
	guideTmpl   *template.Template
	mapboxTok   string
	guideURL    string
	md          goldmark.Markdown
	chatSvc     *ai.Service
	sessions    *ai.SessionStore
	pubSessions *ai.SessionStore
	chatReady   bool
	log         *slog.Logger

	// guideCtx stocke, par session chat (cookie boussole_sid), la dernière
	// réponse brute de l'API Guide (contenant json_patient) — mis à jour à
	// chaque passage dans le proxy, injecté dans le prompt du chat public.
	guideCtx   map[string]json.RawMessage
	guideCtxMu sync.Mutex
}

// ChatDeps regroupe les dépendances optionnelles du chat IA.
type ChatDeps struct {
	Service        *ai.Service
	Sessions       *ai.SessionStore
	PublicSessions *ai.SessionStore
	Ready          bool
}

func New(st *store.Store, mapboxToken, guideURL string, chat ChatDeps, log *slog.Logger) (*Server, error) {
	homeTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/home.html")
	if err != nil {
		return nil, err
	}
	listTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/list.html")
	if err != nil {
		return nil, err
	}
	detTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/detail.html")
	if err != nil {
		return nil, err
	}
	mapTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/map.html")
	if err != nil {
		return nil, err
	}
	chatTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/md.html", "templates/chat.html")
	if err != nil {
		return nil, err
	}
	chatPubTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/guide-widget.html", "templates/md.html", "templates/chat-public.html")
	if err != nil {
		return nil, err
	}
	resTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/ressources.html")
	if err != nil {
		return nil, err
	}
	liensTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/liens-sociaux.html")
	if err != nil {
		return nil, err
	}
	guideTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/guide-widget.html", "templates/guide.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		store: st, homeTmpl: homeTmpl, listTmpl: listTmpl, detTmpl: detTmpl, mapTmpl: mapTmpl, chatTmpl: chatTmpl, chatPubTmpl: chatPubTmpl, resTmpl: resTmpl, liensTmpl: liensTmpl, guideTmpl: guideTmpl,
		mapboxTok: mapboxToken, guideURL: strings.TrimSuffix(guideURL, "/"), log: log,
		chatSvc: chat.Service, sessions: chat.Sessions, pubSessions: chat.PublicSessions, chatReady: chat.Ready,
		md:       goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe())),
		guideCtx: make(map[string]json.RawMessage),
	}, nil
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	// Assets statiques de la charte graphique (polices Poppins, visuels, CSS)
	staticFS, err := fs.Sub(templatesFS, "static")
	if err == nil {
		mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))
	}

	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /", s.handleHome)
	mux.HandleFunc("GET /lexique", s.handleList)
	mux.HandleFunc("GET /lexique/{id}", s.handleDetail)
	mux.HandleFunc("GET /map", s.handleMap)
	mux.HandleFunc("GET /ressources", s.handleRessources)
	mux.HandleFunc("GET /liens-sociaux", s.handleLiensSociaux)
	mux.HandleFunc("GET /guide", s.handleGuidePage)
	mux.HandleFunc("POST /api/guide/session", s.handleGuideSession)
	mux.HandleFunc("POST /api/guide/reponse", s.handleGuideReponse)
	mux.HandleFunc("POST /api/guide/reset", s.handleGuideReset)
	mux.HandleFunc("GET /api/map/categories", s.handleMapCategories)
	mux.HandleFunc("GET /api/map/features", s.handleMapFeatures)
	mux.HandleFunc("GET /api/map/feature/{id}", s.handleMapFeatureDetail)
	mux.HandleFunc("GET /chat", s.handleChatPublicPage)
	mux.HandleFunc("POST /api/chat", s.handleChatPublicStream)
	mux.HandleFunc("POST /api/chat/reset", s.handleChatPublicReset)
	mux.HandleFunc("POST /api/chat/estabs/clear", s.handleChatPublicEstabsClear)
	mux.HandleFunc("GET /chatpro", s.handleChatPage)
	mux.HandleFunc("POST /api/chatpro", s.handleChatStream)
	mux.HandleFunc("POST /api/chatpro/reset", s.handleChatReset)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

type listData struct {
	Terms      []store.Term
	Q          string
	Page       int
	TotalPages int
	Total      int
	Prev, Next int
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.homeTmpl.ExecuteTemplate(w, "home.html", nil); err != nil {
		s.log.Error("template", "name", "home.html", "err", err)
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/lexique" {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query().Get("q")
	page := 1
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil && v > 0 {
			page = v
		}
	}
	const perPage = 50
	offset := (page - 1) * perPage

	total, err := s.store.CountTerms(r.Context(), q)
	if err != nil {
		s.renderError(w, err)
		return
	}
	totalPages := (total + perPage - 1) / perPage
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
		offset = (page - 1) * perPage
	}

	terms, err := s.store.ListTerms(r.Context(), q, offset, perPage)
	if err != nil {
		s.renderError(w, err)
		return
	}

	data := listData{
		Terms:      terms,
		Q:          q,
		Page:       page,
		TotalPages: totalPages,
		Total:      total,
		Prev:       page - 1,
		Next:       page + 1,
	}
	s.renderList(w, data)
}

func (s *Server) renderList(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.listTmpl.ExecuteTemplate(w, "list.html", data); err != nil {
		s.log.Error("template", "name", "list.html", "err", err)
	}
}

func (s *Server) renderDetail(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.detTmpl.ExecuteTemplate(w, "detail.html", data); err != nil {
		s.log.Error("template", "name", "detail.html", "err", err)
	}
}

func (s *Server) handleDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	term, err := s.store.GetTerm(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.renderDetail(w, term)
}

func (s *Server) renderError(w http.ResponseWriter, err error) {
	s.log.Error("store", "err", err)
	http.Error(w, "Erreur interne", http.StatusInternalServerError)
}

type mapPageData struct {
	MapboxToken string
}

func (s *Server) handleMap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.mapTmpl.ExecuteTemplate(w, "map.html", mapPageData{MapboxToken: s.mapboxTok}); err != nil {
		s.log.Error("template", "name", "map.html", "err", err)
	}
}

// --- Ressources ---

type ressourceGroup struct {
	Code  string
	Label string
	Items []store.Ressource
}

var ressourceCatOrder = []string{"numero", "site", "document", "reseau_social", "lien"}

var ressourceCatLabels = map[string]string{
	"numero":        "Numéros utiles",
	"site":          "Sites de référence",
	"document":      "Documents",
	"reseau_social": "Réseaux sociaux",
	"lien":          "Liens utiles",
}

func (s *Server) handleRessources(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListRessources(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}
	byCat := make(map[string][]store.Ressource)
	for _, it := range items {
		// Les liens sociaux ont leur page dédiée.
		if it.Category == "lien_social" {
			continue
		}
		byCat[it.Category] = append(byCat[it.Category], it)
	}
	groups := make([]ressourceGroup, 0, len(ressourceCatOrder))
	for _, c := range ressourceCatOrder {
		if len(byCat[c]) == 0 {
			continue
		}
		groups = append(groups, ressourceGroup{Code: c, Label: ressourceCatLabels[c], Items: byCat[c]})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.resTmpl.ExecuteTemplate(w, "ressources.html", groups); err != nil {
		s.log.Error("template", "name", "ressources.html", "err", err)
	}
}

// --- Liens sociaux (groupes d'entraide, associations, communautés) ---

func (s *Server) handleLiensSociaux(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListRessourcesByCategory(r.Context(), "lien_social")
	if err != nil {
		s.renderError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.liensTmpl.ExecuteTemplate(w, "liens-sociaux.html", items); err != nil {
		s.log.Error("template", "name", "liens-sociaux.html", "err", err)
	}
}

// --- Guide (questionnaire public, API externe ---

func (s *Server) handleGuidePage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct{ Ready bool }{Ready: s.guideURL != ""}
	if err := s.guideTmpl.ExecuteTemplate(w, "guide.html", data); err != nil {
		s.log.Error("template", "name", "guide.html", "err", err)
	}
}

func (s *Server) handleGuideSession(w http.ResponseWriter, r *http.Request) {
	s.guideProxy(w, r, "/sessions")
}

func (s *Server) handleGuideReponse(w http.ResponseWriter, r *http.Request) {
	s.guideProxy(w, r, "/reponse")
}

// handleGuideReset efface le contexte guide stocké côté serveur pour la
// session chat courante (appelé par le bouton ↺ du widget guide).
func (s *Server) handleGuideReset(w http.ResponseWriter, r *http.Request) {
	sid := s.sessionID(w, r)
	s.guideCtxMu.Lock()
	delete(s.guideCtx, sid)
	s.guideCtxMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// guideProxy forward le JSON vers l'API Guide (GUIDE_API_URL + suffixe).
// Chaque réponse réussie est capturée dans le contexte guide de la session
// chat (cookie boussole_sid), pour que le chat public dispose toujours des
// données à jour du questionnaire sans dépendre du client.
func (s *Server) guideProxy(w http.ResponseWriter, r *http.Request, suffix string) {
	if s.guideURL == "" {
		http.Error(w, `{"error":"guide non configuré (GUIDE_API_URL manquante)"}`, http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, `{"error":"lecture de la requête"}`, http.StatusBadRequest)
		return
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, s.guideURL+suffix, bytes.NewReader(body))
	if err != nil {
		s.log.Error("guide proxy", "err", err)
		http.Error(w, `{"error":"requête invalide"}`, http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.log.Error("guide proxy", "suffix", suffix, "err", err)
		http.Error(w, `{"error":"api guide injoignable"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		s.log.Error("guide proxy read", "err", err)
		http.Error(w, `{"error":"lecture de la réponse guide"}`, http.StatusBadGateway)
		return
	}

	// Capturer le contexte guide pour le chat public (sid via cookie —
	// sessionID pose le cookie s'il n'existe pas encore, ex. page /guide
	// autonome utilisée avant d'ouvrir /chat).
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		sid := s.sessionID(w, r)
		s.captureGuideCtx(sid, body, respBody)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(respBody)
}

// guideCtxEntry est le contexte guide stocké côté serveur pour une session chat.
type guideCtxEntry struct {
	Reponses    []guideAnswer   `json:"reponses"`
	Derniere    json.RawMessage `json:"derniere_reponse,omitempty"`
	JSONPatient json.RawMessage `json:"json_patient,omitempty"`
	SessionID   string          `json:"session_id,omitempty"`
	GoChat      bool            `json:"go_chat,omitempty"`
	BlocCrise   json.RawMessage `json:"bloc_crise,omitempty"`
	Updated     time.Time       `json:"-"`
	// Localisation et âge courants : chaque source est horodatée quand SA
	// valeur change, et la plus récente l'emporte (dernier changement gagnant).
	QCommune   string    `json:"q_commune,omitempty"`   // commune du questionnaire
	QCommuneAt time.Time `json:"q_commune_at,omitempty"`
	ChatLoc    string    `json:"chat_loc,omitempty"` // localisation dite au chat
	ChatLocAt  time.Time `json:"chat_loc_at,omitempty"`
	QAge       string    `json:"q_age,omitempty"` // âge du questionnaire
	QAgeAt     time.Time `json:"q_age_at,omitempty"`
	ChatAge    string    `json:"chat_age,omitempty"` // âge dit au chat
	ChatAgeAt  time.Time `json:"chat_age_at,omitempty"`
	QAddr      string    `json:"q_addr,omitempty"` // adresse du questionnaire (q_adresse)
	QAddrAt    time.Time `json:"q_addr_at,omitempty"`
	ChatAddr   string    `json:"chat_addr,omitempty"` // adresse dite au chat
	ChatAddrAt time.Time `json:"chat_addr_at,omitempty"`
}

type guideAnswer struct {
	IDQuestion string `json:"id_question"`
	Reponse    string `json:"reponse"`
}

// captureGuideCtx met à jour le contexte guide de la session chat à partir
// d'une requête (body) et de la réponse brute de l'API Guide.
func (s *Server) captureGuideCtx(sid string, reqBody, respBody []byte) {
	// Analyser la réponse : question suivante (avec json_patient) ou fin.
	var resp struct {
		IDQuestion  string          `json:"id_question"`
		SessionID   string          `json:"session_id"`
		GoChat      bool            `json:"go_chat"`
		JSONPatient json.RawMessage `json:"json_patient"`
		BlocCrise   json.RawMessage `json:"bloc_crise"`
	}
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return // réponse non structurée : on n'enregistre rien
	}

	// Analyser la requête pour récupérer la réponse donnée (s'il y en a une).
	var reqq struct {
		IDQuestion string `json:"id_question"`
		Reponse    string `json:"reponse"`
	}
	_ = json.Unmarshal(reqBody, &reqq)

	s.guideCtxMu.Lock()
	defer s.guideCtxMu.Unlock()

	e, ok := s.guideCtx[sid]
	var entry guideCtxEntry
	if ok {
		_ = json.Unmarshal(e, &entry)
	}

	// Ajouter la réponse à l'historique (seulement si c'est une réponse, pas la création de session).
	if reqq.IDQuestion != "" && reqq.Reponse != "" {
		// Éviter les doublons si le proxy est rappelé pour la même question.
		dedup := entry.Reponses[:0]
		for _, a := range entry.Reponses {
			if a.IDQuestion != reqq.IDQuestion {
				dedup = append(dedup, a)
			}
		}
		entry.Reponses = append(dedup, guideAnswer{IDQuestion: reqq.IDQuestion, Reponse: reqq.Reponse})
	}

	entry.Derniere = json.RawMessage(respBody)
	entry.SessionID = resp.SessionID
	entry.GoChat = resp.GoChat
	if len(resp.JSONPatient) > 0 {
		entry.JSONPatient = resp.JSONPatient
	}
	if len(resp.BlocCrise) > 0 {
		entry.BlocCrise = resp.BlocCrise
	}
	// La commune et l'âge du questionnaire ne sont horodatés que quand ils
	// CHANGENT : une réponse à une autre question ne doit pas écraser une
	// valeur plus récente dite au chat.
	if commune := guideCommune(resp.JSONPatient); commune != "" && commune != entry.QCommune {
		entry.QCommune = commune
		entry.QCommuneAt = time.Now()
	}
	if age := guideAgeValue(resp.JSONPatient); age != "" && age != entry.QAge {
		entry.QAge = age
		entry.QAgeAt = time.Now()
	}
	// L'adresse postale n'est pas conservée dans le JSON patient, mais la
	// réponse brute de q_adresse l'est : on la tracke quand elle change.
	if reqq.IDQuestion == "q_adresse" && reqq.Reponse != "" && reqq.Reponse != "passe" && reqq.Reponse != entry.QAddr {
		entry.QAddr = reqq.Reponse
		entry.QAddrAt = time.Now()
	}
	entry.Updated = time.Now()

	// Persister (re-sérialiser sans Updated).
	out, err := json.Marshal(struct {
		Reponses    []guideAnswer   `json:"reponses"`
		Derniere    json.RawMessage `json:"derniere_reponse,omitempty"`
		JSONPatient json.RawMessage `json:"json_patient,omitempty"`
		SessionID   string          `json:"session_id,omitempty"`
		GoChat      bool            `json:"go_chat,omitempty"`
		BlocCrise   json.RawMessage `json:"bloc_crise,omitempty"`
		QCommune    string          `json:"q_commune,omitempty"`
		QCommuneAt  time.Time       `json:"q_commune_at,omitempty"`
		ChatLoc     string          `json:"chat_loc,omitempty"`
		ChatLocAt   time.Time       `json:"chat_loc_at,omitempty"`
	}{entry.Reponses, entry.Derniere, entry.JSONPatient, entry.SessionID, entry.GoChat, entry.BlocCrise,
		entry.QCommune, entry.QCommuneAt, entry.ChatLoc, entry.ChatLocAt})
	if err != nil {
		return
	}
	s.guideCtx[sid] = out
}

// updateChatValue applique une mise à jour horodatée d'une valeur dite au
// chat (localisation ou âge) — dernier changement gagnant.
func (s *Server) updateChatValue(sid string, apply func(*guideCtxEntry) bool) {
	s.guideCtxMu.Lock()
	defer s.guideCtxMu.Unlock()
	e, ok := s.guideCtx[sid]
	var entry guideCtxEntry
	if ok {
		_ = json.Unmarshal(e, &entry)
	}
	if !apply(&entry) {
		return
	}
	out, err := json.Marshal(entry) // Updated est exclu (json:"-")
	if err != nil {
		return
	}
	s.guideCtx[sid] = out
}

// updateChatLocalisation enregistre une localisation dite au chat (horodatée),
// détectée en fin de tour par la passe carte.
func (s *Server) updateChatLocalisation(sid, loc string) {
	if sid == "" || loc == "" {
		return
	}
	s.updateChatValue(sid, func(e *guideCtxEntry) bool {
		if e.ChatLoc == loc {
			return false // déjà connue : ne pas ré-horodater
		}
		e.ChatLoc = loc
		e.ChatLocAt = time.Now()
		return true
	})
}

// updateChatAge enregistre un âge dit au chat (horodaté), détecté en fin de
// tour — même logique que la localisation.
func (s *Server) updateChatAge(sid, age string) {
	if sid == "" || age == "" {
		return
	}
	s.updateChatValue(sid, func(e *guideCtxEntry) bool {
		if e.ChatAge == age {
			return false
		}
		e.ChatAge = age
		e.ChatAgeAt = time.Now()
		return true
	})
}

// updateChatAddress enregistre une adresse dite au chat (horodatée) — même
// logique, pour affiner la proximité.
func (s *Server) updateChatAddress(sid, addr string) {
	if sid == "" || addr == "" {
		return
	}
	s.updateChatValue(sid, func(e *guideCtxEntry) bool {
		if e.ChatAddr == addr {
			return false
		}
		e.ChatAddr = addr
		e.ChatAddrAt = time.Now()
		return true
	})
}

// guideLocation retourne la localisation courante de la session : la source
// (questionnaire ou chat) dont la valeur a changé le plus récemment l'emporte.
// Sans champ horodaté (contexte fourni directement par la requête), on retombe
// sur la commune du JSON patient.
func guideLocation(raw json.RawMessage) string {
	var g struct {
		QCommune   string    `json:"q_commune"`
		QCommuneAt time.Time `json:"q_commune_at"`
		ChatLoc    string    `json:"chat_loc"`
		ChatLocAt  time.Time `json:"chat_loc_at"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &g) != nil {
		return ""
	}
	if g.QCommune == "" && g.ChatLoc == "" {
		return guideCommune(raw)
	}
	if g.ChatLoc != "" && g.ChatLocAt.After(g.QCommuneAt) {
		return g.ChatLoc
	}
	return g.QCommune
}

// guideAge retourne l'âge courant de la session : même règle que la
// localisation, la source la plus récemment modifiée l'emporte.
func guideAge(raw json.RawMessage) string {
	var g struct {
		QAge      string    `json:"q_age"`
		QAgeAt    time.Time `json:"q_age_at"`
		ChatAge   string    `json:"chat_age"`
		ChatAgeAt time.Time `json:"chat_age_at"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &g) != nil {
		return ""
	}
	if g.QAge == "" && g.ChatAge == "" {
		return guideAgeValue(raw)
	}
	if g.ChatAge != "" && g.ChatAgeAt.After(g.QAgeAt) {
		return g.ChatAge
	}
	return g.QAge
}

// guideAddress retourne l'adresse postale courante : la source (questionnaire
// ou chat) dont la valeur a changé le plus récemment l'emporte. Sert uniquement
// à affiner la proximité (elle reste hors du JSON patient).
func guideAddress(raw json.RawMessage) string {
	var g struct {
		QAddr      string    `json:"q_addr"`
		QAddrAt    time.Time `json:"q_addr_at"`
		ChatAddr   string    `json:"chat_addr"`
		ChatAddrAt time.Time `json:"chat_addr_at"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &g) != nil {
		return ""
	}
	if g.ChatAddr != "" && g.ChatAddrAt.After(g.QAddrAt) {
		return g.ChatAddr
	}
	return g.QAddr
}

// guideAgeValue extrait l'âge déclaré dans un JSON patient (personne.age_ans).
func guideAgeValue(raw json.RawMessage) string {
	var g struct {
		JSONPatient struct {
			Personne struct {
				AgeAns struct {
					Value any `json:"value"`
				} `json:"age_ans"`
			} `json:"personne"`
		} `json:"json_patient"`
		Personne struct {
			AgeAns struct {
				Value any `json:"value"`
			} `json:"age_ans"`
		} `json:"personne"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &g) != nil {
		return ""
	}
	v := g.JSONPatient.Personne.AgeAns.Value
	if v == nil {
		v = g.Personne.AgeAns.Value
	}
	switch t := v.(type) {
	case string:
		if t == "" || t == "pass" {
			return ""
		}
		return t
	case float64:
		return strconv.FormatInt(int64(t), 10)
	}
	return ""
}

// getGuideCtx retourne le contexte guide stocké pour la session chat.
func (s *Server) getGuideCtx(sid string) json.RawMessage {
	s.guideCtxMu.Lock()
	defer s.guideCtxMu.Unlock()
	return s.guideCtx[sid]
}

func (s *Server) handleMapCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.store.ListMapCategories(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(cats)
}

func (s *Server) handleMapFeatures(w http.ResponseWriter, r *http.Request) {
	category := r.URL.Query().Get("category")
	b, err := s.store.MapFeaturesGeoJSON(r.Context(), category)
	if err != nil {
		s.renderError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/geo+json")
	_, _ = w.Write(b)
}

type featureDetailResponse struct {
	ID          int                    `json:"id"`
	Name        string                 `json:"name"`
	Title       string                 `json:"title"`
	Image       string                 `json:"image"`
	Address     string                 `json:"address"`
	Phone       string                 `json:"phone"`
	Website     string                 `json:"website"`
	Subcats     []string               `json:"subcategories"`
	ContentHTML string                 `json:"content_html"`
	Props       map[string]interface{} `json:"props"`
}

func (s *Server) handleMapFeatureDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := s.store.GetFeatureDetail(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	resp := featureDetailResponse{
		ID:    f.ID,
		Name:  f.Name,
		Props: f.Props,
	}
	if v, ok := f.Props["title"].(string); ok {
		resp.Title = v
	}
	if v, ok := f.Props["image"].(string); ok {
		resp.Image = v
	}
	if v, ok := f.Props["address"].(string); ok {
		resp.Address = v
	}
	if v, ok := f.Props["phone"].(string); ok {
		resp.Phone = v
	}
	if v, ok := f.Props["website"].(string); ok {
		resp.Website = v
	}
	if arr, ok := f.Props["subcategories"].([]interface{}); ok {
		for _, a := range arr {
			if s, ok := a.(string); ok {
				resp.Subcats = append(resp.Subcats, s)
			}
		}
	}
	if md, ok := f.Props["content_md"].(string); ok {
		var buf strings.Builder
		if err := s.md.Convert([]byte(md), &buf); err == nil {
			resp.ContentHTML = buf.String()
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// --- Chat IA ---

const chatSessionCookie = "boussole_sid"

func (s *Server) sessionID(w http.ResponseWriter, r *http.Request) string {
	c, err := r.Cookie(chatSessionCookie)
	if err == nil && c.Value != "" {
		return c.Value
	}
	sid := uuid.NewString()
	http.SetCookie(w, &http.Cookie{
		Name:     chatSessionCookie,
		Value:    sid,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   3600,
	})
	return sid
}

func (s *Server) handleChatPage(w http.ResponseWriter, r *http.Request) {
	sid := s.sessionID(w, r)
	history := []ai.HistoryMessage{}
	if s.sessions != nil {
		history = s.sessions.History(sid)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Ready   bool
		History []ai.HistoryMessage
	}{Ready: s.chatReady, History: history}
	if err := s.chatTmpl.ExecuteTemplate(w, "chat.html", data); err != nil {
		s.log.Error("template", "name", "chat.html", "err", err)
	}
}

func (s *Server) handleChatReset(w http.ResponseWriter, r *http.Request) {
	sid := s.sessionID(w, r)
	if s.sessions != nil {
		s.sessions.Reset(sid)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

// --- Chat public (grand public, prompt distinct du ChatPro) ---

func (s *Server) handleChatPublicPage(w http.ResponseWriter, r *http.Request) {
	sid := s.sessionID(w, r)
	history := []ai.HistoryMessage{}
	ests := []ai.Establishment{}
	if s.pubSessions != nil {
		history = s.pubSessions.History(sid)
		ests = s.pubSessions.Establishments(sid)
	}
	estsJSON, err := json.Marshal(ests)
	if err != nil {
		estsJSON = []byte("[]")
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data := struct {
		Ready          bool
		History        []ai.HistoryMessage
		MapboxToken    string
		Establishments template.JS
	}{
		Ready:          s.chatReady,
		History:        history,
		MapboxToken:    s.mapboxTok,
		Establishments: template.JS(estsJSON),
	}
	if err := s.chatPubTmpl.ExecuteTemplate(w, "chat-public.html", data); err != nil {
		s.log.Error("template", "name", "chat-public.html", "err", err)
	}
}

// handleChatPublicEstabsClear vide la liste d'établissements de la session
// (sans toucher à l'historique du chat).
func (s *Server) handleChatPublicEstabsClear(w http.ResponseWriter, r *http.Request) {
	sid := s.sessionID(w, r)
	if s.pubSessions != nil {
		s.pubSessions.ClearEstablishments(sid)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleChatPublicReset(w http.ResponseWriter, r *http.Request) {
	sid := s.sessionID(w, r)
	if s.pubSessions != nil {
		s.pubSessions.Reset(sid)
	}
	// Le reset du chat efface aussi le contexte guide stocké.
	s.guideCtxMu.Lock()
	delete(s.guideCtx, sid)
	s.guideCtxMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) handleChatPublicStream(w http.ResponseWriter, r *http.Request) {
	// Le contexte guide est résolu côté serveur (mis à jour à chaque réponse
	// du widget guide via le proxy) — pas besoin que le client l'envoie.
	// Le prompt de passe 1 (guide) est utilisé ; la passe 2 (carte) utilise
	// son propre prompt interne dans Chat().
	s.streamChat(w, r, ai.SystemPromptPublicGuide, s.pubSessions, true)
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	s.streamChat(w, r, ai.SystemPromptPro, s.sessions, false)
}

// systemWithContext ajoute le contexte du questionnaire « Guide » au prompt
// système fourni (élément de contexte pour le chat public).
func systemWithContext(system string, guideContext json.RawMessage, age, location string) string {
	if len(guideContext) == 0 && age == "" && location == "" {
		return system
	}
	// État courant résolu (dernier changement gagnant entre questionnaire et
	// conversation) : le modèle ne doit pas se fier à une valeur obsolète du
	// JSON ci-dessous.
	etat := ""
	if age != "" || location != "" {
		etat = "\n\n--- État courant de la personne ---\nÂge : " + age + "\nLocalisation : " + location
	}
	if len(guideContext) == 0 {
		return system + etat
	}
	return system + etat + "\n\n--- Contexte du questionnaire « Guide » (réponses de la personne, JSON) ---\n" + string(guideContext)
}

// guideCommune extrait la commune déclarée dans un JSON patient (avec ou sans
// l'enveloppe {json_patient: ...}), repli sur le code INSEE.
func guideCommune(raw json.RawMessage) string {
	type personne struct {
		CommuneNom struct {
			Value any `json:"value"`
		} `json:"commune_nom"`
		CommuneInsee struct {
			Value any `json:"value"`
		} `json:"commune_insee"`
	}
	var g struct {
		JSONPatient struct {
			Personne personne `json:"personne"`
		} `json:"json_patient"`
		Personne personne `json:"personne"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &g) != nil {
		return ""
	}
	p := g.JSONPatient.Personne
	if p.CommuneNom.Value == nil && p.CommuneInsee.Value == nil {
		p = g.Personne
	}
	if v, ok := p.CommuneNom.Value.(string); ok && v != "" && v != "pass" {
		return v
	}
	if v, ok := p.CommuneInsee.Value.(string); ok && v != "" && v != "pass" {
		return v
	}
	return ""
}

// streamChat exécute le chat en streaming SSE pour un prompt et un store donnés.
// useGuideCtx : injecte le contexte guide stocké côté serveur (chat public).
func (s *Server) streamChat(w http.ResponseWriter, r *http.Request, system string, sessions *ai.SessionStore, useGuideCtx bool) {
	if !s.chatReady || s.chatSvc == nil {
		http.Error(w, `{"error":"chat non configuré (AI_* manquantes)"}`, http.StatusServiceUnavailable)
		return
	}

	var req ai.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		http.Error(w, `{"error":"message vide"}`, http.StatusUnprocessableEntity)
		return
	}

	sid := s.sessionID(w, r)
	var history []ai.HistoryMessage
	if sessions != nil {
		history = sessions.History(sid)
	}

	// Contexte guide : stocké serveur (à jour à chaque réponse du widget)
	// en priorité ; le guide_context client sert de repli (ex. page /guide
	// autonome utilisée avant d'ouvrir /chat, même cookie).
	guideCtx := req.GuideContext
	if useGuideCtx {
		if stored := s.getGuideCtx(sid); len(stored) > 0 {
			guideCtx = stored
		}
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming non supporté", http.StatusInternalServerError)
		return
	}

	// Si le client déconnecte (refresh, onglet fermé), on annule l'appel LLM
	// amont plutôt que de laisser tourner les retries dans le vide.
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Trace du tour : identifiant partagé par tous les logs + fiche JSONL.
	space := "pro"
	if useGuideCtx {
		space = "public"
	}
	tr := ai.NewChatTrace(ai.NewTraceID(), space)
	tr.SetGuideCtx(len(guideCtx))
	ctx = ai.WithTrace(ctx, tr)

	// Zone utilisateur : la localisation la plus récemment changée (chat ou
	// questionnaire) — base locale d'abord, geocode BAN en repli.
	if age := guideAge(guideCtx); age != "" {
		tr.SetAge(age, "questionnaire")
	}
	if commune := guideLocation(guideCtx); commune != "" {
		z := s.store.UserZoneInfo(ctx, commune)
		if z == nil {
			// Commune absente de la carte : la géocoder sert au moins de centre.
			if g, ok := ai.GeocodeAddress(ctx, commune); ok {
				z = &store.UserZone{Name: commune, Center: [2]float64{g.Lng, g.Lat}}
			} else {
				z = &store.UserZone{Name: commune}
			}
			tr.SetZone(commune, "geocode", 0)
		} else {
			tr.SetZone(z.Name, "base", len(z.In))
		}
		// Adresse précise (si connue) : référence de proximité depuis la rue
		// plutôt que depuis le centroïde de la commune.
		if addr := guideAddress(guideCtx); addr != "" {
			if g, ok := ai.GeocodeAddress(ctx, addr); ok {
				z.Center = [2]float64{g.Lng, g.Lat}
			}
			tr.SetAddr(addr, "questionnaire")
		}
		ctx = store.WithZone(ctx, z)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	// Mutex protégeant toutes les écritures sur w : le keepalive et la boucle
	// d'événements écrivent depuis des goroutines différentes — sans protection,
	// les écritures s'entrelacent et corrompent le flux SSE (« Error in input
	// stream » côté navigateur).
	var wmu sync.Mutex
	var ttfbAt time.Time
	writeSSE := func(format string, args ...any) error {
		wmu.Lock()
		defer wmu.Unlock()
		_, err := fmt.Fprintf(w, format, args...)
		if err == nil {
			if ttfbAt.IsZero() {
				ttfbAt = time.Now()
			}
			flusher.Flush()
		}
		return err
	}

	// Keepalive SSE : pendant les appels LLM longs (16-40s), aucun octet n'est
	// envoyé — le navigateur/proxy coupe la connexion inactif. On envoie un
	// commentaire SSE `: ping` toutes les 10s (ignoré par le client, mais
	// maintient la connexion vivante).
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := writeSSE(": ping\n\n"); err != nil {
					return
				}
			}
		}
	}()

	events := s.chatSvc.Chat(ctx, history, req.Message,
		systemWithContext(system, guideCtx, guideAge(guideCtx), guideLocation(guideCtx)))

	startHTTP := time.Now()
	var firstTokAt time.Time
	sentEvents, sentBytes := 0, 0
	var fullAnswer strings.Builder

	// finish clôt la trace du tour (logs + JSONL), en succès ou déconnexion.
	finish := func(clientGone bool) {
		firstTokMs, ttfbMs := int64(0), int64(0)
		if !firstTokAt.IsZero() {
			firstTokMs = firstTokAt.Sub(startHTTP).Milliseconds()
		}
		if !ttfbAt.IsZero() {
			ttfbMs = ttfbAt.Sub(startHTTP).Milliseconds()
		}
		tr.SetOutput(sentEvents, sentBytes, fullAnswer.Len(), firstTokMs, ttfbMs, clientGone)
		tr.SetExcerpts(ai.Excerpt(req.Message, 200), ai.Excerpt(fullAnswer.String(), 400))
		tr.Finish()
		ai.WriteTrace(tr)
		snap := tr.Snapshot()
		// Localisation et âge dits dans la conversation : retenus pour les
		// tours suivants (dernier changement gagnant face au questionnaire).
		if snap.ZoneSource == "conversation" && snap.Zone != "" {
			s.updateChatLocalisation(sid, snap.Zone)
		}
		if snap.AgeSource == "conversation" && snap.Age != "" {
			s.updateChatAge(sid, snap.Age)
		}
		if snap.AddrSource == "conversation" && snap.Addr != "" {
			s.updateChatAddress(sid, snap.Addr)
		}
		s.log.Info("chat http done", "trace", snap.TraceID, "space", snap.Space,
			"http_ms", snap.TotalMs, "http_ttfb_ms", snap.TTFBMs, "first_token_ms", snap.FirstTokMs,
			"guide_pass_ms", snap.GuideMs, "map_pass_ms", snap.MapMs,
			"llm_calls", snap.LLMCalls, "ttft_ms", snap.TTFTMs, "llm_stream_ms", snap.LLMStreamMs,
			"tokens_in", snap.TokensIn, "tokens_out", snap.TokensOut,
			"chars_sent", snap.CharsSent, "chars_recv", snap.CharsRecv, "chars_out", snap.CharsOut,
			"tool_calls", snap.ToolCallsCount, "tools_used", snap.ToolsUsed, "tool_ms", snap.ToolMs,
			"map_names", snap.MapNames, "map_matched", snap.MapMatched,
			"nearby_fallback", snap.NearbyFallback, "nearby_radius_km", snap.NearbyRadiusKm,
			"zone", snap.Zone, "zone_source", snap.ZoneSource, "zone_inside", snap.ZoneInside,
			"sse_events", snap.SSEEvents, "sse_bytes", snap.SSEBytes,
			"truncations", snap.Truncations, "retries", snap.LLMRetries,
			"forced_final", snap.ForcedFinal, "fallback", snap.Fallback,
			"client_gone", snap.ClientGone)
	}

	for ev := range events {
		data, _ := json.Marshal(ev)
		sentEvents++
		sentBytes += len(data)
		switch ev.Type {
		case "token":
			if firstTokAt.IsZero() {
				firstTokAt = time.Now()
			}
			fullAnswer.WriteString(ev.Token)
		case "tool":
			// already in event
		case "establishments":
			// Remplace les propositions de la session (panneau = dernière
			// recommandation du chat).
			if sessions != nil {
				sessions.SetEstablishments(sid, ev.Establishments)
			}
		case "done":
			if sessions != nil {
				sessions.Add(sid, req.Message, fullAnswer.String())
			}
		case "error":
			s.log.Error("chat error", "trace", tr.TraceID, "err", ev.Error)
		}
		if err := writeSSE("event: %s\ndata: %s\n\n", ev.Type, data); err != nil {
			s.log.Debug("chat client déconnecté", "trace", tr.TraceID, "err", err)
			cancel()
			// on vide le canal pour que la goroutine Chat se termine
			for range events {
			}
			finish(true)
			return
		}
	}
	finish(false)
}
