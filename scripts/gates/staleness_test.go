package main

import (
	"slices"
	"testing"
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
	src := []byte(`def(&s.Profile, "profile", "PROFILE", "default", "x")
	removed(&s.RequirePrompt, env, "REQUIRE_PROMPT")`)
	if got, n := settingProblems(src, "`GCAL_PROFILE` and the removed `GCAL_REQUIRE_PROMPT`"); len(got) != 0 || n != 1 {
		t.Errorf("got %v and %d settings, want none and 1", got, n)
	}
	got, _ := settingProblems(src, "`GCAL_PROFILE` and `GCAL_ASK_FIRST`")
	want := []string{"docs/configuration.md documents GCAL_ASK_FIRST, which the code does not define"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// A switch is a setting like any other, and an old name a release
// replaced may be named, to say it still works.
func TestTheDocsHoldSwitchesAndOldNames(t *testing.T) {
	src := []byte(`sw(&s.ReadOnly, "read-only", "READ_ONLY", false, "x")
	renamed(&s.ReadOnlyOld, env, "READONLY")`)
	if got, n := settingProblems(src, "`GCAL_READ_ONLY`, once `GCAL_READONLY`"); len(got) != 0 || n != 1 {
		t.Errorf("got %v and %d settings, want none and 1", got, n)
	}
	got, _ := settingProblems(src, "`GCAL_READONLY` alone")
	want := []string{"docs/configuration.md does not document GCAL_READ_ONLY"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
