package tools

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// A call that fails without a result is [blocked] while its question is
// out, and [ambiguous_outcome] from the moment an answer confirmed the
// write: never "nothing was changed" after that. The client's error text
// is not repeated.
func TestAFailureAfterTheAnswerIsNeverNothingWritten(t *testing.T) {
	for _, tc := range []struct {
		stage int32
		want  []string
	}{
		{stageWaiting, []string{"[blocked]", "not confirmed by the person", "Nothing was changed"}},
		{stageWriting, []string{"[ambiguous_outcome]", "verdict: unknown", "Do not make the call again"}},
		{stageWritten, []string{"[ambiguous_outcome]", "verdict: written"}},
	} {
		mw := askFailures(newAsking(slog.New(slog.DiscardHandler)))
		next := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			setStage(ctx, tc.stage)
			return nil, errors.New("client-side secret text")
		}
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "delete_calendar"}}
		res, err := mw(next)(context.Background(), "tools/call", req)
		if err != nil {
			t.Fatalf("stage %d: a protocol error came through: %v", tc.stage, err)
		}
		out := res.(*mcp.CallToolResult).Content[0].(*mcp.TextContent).Text
		for _, w := range tc.want {
			if !strings.Contains(out, w) {
				t.Errorf("stage %d: %q does not say %q", tc.stage, out, w)
			}
		}
		if strings.Contains(out, "secret") || !strings.Contains(out, "delete_calendar") {
			t.Errorf("stage %d: %s", tc.stage, out)
		}
	}
	// A call that never asked fails as it would have.
	mw := askFailures(newAsking(slog.New(slog.DiscardHandler)))
	boom := errors.New("boom")
	next := func(context.Context, string, mcp.Request) (mcp.Result, error) { return nil, boom }
	if _, err := mw(next)(context.Background(), "tools/call", &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "list_events"}}); !errors.Is(err, boom) {
		t.Errorf("a call that asked nothing: %v", err)
	}
}
