//go:build evals

// Command evals scores whether a model can complete a task through this
// server's tools (§13).
//
// It is the other half of the live driver, and it asks a different
// question. The driver asks whether the server does what it says; this
// asks whether a model reading only the tool descriptions can get the
// right answer out of it. Both have found things unit tests cannot, and
// the standard's §12 names the one this is for: a tool the model never
// discovered.
//
// The three tasks are the three failures of §3 — the ones this whole
// design exists to avoid:
//
//  1. an all-day event created by a user in a negative-offset zone;
//  2. a weekly recurrence that must survive a daylight-saving change;
//  3. an invitation that must actually reach an external guest.
//
// It runs against the in-memory calendar of internal/gapi/caltest, not
// a real account: the score is about the model and the tool surface, and
// nothing here should cost somebody's quota or reach anybody's inbox.
// The server is the real one — server.New over an in-memory transport —
// so the model sees the descriptions, schemas and refusals that ship.
//
// It is NOT part of `make check`. It costs money and it is not
// deterministic, so it is run by hand like the live driver:
//
//	ANTHROPIC_API_KEY=... make evals
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/internal/config"
	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/internal/redact"
	"github.com/mmedum/google-calendar-mcp/internal/server"
	"github.com/mmedum/google-calendar-mcp/internal/service"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

func main() {
	model := flag.String("model", defaultModel, "the model to score")
	only := flag.String("task", "", "run only the tasks whose name contains this")
	turns := flag.Int("max-turns", defaultMaxTurn, "give up after this many assistant turns")
	verbose := flag.Bool("v", false, "print every tool call and the model's own words")
	// Everything except the model, so the harness itself can be checked
	// without spending anything — and so the scorers are watched
	// FAILING, which is the only way to know they discriminate.
	selfCheck := flag.Bool("self-check", false,
		"build each task's calendar and score it untouched: every task must fail. No API key needed")
	flag.Parse()

	out := redact.New(os.Stderr)
	if *selfCheck {
		os.Exit(check(context.Background(), out))
	}
	code := run(context.Background(), out, *model, *only, *turns, *verbose)
	os.Exit(code)
}

// check runs the harness with no model in it.
//
// A scorer that passes on a calendar nobody touched is scoring nothing,
// and it would report a model as correct for doing nothing at all. This
// asserts the opposite of the usual test: every task must FAIL here, and
// the tool list has to reach the point where a model would see it.
func check(ctx context.Context, out *redact.Printer) int {
	bad := 0
	for _, t := range tasks() {
		fake, session, closeAll, err := newSession(t)
		if err != nil {
			out.Printf("ERROR %-28s %v\n", t.name, redact.String(err.Error()))
			bad++
			continue
		}
		tools, terr := toolDefs(ctx, session)
		closeAll()
		if terr != nil {
			out.Printf("ERROR %-28s %v\n", t.name, redact.String(terr.Error()))
			bad++
			continue
		}
		if len(tools) < 5 {
			out.Printf("FAIL  %-28s the model would be offered %d tools\n", t.name, len(tools))
			bad++
			continue
		}
		pass, note := t.score(fake)
		if pass {
			out.Printf("FAIL  %-28s scored a PASS on a calendar nobody touched: %s\n", t.name, note)
			bad++
			continue
		}
		out.Printf("ok    %-28s %d tools offered; unscored calendar fails: %s\n", t.name, len(tools), note)
	}
	if bad > 0 {
		out.Printf("\n%d task(s) cannot be trusted to score anything\n", bad)
		return 1
	}
	out.Printf("\n%d tasks, each refusing an empty calendar. Run `make evals` with a key to score a model.\n",
		len(tasks()))
	return 0
}

