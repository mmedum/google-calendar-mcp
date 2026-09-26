package tools

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v2/internal/config"
)

// Exported for tests in the tools_test package. The gates and the
// annotation table are the two things worth asserting exhaustively, and
// both are unexported because nothing outside this package should call
// them in production.

// Allowed exposes allowed.
func Allowed(k Kind, cfg config.Config) bool { return allowed(k, cfg) }

// AnnotationsFor exposes annotationsFor.
func AnnotationsFor(k Kind) *mcp.ToolAnnotations { return annotationsFor(k) }

// Fail exposes fail.
func Fail(err error) error { return fail(err) }
