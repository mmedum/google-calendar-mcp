//go:build live

package main

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v5/internal/redact"
)

// A step whose call the interrupt cut short is not run rather than
// failed: what the server would have answered is not known.
func TestAStepCutShortByTheInterruptIsNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &session{
		stdin: discard{}, out: bufio.NewScanner(interrupted{cancel}),
		stderr: &strings.Builder{}, nextID: 1,
	}
	var printed strings.Builder
	r := &results{out: redact.New(&printed), invented: map[string]bool{}}
	r.run(ctx, s, step{
		name: "create_calendar", tool: "create_calendar", args: map[string]any{"title": "Livecal probe"},
		check: func(callResult) (verdict, string) { return pass, "" },
	})
	if r.failed != 0 || r.undetermined != 1 ||
		!strings.Contains(printed.String(), "not run: the driver was interrupted during it") {
		t.Fatalf("failed=%d undetermined=%d, want the step counted as not run:\n%s",
			r.failed, r.undetermined, printed.String())
	}
}

// interrupted is a server the interrupt reaches before it answers: the
// read that waits for the answer cancels the run and finds the stream
// closed.
type interrupted struct{ cancel context.CancelFunc }

func (i interrupted) Read([]byte) (int, error) {
	i.cancel()
	return 0, io.EOF
}

// discard takes the request and closes without complaint.
type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
func (discard) Close() error                { return nil }

// A step that expects a refusal passes only on its class and its words,
// so a refusal for another reason, even of the same class, fails it.
func TestRefusedHoldsTheClassAndTheWords(t *testing.T) {
	for _, c := range []struct {
		name string
		r    callResult
		want verdict
		say  string
	}{
		{"the call went through", callResult{text: "created"}, fail, "accepted"},
		{"another class", callResult{text: "[blocked] notify:none is refused there", isError: true}, fail,
			"not classified invalid"},
		{"the class for another reason", callResult{text: "[invalid] missing properties: [\"to\"]", isError: true},
			fail, `does not say "crosses midnight"`},
		{"one word of two", callResult{text: "[invalid] crosses midnight", isError: true}, fail,
			`does not say "night shift"`},
		{"the class and every word", callResult{text: "[invalid] a mask that crosses midnight: a night shift",
			isError: true}, pass, `refused with [invalid], saying "crosses midnight", "night shift"`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, msg := refused(c.r, "accepted", "invalid", "crosses midnight", "night shift")
			if got != c.want || !strings.Contains(msg, c.say) {
				t.Errorf("refused(%q) = %v, %q; want %v and a message holding %q", c.r.text, got, msg, c.want, c.say)
			}
		})
	}
}
