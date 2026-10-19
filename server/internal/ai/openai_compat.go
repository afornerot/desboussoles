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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bornholm/genai/llm"
	"github.com/google/uuid"
)

// compatClient est un client LLM qui appelle un endpoint OpenAI-compatible
// directement (sans passer par le SDK openai-go) : OpenCode Go (champs
// reasoning de mimo mal décodés par le SDK) et Gemini (thought_signature
// des function calls à rejouer tels quels, ignorés par le SDK).
type compatClient struct {
	baseURL string
	apiKey  string
	headers map[string]string
	http    *http.Client
	log     *slog.Logger

	// sigs mémorise le thought_signature de Gemini par id de tool call
	// (extra_content.google.thought_signature), capturé dans la réponse puis
	// réinjecté quand l'historique rejoue l'assistant tool calls. Sans ça,
	// Gemini refuse la requête suivante (400 INVALID_ARGUMENT).
	sigMu sync.Mutex
	sigs  map[string]string
}

// NewOpenCodeClient crée un client pour l'endpoint OpenCode Go.
func NewOpenCodeClient(baseURL, apiKey string, log *slog.Logger) llm.Client {
	return newCompatClient(baseURL, apiKey, map[string]string{
		"x-opencode-session": uuid.NewString(),
	}, log)
}

// NewOpenAICompatClient crée un client pour un endpoint OpenAI-compatible
// (Gemini, Groq, DeepSeek…). Le thought_signature de Gemini est réinjecté
// quand il a été capturé ; sans effet pour les autres providers.
func NewOpenAICompatClient(baseURL, apiKey string, log *slog.Logger) llm.Client {
	return newCompatClient(baseURL, apiKey, nil, log)
}

func newCompatClient(baseURL, apiKey string, headers map[string]string, log *slog.Logger) *compatClient {
	if log == nil {
		log = slog.Default()
	}
	return &compatClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		headers: headers,
		http:    &http.Client{},
		log:     log,
		sigs:    make(map[string]string),
	}
}

// rememberThoughtSignature enregistre le thought_signature d'un tool call.
// La map est purgée quand elle devient trop grande : les ids sont uniques
// par réponse, les entrées anciennes sont du gaspillage.
func (c *compatClient) rememberThoughtSignature(id, sig string) {
	if id == "" || sig == "" {
		return
	}
	c.sigMu.Lock()
	defer c.sigMu.Unlock()
	if len(c.sigs) > 1024 {
		c.sigs = make(map[string]string)
	}
	c.sigs[id] = sig
}

// thoughtSignature retourne le thought_signature mémorisé pour un tool call.
func (c *compatClient) thoughtSignature(id string) string {
	c.sigMu.Lock()
	defer c.sigMu.Unlock()
	return c.sigs[id]
}

type ocToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	// ExtraContent porte le thought_signature de Gemini (outil + thinking).
	ExtraContent *struct {
		Google *struct {
			ThoughtSignature string `json:"thought_signature"`
		} `json:"google"`
	} `json:"extra_content"`
}

type ocDelta struct {
	Role      string       `json:"role"`
	Content   string       `json:"content"`
	ToolCalls []ocToolCall `json:"tool_calls"`
}

