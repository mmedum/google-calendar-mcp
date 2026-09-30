//go:build live

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmedum/google-calendar-mcp/v3/internal/redact"
)

// session drives the built binary over stdio, the way a client does.
//
// It holds stdin open for the life of the run: a reader already at EOF
// ends the session before it processes anything queued behind it, which
// reads as a dead server and is really a dead test.
type session struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	out    *bufio.Scanner
	stderr *strings.Builder
	mu     sync.Mutex
	nextID int
	// person answers the questions the server puts to the person.
	person *person
}

// person is who the server asks before a write it cannot take back
// (§9a). It prints every question into the transcript and accepts,
// unless a step has queued another answer.
type person struct {
	out *redact.Printer
	// next is the answer to the next question, accept when empty; that
	// question spends it.
	next     string
	asked    int
	declined int
}

func (p *person) answer(message string) string {
	p.asked++
	a := p.next
	p.next = ""
	if a == "" {
		a = "accept"
	}
	if a != "accept" {
		p.declined++
	}
	p.out.Printf("      the server asked the person:\n")
	for _, line := range strings.Split(strings.TrimSpace(message), "\n") {
		if line != "" {
			p.out.Printf("        %s\n", line)
		}
	}
	p.out.Printf("      the person answered: %s\n", a)
	return a
}

type callResult struct {
	text    string
	isError bool
}

func startServer(ctx context.Context, bin, profile string, p *person) (*session, error) {
	cmd := exec.CommandContext(ctx, bin)
	// The server must read the same login the driver set up with, or the
	// two halves of the run would be looking at different accounts.
	//
	// The destructive flag is set here and nowhere else. delete_calendar
	// and clear_calendar do not REGISTER without it (§9), so a driver
	// that left it unset could not drive them at all and would leave the
	// probe calendar behind on every run. What makes arming it safe is
	// not care: every step that could reach one names a calendar this
	// driver created, and a step whose calendar was never created is
	// SKIPPED rather than called — because a tool call naming no
	// calendar resolves to the account's primary one.
	cmd.Env = append(cmd.Environ(),
		"GCAL_LOG_LEVEL=error", "GCAL_PROFILE="+profile, "GCAL_ENABLE_DESTRUCTIVE=true")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &strings.Builder{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	s := &session{cmd: cmd, stdin: stdin, out: sc, stderr: stderr, nextID: 1, person: p}

	// The driver declares form elicitation, so the server asks it before
	// each write that asks the person, the way a client with a person at
	// it would be asked. On 2025-06-18 the server asks inside the call,
	// with elicitation/create.
	if _, err := s.request(ctx, "initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{"elicitation": map[string]any{"form": map[string]any{}}},
		"clientInfo":      map[string]any{"name": "livecal", "version": "0"},
	}); err != nil {
		return nil, err
	}
	if err := s.notify("notifications/initialized"); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *session) close() {
	_ = s.stdin.Close()
	done := make(chan struct{})
	go func() { _, _ = s.cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		_ = s.cmd.Process.Kill()
	}
}

func (s *session) notify(method string) error {
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.stdin, "%s\n", b)
	return err
}

func (s *session) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.nextID
	s.nextID++

	b, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	})
	if err != nil {
		return nil, err
	}
	if _, err := fmt.Fprintf(s.stdin, "%s\n", b); err != nil {
		return nil, err
	}

	for s.out.Scan() {
		line := strings.TrimSpace(s.out.Text())
		if line == "" {
			continue
		}
		var frame struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				Message string `json:"message"`
			} `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			// Stdout must carry only JSON-RPC frames. Anything else is
			// the protocol being corrupted, and every later step would
			// report nonsense.
			return nil, fmt.Errorf("stdout carried something that is not a JSON-RPC frame: %q", line)
		}
		if frame.Method != "" && len(frame.ID) > 0 {
			// The server's own request, made while this call waits on it.
			if err := s.answer(frame.ID, frame.Method, frame.Params.Message); err != nil {
				return nil, fmt.Errorf("answer %s: %w", frame.Method, err)
			}
			continue
		}
		if string(frame.ID) != strconv.Itoa(id) {
			continue
		}
		if frame.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, frame.Error.Message)
		}
		return frame.Result, nil
	}
	if err := s.out.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("%s: the server closed without answering (stderr: %s)", method, s.stderr.String())
}

// answer replies to one request the server sent: ping with an empty
// result, elicitation/create through the person, anything else with
// method-not-found.
func (s *session) answer(id json.RawMessage, method, message string) error {
	frame := map[string]any{"jsonrpc": "2.0", "id": id}
	switch {
	case method == "ping":
		frame["result"] = map[string]any{}
	case method == "elicitation/create" && s.person != nil:
		result := map[string]any{"action": s.person.answer(message)}
		if result["action"] == "accept" {
			// The question's form has no fields (§9a): the accept is the
			// answer.
			result["content"] = map[string]any{}
		}
		frame["result"] = result
	default:
		frame["error"] = map[string]any{"code": -32601, "message": "this client does not take " + method}
	}
	b, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.stdin, "%s\n", b)
	return err
}

// call invokes one tool and returns the text half plus whether the
// result was an error.
func (s *session) call(ctx context.Context, tool string, args map[string]any) (callResult, error) {
	params := map[string]any{"name": tool}
	if args != nil {
		params["arguments"] = args
	}
	raw, err := s.request(ctx, "tools/call", params)
	if err != nil {
		return callResult{}, err
	}
	var res struct {
		IsError bool `json:"isError"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return callResult{}, err
	}
	var b strings.Builder
	for _, c := range res.Content {
		b.WriteString(c.Text)
	}
	return callResult{text: b.String(), isError: res.IsError}, nil
}

// readResource reads one resource, the way a client that attaches
// rather than calls would (§8).
//
// A resource read answers with a protocol error rather than an error
// result, so a refusal arrives here as a transport failure and is
// turned into the same callResult shape a tool refusal takes — the
// steps then read alike.
func (s *session) readResource(ctx context.Context, uri string) (callResult, error) {
	raw, err := s.request(ctx, "resources/read", map[string]any{"uri": uri})
	if err != nil {
		return callResult{text: err.Error(), isError: true}, nil
	}
	var res struct {
		Contents []struct {
			Text string `json:"text"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return callResult{}, err
	}
	var b strings.Builder
	for _, c := range res.Contents {
		b.WriteString(c.Text)
	}
	return callResult{text: b.String()}, nil
}
