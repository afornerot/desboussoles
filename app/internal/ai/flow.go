package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/bornholm/genai/llm"
)

// StreamEvent est un événement émis pendant le streaming du chat.
type StreamEvent struct {
	Type  string   `json:"type"` // token | tool | done | error
	Token string   `json:"t,omitempty"`
	Tool  string   `json:"name,omitempty"`
	Tools []string `json:"tools_used,omitempty"`
	Error string   `json:"error,omitempty"`
}

// HistoryMessage est un message d'historique (session).
type HistoryMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest est le corps de POST /api/chat.
type ChatRequest struct {
	Message string `json:"message"`
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

// systemPrompt décrit Boussole et ses outils au modèle.
const systemPrompt = `Tu es l'assistant de Boussole, une application santé mentale pour le département du Doubs (25).

Tu as accès à ces données via des outils :
- lexique_search : un lexique de ~306 termes santé mentale (définitions, acronymes, dispositifs)
- map_search : recherche par nom sur la carte (établissements de santé mentale, cantons, villes)
- map_nearby : entités proches d'un point GPS dans un rayon en km
- map_within : entités à l'intérieur d'une zone (canton, ville) par nom
- guide_search : recherche dans le Guide des partenaires en santé mentale du Doubs (structures, coordonnées, contacts, dispositifs)

Catégories carte : etablissement, departement, canton, ville.
Sous-catégories établissements : hospitalisation, ambulatoire, adultes, enfants-ados, urgences, therapeutique, jour, rehabilitation, ressource, autisme, jeunes, ecoute, accompagnement, social, precarite, hebergement, consultation.

Réponds en français. Utilise les outils quand la question porte sur un terme, un lieu ou un établissement. Après avoir obtenu les résultats des outils, réponds directement avec les informations trouvées — n'appelle pas les outils en boucle. Mentionne les sources (nom de l'établissement, terme du lexique). Si tu n'as pas la réponse, dis-le honnêtement.`

// streamWithRetry tente d'ouvrir un stream, avec retries sur erreur.
func (s *Service) streamWithRetry(ctx context.Context, messages []llm.Message) (<-chan llm.StreamChunk, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		stream, err := s.client.ChatCompletionStream(ctx,
			llm.WithMessages(messages...),
			llm.WithTools(s.tools...),
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
// Retourne le contenu textuel, les tool calls accumulés, et l'erreur éventuelle.
func (s *Service) consumeStream(
	ctx context.Context,
	stream <-chan llm.StreamChunk,
	emit func(StreamEvent),
) (content string, toolCalls []llm.ToolCall, err error) {
	toolAcc := map[int]*struct {
		id, name, params string
	}{}
	var toolOrder []int

	for chunk := range stream {
		if chunk.Error() != nil {
			return content, nil, chunk.Error()
		}
		d := chunk.Delta()
		if d == nil {
			continue
		}
		if txt := d.Content(); txt != "" {
			content += txt
			emit(StreamEvent{Type: "token", Token: txt})
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
	return content, toolCalls, nil
}

// chatOnce exécute un seul appel LLM avec streaming et retourne le résultat.
// Les tokens sont émis en temps réel via emit.
func (s *Service) chatOnce(ctx context.Context, messages []llm.Message, emit func(StreamEvent)) (string, []llm.ToolCall, error) {
	callCtx, cancel := context.WithTimeout(ctx, s.cfg.CallTimeout)
	defer cancel()

	stream, err := s.streamWithRetry(callCtx, messages)
	if err != nil {
		return "", nil, fmt.Errorf("Erreur de connexion à l'API: %s", err)
	}

	content, toolCalls, streamErr := s.consumeStream(callCtx, stream, emit)
	if streamErr != nil {
		return "", nil, streamErr
	}
	return content, toolCalls, nil
}

// Chat exécute le workflow streaming : écrit les événements dans le canal fourni.
func (s *Service) Chat(ctx context.Context, history []HistoryMessage, userMsg string) <-chan StreamEvent {
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
			llm.NewMessage(llm.RoleSystem, systemPrompt),
		}
		for _, m := range history {
			role := m.Role
			if role == "" {
				role = "user"
			}
			messages = append(messages, llm.NewMessage(llm.Role(role), m.Content))
		}
		messages = append(messages, llm.NewMessage(llm.RoleUser, userMsg))

		toolsUsed := []string{}

		for i := 0; i < s.cfg.MaxIterations; i++ {
			content, toolCalls, err := s.chatOnce(ctx, messages, func(ev StreamEvent) {
				out <- ev
			})
			_ = content
			if err != nil {
				// Retry complet sur erreur de stream
				if i < s.cfg.MaxIterations-1 {
					s.log.Warn("chat iteration retry", "err", err, "attempt", i)
					continue
				}
				out <- StreamEvent{Type: "error", Error: err.Error()}
				return
			}

			if len(toolCalls) == 0 {
				out <- StreamEvent{Type: "done", Tools: toolsUsed}
				return
			}

			// Tool calls sans contenu textuel (mimo-v2.5 reasoning casse le parse)
			messages = append(messages, llm.NewToolCallsMessage(toolCalls...))
			for _, tc := range toolCalls {
				toolsUsed = append(toolsUsed, tc.Name())
				out <- StreamEvent{Type: "tool", Tool: tc.Name()}
				msg, err := llm.ExecuteToolCall(ctx, tc, s.tools...)
				if err != nil {
					s.log.Error("tool exec", "name", tc.Name(), "err", err)
					msg = llm.NewToolMessage(tc.ID(), llm.NewToolResult(fmt.Sprintf("Tool execution error: %s", err.Error())))
				}
				messages = append(messages, msg)
			}
		}

		out <- StreamEvent{Type: "error", Error: fmt.Sprintf("nombre maximal d'itérations atteint (%d)", s.cfg.MaxIterations)}
	}()

	return out
}

// SessionStore maintient l'historique de conversation par session (en mémoire, TTL).
type SessionStore struct {
	mu       sync.Mutex
	sessions map[string]*session
	ttl      time.Duration
}

type session struct {
	messages []HistoryMessage
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

// Reset efface l'historique d'une session.
func (ss *SessionStore) Reset(sid string) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	delete(ss.sessions, sid)
}