type ocChunk struct {
	Choices []struct {
		Delta        ocDelta `json:"delta"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Usage *ocUsage `json:"usage"`
}

// ocUsage porte les compteurs de tokens (demandés via stream_options.include_usage).
type ocUsage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
}

type ocMessage struct {
	Role      string       `json:"role"`
	Content   string       `json:"content,omitempty"`
	ToolCalls []ocToolCall `json:"tool_calls,omitempty"`
}

type ocToolMessage struct {
	Role       string `json:"role"`
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

type ocFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ocTool struct {
	Type     string     `json:"type"`
	Function ocFunction `json:"function"`
}

// HTTPStatusError porte le statut d'un refus API et le délai de retry annoncé
// par le serveur (header retry-after ou « try again in Xs » dans le corps).
type HTTPStatusError struct {
	Status     int
	RetryAfter time.Duration
	Msg        string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("http %d: %s", e.Status, e.Msg)
}

// retryAfterRegex repère le délai annoncé dans le corps d'erreur Groq.
var retryAfterRegex = regexp.MustCompile(`try again in ([0-9]+(?:\.[0-9]+)?)s`)

// parseRetryAfter extrait le délai d'attente conseillé par le serveur.
func parseRetryAfter(header, body string) time.Duration {
	if secs, err := strconv.ParseFloat(strings.TrimSpace(header), 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if m := retryAfterRegex.FindStringSubmatch(body); m != nil {
		if secs, err := strconv.ParseFloat(m[1], 64); err == nil && secs > 0 {
			return time.Duration(secs * float64(time.Second))
		}
	}
	return 0
}

// ChatCompletionStream implémente llm.ChatCompletionStreamingClient.
func (c *compatClient) ChatCompletionStream(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (<-chan llm.StreamChunk, error) {
	start := time.Now()
	tr := TraceFrom(ctx)
	opts := llm.NewChatCompletionOptions(funcs...)

	// Convertir les messages genai → format OpenAI
	var msgs []any
	if len(opts.Messages) > 0 {
		for _, m := range opts.Messages {
			msgs = append(msgs, c.convertMessage(m))
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
		// Dernier chunk SSE portant les compteurs de tokens (traçabilité).
		"stream_options": map[string]any{"include_usage": true},
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
	// Raisonnement des modèles type gpt-oss : effort et visibilité pilotés par
	// env (AI_REASONING_EFFORT, AI_INCLUDE_REASONING) — sans effet ailleurs.
	if v := os.Getenv("AI_REASONING_EFFORT"); v != "" {
		body["reasoning_effort"] = v
	}
	if v := os.Getenv("AI_INCLUDE_REASONING"); v != "" {
		body["include_reasoning"] = v == "true" || v == "1"
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
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	respAt := time.Now()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		c.log.Warn("llm http", "trace", traceID(tr), "model", os.Getenv("AI_MODEL"),
			"status", resp.StatusCode, "req_chars", len(payload),
			"duration_ms", respAt.Sub(start).Milliseconds())
		return nil, &HTTPStatusError{
			Status:     resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("retry-after"), string(body)),
			Msg:        string(body),
		}
	}

	chunks := make(chan llm.StreamChunk, 10)

	go func() {
		defer close(chunks)
		defer resp.Body.Close()

		toolAcc := map[int]*struct {
			id, name, args, sig string
		}{}
		var toolOrder []int
		var contentBuilder strings.Builder
		var firstAt time.Time
		var usage *ocUsage
		recvChars := 0

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
			recvChars += len(data)

			var chunk ocChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // ignorer les chunks malformés
			}
			// Le chunk d'usage n'a pas de choix — le capter avant le filtre.
			if chunk.Usage != nil {
				usage = chunk.Usage
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			d := chunk.Choices[0].Delta

			// Accumuler le contenu
			if d.Content != "" {
				if firstAt.IsZero() {
					firstAt = time.Now()
				}
				contentBuilder.WriteString(d.Content)
				llm.SendChunk(ctx, chunks, llm.NewStreamChunk(
					llm.NewStreamDelta(llm.RoleAssistant, d.Content),
				))
			}

			// Accumuler les tool calls
			for _, tc := range d.ToolCalls {
				if firstAt.IsZero() {
					firstAt = time.Now()
				}
				idx := tc.Index
				acc, ok := toolAcc[idx]
				if !ok {
					acc = &struct{ id, name, args, sig string }{}
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
				if tc.ExtraContent != nil && tc.ExtraContent.Google != nil &&
					tc.ExtraContent.Google.ThoughtSignature != "" {
					acc.sig = tc.ExtraContent.Google.ThoughtSignature
				}
			}
		}

		// Envoyer le chunk terminal avec les tool calls accumulés.
		// Le contenu n'est PAS renvoyé ici : chaque token a déjà été émis
	 // individuellement pendant le stream (sinon doublon dans consumeStream).
		var allToolCallDeltas []llm.ToolCallDelta
		for i, idx := range toolOrder {
			acc := toolAcc[idx]
			if acc.name == "" {
				continue
			}
			c.rememberThoughtSignature(acc.id, acc.sig)
			args := acc.args
			if args == "" || !json.Valid([]byte(args)) {
				args = "{}"
			}
			allToolCallDeltas = append(allToolCallDeltas, llm.NewToolCallDelta(i, acc.id, acc.name, args))
		}

		finalDelta := llm.NewStreamDelta(llm.RoleAssistant, "", allToolCallDeltas...)

		// Métriques de l'appel : TTFT, durée de stream, tailles, usage tokens.
		end := time.Now()
		ttft := end.Sub(start).Milliseconds()
		if !firstAt.IsZero() {
			ttft = firstAt.Sub(start).Milliseconds()
		}
		var tokIn, tokOut int64
		if usage != nil {
			tokIn, tokOut = usage.PromptTokens, usage.CompletionTokens
		}
		if tr != nil {
			tr.AddLLM(ttft, end.Sub(respAt).Milliseconds(), len(payload), recvChars, tokIn, tokOut)
		}
		c.log.Info("llm http", "trace", traceID(tr), "model", os.Getenv("AI_MODEL"),
			"ttft_ms", ttft, "stream_ms", end.Sub(respAt).Milliseconds(),
			"req_chars", len(payload), "resp_chars", recvChars,
			"tokens_in", tokIn, "tokens_out", tokOut)

		llm.SendTerminalChunk(ctx, chunks, llm.NewStreamChunk(finalDelta))
	}()

	return chunks, nil
}

// ChatCompletion implémente llm.ChatCompletionClient (non-streaming, rarement utilisé ici).
func (c *compatClient) ChatCompletion(ctx context.Context, funcs ...llm.ChatCompletionOptionFunc) (llm.ChatCompletionResponse, error) {
	return nil, fmt.Errorf("non supporté dans ce client")
}

// Embeddings / Transcription non supportés via ce client.
func (c *compatClient) Embeddings(ctx context.Context, inputs []string, funcs ...llm.EmbeddingsOptionFunc) (llm.EmbeddingsResponse, error) {
	return nil, fmt.Errorf("non supporté")
}
func (c *compatClient) Transcription(ctx context.Context, audio []byte, funcs ...llm.TranscriptionOptionFunc) (llm.TranscriptionResponse, error) {
	return nil, fmt.Errorf("non supporté")
}

// convertMessage convertit un message genai en format OpenAI.
// Les thought_signature de Gemini mémorisés sont réinjectés dans les
// assistant tool calls, exigés par Gemini quand l'historique est rejoué.
func (c *compatClient) convertMessage(m llm.Message) any {
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
				ocTC := map[string]any{
					"id":   tc.ID(),
					"type": "function",
					"function": map[string]any{
						"name":      tc.Name(),
						"arguments": argsStr,
					},
				}
				if sig := c.thoughtSignature(tc.ID()); sig != "" {
					ocTC["extra_content"] = map[string]any{
						"google": map[string]any{"thought_signature": sig},
					}
				}
				ocTCs = append(ocTCs, ocTC)
			}
			out := map[string]any{
				"role":       "assistant",
				"tool_calls": ocTCs,
			}
			// Gemini rejoue les tool calls sans content : n'émettre le
			// champ que s'il y a du texte.
			if content := m.Content(); content != "" {
				out["content"] = content
			}
			return out
		}
	}

	if role == "tool" {
		if tm, ok := m.(llm.ToolMessage); ok {
			return map[string]any{
				"role":         "tool",
				"tool_call_id": tm.ID(),
				"content":      m.Content(),
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
