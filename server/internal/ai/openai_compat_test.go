package ai

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/bornholm/genai/llm"
)

// TestThoughtSignatureRoundTrip vérifie le contrat Gemini : le
// thought_signature capturé dans la réponse streamée doit être réinjecté
// dans l'assistant tool calls quand l'historique est rejoué, sinon Gemini
// répond 400 « Function call is missing a thought_signature ».
func TestThoughtSignatureRoundTrip(t *testing.T) {
	var replayed []byte
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/event-stream")
		if calls == 1 {
			io.WriteString(w, `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lexique_search","arguments":"{\"query\":\"CMP\"}"},"extra_content":{"google":{"thought_signature":"SIG-123"}}}]}}]}`+"\n\n")
		} else {
			body, _ := io.ReadAll(r.Body)
			replayed = body
			io.WriteString(w, `data: {"choices":[{"delta":{"content":"ok"}}]}`+"\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := newCompatClient(srv.URL, "key", nil, slog.Default())
	ctx := context.Background()

	// Tour 1 : le modèle demande un tool call, la signature est capturée.
	stream, err := c.ChatCompletionStream(ctx,
		llm.WithMessages(llm.NewMessage(llm.RoleUser, "Définition de CMP ?")))
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}

	// Tour 2 : l'historique rejoue le tool call et son résultat.
	stream, err = c.ChatCompletionStream(ctx, llm.WithMessages(
		llm.NewMessage(llm.RoleUser, "Définition de CMP ?"),
		llm.NewToolCallsMessage(llm.NewToolCall("call_1", "lexique_search", `{"query":"CMP"}`)),
		llm.NewToolMessage("call_1", llm.NewToolResult(`{"results":[]}`)),
	))
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}

	if !strings.Contains(string(replayed), `"extra_content"`) ||
		!strings.Contains(string(replayed), `"thought_signature":"SIG-123"`) {
		t.Fatalf("thought_signature non réinjecté dans la requête rejouée: %s", replayed)
	}
}

// TestConvertMessageWithoutSignature vérifie qu'aucun extra_content n'est
// inventé quand la signature est inconnue (providers non-Gemini inchangés).
func TestConvertMessageWithoutSignature(t *testing.T) {
	c := newCompatClient("http://example.invalid", "key", nil, slog.Default())
	msg := c.convertMessage(llm.NewToolCallsMessage(
		llm.NewToolCall("call_x", "lexique_search", `{"query":"CMP"}`)))

	raw, err := json.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "extra_content") {
		t.Fatalf("extra_content émis sans signature connue: %s", raw)
	}
	if !strings.Contains(string(raw), `"arguments":"{\"query\":\"CMP\"}"`) {
		t.Fatalf("arguments du tool call absents: %s", raw)
	}
}
