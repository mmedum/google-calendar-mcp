package tools

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v4/internal/config"
	"github.com/mmedum/google-calendar-mcp/v4/internal/service"
)

// The mark makes Claude Code prompt on every call, so it goes only on a
// tool the destructive flag registers (§9a). One that asks for it on any
// other kind fails when the server starts, not when a person first meets
// a prompt on every call.
func TestOnlyAFlaggedToolMayPromptEveryCall(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a Write tool that prompts on every call was registered")
		}
	}()
	add(mcp.NewServer(&mcp.Implementation{Name: "t", Version: "0"}, nil), Deps{Config: FullSurface(config.Config{})},
		Def[destructiveIn, service.CalendarWriteResult]{
			Name: "x", Description: "x", Kind: Write, PromptEveryCall: true,
			Handle: func(context.Context, destructiveIn) (service.CalendarWriteResult, error) {
				return service.CalendarWriteResult{}, nil
			},
		})
}
