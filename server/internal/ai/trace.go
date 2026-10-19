package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// trace.go — observabilité du pipeline chat : un identifiant par tour de
// conversation, des métriques accumulées (durées, tokens, tailles) et une
// trace JSONL exploitable a posteriori (jq, tableurs).
//
// Contenu des messages : jamais loggé par défaut (conversations santé
// mentale). AI_LOG_CONTENT=1 ajoute de courts extraits pour le débogage.

// ChatTrace cumule les métriques d'un tour de conversation. Thread-safe : le
// client LLM (goroutine de lecture) et la boucle de chat y écrivent.
type ChatTrace struct {
	mu sync.Mutex
	ChatTraceData
}

// ChatTraceData porte les métriques sérialisables d'un tour (JSONL, logs).
type ChatTraceData struct {
	TraceID  string    `json:"trace_id"`
	Space    string    `json:"space"` // public | pro
	Provider string    `json:"provider,omitempty"`
	Model    string    `json:"model,omitempty"`
	Start    time.Time `json:"start"`

	// Contexte d'entrée.
	HistoryMsgs   int `json:"history_msgs"`
	HistoryChars  int `json:"history_chars"`
	UserChars     int `json:"user_chars"`
	GuideCtxChars int `json:"guide_ctx_chars"`
	ToolsCount    int `json:"tools"`

	// Appels LLM.
	LLMCalls     int   `json:"llm_calls"`
	TTFTMs       int64 `json:"ttft_ms"` // temps avant le 1er octet utile du 1er appel
	LLMStreamMs  int64 `json:"llm_stream_ms"`
	LLMRetries   int   `json:"retries"`
	TokensIn     int64 `json:"tokens_in"`
	TokensOut    int64 `json:"tokens_out"`
	CharsSent    int   `json:"chars_sent"`
	CharsRecv    int   `json:"chars_recv"`

	// Outils.
	ToolCallsCount int      `json:"tool_calls"`
	ToolsUsed      []string `json:"tools_used,omitempty"`
	ToolMs         int64    `json:"tool_ms"`

	// Sortie utilisateur.
	SSEEvents   int   `json:"sse_events"`
	SSEBytes    int   `json:"sse_bytes"`
	CharsOut    int   `json:"chars_out"`
	FirstTokMs  int64 `json:"first_token_ms"`
	TTFBMs      int64 `json:"http_ttfb_ms"`
	ClientGone  bool  `json:"client_gone"`

	// Comportements de repli et bornes.
	Truncations int    `json:"truncations"`
	ForcedFinal bool   `json:"forced_final_answer"`
	Fallback    bool   `json:"fallback"`
	FallbackWhy string `json:"fallback_reason,omitempty"`

	// Chronométrie du tour.
	GuideMs int64 `json:"guide_pass_ms"`
	MapMs   int64 `json:"map_pass_ms"`
	TotalMs int64 `json:"total_ms"`

	// Passe carte v2 : noms extraits de la réponse, puis retrouvés en base.
	MapNames   int `json:"map_names"`
	MapMatched int `json:"map_matched"`
	// Repli « structures proches de chez vous » (élargissement de rayon).
	NearbyFallback   bool    `json:"nearby_fallback,omitempty"`
	NearbyRadiusKm   float64 `json:"nearby_radius_km,omitempty"`

	// Zone utilisateur (commune) et âge courants pour le ciblage.
	Zone       string `json:"zone,omitempty"`
	ZoneSource string `json:"zone_source,omitempty"` // base | geocode | conversation | none
	ZoneInside int    `json:"zone_inside,omitempty"` // entités contenues dans la zone
	Age        string `json:"age,omitempty"`
	AgeSource  string `json:"age_source,omitempty"` // questionnaire | conversation
	Addr       string `json:"addr,omitempty"`       // adresse précise (proximité)
	AddrSource string `json:"addr_source,omitempty"`

	// Extraits de contenu — uniquement si AI_LOG_CONTENT=1.
	UserExcerpt   string `json:"user_excerpt,omitempty"`
	AnswerExcerpt string `json:"answer_excerpt,omitempty"`
}

// NewChatTrace crée la trace d'un tour.
func NewChatTrace(id, space string) *ChatTrace {
	return &ChatTrace{ChatTraceData: ChatTraceData{
		TraceID:  id,
		Space:    space,
		Start:    time.Now(),
		Provider: os.Getenv("AI_PROVIDER"),
		Model:    os.Getenv("AI_MODEL"),
	}}
}

// NewTraceID génère un identifiant court de tour.
func NewTraceID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type traceKey struct{}

// WithTrace place une trace dans le contexte.
func WithTrace(ctx context.Context, tr *ChatTrace) context.Context {
	return context.WithValue(ctx, traceKey{}, tr)
}

// TraceFrom récupère la trace du contexte (nil si absente).
func TraceFrom(ctx context.Context) *ChatTrace {
	tr, _ := ctx.Value(traceKey{}).(*ChatTrace)
	return tr
}

// traceID renvoie l'identifiant de tour (chaîne vide si absente).
func traceID(tr *ChatTrace) string {
	if tr == nil {
		return ""
	}
	return tr.TraceID
}

// approxTokens estime un nombre de tokens à partir de caractères (≈3 car/token).
func approxTokens(chars int) int { return chars / 3 }

