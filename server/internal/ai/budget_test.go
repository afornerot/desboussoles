package ai

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewBudget(t *testing.T) {
	cases := []struct {
		name      string
		limits    Limits
		wantHist  int
		wantTool  int
		wantIter  int
	}{
		{
			name:     "groq gratuit (8K TPM, 1K RPD)",
			limits:   Limits{MaxRequestTokens: 8000, RequestsPerDay: 1000},
			wantHist: 8000 / 2 * 50 / 100 * charsPerToken,
			wantTool: 8000 / 2 * 25 / 100 * charsPerToken,
			wantIter: 4,
		},
		{
			name:     "quota moyen — itérations réduites",
			limits:   Limits{MaxRequestTokens: 8000, RequestsPerDay: 150},
			wantHist: 8000 / 2 * 50 / 100 * charsPerToken,
			wantTool: 8000 / 2 * 25 / 100 * charsPerToken,
			wantIter: 3,
		},
		{
			name:     "tier payant large — plafonds absolus",
			limits:   Limits{MaxRequestTokens: 500000, RequestsPerDay: 100000},
			wantHist: maxHistoryChars,
			wantTool: maxToolResultChars,
			wantIter: 4,
		},
		{
			name:     "quota minuscule — itérations réduites",
			limits:   Limits{MaxRequestTokens: 20000, RequestsPerDay: 20},
			wantHist: 20000 / 2 * 50 / 100 * charsPerToken,
			wantTool: 20000 / 2 * 25 / 100 * charsPerToken,
			wantIter: 2,
		},
		{
			name:     "inconnu — défaut",
			limits:   Limits{MaxRequestTokens: 0, RequestsPerDay: 0},
			wantHist: DefaultMaxRequestTokens / 2 * 50 / 100 * charsPerToken,
			wantTool: DefaultMaxRequestTokens / 2 * 25 / 100 * charsPerToken,
			wantIter: 4,
		},
		{
			name:     "plafond ridicule — plancher",
			limits:   Limits{MaxRequestTokens: 10, RequestsPerDay: 0},
			wantHist: minPerRequestTokens * 50 / 100 * charsPerToken,
			wantTool: minPerRequestTokens * 25 / 100 * charsPerToken,
			wantIter: 4,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := NewBudget(c.limits)
			if b.MaxHistoryChars != c.wantHist {
				t.Errorf("MaxHistoryChars = %d, want %d", b.MaxHistoryChars, c.wantHist)
			}
			if b.MaxToolResultChars != c.wantTool {
				t.Errorf("MaxToolResultChars = %d, want %d", b.MaxToolResultChars, c.wantTool)
			}
			if b.MaxIterations != c.wantIter {
				t.Errorf("MaxIterations = %d, want %d", b.MaxIterations, c.wantIter)
			}
		})
	}
}

func TestProbeLimits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-ratelimit-limit-tokens", "8000")
		w.Header().Set("x-ratelimit-limit-requests", "1000")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	l, ok := probeLimits(context.Background(), srv.URL, "key", "model")
	if !ok {
		t.Fatal("sonde sans résultat")
	}
	if l.Source != "probe" || l.MaxRequestTokens != 8000 || l.RequestsPerDay != 1000 {
		t.Fatalf("limits = %+v", l)
	}
}

func TestDetectLimitsEnvWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("x-ratelimit-limit-tokens", "8000")
		w.Header().Set("x-ratelimit-limit-requests", "1000")
	}))
	defer srv.Close()

	t.Setenv("AI_MAX_REQUEST_TOKENS", "1234")
	t.Setenv("AI_REQUESTS_PER_DAY", "99")

	l := DetectLimits(context.Background(), "groq", srv.URL, "key", "model", slog.Default())
	if l.Source != "env" || l.MaxRequestTokens != 1234 || l.RequestsPerDay != 99 {
		t.Fatalf("limits = %+v", l)
	}
}

func TestDetectLimitsProfileFallback(t *testing.T) {
	l := DetectLimits(context.Background(), "gemini", "", "", "gemini-3.5-flash-lite", slog.Default())
	if l.Source != "profile" || l.RequestsPerDay != 500 {
		t.Fatalf("limits = %+v", l)
	}
	l = DetectLimits(context.Background(), "gemini", "", "", "gemini-3.8-flash", slog.Default())
	if l.RequestsPerDay != 20 {
		t.Fatalf("limits = %+v", l)
	}
}