func run(ctx context.Context, out *redact.Printer, model, only string, maxTurns int, verbose bool) int {
	claude, err := newClaude(model)
	if err != nil {
		out.Printf("%v\n", err)
		return 2
	}
	out.Printf("evals: scoring %s against the fake calendar\n\n", model)

	var passed, failed, skipped int
	var spent usage
	for _, task := range tasks() {
		if only != "" && !strings.Contains(task.name, only) {
			skipped++
			continue
		}
		result := runTask(ctx, out, claude, task, maxTurns, verbose)
		switch {
		case result.err != nil:
			failed++
			out.Printf("ERROR %-28s %s\n", task.name, redact.String(result.err.Error()))
		case result.pass:
			passed++
			out.Printf("ok    %-28s %s  (%s)\n", task.name, result.note, result.cost())
		default:
			failed++
			out.Printf("FAIL  %-28s %s  (%s)\n", task.name, result.note, result.cost())
		}
		spent.add(result.tokens)
	}

	out.Printf("\n%d passed, %d failed, %d skipped\n", passed, failed, skipped)
	out.Printf("%d input tokens (%d read from cache, %d written), %d output\n",
		spent.InputTokens, spent.CacheReadTokens, spent.CacheCreationTokens, spent.OutputTokens)
	if spent.CacheReadTokens == 0 && spent.CacheCreationTokens > 0 {
		// A cache that is written and never read is a cache that is not
		// working, and nothing else in the run would say so.
		out.Printf("Nothing was read from the prompt cache, so the tool block was billed every turn.\n")
	}
	if failed > 0 {
		// The same discipline as the live driver: a failure here is
		// usually a tool description rather than a model, and the
		// transcript is where that shows.
		out.Printf("\nA failure here is more often a tool description than a model. Re-run with -v and\n")
		out.Printf("read what it tried before changing anything.\n")
		return 1
	}
	return 0
}

// result is one task's outcome.
type result struct {
	pass   bool
	note   string
	turns  int
	calls  int
	tokens usage
	err    error
}

// add accumulates one turn's usage.
func (u *usage) add(v usage) {
	u.InputTokens += v.InputTokens
	u.OutputTokens += v.OutputTokens
	u.CacheCreationTokens += v.CacheCreationTokens
	u.CacheReadTokens += v.CacheReadTokens
}

// runTask gives one task its own calendar, its own session and its own
// conversation, so nothing a previous task wrote can satisfy this one.
func runTask(ctx context.Context, out *redact.Printer, claude *claudeClient,
	t task, maxTurns int, verbose bool,
) result {
	fake, session, closeAll, err := newSession(t)
	if err != nil {
		return result{err: err}
	}
	defer closeAll()

	tools, err := toolDefs(ctx, session)
	if err != nil {
		return result{err: err}
	}

	first, err := userMessage(t.prompt)
	if err != nil {
		return result{err: err}
	}
	req := request{System: t.system, Messages: []message{first}, Tools: tools}

	var turns, calls int
	var tokens usage
	for turns < maxTurns {
		resp, serr := claude.send(ctx, req)
		if serr != nil {
			return result{turns: turns, calls: calls, tokens: tokens, err: serr}
		}
		turns++
		tokens.add(resp.Usage)
		blocks, berr := resp.blocks()
		if berr != nil {
			return result{turns: turns, calls: calls, tokens: tokens, err: berr}
		}
		if resp.StopReason == "refusal" || resp.StopReason == "max_tokens" {
			// Said rather than scored: the task is unfinished for a
			// reason that is not about this server's tools, and a
			// failure line would put it in the same bucket as a model
			// that wrote the wrong event.
			return result{turns: turns, calls: calls, tokens: tokens,
				err: fmt.Errorf("the model stopped with stop_reason=%s", resp.StopReason)}
		}
		// Appended verbatim: a thinking block has to go back exactly as
		// it arrived, and re-encoding it from parts is how it stops
		// matching.
		req.Messages = append(req.Messages, message{Role: "assistant", Content: resp.Content})

		var answers []map[string]any
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if verbose && strings.TrimSpace(b.Text) != "" {
					out.Printf("      | %s\n", strings.ReplaceAll(strings.TrimSpace(b.Text), "\n", "\n      | "))
				}
			case "tool_use":
				calls++
				if verbose {
					out.Printf("      > %s %s\n", b.Name, string(b.Input))
				}
				text, isErr := callTool(ctx, session, b.Name, b.Input)
				if verbose {
					out.Printf("      < %s\n", firstLine(text))
				}
				answers = append(answers, map[string]any{
					"type": "tool_result", "tool_use_id": b.ID,
					"content": text, "is_error": isErr,
				})
			}
		}
		if len(answers) == 0 {
			// The model stopped calling tools, so the task is over
			// whatever it says about it. The score comes from the
			// calendar, never from the model's summary of what it did.
			break
		}
		results, merr := toolResults(answers)
		if merr != nil {
			return result{turns: turns, calls: calls, tokens: tokens, err: merr}
		}
		req.Messages = append(req.Messages, results)
	}

	pass, note := t.score(fake)
	return result{pass: pass, note: note, turns: turns, calls: calls, tokens: tokens}
}

