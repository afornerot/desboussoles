package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bornholm/genai/llm"
)

// StreamEvent est un événement émis pendant le streaming du chat.
type StreamEvent struct {
	Type  string   `json:"type"` // token | tool | establishments | done | error
	Token string   `json:"t,omitempty"`
	Tool  string   `json:"name,omitempty"`
	Tools []string `json:"tools_used,omitempty"`
	Error string   `json:"error,omitempty"`
	// Establishments porte les résultats d'un outil carte (map_search,
	// map_nearby, map_within) pour les afficher dans la colonne dédiée.
	Establishments []Establishment `json:"establishments,omitempty"`
}

// Establishment est un établissement renvoyé par un outil carte.
type Establishment struct {
	ID            int      `json:"id,omitempty"`
	Name          string   `json:"name"`
	Title         string   `json:"title,omitempty"`
	Address       string   `json:"address,omitempty"`
	Phone         string   `json:"phone,omitempty"`
	Category      string   `json:"category,omitempty"`
	Subcategories []string `json:"subcategories,omitempty"`
	DistanceKm    string   `json:"distance_km,omitempty"`
	Lng           float64  `json:"lng,omitempty"`
	Lat           float64  `json:"lat,omitempty"`
}

// HistoryMessage est un message d'historique (session).
type HistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest est le corps de POST /api/chat.
type ChatRequest struct {
	Message string `json:"message"`
	// GuideContext est le contexte du questionnaire « Guide » (réponses +
	// dernière réponse API, JSON). Injecté dans le prompt du chat public.
	GuideContext json.RawMessage `json:"guide_context,omitempty"`
}

// Config règle la boucle d'outils et le timeout d'appel LLM.
type Config struct {
	MaxIterations int
	CallTimeout   time.Duration
}

// Service exécute le workflow chat avec streaming.
type Service struct {
	client llm.Client
	tools  []llm.Tool
	cfg    Config
	log    *slog.Logger
}

// New construit le service chat.
func New(client llm.Client, tools []llm.Tool, cfg Config, log *slog.Logger) *Service {
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 5
	}
	if cfg.CallTimeout <= 0 {
		cfg.CallTimeout = 120 * time.Second
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{client: client, tools: tools, cfg: cfg, log: log}
}

// SystemPromptPro est le prompt du chat professionnel (ChatPro) — une seule
// passe avec tous les outils.
const SystemPromptPro = `Tu es l'assistant de Boussole, une application santé mentale pour le département du Doubs (25).

Tu as accès à ces données via des outils :
- lexique_search : un lexique de ~306 termes santé mentale (définitions, acronymes, dispositifs)
- map_search : recherche par nom sur la carte (établissements de santé mentale, cantons, villes)
- map_nearby : entités proches d'un point GPS dans un rayon en km
- map_within : entités à l'intérieur d'une zone (canton, ville) par nom
- guide_search : recherche dans le Guide des partenaires en santé mentale du Doubs (structures, coordonnées, contacts, dispositifs)
- geocode : convertit une adresse en coordonnées GPS (utilise-le avec l'adresse du contexte guide pour alimenter map_nearby)

Catégories carte : etablissement, departement, canton, ville.
Sous-catégories établissements : hospitalisation, ambulatoire, adultes, enfants-ados, urgences, therapeutique, jour, rehabilitation, ressource, autisme, jeunes, ecoute, accompagnement, social, precarite, hebergement, consultation.

Réponds en français. Utilise les outils quand la question porte sur un terme, un lieu ou un établissement. Après avoir obtenu les résultats des outils, réponds directement avec les informations trouvées — n'appelle pas les outils en boucle. Mentionne les sources (nom de l'établissement, terme du lexique). Si tu n'as pas la réponse, dis-le honnêtement.`

