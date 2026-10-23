package tools_test

import (
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/google-calendar-mcp/v3/internal/tools"
	"github.com/mmedum/google-calendar-mcp/v3/internal/userconfig"
)

// argumentFor is a value for each required input a tool takes, by its
// name, so every registered tool can be called. A call need not succeed:
// what is held is what it leaves behind.
var argumentFor = map[string]any{
	"calendar": "primary", "calendars": []string{"primary"}, "event_id": "ev-standup",
	"title": "Settings probe", "start": "2026-04-01T09:00:00+02:00", "end": "2026-04-01T10:00:00+02:00",
	"from": "2026-03-16", "to": "2026-03-17", "query": "sync", "who": "colleague@example.test",
	"role": "reader", "notify": "none", "response": "accepted",
	"to_calendar": "team@group.calendar.example.test", "confirm": true,
}

// §3: the model never widens its own reach. Only the operator turns a
// group on, changes a limit, edits an allowlist or lifts a refusal, and
// does it outside the conversation, in the environment, the flags and
// the profile directory. Every registered tool is called, and none of
// them changes any of it.
func TestNoToolWritesTheServersOwnSettings(t *testing.T) {
	t.Setenv(userconfig.EnvDir, t.TempDir())
	if err := userconfig.Save("default", userconfig.Config{AccountEmail: "owner@example.test"}); err != nil {
		t.Fatal(err)
	}
	base, err := userconfig.BaseDir()
	if err != nil {
		t.Fatal(err)
	}
	profile, env := snapshot(t, base), os.Environ()

	cfg := tools.FullSurface(baseConfig())
	cs, cleanup := session(t, cfg)
	defer cleanup()
	listed := listTools(t, cfg)
	for _, tool := range listed {
		args := map[string]any{}
		if in, ok := tool.InputSchema.(map[string]any); ok {
			required, _ := in["required"].([]any)
			for _, r := range required {
				name, _ := r.(string)
				v, known := argumentFor[name]
				if !known {
					t.Fatalf("%s requires %q, which the test has no value for", tool.Name, name)
				}
				args[name] = v
			}
		}
		if _, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool.Name, Arguments: args}); err != nil {
			t.Fatalf("CallTool(%s): %v", tool.Name, err)
		}
	}
	// A floor, so a surface that registered nothing is not a pass.
	if len(listed) < 21 {
		t.Fatalf("called %d tools; the full surface has more", len(listed))
	}

	if now := snapshot(t, base); !maps.Equal(now, profile) {
		t.Errorf("the profile directory changed under the tools:\nbefore %v\nafter  %v", profile, now)
	}
	if now := os.Environ(); !slices.Equal(now, env) {
		t.Error("the environment changed under the tools")
	}
}

// snapshot is every file under dir, by path, with its mode and contents.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content := ""
		if !d.IsDir() {
			b, err := os.ReadFile(path) //nolint:gosec // a path under the test's own temporary directory
			if err != nil {
				return err
			}
			content = string(b)
		}
		out[path] = info.Mode().String() + " " + content
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
