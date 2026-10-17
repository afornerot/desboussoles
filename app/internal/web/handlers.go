package web

import (
	"bytes"
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
	store     *store.Store
	homeTmpl  *template.Template
	listTmpl  *template.Template
	detTmpl   *template.Template
	mapTmpl   *template.Template
	chatTmpl  *template.Template
	resTmpl   *template.Template
	guideTmpl *template.Template
	mapboxTok string
	guideURL  string
	md        goldmark.Markdown
	chatSvc   *ai.Service
	sessions  *ai.SessionStore
	chatReady bool
	log       *slog.Logger
}

// ChatDeps regroupe les dépendances optionnelles du chat IA.
type ChatDeps struct {
	Service  *ai.Service
	Sessions *ai.SessionStore
	Ready    bool
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
	chatTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/chat.html")
	if err != nil {
		return nil, err
	}
	resTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/ressources.html")
	if err != nil {
		return nil, err
	}
	guideTmpl, err := template.ParseFS(templatesFS, "templates/base.html", "templates/guide.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		store: st, homeTmpl: homeTmpl, listTmpl: listTmpl, detTmpl: detTmpl, mapTmpl: mapTmpl, chatTmpl: chatTmpl, resTmpl: resTmpl, guideTmpl: guideTmpl,
		mapboxTok: mapboxToken, guideURL: strings.TrimSuffix(guideURL, "/"), log: log,
		chatSvc: chat.Service, sessions: chat.Sessions, chatReady: chat.Ready,
		md: goldmark.New(goldmark.WithRendererOptions(html.WithUnsafe())),
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
	mux.HandleFunc("GET /guide", s.handleGuidePage)
	mux.HandleFunc("POST /api/guide/session", s.handleGuideSession)
	mux.HandleFunc("POST /api/guide/reponse", s.handleGuideReponse)
	mux.HandleFunc("GET /api/map/categories", s.handleMapCategories)
	mux.HandleFunc("GET /api/map/features", s.handleMapFeatures)
	mux.HandleFunc("GET /api/map/feature/{id}", s.handleMapFeatureDetail)
	mux.HandleFunc("GET /chat", s.handleChatPage)
	mux.HandleFunc("POST /api/chat", s.handleChatStream)
	mux.HandleFunc("POST /api/chat/reset", s.handleChatReset)
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

// guideProxy forward le JSON vers l'API Guide (GUIDE_API_URL + suffixe).
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

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	if _, err := io.Copy(w, resp.Body); err != nil {
		s.log.Error("guide proxy copy", "err", err)
	}
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

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
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
	history := s.sessions.History(sid)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming non supporté", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	events := s.chatSvc.Chat(r.Context(), history, req.Message)

	var fullAnswer strings.Builder
	for ev := range events {
		data, _ := json.Marshal(ev)
		switch ev.Type {
		case "token":
			fullAnswer.WriteString(ev.Token)
		case "tool":
			// already in event
		case "done":
			if s.sessions != nil {
				s.sessions.Add(sid, req.Message, fullAnswer.String())
			}
		case "error":
			s.log.Error("chat error", "err", ev.Error)
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, data)
		flusher.Flush()
	}
}
