package main

import (
	"slices"
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

// A setting a release removed may be named in the documentation, to say
// it is gone; a setting the code never read may not.
func TestTheDocsMayNameARemovedSetting(t *testing.T) {
	vars := []config.Var{{Name: "GCAL_PROFILE"}, {Name: "GCAL_REQUIRE_PROMPT", Removed: true}}
	if got, n := settingProblems(vars, "`GCAL_PROFILE` and the removed `GCAL_REQUIRE_PROMPT`"); len(got) != 0 || n != 1 {
		t.Errorf("got %v and %d settings, want none and 1", got, n)
	}
	got, _ := settingProblems(vars, "`GCAL_PROFILE` and `GCAL_ASK_FIRST`")
	want := []string{"docs/configuration.md documents GCAL_ASK_FIRST, which the code does not define"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	got, _ = settingProblems(vars, "the removed `GCAL_REQUIRE_PROMPT` alone")
	want = []string{"docs/configuration.md does not document GCAL_PROFILE"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
