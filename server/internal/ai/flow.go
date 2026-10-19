package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bornholm/genai/llm"
)

// Config règle la boucle d'outils et le timeout d'appel LLM.
type Config struct {
	MaxIterations int
	CallTimeout   time.Duration
	// Budget porte les bornes dérivées des limites du provider (taille des
	// résultats d'outils, historique). Optionnel mais recommandé.
	Budget *Budget
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

// forceFinalAnswer demande au modèle une synthèse finale sans outils, en
// dernier recours (boucle d'outils ou itérations épuisées). Retourne le texte
// généré, ou "" si la tentative échoue.
func (s *Service) forceFinalAnswer(ctx context.Context, messages []llm.Message, out chan StreamEvent) string {
	messages = append(messages, llm.NewMessage(llm.RoleAssistant,
		"Tu as déjà effectué toutes les recherches nécessaires. Réponds maintenant directement avec les informations dont tu disposes, sans appeler d'outil."))
	_, ftokens, ftoolCalls, ferr := s.chatOnceTools(ctx, messages, nil)
	if ferr != nil || len(ftoolCalls) > 0 {
		if ferr != nil {
			s.log.Warn("forced final answer failed", "err", ferr)
		}
		return ""
	}
	content := strings.Join(ftokens, "")
	for _, t := range ftokens {
		out <- StreamEvent{Type: "token", Token: t}
	}
	return content
}

// capToolMessage borne la taille d'un résultat d'outil avant son rejouage
// dans la requête suivante : un outil verbeux (guide_search) ferait exploser
// le nombre de tokens envoyés et déclencherait un refus du provider.
func (s *Service) capToolMessage(msg llm.Message) llm.Message {
	limit := maxToolResultChars
	if s.cfg.Budget != nil && s.cfg.Budget.MaxToolResultChars > 0 {
		limit = s.cfg.Budget.MaxToolResultChars
	}
	content := msg.Content()
	if len(content) <= limit {
		return msg
	}
	id := ""
	if tm, ok := msg.(llm.ToolMessage); ok {
		id = tm.ID()
	}
	return llm.NewToolMessage(id, llm.NewToolResult(content[:limit]+"\n… (résultat tronqué)"))
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
				sendFallback(out)
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
				s.log.Error("chat error", "err", err)
				sendFallback(out)
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
				if content := s.forceFinalAnswer(ctx, messages, out); content != "" {
					finalContent = content
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
				messages = append(messages, s.capToolMessage(msg))
			}
			if i == s.cfg.MaxIterations-1 {
				// Itérations épuisées : demander une synthèse finale sans
				// outils plutôt que d'échouer — le budget du provider peut
				// être trop juste pour une exploration complète.
				s.log.Warn("max iterations reached, forcing final answer", "iterations", s.cfg.MaxIterations)
				if content := s.forceFinalAnswer(ctx, messages, out); content != "" {
					finalContent = content
					break
				}
				s.log.Error("chat error", "err", "max iterations sans réponse exploitable")
				sendFallback(out)
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

	mapIterations := s.cfg.MaxIterations
	if mapIterations > 3 {
		mapIterations = 3
	}
	for i := 0; i < mapIterations; i++ {
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
			messages = append(messages, s.capToolMessage(msg))
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
		Results []Establishment `json:"results"`
	}
	if err := json.Unmarshal([]byte(resultContent), &resp); err != nil {
		return nil
	}
	ests := make([]Establishment, 0, len(resp.Results))
	for _, r := range resp.Results {
		if r.Name == "" {
			continue
		}
		ests = append(ests, r)
	}
	return ests
}
