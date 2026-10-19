package ai

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/bornholm/genai/llm"
)

// failingClient simule un provider LLM en échec (quota épuisé, panne réseau).
type failingClient struct{}

func (failingClient) ChatCompletion(context.Context, ...llm.ChatCompletionOptionFunc) (llm.ChatCompletionResponse, error) {
	return nil, errors.New("panne")
}

func (failingClient) ChatCompletionStream(context.Context, ...llm.ChatCompletionOptionFunc) (<-chan llm.StreamChunk, error) {
	return nil, errors.New("http 429: quota épuisé")
}

func (failingClient) Embeddings(context.Context, []string, ...llm.EmbeddingsOptionFunc) (llm.EmbeddingsResponse, error) {
	return nil, errors.New("panne")
}

func (failingClient) Transcription(context.Context, []byte, ...llm.TranscriptionOptionFunc) (llm.TranscriptionResponse, error) {
	return nil, errors.New("panne")
}

// TestChatAlwaysAnswers vérifie la règle d'or : même en échec du provider,
// l'utilisateur reçoit le message de repli (orientation), jamais une erreur
// technique.
func TestChatAlwaysAnswers(t *testing.T) {
	svc := New(failingClient{}, nil, Config{
		MaxIterations: 2,
		CallTimeout:   5 * time.Second,
		Budget:        NewBudget(Limits{MaxRequestTokens: 8000, RequestsPerDay: 1000}),
	}, slog.Default())

	var tokens string
	sawDone, sawError := false, false
	for ev := range svc.Chat(context.Background(), nil, "Bonjour", SystemPromptPro) {
		switch ev.Type {
		case "token":
			tokens += ev.Token
		case "done":
			sawDone = true
		case "error":
			sawError = true
		}
	}
	if sawError {
		t.Fatal("événement error émis : le fallback doit être envoyé comme réponse")
	}
	if !sawDone {
		t.Fatal("événement done manquant")
	}
	if tokens != FallbackMessage {
		t.Fatalf("réponse = %q, want FallbackMessage", tokens)
	}
}