// AddLLM enregistre un appel LLM (côté client HTTP).
func (t *ChatTrace) AddLLM(ttftMs, streamMs int64, charsSent, charsRecv int, tokensIn, tokensOut int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.LLMCalls++
	if t.TTFTMs == 0 {
		t.TTFTMs = ttftMs
	}
	t.LLMStreamMs += streamMs
	t.CharsSent += charsSent
	t.CharsRecv += charsRecv
	t.TokensIn += tokensIn
	t.TokensOut += tokensOut
}

// AddRetry compte une nouvelle tentative d'ouverture de stream.
func (t *ChatTrace) AddRetry() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.LLMRetries++
}

// AddTool enregistre un appel d'outil.
func (t *ChatTrace) AddTool(name string, durationMs int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ToolCallsCount++
	t.ToolMs += durationMs
	for _, n := range t.ToolsUsed {
		if n == name {
			return
		}
	}
	t.ToolsUsed = append(t.ToolsUsed, name)
}

// AddTruncation compte une troncature imposée par le budget.
func (t *ChatTrace) AddTruncation() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Truncations++
}

// MarkFallback signale l'envoi du message de repli.
func (t *ChatTrace) MarkFallback(reason string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Fallback = true
	t.FallbackWhy = reason
}

// SetMapResult enregistre le bilan de la passe carte (noms extraits, retrouvés).
func (t *ChatTrace) SetMapResult(names, matched int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.MapNames, t.MapMatched = names, matched
}

// SetZone enregistre la zone utilisateur (commune, origine, entités contenues).
func (t *ChatTrace) SetZone(name, source string, inside int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Zone, t.ZoneSource, t.ZoneInside = name, source, inside
}

// SetAge enregistre l'âge courant et son origine.
func (t *ChatTrace) SetAge(age, source string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Age, t.AgeSource = age, source
}

// SetAddr enregistre l'adresse précise (proximité) et son origine.
func (t *ChatTrace) SetAddr(addr, source string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.Addr, t.AddrSource = addr, source
}

// MarkNearbyFallback signale le repli « structures proches » et son rayon.
func (t *ChatTrace) MarkNearbyFallback(radiusKm float64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.NearbyFallback = true
	t.NearbyRadiusKm = radiusKm
}

// MarkForcedFinal signale une synthèse forcée en fin de boucle d'outils.
func (t *ChatTrace) MarkForcedFinal() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ForcedFinal = true
}

// Finish clôt la chronométrie du tour.
func (t *ChatTrace) Finish() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.TotalMs = time.Since(t.Start).Milliseconds()
}

// SetIntro enregistre le contexte d'entrée du tour.
func (t *ChatTrace) SetIntro(historyMsgs, historyChars, userChars, tools int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.HistoryMsgs, t.HistoryChars = historyMsgs, historyChars
	t.UserChars, t.ToolsCount = userChars, tools
}

// SetGuideCtx enregistre la taille du contexte questionnaire injecté.
func (t *ChatTrace) SetGuideCtx(chars int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.GuideCtxChars = chars
}

// SetGuideMs enregistre la durée de la passe guide.
func (t *ChatTrace) SetGuideMs(ms int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.GuideMs = ms
}

// SetMapMs enregistre la durée de la passe carte.
func (t *ChatTrace) SetMapMs(ms int64) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.MapMs = ms
}

// SetOutput enregistre la sortie utilisateur (SSE + premier token).
func (t *ChatTrace) SetOutput(events, bytes, chars int, firstTokMs, ttfbMs int64, clientGone bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.SSEEvents, t.SSEBytes, t.CharsOut = events, bytes, chars
	t.FirstTokMs, t.TTFBMs, t.ClientGone = firstTokMs, ttfbMs, clientGone
}

// SetExcerpts enregistre les extraits de contenu (si AI_LOG_CONTENT=1).
func (t *ChatTrace) SetExcerpts(user, answer string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.UserExcerpt, t.AnswerExcerpt = user, answer
}

// Snapshot renvoie une copie figée des métriques (pour log et JSONL).
func (t *ChatTrace) Snapshot() ChatTraceData {
	if t == nil {
		return ChatTraceData{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ChatTraceData
}

// contentLogging indique si les extraits de contenu sont autorisés.
func contentLogging() bool {
	v := os.Getenv("AI_LOG_CONTENT")
	return v == "1" || strings.EqualFold(v, "true")
}

// Excerpt renvoie un extrait de contenu si AI_LOG_CONTENT=1, sinon "".
func Excerpt(s string, max int) string {
	if !contentLogging() {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// --- trace JSONL ---

var (
	traceMu sync.Mutex
	traceW  *os.File
)

// SetTraceFile ouvre (et crée) le fichier de traces JSONL. Chaîne vide ou
// erreur = désactivé (les logs Docker restent la source de vérité).
func SetTraceFile(path string) error {
	traceMu.Lock()
	defer traceMu.Unlock()
	if traceW != nil {
		_ = traceW.Close()
		traceW = nil
	}
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	traceW = f
	return nil
}

// WriteTrace ajoute une ligne JSON au fichier de traces (no-op si désactivé).
func WriteTrace(t *ChatTrace) {
	if t == nil {
		return
	}
	traceMu.Lock()
	defer traceMu.Unlock()
	if traceW == nil {
		return
	}
	line, err := json.Marshal(t.Snapshot())
	if err != nil {
		return
	}
	_, _ = traceW.Write(append(line, '\n'))
}
