// Package tools registers the MCP tools. A handler validates its input,
// calls the service and hands back a result; every rule worth testing
// lives in the service.
//
// Registration is one function rather than a convention, because rules
// kept by hand at twenty call sites are twenty ways to be quietly wrong.
// One Kind per tool decides its annotations, whether read-only mode
// keeps it, and whether it registers at all.
//
// Registration gates the tool; the service gates the act. `confirm` and
// the two gated actions live in the service, where the caller can be
// told why.
package tools

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v2/internal/config"
	"github.com/mmedum/google-calendar-mcp/v2/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v2/internal/service"
)

// Kind says which world a tool touches.
type Kind int

// Kinds.
const (
	// Read changes nothing and is registered in read-only mode.
	Read Kind = iota
	// Write changes the calendar. Repeating it is not the same as doing
	// it once.
	Write
	// IdempotentWrite changes the calendar to a stated end state, so
	// repeating it lands in the same place.
	IdempotentWrite
	// Sharing changes who can see a calendar. It is its own Kind rather
	// than a Write because GCAL_SHARING=off removes exactly these, and
	// because widening access is the one act here whose blast radius
	// leaves the account.
	Sharing
	// SharingRead reads who can see a calendar. It changes nothing, so
	// its annotations say read-only — but GCAL_SHARING=off removes it
	// with the other two, because a deployment that has turned sharing
	// off has turned off the surface, not merely the writes.
	//
	// Read-only mode drops it as well. §8 registers the eight read tools
	// there and this is not one of them: get_calendar already reports a
	// calendar's exposure, so nothing is unreachable, and the read-only
	// surface stays the list §8 names rather than the list minus a
	// judgment call.
	SharingRead
	// Canceling removes a meeting. It is its own Kind for one reason:
	// its annotation has to say DESTRUCTIVE while it still registers
	// without the destructive flag.
	//
	// §9 argues both halves. Canceling a meeting is what a calendar is
	// for and Google keeps the record, so gating it would put a flag
	// between the model and the most ordinary write there is — training
	// people to set GCAL_ENABLE_DESTRUCTIVE permanently, which would arm
	// clear_calendar too. A gate everybody turns on protects nobody. But
	// a client deciding whether to confirm with a person deserves the
	// truthful hint, and "this may destroy something" is the truth.
	Canceling
	// Destructive removes something Calendar cannot bring back. Not
	// registered at all unless the destructive flag is set, and still
	// needs confirm on the call. The two are delete_calendar and
	// clear_calendar; cancel_event is Canceling above, and §9 argues
	// the difference.
	Destructive
)

// Deps are what the tools need.
type Deps struct {
	Service *service.Service
	Config  config.Config
	Logger  *slog.Logger
}

// Register adds every tool the configuration allows.
func Register(s *mcp.Server, d Deps) {
	if d.Logger == nil {
		d.Logger = slog.New(slog.DiscardHandler)
	}
	registerRead(s, d)
	registerWrite(s, d)
	registerCalendars(s, d)
	registerResources(s, d)
}

// Def is one tool.
//
// Out is constrained to service.Rendered, so a tool whose result has no
// rendering does not compile. That is the mechanism behind "send both
// halves": every tool returns both, and they are never the same bytes.
type Def[In any, Out service.Rendered] struct {
	Name        string
	Description string
	Kind        Kind
	Handle      func(ctx contextContext, in In) (Out, error)
}

// contextContext is context.Context; aliased so the import list above
// stays short in a file that otherwise needs no context.
type contextContext = ctxContext

// add registers one tool if the configuration allows it.
func add[In any, Out service.Rendered](s *mcp.Server, d Deps, def Def[In, Out]) {
	if !allowed(def.Kind, d.Config) {
		return
	}
	tool := &mcp.Tool{
		Name:        def.Name,
		Description: def.Description,
		Annotations: annotationsFor(def.Kind),
	}
	if def.Kind == Destructive {
		// A signal a client MAY act on, never a control. The standard is
		// explicit: a host in auto-approve runs an annotated tool without
		// prompting, so the real gate is that this tool is unregistered
		// unless the flag is set.
		tool.Meta = mcp.Meta{"anthropic/requiresUserInteraction": true}
	}
	mcp.AddTool(s, tool, func(ctx contextContext, _ *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		out, err := def.Handle(ctx, in)
		if err != nil {
			var zero Out
			return nil, zero, fail(err)
		}
		// Content is set here, always. Left unset, the SDK fills it with
		// the JSON of the output — the same bytes twice, and the one
		// shape the specification asks callers not to send.
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: out.Render()}},
		}, out, nil
	})
}

// FullSurface is the configuration under which every tool registers.
//
// It lives beside allowed rather than in the command that dumps the
// schemas, because it has to name every gate allowed reads and the dump
// has no way to know when a new one appears. A sibling's dump set one
// flag by hand for three phases; a fourth gate arrived and the tool
// behind it was missing from the schema diff with every test green.
func FullSurface(cfg config.Config) config.Config {
	cfg.ReadOnly = false
	cfg.EnableDestructive = true
	cfg.Sharing = true
	return cfg
}

// allowed applies the registration gates. All of them are server-side:
// annotations are hints the specification says a client may not trust.
func allowed(k Kind, cfg config.Config) bool {
	switch k {
	case Read:
		return true
	case Destructive:
		return cfg.EnableDestructive && !cfg.ReadOnly
	case Sharing, SharingRead:
		return cfg.Sharing && !cfg.ReadOnly
	default:
		return !cfg.ReadOnly
	}
}

func annotationsFor(k Kind) *mcp.ToolAnnotations {
	no, yes := ptr(false), ptr(true)
	switch k {
	case Read, SharingRead:
		return &mcp.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: no}
	case IdempotentWrite:
		return &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: no, OpenWorldHint: no}
	case Canceling:
		return &mcp.ToolAnnotations{IdempotentHint: true, DestructiveHint: yes, OpenWorldHint: no}
	case Destructive:
		return &mcp.ToolAnnotations{DestructiveHint: yes, OpenWorldHint: no}
	case Sharing:
		// OpenWorldHint is true here and nowhere else: sharing is the one
		// act whose effect leaves this account.
		return &mcp.ToolAnnotations{DestructiveHint: no, OpenWorldHint: yes}
	default:
		return &mcp.ToolAnnotations{DestructiveHint: no, OpenWorldHint: no}
	}
}

func ptr[T any](v T) *T { return &v }

// fail turns an error into the tool result the standard specifies:
// "[class] actionable message", never a protocol error.
func fail(err error) error {
	var e *gapi.Error
	if errors.As(err, &e) {
		return errors.New(e.Error())
	}
	// Anything unclassified is a bug in this server rather than
	// something the caller did. It still has to arrive as a tool result
	// with a class, so the vocabulary stays closed from the caller's
	// side (§6.5).
	return fmt.Errorf("[%s] %s", gapi.ClassUnavailable, err.Error())
}
