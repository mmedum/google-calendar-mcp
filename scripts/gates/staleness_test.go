package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
)

// A Claude Code permission rule for a tool the binary does not publish
// matches nothing, so the person it was meant to ask is never asked.
func TestAPermissionRuleNamesAPublishedTool(t *testing.T) {
	tools := []string{"list_events", "delete_calendar"}
	table := "| `list_events` | x |\n| `delete_calendar` | x |\n"
	if got := readmeToolProblems(table+`"ask": ["mcp__google-calendar__delete_calendar"]`, tools); len(got) != 0 {
		t.Errorf("a rule for a published tool was reported: %v", got)
	}
	got := readmeToolProblems(table+`"ask": ["mcp__google-calendar__delete_calendars"]`, tools)
	want := []string{"README gives a permission rule for delete_calendars, which the binary does not publish"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The configuration page names every variable the server reads, a
// removed one included, as a whole word, names no other, and says what
// a profile name is in the server's own words.
func TestConfigDocProblems(t *testing.T) {
	doc := string(repoFile(t, configDocPath))
	if got := configDocProblems(config.Vars(), doc); len(got) != 0 {
		t.Errorf("the committed page: %q", got)
	}
	for _, tc := range []struct {
		name, old, new string
		want           []string
	}{
		{"a variable left out", "`GCAL_WRITE_TIMEOUT`", "`the write timeout`",
			[]string{"docs/configuration.md does not document GCAL_WRITE_TIMEOUT"}},
		{"a removed variable left out", "`GCAL_REQUIRE_PROMPT`", "`the prompt setting`",
			[]string{"docs/configuration.md does not document GCAL_REQUIRE_PROMPT"}},
		{"a name kept only inside a longer one", "`GCAL_CONFIG_DIR`", "`GCAL_CONFIG_DIRECTORY`", []string{
			"docs/configuration.md does not document GCAL_CONFIG_DIR",
			"docs/configuration.md documents GCAL_CONFIG_DIRECTORY, which the code does not read",
		}},
		{"a variable the code does not read", "`GCAL_CONCURRENCY`", "`GCAL_CONCURRENCY` and `GCAL_FANOUT`",
			[]string{"docs/configuration.md documents GCAL_FANOUT, which the code does not read"}},
		// A letter of either case on either side makes a longer word.
		{"a name kept only inside a plural", "`GCAL_CONCURRENCY`", "`GCAL_CONCURRENCYs`",
			[]string{"docs/configuration.md does not document GCAL_CONCURRENCY"}},
		{"a name kept only after a letter", "`GCAL_LOG_FORMAT`", "`xGCAL_LOG_FORMAT`",
			[]string{"docs/configuration.md does not document GCAL_LOG_FORMAT"}},
		{"a name kept only after a digit", "`GCAL_LOG_LEVEL`", "`2GCAL_LOG_LEVEL`",
			[]string{"docs/configuration.md does not document GCAL_LOG_LEVEL"}},
		// A name followed by * is a pattern, documenting nothing, and a
		// prefix ending in _ is not a name.
		{"a name kept only as a pattern", "`GCAL_HTTP_TIMEOUT`", "`GCAL_HTTP_TIMEOUT*` and `GCAL_LIVE_*`",
			[]string{"docs/configuration.md does not document GCAL_HTTP_TIMEOUT"}},
		{"a pattern beside the name", "`GCAL_HTTP_TIMEOUT`", "`GCAL_HTTP_TIMEOUT` and `GCAL_HTTP*`", nil},
		// Bold and italics put a * on both sides of a name.
		{"a name in bold", "`GCAL_HTTP_TIMEOUT`", "**GCAL_HTTP_TIMEOUT** and *GCAL_FANOUT*",
			[]string{"docs/configuration.md documents GCAL_FANOUT, which the code does not read"}},
		{"the profile rule in other words", "- and _, starting", "`-` and `_`, starting",
			[]string{`docs/configuration.md does not say a profile name is "up to 64 characters from a to z, ` +
				`0 to 9, - and _, starting with a letter or digit"`}},
	} {
		if !strings.Contains(doc, tc.old) {
			t.Fatalf("%s: the page does not say %q", tc.name, tc.old)
		}
		if got := configDocProblems(config.Vars(), strings.ReplaceAll(doc, tc.old, tc.new)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
