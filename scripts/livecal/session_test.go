//go:build live

package main

import (
	"bufio"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v3/internal/redact"
)

// A step whose call the interrupt cut short is not run rather than
// failed: what the server would have answered is not known.
func TestAStepCutShortByTheInterruptIsNotRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := &session{
		stdin: discard{}, out: bufio.NewScanner(interrupted{cancel}),
		stderr: &strings.Builder{}, nextID: 1, person: &person{},
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