// SystemPromptPublicGuide est le prompt de la PASSE 1 (chat public) —
// recherche dans le guide uniquement. Aucun outil carte n'est disponible.
const SystemPromptPublicGuide = `Tu es l'assistant de Boussole, une application santé mentale pour le département du Doubs (25).

Ton **unique objectif** est de guider le grand public vers le professionnel qui correspond le mieux à sa situation, tant pathologique que géographique.

Ne donne aucune réponse tant que l'on n'a pas connaissance de :
- l'âge ou la tranche d'âge de la personne
- son adresse

Si tu ne trouves aucun établissement adapté, propose de contacter le DAC = Dispositif d'Appui à la Coordination, au 03.81.25.05.30 ou le dac-fr@dac-fc.org.

Tu as accès à ces outils :
- guide_search : recherche dans le Guide des partenaires en santé mentale du Doubs (structures, coordonnées, contacts, dispositifs)
- lexique_search : un lexique de ~306 termes santé mentale (définitions, acronymes, dispositifs)

Ta démarche :
1. Utilise guide_search pour identifier les structures du guide adaptées à la situation de la personne
2. Utilise lexique_search si un terme médical doit être clarifié
3. Rédige une réponse claire et bienveillante qui recommande les structures pertinentes du guide, avec leurs coordonnées quand disponibles

Ne propose jamais une structure qui n'existe pas dans le Guide des partenaires. Si le guide ne contient rien d'adapté, dis-le honnêtement et oriente vers le DAC.

Réponds en français. Ne porte aucun jugement moral. Ne donne aucun diagnostic médical.`

// SystemPromptPublicMap est le prompt de la PASSE 2 (chat public) —
// vérification carte des établissements mentionnés dans la réponse.
// Aucun outil guide ou lexique n'est disponible.
const SystemPromptPublicMap = `Tu es un assistant qui vérifie la présence d'établissements sur une carte de santé mentale du Doubs.

On te fournit une réponse qui mentionne des structures et établissements. Ta tâche :
1. Identifie chaque structure/établissement concret mentionné dans la réponse
2. Pour chacun, appelle map_search (ou map_nearby si tu as des coordonnées) pour vérifier s'il existe sur la carte
3. N'appelle les outils que pour les structures réellement mentionnées, pas pour des termes génériques

Tu n'as accès qu'aux outils carte. Ne produis aucun texte — appelle uniquement les outils de recherche.`

// streamWithRetry tente d'ouvrir un stream, avec retries sur erreur.
func (s *Service) streamWithRetry(ctx context.Context, messages []llm.Message) (<-chan llm.StreamChunk, error) {
	return s.streamWithRetryTools(ctx, messages, s.tools)
}

// streamWithRetryTools est comme streamWithRetry mais avec une liste d'outils explicite.
func (s *Service) streamWithRetryTools(ctx context.Context, messages []llm.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		stream, err := s.client.ChatCompletionStream(ctx,
			llm.WithMessages(messages...),
			llm.WithTools(tools...),
			llm.WithToolChoice(llm.ToolChoiceAuto),
		)
		if err == nil {
			return stream, nil
		}
		lastErr = err
		s.log.Warn("stream retry", "attempt", attempt+1, "err", err)
		time.Sleep(time.Duration(attempt+1) * 500 * time.Millisecond)
	}
	return nil, lastErr
}

// consumeStream lit le stream et accumule contenu + tool calls.
// Les tokens sont collectés dans tokens (pas émis) : c'est le caller qui
// décide de les émettre (uniquement sur l'itération finale, sans tool calls,
// pour éviter les doublons quand le modèle régénère sa réponse après outils).
func (s *Service) consumeStream(
	ctx context.Context,
	stream <-chan llm.StreamChunk,
) (content string, tokens []string, toolCalls []llm.ToolCall, err error) {
	toolAcc := map[int]*struct {
		id, name, params string
	}{}
	var toolOrder []int

	for chunk := range stream {
		if chunk.Error() != nil {
			return content, tokens, nil, chunk.Error()
		}
		d := chunk.Delta()
		if d == nil {
			continue
		}
		if txt := d.Content(); txt != "" {
			content += txt
			tokens = append(tokens, txt)
		}
		for _, tcDelta := range d.ToolCalls() {
			idx := tcDelta.Index()
			acc, ok := toolAcc[idx]
			if !ok {
				acc = &struct{ id, name, params string }{}
				toolAcc[idx] = acc
				toolOrder = append(toolOrder, idx)
			}
			if tcDelta.ID() != "" {
				acc.id = tcDelta.ID()
			}
			if tcDelta.Name() != "" {
				acc.name = tcDelta.Name()
			}
			if tcDelta.ParametersDelta() != "" {
				acc.params += tcDelta.ParametersDelta()
			}
		}
	}

	for _, idx := range toolOrder {
		acc := toolAcc[idx]
		if acc.name == "" {
			continue
		}
		params := acc.params
		if params == "" || !json.Valid([]byte(params)) {
			params = "{}"
		}
		toolCalls = append(toolCalls, llm.NewToolCall(acc.id, acc.name, params))
	}
	return content, tokens, toolCalls, nil
}

