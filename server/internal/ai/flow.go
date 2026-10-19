package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/bornholm/genai/llm"

	"boussole/internal/store"
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
// maxRetryWait borne l'attente entre deux tentatives d'appel LLM.
const maxRetryWait = 25 * time.Second

func (s *Service) streamWithRetryTools(ctx context.Context, messages []llm.Message, tools []llm.Tool) (<-chan llm.StreamChunk, error) {
	tr := TraceFrom(ctx)
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
		tr.AddRetry()
		// Attendre le délai annoncé par le serveur (429 quota) quand il existe,
		// plutôt qu'un backoff fixe qui échouerait dans le vide.
		wait := time.Duration(attempt+1) * 500 * time.Millisecond
		var he *HTTPStatusError
		if errors.As(err, &he) && he.RetryAfter > 0 {
			wait = he.RetryAfter + 250*time.Millisecond
		}
		if wait > maxRetryWait {
			wait = maxRetryWait
		}
		s.log.Warn("stream retry", "trace", traceID(tr), "attempt", attempt+1,
			"wait_ms", wait.Milliseconds(), "err", err)
		time.Sleep(wait)
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
	tr := TraceFrom(ctx)
	tr.MarkForcedFinal()
	messages = append(messages, llm.NewMessage(llm.RoleAssistant,
		"Tu as déjà effectué toutes les recherches nécessaires. Réponds maintenant directement avec les informations dont tu disposes, sans appeler d'outil."))
	_, ftokens, ftoolCalls, ferr := s.chatOnceTools(ctx, messages, nil)
	if ferr != nil || len(ftoolCalls) > 0 {
		if ferr != nil {
			s.log.Warn("forced final answer failed", "trace", traceID(tr), "err", ferr)
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
func (s *Service) capToolMessage(ctx context.Context, msg llm.Message) llm.Message {
	limit := maxToolResultChars
	if s.cfg.Budget != nil && s.cfg.Budget.MaxToolResultChars > 0 {
		limit = s.cfg.Budget.MaxToolResultChars
	}
	content := msg.Content()
	if len(content) <= limit {
		return msg
	}
	tr := TraceFrom(ctx)
	tr.AddTruncation()
	s.log.Warn("budget cap", "trace", traceID(tr), "what", "tool_result",
		"from_chars", len(content), "to_chars", limit)
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
		tr := TraceFrom(ctx)
		if tr == nil {
			tr = NewChatTrace(NewTraceID(), "")
			ctx = WithTrace(ctx, tr)
		}
		log := s.log.With("trace", tr.TraceID)
		tr.SetIntro(len(history), historyChars(history), len(userMsg), len(s.tools))
		log.Info("chat start", "space", tr.Space, "history_msgs", len(history),
			"history_chars", historyChars(history), "user_chars", len(userMsg),
			"guide_ctx_chars", tr.GuideCtxChars, "tools", len(s.tools))
		defer func() {
			if r := recover(); r != nil {
				log.Error("chat panic", "panic", r)
				sendFallback(out, tr, "panic")
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

		// Passe guide bornée à 3 itérations d'outils : au-delà, la boucle
		// coûte plus qu'elle ne rapporte (latence + fenêtre minute du provider).
		guideIterations := s.cfg.MaxIterations
		if guideIterations > 3 {
			guideIterations = 3
		}

		for i := 0; i < guideIterations; i++ {
			startLLM := time.Now()
			content, tokens, toolCalls, err := s.chatOnceTools(ctx, messages, guideTools)
			log.Info("llm timing", "pass", "guide", "iteration", i,
				"duration_ms", time.Since(startLLM).Milliseconds(),
				"tool_calls", len(toolCalls), "out_chars", len(content))
			if err != nil {
				if i < guideIterations-1 {
					log.Warn("chat iteration retry", "err", err, "attempt", i)
					continue
				}
				log.Error("chat error", "err", err)
				sendFallback(out, tr, "llm_error")
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
				log.Warn("guide pass looping detected, forcing final answer", "iterations", i)
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
				durTool := time.Since(startTool).Milliseconds()
				tr.AddTool(tc.Name(), durTool)
				if err != nil {
					log.Error("tool exec", "name", tc.Name(), "err", err)
					msg = llm.NewToolMessage(tc.ID(), llm.NewToolResult(fmt.Sprintf("Tool execution error: %s", err.Error())))
				}
				log.Info("tool timing", "pass", "guide", "name", tc.Name(),
					"duration_ms", durTool, "out_chars", len(msg.Content()))
				messages = append(messages, s.capToolMessage(ctx, msg))
			}
			if i == guideIterations-1 {
				// Itérations épuisées : demander une synthèse finale sans
				// outils plutôt que d'échouer — le budget du provider peut
				// être trop juste pour une exploration complète.
				log.Warn("max iterations reached, forcing final answer", "iterations", guideIterations)
				if content := s.forceFinalAnswer(ctx, messages, out); content != "" {
					finalContent = content
					break
				}
				log.Error("chat error", "err", "max iterations sans réponse exploitable")
				sendFallback(out, tr, "max_iterations")
				return
			}
		}

		guideMs := time.Since(startAll).Milliseconds()
		out <- StreamEvent{Type: "done", Tools: toolsUsed}
		tr.SetGuideMs(guideMs)
		snap := tr.Snapshot()
		log.Info("chat done", "pass", "guide", "total_ms", guideMs, "tools", toolsUsed,
			"llm_calls", snap.LLMCalls, "tokens_in", snap.TokensIn,
			"tokens_out", snap.TokensOut, "truncations", snap.Truncations)

		// --- Pass 2 : vérification carte (extraction de noms + map_search) ---
		// Passe légère, toujours exécutée : la réponse est déjà livrée et un
		// 429 éventuel est géré par le backoff retry-after.
		if finalContent != "" {
			s.verifyMapEstablishments(ctx, finalContent, out)
		}
	}()

	return out
}

// verifyMapEstablishments est la PASSE 2 : après la réponse du guide, on
// extrait les noms de structures citées (un seul appel LLM, sans outils) puis
// on vérifie leur présence sur la carte par map_search (PostGIS). Seuls ceux
// trouvés sont émis en événements « establishments ». Passe légère : elle
// s'exécute toujours, contrairement à l'ancienne boucle d'outils.
func (s *Service) verifyMapEstablishments(ctx context.Context, response string, out chan StreamEvent) {
	mapTools := filterTools(s.tools, []string{"map_search", "map_nearby"})
	if len(mapTools) == 0 {
		return
	}
	tr := TraceFrom(ctx)
	log := s.log.With("trace", traceID(tr))
	start := time.Now()

	names := s.extractEstablishmentNames(ctx, response)
	var allEsts []Establishment
	seen := map[string]bool{}
	for i, name := range names {
		params, _ := json.Marshal(map[string]string{"query": name})
		tc := llm.NewToolCall(fmt.Sprintf("map-%d", i), "map_search", string(params))
		startTool := time.Now()
		msg, err := llm.ExecuteToolCall(ctx, tc, mapTools...)
		durTool := time.Since(startTool).Milliseconds()
		tr.AddTool("map_search", durTool)
		if err != nil {
			log.Error("tool exec", "name", "map_search", "query", name, "err", err)
			continue
		}
		log.Info("tool timing", "pass", "map", "name", "map_search", "query", name,
			"duration_ms", durTool, "out_chars", len(msg.Content()))
		for _, e := range extractEstablishments("map_search", msg.Content()) {
			key := estabKey(e)
			if !seen[key] {
				seen[key] = true
				allEsts = append(allEsts, e)
			}
		}
	}

	// Repli « structures proches de chez vous » : si aucun nom n'est retrouvé
	// en carte, élargir autour de la zone de l'utilisateur plutôt que de
	// laisser le panneau vide.
	if len(allEsts) == 0 {
		if z := store.ZoneFrom(ctx); z != nil {
			allEsts = s.nearbyFallback(ctx, z, mapTools, tr, log)
		}
	}

	mapMs := time.Since(start).Milliseconds()
	tr.SetMapMs(mapMs)
	tr.SetMapResult(len(names), len(allEsts))
	zoneName := ""
	if z := store.ZoneFrom(ctx); z != nil {
		zoneName = z.Name
	}
	log.Info("map verify done", "duration_ms", mapMs, "names", len(names),
		"establishments", len(allEsts), "zone", zoneName,
		"nearby_fallback", tr.Snapshot().NearbyFallback)
	if len(allEsts) > 0 {
		out <- StreamEvent{Type: "establishments", Establishments: allEsts}
	}
}

// nearbyFallback élargit progressivement la recherche autour de la zone de
// l'utilisateur (10 → 30 → 60 → 100 km) et retourne les structures proches.
func (s *Service) nearbyFallback(ctx context.Context, z *store.UserZone, mapTools []llm.Tool, tr *ChatTrace, log *slog.Logger) []Establishment {
	for _, radius := range []float64{10, 30, 60, 100} {
		params, _ := json.Marshal(map[string]any{
			"lng": z.Center[0], "lat": z.Center[1],
			"radius_km": radius, "limit": 8,
		})
		tc := llm.NewToolCall(fmt.Sprintf("nearby-%.0f", radius), "map_nearby", string(params))
		startTool := time.Now()
		msg, err := llm.ExecuteToolCall(ctx, tc, mapTools...)
		durTool := time.Since(startTool).Milliseconds()
		tr.AddTool("map_nearby", durTool)
		if err != nil {
			log.Warn("nearby fallback", "radius_km", radius, "err", err)
			continue
		}
		ests := extractEstablishments("map_nearby", msg.Content())
		if len(ests) > 0 {
			tr.MarkNearbyFallback(radius)
			log.Info("nearby fallback", "radius_km", radius, "establishments", len(ests))
			return ests
		}
	}
	return nil
}

// extractEstablishmentNames demande au modèle les noms de structures cités
// dans la réponse (JSON, sans outils) — un seul appel, sortie courte.
func (s *Service) extractEstablishmentNames(ctx context.Context, response string) []string {
	content, _, toolCalls, err := s.chatOnceTools(ctx, []llm.Message{
		llm.NewMessage(llm.RoleSystem, SystemPromptExtractNames),
		llm.NewMessage(llm.RoleUser, "Réponse à analyser :\n"+response),
	}, nil)
	if err != nil {
		s.log.Warn("extract names failed", "trace", traceID(TraceFrom(ctx)), "err", err)
		return nil
	}
	if len(toolCalls) > 0 {
		return nil
	}
	return decodeNames(content)
}

// decodeNames parse le JSON {"names": [...]} renvoyé par le modèle, tolérant
// aux éventuels délimiteurs markdown autour du JSON.
func decodeNames(content string) []string {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return nil
	}
	var parsed struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &parsed); err != nil {
		return nil
	}
	out := make([]string, 0, 6)
	seen := map[string]bool{}
	for _, n := range parsed.Names {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
		if len(out) == 6 {
			break
		}
	}
	return out
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
