package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// Limits regroupe les plafonds du provider LLM, mesurés ou connus à
// l'amorçage du service.
type Limits struct {
	// Source indique l'origine des chiffres : env, probe, profile, default.
	Source string
	// MaxRequestTokens est le plafond de tokens d'une requête (0 = inconnu).
	MaxRequestTokens int
	// RequestsPerDay est le quota de requêtes par jour (0 = inconnu/illimité).
	RequestsPerDay int
}

// Budget traduit les limites du provider en bornes de construction du prompt
// (en caractères) et en nombre maximal d'itérations d'outils.
type Budget struct {
	Limits
	MaxHistoryChars    int
	MaxToolResultChars int
	MaxIterations      int
}

// Bornes de dérivation du budget.
const (
	// DefaultMaxRequestTokens sert quand le provider ne livre aucun plafond.
	DefaultMaxRequestTokens = 24000
	// charsPerToken est une approximation prudente (texte français + JSON).
	charsPerToken = 3
	// minPerRequestTokens évite des bornes absurdes sur des plafonds minuscules.
	minPerRequestTokens = 800
	// Plafonds absolus, même avec un tier payant très large.
	maxHistoryChars    = 60000
	maxToolResultChars = 20000
)

// DetectLimits détermine les plafonds du provider, en cascade : variables
// d'environnement (AI_MAX_REQUEST_TOKENS, AI_REQUESTS_PER_DAY) → sonde de
// l'endpoint (en-têtes x-ratelimit-*) → profils connus → défaut.
func DetectLimits(ctx context.Context, provider, baseURL, apiKey, model string, log *slog.Logger) Limits {
	if log == nil {
		log = slog.Default()
	}
	l := Limits{Source: "default", MaxRequestTokens: DefaultMaxRequestTokens}

	if p, ok := providerProfile(provider, model); ok {
		l = p
	}
	if baseURL != "" && apiKey != "" {
		if p, ok := probeLimits(ctx, baseURL, apiKey, model); ok {
			l = p
		}
	}
	if v, err := strconv.Atoi(os.Getenv("AI_MAX_REQUEST_TOKENS")); err == nil && v > 0 {
		l.MaxRequestTokens = v
		l.Source = "env"
	}
	if v, err := strconv.Atoi(os.Getenv("AI_REQUESTS_PER_DAY")); err == nil && v >= 0 {
		l.RequestsPerDay = v
		l.Source = "env"
	}
	if l.MaxRequestTokens <= 0 {
		l.MaxRequestTokens = DefaultMaxRequestTokens
	}
	log.Debug("limites LLM détectées", "source", l.Source,
		"max_request_tokens", l.MaxRequestTokens, "requests_per_day", l.RequestsPerDay)
	return l
}

// providerProfile retourne le profil connu d'un provider (valeurs du tier
// gratuit), utilisé quand la sonde ne fournit rien.
func providerProfile(provider, model string) (Limits, bool) {
	switch provider {
	case "groq":
		// Free tier : 8K TPM, 1K RPD par modèle de chat.
		return Limits{Source: "profile", MaxRequestTokens: 8000, RequestsPerDay: 1000}, true
	case "gemini":
		// Free tier : 20 RPD sur les Flash, 500 RPD sur les Flash-Lite.
		rpd := 20
		if strings.Contains(model, "flash-lite") {
			rpd = 500
		}
		return Limits{Source: "profile", RequestsPerDay: rpd}, true
	}
	return Limits{}, false
}

// probeLimits envoie une requête minimale et lit les en-têtes de rate limit
// (x-ratelimit-limit-tokens = TPM, x-ratelimit-limit-requests = RPD). Les
// en-têtes sont présents même sur les réponses d'erreur (429).
func probeLimits(ctx context.Context, baseURL, apiKey, model string) (Limits, bool) {
	payload, err := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "."}},
		"max_tokens": 1,
	})
	if err != nil {
		return Limits{}, false
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost,
		strings.TrimRight(baseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return Limits{}, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return Limits{}, false
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
	}()

	l := Limits{Source: "probe"}
	found := false
	if v, err := strconv.Atoi(resp.Header.Get("x-ratelimit-limit-tokens")); err == nil && v > 0 {
		l.MaxRequestTokens = v
		found = true
	}
	if v, err := strconv.Atoi(resp.Header.Get("x-ratelimit-limit-requests")); err == nil && v > 0 {
		l.RequestsPerDay = v
		found = true
	}
	return l, found
}

// NewBudget dérive les bornes de prompt des limites du provider. Le plafond
// de requête est divisé par 2 pour laisser passer plusieurs appels d'outils
// dans la même fenêtre d'une minute.
func NewBudget(l Limits) *Budget {
	tok := l.MaxRequestTokens
	if tok <= 0 {
		tok = DefaultMaxRequestTokens
	}
	perReq := tok / 2
	if perReq < minPerRequestTokens {
		perReq = minPerRequestTokens
	}

	b := &Budget{Limits: l}
	b.MaxHistoryChars = perReq * 50 / 100 * charsPerToken
	b.MaxToolResultChars = perReq * 25 / 100 * charsPerToken
	if b.MaxHistoryChars > maxHistoryChars {
		b.MaxHistoryChars = maxHistoryChars
	}
	if b.MaxToolResultChars > maxToolResultChars {
		b.MaxToolResultChars = maxToolResultChars
	}

	b.MaxIterations = 4
	switch {
	case l.RequestsPerDay > 0 && l.RequestsPerDay <= 50:
		b.MaxIterations = 2 // quota extrême : économiser les appels
	case l.RequestsPerDay > 0 && l.RequestsPerDay <= 200:
		b.MaxIterations = 3
	}
	return b
}