// chatOnce exécute un seul appel LLM avec streaming et retourne le résultat.
// tokens contient les tokens accumulés (non émis) — c'est le caller qui
// décide de les émettre sur l'itération finale.
func (s *Service) chatOnce(ctx context.Context, messages []llm.Message) (string, []string, []llm.ToolCall, error) {
	return s.chatOnceTools(ctx, messages, s.tools)
}

// chatOnceTools est comme chatOnce mais avec une liste d'outils explicite
// (permet de filtrer les outils par passe : guide vs carte).
func (s *Service) chatOnceTools(ctx context.Context, messages []llm.Message, tools []llm.Tool) (string, []string, []llm.ToolCall, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.cfg.CallTimeout)
	defer cancel()

	stream, err := s.streamWithRetryTools(callCtx, messages, tools)
	if err != nil {
		return "", nil, nil, fmt.Errorf("Erreur de connexion à l'API: %s", err)
	}

	content, tokens, toolCalls, streamErr := s.consumeStream(callCtx, stream)
	if streamErr != nil {
		return content, tokens, toolCalls, streamErr
	}
	return content, tokens, toolCalls, nil
}

// Chat exécute le workflow streaming en DEUX PASSES :
//  1. Pass guide : recherche dans le guide (guide_search, lexique_search)
//     — génère la réponse complète. Aucun outil carte n'est disponible.
//  2. Pass carte : après la réponse, vérifie la présence des établissements
//     mentionnés sur la carte (map_search, map_nearby) et émet les événements
//     « establishments » pour la colonne dédiée du front. Aucun outil guide
//     ni lexique n'est disponible.
//
// system est le prompt système de la passe 1 (SystemPromptPro ou
// SystemPromptPublicGuide). Le prompt de la passe 2 est interne
// (SystemPromptPublicMap).
func (s *Service) Chat(ctx context.Context, history []HistoryMessage, userMsg, system string) <-chan StreamEvent {
	out := make(chan StreamEvent, 64)

	go func() {
		defer close(out)
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("chat panic", "panic", r)
				out <- StreamEvent{Type: "error", Error: fmt.Sprintf("Erreur inattendue: %v", r)}
			}
		}()

		messages := []llm.Message{
			llm.NewMessage(llm.RoleSystem, system),
		}
		for _, m := range history {
			role := m.Role
			if role == "" {
				role = "user"
			}
			messages = append(messages, llm.NewMessage(llm.Role(role), m.Content))
		}
		messages = append(messages, llm.NewMessage(llm.RoleUser, userMsg))

		// --- Pass 1 : outils guide uniquement (pas de carte, pas de geocode) ---
		guideTools := filterTools(s.tools, []string{"guide_search", "lexique_search"})
		toolsUsed := []string{}
		startAll := time.Now()
		finalContent := ""
		// Garde anti-boucle : track les appels (outil+params) pour détecter
		// quand le modèle répète la même recherche sans progresser.
		type toolKey struct{ name, params string }
		seenCalls := map[toolKey]int{}

		for i := 0; i < s.cfg.MaxIterations; i++ {
			startLLM := time.Now()
			content, tokens, toolCalls, err := s.chatOnceTools(ctx, messages, guideTools)
			s.log.Info("llm timing", "pass", "guide", "iteration", i, "duration_ms", time.Since(startLLM).Milliseconds(), "tool_calls", len(toolCalls))
			if err != nil {
				if i < s.cfg.MaxIterations-1 {
					s.log.Warn("chat iteration retry", "err", err, "attempt", i)
					continue
				}
				out <- StreamEvent{Type: "error", Error: err.Error()}
				return
			}

			if len(toolCalls) == 0 {
				finalContent = content
				for _, t := range tokens {
					out <- StreamEvent{Type: "token", Token: t}
				}
				break
			}

			// Anti-boucle : si TOUS les tool calls de cette itération ont déjà
			// été vus au moins 2 fois, on force la réponse finale sans outils.
			allRepeated := true
			for _, tc := range toolCalls {
				k := toolKey{tc.Name(), tc.Parameters().(string)}
				seenCalls[k]++
				if seenCalls[k] < 2 {
					allRepeated = false
				}
			}
			if allRepeated && i > 0 {
				s.log.Warn("guide pass looping detected, forcing final answer", "iterations", i)
				messages = append(messages, llm.NewMessage(llm.RoleAssistant,
					"Tu as déjà effectué toutes les recherches nécessaires. Réponds maintenant directement avec les informations dont tu disposeras, sans appeler d'outil."))
				_, ftokens, ftoolCalls, ferr := s.chatOnceTools(ctx, messages, nil)
				if ferr == nil && len(ftoolCalls) == 0 {
					finalContent = strings.Join(ftokens, "")
					for _, t := range ftokens {
						out <- StreamEvent{Type: "token", Token: t}
					}
				}
				break
			}

			_ = content
			messages = append(messages, llm.NewToolCallsMessage(toolCalls...))
			for _, tc := range toolCalls {
				toolsUsed = append(toolsUsed, tc.Name())
				startTool := time.Now()
				out <- StreamEvent{Type: "tool", Tool: tc.Name()}
				msg, err := llm.ExecuteToolCall(ctx, tc, guideTools...)
				s.log.Info("tool timing", "pass", "guide", "name", tc.Name(), "duration_ms", time.Since(startTool).Milliseconds())
				if err != nil {
					s.log.Error("tool exec", "name", tc.Name(), "err", err)
					msg = llm.NewToolMessage(tc.ID(), llm.NewToolResult(fmt.Sprintf("Tool execution error: %s", err.Error())))
				}
				messages = append(messages, msg)
			}
			if i == s.cfg.MaxIterations-1 {
				out <- StreamEvent{Type: "error", Error: fmt.Sprintf("nombre maximal d'itérations atteint (%d)", s.cfg.MaxIterations)}
				return
			}
		}

		out <- StreamEvent{Type: "done", Tools: toolsUsed}
		s.log.Info("chat done", "pass", "guide", "total_ms", time.Since(startAll).Milliseconds(), "tools", toolsUsed)

		// --- Pass 2 : vérification carte (prompt et outils dédiés) ---
		if finalContent != "" {
			s.verifyMapEstablishments(ctx, finalContent, out)
		}
	}()

	return out
}

