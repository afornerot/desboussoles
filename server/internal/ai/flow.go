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
	// Store permet de résoudre la localisation de la conversation en zone
	// géographique (recherche carte). Optionnel.
	Store *store.Store
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
			s.verifyMapEstablishments(ctx, userMsg, finalContent, out)
		}
	}()

	return out
}

// recommendedStructure est une structure citée par la réponse : son nom, la
// ville annoncée et le téléphone indiqué (clé d'identification fiable entre le
// guide et la carte, dont les intitulés diffèrent).
type recommendedStructure struct {
	Nom       string `json:"nom"`
	Ville     string `json:"ville"`
	Telephone string `json:"telephone"`
}

// extractionResult porte les structures citées et les données de la personne
// APRÈS la conversation (localisation, âge, adresse — qui peuvent différer du
// questionnaire).
type extractionResult struct {
	Structures   []recommendedStructure
	Localisation string
	Age          string
	Adresse      string
}

// verifyMapEstablishments est la PASSE 2 : après la réponse du guide, on
// extrait les structures citées (nom + ville, un seul appel LLM sans outils)
// puis on vérifie leur présence dans les ÉTABLISSEMENTS de la carte. Le
// panneau reflète les recommandations de la réponse : au mieux une entrée par
// structure, et rien plutôt qu'une correspondance douteuse.
func (s *Service) verifyMapEstablishments(ctx context.Context, userMsg, response string, out chan StreamEvent) {
	mapTools := filterTools(s.tools, []string{"map_search", "map_nearby"})
	if len(mapTools) == 0 {
		return
	}
	tr := TraceFrom(ctx)
	log := s.log.With("trace", traceID(tr))
	start := time.Now()

	res := s.extractStructures(ctx, userMsg, response)

	// Âge, localisation et adresse de la conversation : ils priment sur le
	// questionnaire pour ce tour (et sont retenus pour les suivants via la trace).
	if res.Age != "" {
		tr.SetAge(res.Age, "conversation")
	}
	if res.Adresse != "" {
		tr.SetAddr(res.Adresse, "conversation")
	}

	// Zone du tour : la localisation de la conversation prime sur celle du
	// questionnaire, et l'adresse précise (géocodée) sert de référence de
	// proximité — la commune garde son rôle pour l'appartenance (ST_Contains).
	if s.cfg.Store != nil && (res.Localisation != "" || res.Adresse != "") {
		zoneName := res.Localisation
		if zoneName == "" {
			if cur := store.ZoneFrom(ctx); cur != nil {
				zoneName = cur.Name
			}
		}
		z := &store.UserZone{Name: zoneName}
		if zoneName != "" && store.NormalizeName(zoneName) != "" {
			if zi := s.cfg.Store.UserZoneInfo(ctx, zoneName); zi != nil {
				z = zi
			}
		}
		if res.Adresse != "" {
			if g, ok := GeocodeAddress(ctx, res.Adresse); ok {
				z.Center = [2]float64{g.Lng, g.Lat}
			}
		}
		ctx = store.WithZone(ctx, z)
		tr.SetZone(z.Name, "conversation", len(z.In))
	}

	structs := res.Structures
	var allEsts []Establishment
	seen := map[string]bool{}
	for i, st := range structs {
		params, _ := json.Marshal(map[string]any{
			"query": st.Nom, "category": "etablissement", "limit": 5,
		})
		tc := llm.NewToolCall(fmt.Sprintf("map-%d", i), "map_search", string(params))
		startTool := time.Now()
		msg, err := llm.ExecuteToolCall(ctx, tc, mapTools...)
		durTool := time.Since(startTool).Milliseconds()
		tr.AddTool("map_search", durTool)
		if err != nil {
			log.Error("tool exec", "name", "map_search", "query", st.Nom, "err", err)
			continue
		}
		log.Info("tool timing", "pass", "map", "name", "map_search", "query", st.Nom,
			"ville", st.Ville, "duration_ms", durTool, "out_chars", len(msg.Content()))
		e, ok := pickRecommended(st, extractEstablishments("map_search", msg.Content()))
		if !ok {
			log.Info("map match refuse", "nom", st.Nom, "ville", st.Ville)
			continue
		}
		key := estabKey(e)
		if !seen[key] {
			seen[key] = true
			allEsts = append(allEsts, e)
		}
	}

	// Repli « structures proches de chez vous » : uniquement si le panneau
	// serait vide (les recommandations priment).
	if len(allEsts) == 0 {
		if z := store.ZoneFrom(ctx); z != nil {
			allEsts = s.nearbyFallback(ctx, z, mapTools, tr, log)
		}
	}

	mapMs := time.Since(start).Milliseconds()
	tr.SetMapMs(mapMs)
	tr.SetMapResult(len(structs), len(allEsts))
	zoneName := ""
	if z := store.ZoneFrom(ctx); z != nil {
		zoneName = z.Name
	}
	log.Info("map verify done", "duration_ms", mapMs, "names", len(structs),
		"establishments", len(allEsts), "zone", zoneName,
		"nearby_fallback", tr.Snapshot().NearbyFallback)
	if len(allEsts) > 0 {
		out <- StreamEvent{Type: "establishments", Establishments: allEsts}
	}
}

