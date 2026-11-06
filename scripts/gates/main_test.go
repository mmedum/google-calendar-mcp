package main

import (
	"slices"
	"strings"
	"testing"
)

// The usage text is generated from the dispatch, so a command the
// dispatch runs is never missing from it, the manual ones included, and
// each line names the pipeline that runs it.
func TestUsageNamesEachCommandWithItsArguments(t *testing.T) {
	lines := strings.Split(usage(), "\n")
	if len(lines) != len(commands)+2 {
		t.Fatalf("usage has %d lines for %d commands:\n%s", len(lines), len(commands), usage())
	}
	for _, want := range []string{
		"  schema-refetch                     check the vendored schemas against what their sources serve over the network (manual)",
		"  release-notes VERSION [CHANGELOG]  print the CHANGELOG section a tag publishes (release)",
		"  live-cover BIN                     every published tool has a step in the live driver (make check)",
		"  schema-ack BASE HEAD               every tool-surface change since the merge-base is named in a commit footer (pull request)",
	} {
		if !slices.Contains(lines, want) {
			t.Errorf("usage has no line %q:\n%s", want, usage())
		}
	}
}

// A command takes as many words as its usage spells, and a word in
// brackets may be left out.
func TestArityIsReadFromTheUsage(t *testing.T) {
	for _, tc := range []struct {
		args        string
		least, most int
	}{
		{"", 0, 0},
		{"BIN", 1, 1},
		{"[history]", 0, 1},
		{"VERSION [CHANGELOG]", 1, 2},
		{"DIST VERSION OUT", 3, 3},
	} {
		if least, most := arity(tc.args); least != tc.least || most != tc.most {
			t.Errorf("arity(%q) = %d, %d, want %d, %d", tc.args, least, most, tc.least, tc.most)
		}
	}
}