// verifyMapEstablishments est la PASSE 2 : après la réponse du guide, on
// demande au modèle (prompt et outils dédiés, sans guide ni lexique) de
// vérifier la présence des établissements mentionnés sur la carte.
// Seuls ceux trouvés sur la carte sont émis en événements « establishments ».
func (s *Service) verifyMapEstablishments(ctx context.Context, response string, out chan StreamEvent) {
	mapTools := filterTools(s.tools, []string{"map_search", "map_nearby"})
	if len(mapTools) == 0 {
		return
	}

	messages := []llm.Message{
		llm.NewMessage(llm.RoleSystem, SystemPromptPublicMap),
		llm.NewMessage(llm.RoleUser, "Réponse à vérifier :\n"+response),
	}

	start := time.Now()
	var allEsts []Establishment
	seen := map[string]bool{}

	for i := 0; i < 3; i++ {
		_, _, toolCalls, err := s.chatOnceTools(ctx, messages, mapTools)
		if err != nil || len(toolCalls) == 0 {
			break
		}
		messages = append(messages, llm.NewToolCallsMessage(toolCalls...))
		for _, tc := range toolCalls {
			startTool := time.Now()
			msg, err := llm.ExecuteToolCall(ctx, tc, mapTools...)
			s.log.Info("tool timing", "pass", "map", "name", tc.Name(), "duration_ms", time.Since(startTool).Milliseconds())
			if err != nil {
				msg = llm.NewToolMessage(tc.ID(), llm.NewToolResult(fmt.Sprintf("Tool execution error: %s", err.Error())))
			}
			// Extraire les établissements des résultats carte
			if ests := extractEstablishments(tc.Name(), msg.Content()); len(ests) > 0 {
				for _, e := range ests {
					key := fmt.Sprintf("%d|%s", e.ID, e.Name)
					if !seen[key] {
						seen[key] = true
						allEsts = append(allEsts, e)
					}
				}
			}
			messages = append(messages, msg)
		}
	}

	s.log.Info("map verify done", "duration_ms", time.Since(start).Milliseconds(), "establishments", len(allEsts))
	if len(allEsts) > 0 {
		out <- StreamEvent{Type: "establishments", Establishments: allEsts}
	}
}

// filterTools retourne uniquement les outils dont le nom est dans names.
func filterTools(tools []llm.Tool, names []string) []llm.Tool {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []llm.Tool
	for _, t := range tools {
		if want[t.Name()] {
			out = append(out, t)
		}
	}
	return out
}

