package main

import (
	"strings"
	"testing"
)

// schema-ack fails a pull request whose surface changed unless a commit
// it adds carries the footer in its footer block, and a break needs
// BREAKING CHANGE:.
func TestSchemaAckNeedsAFooter(t *testing.T) {
	const needsFooter, needsBreaking = "says SCHEMA-CHANGE: or BREAKING CHANGE:", "says BREAKING CHANGE:"
	none := surfaceChanges{}
	changed := surfaceChanges{changed: []string{"get_event (annotations.destructiveHint)"}}
	broke := surfaceChanges{breaking: []string{"get_event: input field removed calendar"}}
	for _, tc := range []struct {
		name    string
		changes surfaceChanges
		message string
		wantErr string
	}{
		{"an unchanged surface needs nothing", none, "Tidy the README", ""},
		{"a change needs a footer", changed, "Mark get_event destructive", needsFooter},
		{"SCHEMA-CHANGE: acknowledges it", changed, "Mark it\n\nSCHEMA-CHANGE: destructiveHint true", ""},
		{"an empty footer says nothing", changed, "Mark it\n\nSCHEMA-CHANGE:", needsFooter},
		{"the next line is not the footer's text", changed, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: someone", needsFooter},
		{"a footer must open its line", changed, "Mark it, see SCHEMA-CHANGE: hints", needsFooter},
		{"a line of prose is not a footer", changed, "Mark it\n\nThe gate fails unless a commit carries a\nSCHEMA-CHANGE: footer, which this one does not.", needsFooter},
		{"a subject is not a footer", changed, "SCHEMA-CHANGE: hints", needsFooter},
		{"an indented block is not a footer block", changed, "Mark it\n\nSCHEMA-CHANGE: hints\n\n    make schema-ack", needsFooter},
		{"a footer above the co-author trailer", changed, "Mark it\n\nSCHEMA-CHANGE: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", changed, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: hints", ""},
		{"a break needs BREAKING CHANGE:", broke, "Drop it\n\nSCHEMA-CHANGE: calendar is gone", needsBreaking},
		{"BREAKING CHANGE: acknowledges a break", broke, "Drop it\n\nBREAKING CHANGE: calendar is gone", ""},
		{"BREAKING-CHANGE: is its synonym", broke, "Drop it\n\nBREAKING-CHANGE: calendar is gone", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkAck(tc.changes, []string{"Tidy the docs", tc.message}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}