// pickRecommended retourne la correspondance crédible pour une structure
// recommandée.
//
//  1. Le TÉLÉPHONE décide s'il est connu : les intitulés diffèrent entre le
//     guide et la carte, le numéro est la clé fiable. Aucun candidat au bon
//     numéro → rien (la structure n'est pas dans la carte).
//  2. Sinon le NOM décide (meilleur score ≥ 2 mots significatifs ou une
//     inclusion) : le classement de la recherche, qui mélange proximité et
//     nom, ne doit pas laisser un voisin géographique voler la place.
//
// Dans les deux cas, la ville annoncée doit être celle de l'adresse (après le
// code postal) : « rue de Morteau » à Pontarlier n'est pas « Morteau ».
func pickRecommended(st recommendedStructure, ests []Establishment) (Establishment, bool) {
	// 1. Le téléphone est un signal fort s'il correspond : les intitulés
	// diffèrent entre le guide et la carte, le numéro identifie la structure.
	// Un numéro inconnu n'élimine PAS — les annuaires divergent (standard vs
	// ligne directe) et certains établissements n'ont pas de numéro en base.
	if phone := store.NormalizePhone(st.Telephone); len(phone) >= 9 {
		for _, e := range ests {
			if store.SamePhone(e.Phone, st.Telephone) && store.SameCity(e.Address, st.Ville) {
				return e, true
			}
		}
	}
	// 2. Le NOM décide (la ville départage à score égal, via l'ordre de la
	// recherche). Un candidat est crédible s'il couvre tous les mots spécifiques
	// de la recommandation, ou s'il partage assez de vocabulaire malgré des
	// intitulés différents (score ≥ 25) — en dessous, c'est un voisin, pas la
	// structure.
	const minNameScore = 18
	const strongNameScore = 25
	best := Establishment{}
	bestScore := -1
	for _, e := range ests {
		if !store.SameCity(e.Address, st.Ville) {
			continue
		}
		sc := store.MatchScore(st.Nom, e.Name, e.Title, e.Address)
		if sc < minNameScore || sc <= bestScore {
			continue
		}
		if sc < strongNameScore && !store.SpecificWordsCovered(st.Nom, e.Name, e.Title, e.Address) {
			continue
		}
		bestScore, best = sc, e
	}
	if bestScore < 0 {
		return Establishment{}, false
	}
	return best, true
}

// nearbyFallback élargit progressivement la recherche autour de la zone de
// l'utilisateur (10 → 30 → 60 → 100 km) et retourne les structures proches.
func (s *Service) nearbyFallback(ctx context.Context, z *store.UserZone, mapTools []llm.Tool, tr *ChatTrace, log *slog.Logger) []Establishment {
	for _, radius := range []float64{10, 30, 60, 100} {
		params, _ := json.Marshal(map[string]any{
			"lng": z.Center[0], "lat": z.Center[1],
			"radius_km": radius, "category": "etablissement", "limit": 8,
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

// extractStructures demande au modèle les structures citées et les données de
// la personne (JSON, sans outils) — un seul appel, sortie courte. Le message
// de la personne fait partie de l'entrée : âge, localisation ou adresse y sont
// souvent annoncés sans être repris dans la réponse.
func (s *Service) extractStructures(ctx context.Context, userMsg, response string) extractionResult {
	content, _, toolCalls, err := s.chatOnceTools(ctx, []llm.Message{
		llm.NewMessage(llm.RoleSystem, SystemPromptExtractNames),
		llm.NewMessage(llm.RoleUser,
			"Message de la personne :\n"+userMsg+"\n\nRéponse de l'assistant :\n"+response),
	}, nil)
	if err != nil {
		s.log.Warn("extract structures failed", "trace", traceID(TraceFrom(ctx)), "err", err)
		return extractionResult{}
	}
	if len(toolCalls) > 0 {
		return extractionResult{}
	}
	return decodeStructures(content)
}

// decodeStructures parse le JSON {"structures": [{nom, ville}], "localisation",
// "age"} renvoyé par le modèle, tolérant aux délimiteurs markdown.
func decodeStructures(content string) extractionResult {
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return extractionResult{}
	}
	var parsed struct {
		Structures   []recommendedStructure `json:"structures"`
		Localisation string                 `json:"localisation"`
		Age          string                 `json:"age"`
		Adresse      string                 `json:"adresse"`
		Names        []string               `json:"names"` // ancien format, toléré
	}
	if err := json.Unmarshal([]byte(content[start:end+1]), &parsed); err != nil {
		return extractionResult{}
	}
	out := extractionResult{
		Localisation: strings.TrimSpace(parsed.Localisation),
		Age:          strings.TrimSpace(parsed.Age),
		Adresse:      strings.TrimSpace(parsed.Adresse),
	}
	seen := map[string]bool{}
	add := func(st recommendedStructure) {
		st.Nom = strings.TrimSpace(st.Nom)
		if st.Nom == "" || seen[st.Nom] {
			return
		}
		seen[st.Nom] = true
		out.Structures = append(out.Structures, st)
	}
	for _, st := range parsed.Structures {
		add(st)
		if len(out.Structures) == 6 {
			return out
		}
	}
	for _, n := range parsed.Names {
		add(recommendedStructure{Nom: n})
		if len(out.Structures) == 6 {
			return out
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