// extractEstablishments parse le résultat JSON d'un outil carte et en extrait
// les établissements pour l'événement SSE « establishments ».
func extractEstablishments(toolName, resultContent string) []Establishment {
	switch toolName {
	case "map_search", "map_nearby", "map_within":
	default:
		return nil
	}
	var resp struct {
		Results []struct {
			ID            int      `json:"id"`
			Name          string   `json:"name"`
			Title         string   `json:"title"`
			Address       string   `json:"address"`
			Phone         string   `json:"phone"`
			Category      string   `json:"category"`
			Subcategories []string `json:"subcategories"`
			DistanceKm    string   `json:"distance_km"`
			Lng           float64  `json:"lng"`
			Lat           float64  `json:"lat"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(resultContent), &resp); err != nil {
		return nil
	}
	ests := make([]Establishment, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Name == "" {
			continue
		}
		ests = append(ests, Establishment{
			ID:            r.ID,
			Name:          r.Name,
			Title:         r.Title,
			Address:       r.Address,
			Phone:         r.Phone,
			Category:      r.Category,
			Subcategories: r.Subcategories,
			DistanceKm:    r.DistanceKm,
			Lng:           r.Lng,
			Lat:           r.Lat,
		})
	}
	return ests
}

// SessionStore maintient l'historique de conversation par session (en mémoire, TTL).
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*session
	ttl      time.Duration
}

type session struct {
	messages []HistoryMessage
	ests     []Establishment
	updated  time.Time
}

// NewSessionStore crée un store avec TTL (défaut 1h).
func NewSessionStore(ttl time.Duration) *SessionStore {
	if ttl <= 0 {
		ttl = time.Hour
	}
	ss := &SessionStore{
		sessions: make(map[string]*session),
		ttl:      ttl,
	}
	go ss.cleanupLoop()
	return ss
}

func (ss *SessionStore) cleanupLoop() {
	t := time.NewTicker(10 * time.Minute)
	for range t.C {
		ss.cleanup()
	}
}

func (ss *SessionStore) cleanup() {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	now := time.Now()
	for id, s := range ss.sessions {
		if now.Sub(s.updated) > ss.ttl {
			delete(ss.sessions, id)
		}
	}
}

// Add ajoute un échange user/assistant à la session.
func (ss *SessionStore) Add(sid, userMsg, assistantMsg string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[sid]
	if !ok {
		s = &session{}
		ss.sessions[sid] = s
	}
	s.messages = append(s.messages,
		HistoryMessage{Role: "user", Content: userMsg},
		HistoryMessage{Role: "assistant", Content: assistantMsg},
	)
	if len(s.messages) > 40 {
		s.messages = s.messages[len(s.messages)-40:]
	}
	s.updated = time.Now()
}

// History retourne l'historique de la session.
func (ss *SessionStore) History(sid string) []HistoryMessage {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[sid]
	if !ok {
		return nil
	}
	out := make([]HistoryMessage, len(s.messages))
	copy(out, s.messages)
	return out
}

// estabKey identifie un établissement pour le dédoublonnage.
func estabKey(e Establishment) string {
	if e.ID != 0 {
		return fmt.Sprintf("id:%d", e.ID)
	}
	return "name:" + e.Name + "|" + e.Address
}

// AddEstablishments cumule les établissements d'un outil carte dans la session
// (dédoublonnés), afin de les retrouver après navigation vers une autre page.
func (ss *SessionStore) AddEstablishments(sid string, ests []Establishment) {
	if len(ests) == 0 {
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[sid]
	if !ok {
		s = &session{}
		ss.sessions[sid] = s
	}
	seen := make(map[string]bool, len(s.ests))
	for _, e := range s.ests {
		seen[estabKey(e)] = true
	}
	for _, e := range ests {
		k := estabKey(e)
		if seen[k] {
			continue
		}
		seen[k] = true
		s.ests = append(s.ests, e)
	}
	if len(s.ests) > 100 {
		s.ests = s.ests[len(s.ests)-100:]
	}
	s.updated = time.Now()
}

// Establishments retourne les établissements accumulés pour la session.
func (ss *SessionStore) Establishments(sid string) []Establishment {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	s, ok := ss.sessions[sid]
	if !ok {
		return nil
	}
	out := make([]Establishment, len(s.ests))
	copy(out, s.ests)
	return out
}

// ClearEstablishments vide la liste d'établissements sans toucher à l'historique.
func (ss *SessionStore) ClearEstablishments(sid string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if s, ok := ss.sessions[sid]; ok {
		s.ests = nil
		s.updated = time.Now()
	}
}

// Reset efface l'historique d'une session.
func (ss *SessionStore) Reset(sid string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.sessions, sid)
}
