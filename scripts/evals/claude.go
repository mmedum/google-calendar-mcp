//go:build evals

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// The Messages API client, hand-rolled for the same reason
// internal/gapi is (hard rule 9): this repository owns its wire types
// and takes no third-party client. The official Anthropic Go SDK would
// be the ordinary choice, and it brings eleven transitive modules into
// the module graph of a binary whose whole distribution story is
// supply-chain hygiene — an SBOM per archive, a signature over the
// checksums, govulncheck and a licence allow-list on every commit — to
// make one shape of request from maintainer tooling that never ships.
// The shape below is four fields and a loop.
//
// The API key is read from the environment and never printed, never
// written to a file, and never passed as a flag, where it would land in
// a shell history.

const (
	messagesURL    = "https://api.anthropic.com/v1/messages"
	anthropicVer   = "2023-06-01"
	defaultModel   = "claude-opus-5"
	defaultMaxTurn = 12
	maxTokens      = 16000
)

// claudeClient calls the Messages API.
type claudeClient struct {
	key   string
	model string
	http  *http.Client
}

func newClaude(model string) (*claudeClient, error) {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("ANTHROPIC_API_KEY is not set; the evals need one and nothing else here does")
	}
	if model == "" {
		model = defaultModel
	}
	return &claudeClient{key: key, model: model, http: &http.Client{Timeout: 10 * time.Minute}}, nil
}

// toolDef is one tool as the Messages API takes it. The three fields are
// exactly what tools/list gives, so the model sees the server's own
// descriptions and schemas rather than a paraphrase — which is the
// point: an eval that reworded them would be scoring the rewording.
type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	// CacheControl on the LAST tool caches the whole tool block, which
	// is rendered before the system prompt and the messages. This
	// server's schemas are about 7k tokens and every turn resends them;
	// twelve turns across three tasks is a quarter of a million input
	// tokens for a list that never changes. A cache read bills at a
	// tenth of that.
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

// message is one turn. Content is raw so an assistant turn can be
// appended back EXACTLY as it arrived: thinking blocks are bound to the
// model that produced them and must be echoed unchanged.
type message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type request struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []message `json:"messages"`
	Tools     []toolDef `json:"tools,omitempty"`
	Thinking  *thinking `json:"thinking,omitempty"`
}

type thinking struct {
	Type string `json:"type"`
}

type response struct {
	// StopReason is read rather than assumed: a turn that stopped at
	// max_tokens or was refused has no tool calls in it, and a loop that
	// only counted tool calls would score that as "the model decided it
	// was finished".
	StopReason string          `json:"stop_reason"`
	Content    json.RawMessage `json:"content"`
	Usage      usage           `json:"usage"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// usage is what a run costs, and the cache counters are the half worth
// having: a cache read of zero on the second turn is the only way to
// see that something invalidated the prefix.
type usage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
}

// block is one content block, read rather than round-tripped: the loop
// needs the tool calls and the text, and passes the whole array back
// untouched.
type block struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// send makes one request, retrying a rate limit or a server error once.
// Anything else is returned: an eval that silently retried a refusal
// would score the retry.
func (c *claudeClient) send(ctx context.Context, req request) (*response, error) {
	req.Model = c.model
	req.MaxTokens = maxTokens
	req.Thinking = &thinking{Type: "adaptive"}

	var last error
	for attempt := range 2 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(10 * time.Second):
			}
		}
		out, status, err := c.post(ctx, req)
		switch {
		case err == nil:
			return out, nil
		case status == http.StatusTooManyRequests || status >= 500:
			last = err
		default:
			return nil, err
		}
	}
	return nil, last
}

func (c *claudeClient) post(ctx context.Context, req request) (*response, int, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, messagesURL, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("anthropic-version", anthropicVer)
	httpReq.Header.Set("x-api-key", c.key)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	var out response
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("messages: %d, and the answer is not JSON", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		if out.Error != nil {
			return nil, resp.StatusCode, fmt.Errorf("messages: %d %s: %s",
				resp.StatusCode, out.Error.Type, out.Error.Message)
		}
		return nil, resp.StatusCode, fmt.Errorf("messages: %d", resp.StatusCode)
	}
	return &out, resp.StatusCode, nil
}

// blocks decodes the content array for the loop to read.
func (r *response) blocks() ([]block, error) {
	var out []block
	if err := json.Unmarshal(r.Content, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// userMessage builds a plain text turn.
func userMessage(text string) (message, error) {
	content, err := json.Marshal([]map[string]any{{"type": "text", "text": text}})
	if err != nil {
		return message{}, err
	}
	return message{Role: "user", Content: content}, nil
}

// toolResults builds the single user turn that answers every tool call
// in one assistant turn. One message, not one per call: splitting them
// teaches the model to stop calling tools in parallel.
func toolResults(results []map[string]any) (message, error) {
	content, err := json.Marshal(results)
	if err != nil {
		return message{}, err
	}
	return message{Role: "user", Content: content}, nil
}
