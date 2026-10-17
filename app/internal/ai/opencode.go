package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/bornholm/genai/llm"
	"github.com/google/uuid"
)

// openCodeClient est un client LLM qui appelle l'endpoint OpenCode Go
// directement (sans passer par le SDK openai-go qui a des problèmes de
// décodage SSE avec les champs reasoning de mimo).
type openCodeClient struct {
	baseURL   string
	apiKey    string
	sessionID string
	http      *http.Client
	log       *slog.Logger
}

// NewOpenCodeClient crée un client pour l'endpoint OpenCode Go.
func NewOpenCodeClient(baseURL, apiKey string, log *slog.Logger) llm.Client {
	return newOpenCodeClient(baseURL, apiKey, uuid.NewString(), log)
}

func newOpenCodeClient(baseURL, apiKey, sessionID string, log *slog.Logger) *openCodeClient {
	if log == nil {
		log = slog.Default()
	}
	return &openCodeClient{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		sessionID: sessionID,
		http:      &http.Client{},
		log:       log,
	}
}

type ocToolCall struct {
	Index   int    `json:"index"`
	ID      string `json:"id"`
	Type    string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ocDelta struct {
	Role      string      `json:"role"`
	Content   string      `json:"content"`
	ToolCalls []ocToolCall `json:"tool_calls"`
}

type ocChunk struct {
	Choices []struct {
		Delta       ocDelta `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

type ocMessage struct {
	Role      string      `json:"role"`
	Content   string      `json:"content,omitempty"`
	ToolCalls []ocToolCall `json:"tool_calls,omitempty"`
}

type ocToolMessage struct {
	Role       string `json:"role"`
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

type ocFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  map[string]any  `json:"parameters"`
}

type ocTool struct {
	Type     string     `json:"type"`
	Function ocFunction `json:"function"`
}

// ChatCompletionStream implémente llm.ChatCompletionStreamingClient.
func (c *openCodeClient) ChatCompletionStream(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (<-chan llm.StreamChunk, error) {
	opts := llm.NewChatCompletionOptions(funcs...)

	// Convertir les messages genai → format OpenAI
	var msgs []any
	if len(opts.Messages) > 0 {
		for _, m := range opts.Messages {
			msgs = append(msgs, convertMessage(m))
		}
	}

	// Convertir les outils
	var tools []any
	for _, t := range opts.Tools {
		tools = append(tools, convertTool(t))
	}

	body := map[string]any{
		"model":    os.Getenv("AI_MODEL"),
		"messages": msgs,
		"stream":   true,
	}
	if len(tools) > 0 {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}
	if opts.MaxCompletionTokens != nil {
		body["max_tokens"] = *opts.MaxCompletionTokens
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("x-opencode-session", c.sessionID)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, string(body))
	}

	chunks := make(chan llm.StreamChunk, 10)

	go func() {
		defer close(chunks)
		defer resp.Body.Close()

		toolAcc := map[int]*struct {
			id, name, args string
		}{}
		var toolOrder []int
		var contentBuilder strings.Builder

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk ocChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // ignorer les chunks malformés
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			d := chunk.Choices[0].Delta

			// Accumuler le contenu
			if d.Content != "" {
				contentBuilder.WriteString(d.Content)
				llm.SendChunk(ctx, chunks, llm.NewStreamChunk(
					llm.NewStreamDelta(llm.RoleAssistant, d.Content),
				))
			}

			// Accumuler les tool calls
			for _, tc := range d.ToolCalls {
				idx := tc.Index
				acc, ok := toolAcc[idx]
				if !ok {
					acc = &struct{ id, name, args string }{}
					toolAcc[idx] = acc
					toolOrder = append(toolOrder, idx)
				}
				if tc.ID != "" {
					acc.id = tc.ID
				}
				if tc.Function.Name != "" {
					acc.name = tc.Function.Name
				}
				acc.args += tc.Function.Arguments
			}
		}

		// Envoyer le chunk terminal avec les tool calls accumulés
		var allToolCallDeltas []llm.ToolCallDelta
		for i, idx := range toolOrder {
			acc := toolAcc[idx]
			if acc.name == "" {
				continue
			}
			args := acc.args
			if args == "" || !json.Valid([]byte(args)) {
				args = "{}"
			}
			allToolCallDeltas = append(allToolCallDeltas, llm.NewToolCallDelta(i, acc.id, acc.name, args))
		}

		finalDelta := llm.NewStreamDelta(llm.RoleAssistant, contentBuilder.String(), allToolCallDeltas...)
		llm.SendTerminalChunk(ctx, chunks, llm.NewStreamChunk(finalDelta))
	}()

	return chunks, nil
}

// ChatCompletion implémente llm.ChatCompletionClient (non-streaming, rarement utilisé ici).
func (c *openCodeClient) ChatCompletion(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (llm.ChatCompletionResponse, error) {
	return nil, fmt.Errorf("non supporté dans ce client")
}

// Embeddings / Transcription non supportés via ce client.
func (c *openCodeClient) Embeddings(ctx context.Context, inputs []string, funcs ...llm.EmbeddingsOptionFunc) (llm.EmbeddingsResponse, error) {
	return nil, fmt.Errorf("non supporté")
}
func (c *openCodeClient) Transcription(ctx context.Context, audio []byte, funcs ...llm.TranscriptionOptionFunc) (llm.TranscriptionResponse, error) {
	return nil, fmt.Errorf("non supporté")
}

// convertMessage convertit un message genai en format OpenAI.
func convertMessage(m llm.Message) any {
	role := string(m.Role())

	// Assistant avec tool_calls
	if tcMsg, ok := m.(interface{ ToolCalls() []llm.ToolCall }); ok {
		tcs := tcMsg.ToolCalls()
		if len(tcs) > 0 {
			var ocTCs []map[string]any
			for _, tc := range tcs {
				args := tc.Parameters()
				argsStr, ok := args.(string)
				if !ok {
					b, _ := json.Marshal(args)
					argsStr = string(b)
				}
				if !json.Valid([]byte(argsStr)) {
					argsStr = "{}"
				}
				ocTCs = append(ocTCs, map[string]any{
					"id":   tc.ID(),
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name(),
						"arguments": argsStr,
					},
				})
			}
			return map[string]any{
				"role":       "assistant",
				"content":    m.Content(),
				"tool_calls": ocTCs,
			}
		}
	}

	if role == "tool" {
		if tm, ok := m.(llm.ToolMessage); ok {
			return map[string]any{
				"role":        "tool",
				"tool_call_id": tm.ID(),
				"content":     m.Content(),
			}
		}
		return map[string]any{"role": "tool", "content": m.Content()}
	}
	return map[string]any{"role": role, "content": m.Content()}
}

// convertTool convertit un outil genai en format OpenAI.
func convertTool(t llm.Tool) any {
	params := t.Parameters()
	if params == nil {
		params = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        t.Name(),
			"description": t.Description(),
			"parameters":  params,
		},
	}
}