// newSession builds a calendar, a server and a client for one task.
func newSession(t task) (*caltest.Server, *mcp.ClientSession, func(), error) {
	fake := t.seed()
	base := fake.Start()

	api := gapi.New(nil)
	api.Base = base
	cfg := config.Config{MaxEvents: 250, MaxCalendars: 25, Concurrency: 4, Sharing: true}
	svc := service.New(api, cfg)
	// A fixed clock, so a task that says "next Tuesday" fails for the
	// reason it should rather than because today moved.
	svc.Clock = when.FixedClock(time.Date(2026, time.March, 9, 9, 0, 0, 0, time.UTC))

	srv := server.New(server.Deps{Service: svc, Config: cfg, Version: "evals"})
	ct, st := mcp.NewInMemoryTransports()
	ctx := context.Background()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		fake.Close()
		return nil, nil, nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "evals", Version: "0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		_ = ss.Wait()
		fake.Close()
		return nil, nil, nil, err
	}
	return fake, cs, func() {
		_ = cs.Close()
		_ = ss.Wait()
		fake.Close()
	}, nil
}

// toolDefs hands the model the server's own tool list, unedited.
func toolDefs(ctx context.Context, cs *mcp.ClientSession) ([]toolDef, error) {
	listed, err := cs.ListTools(ctx, nil)
	if err != nil {
		return nil, err
	}
	var out []toolDef
	for _, t := range listed.Tools {
		schema, merr := json.Marshal(t.InputSchema)
		if merr != nil {
			return nil, merr
		}
		out = append(out, toolDef{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	if len(out) > 0 {
		// One breakpoint, on the last tool: the block in front of it is
		// identical on every turn and in every task, so it is read from
		// the cache rather than re-billed.
		out[len(out)-1].CacheControl = &cacheControl{Type: "ephemeral"}
	}
	return out, nil
}

// callTool runs one tool call and returns what the model sees.
//
// A refusal comes back as a tool result with is_error, exactly as it
// would in a client, because recovering from one is part of what these
// tasks measure: §4.3 and §4.2 refuse a write that has not chosen, and a
// model that cannot read the refusal cannot finish the task.
func callTool(ctx context.Context, cs *mcp.ClientSession, name string, input json.RawMessage) (string, bool) {
	var args map[string]any
	if len(input) > 0 {
		if err := json.Unmarshal(input, &args); err != nil {
			return fmt.Sprintf("this server could not read those arguments as JSON: %v", err), true
		}
	}
	out, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return err.Error(), true
	}
	var b strings.Builder
	for _, c := range out.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String(), out.IsError
}

// cost is the one-line summary of what a task took.
func (r result) cost() string {
	return fmt.Sprintf("%d turns, %d tool calls, %d in / %d out",
		r.turns, r.calls, r.tokens.InputTokens+r.tokens.CacheReadTokens, r.tokens.OutputTokens)
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if len(line) > 120 {
		return line[:120] + "…"
	}
	return line
}
