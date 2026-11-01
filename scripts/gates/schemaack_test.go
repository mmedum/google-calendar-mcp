package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// plain is a released surface: two tools and a resource template.
const plain = `{"version":"v1.0.0","tools":[
	{"name":"create_event","annotations":{"destructiveHint":false},
	 "input_schema":{"type":"object","properties":{"calendar":{"type":"string"}}}},
	{"name":"get_event","annotations":{"readOnlyHint":true},
	 "input_schema":{"type":"object","properties":{"calendar":{"type":"string"}}}}],
	"resources":[{"uri_template":"gcal://calendars/{+calendar_id}","name":"calendar"}]}`

// flipped is plain with create_event made destructive.
var flipped = strings.Replace(plain, `"destructiveHint":false`, `"destructiveHint":true`, 1)

// schema-ack fails a pull request unless its commits name every changed
// item in a footer, and every break of a release in a BREAKING CHANGE:
// footer.
func TestSchemaAckNeedsEveryItemNamed(t *testing.T) {
	relabeled := strings.Replace(plain, `"name":"calendar"`, `"name":"cal"`, 1)
	broken := strings.Replace(plain, `"properties":{"calendar"`, `"properties":{"cal"`, 1)
	withoutTemplate := strings.Replace(plain, `{"uri_template":"gcal://calendars/{+calendar_id}","name":"calendar"}`, ``, 1)
	reworded := strings.Replace(plain, `"version":"v1.0.0",`, `"version":"v1.0.0","instructions":"new",`, 1)
	const (
		unnamed      = "no footer names create_event"
		unnamedBreak = "no BREAKING CHANGE: footer names create_event"
	)
	for _, tc := range []struct {
		name, built, message, wantErr string
	}{
		{"an unchanged surface needs nothing", plain, "Tidy the README", ""},
		{"a change needs a footer", flipped, "Mark create_event destructive", unnamed},
		{"a footer naming the tool acknowledges it", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event: destructiveHint true", ""},
		{"a footer naming another tool does not", flipped, "Mark it\n\nSCHEMA-CHANGE: get_event: destructiveHint true", unnamed},
		{"a name is a whole token", flipped, "Mark it\n\nSCHEMA-CHANGE: create_events: destructiveHint true", unnamed},
		{"a continuation line can name it", flipped, "Mark them\n\nSCHEMA-CHANGE: get_event,\n  create_event: destructiveHint true", ""},
		{"an empty footer says nothing", flipped, "Mark it\n\nSCHEMA-CHANGE:", unnamed},
		{"the next line is not the footer's text", flipped, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: create_event", unnamed},
		{"a footer must open its line", flipped, "Mark it, see SCHEMA-CHANGE: create_event", unnamed},
		{"a line of prose is not a footer", flipped, "Mark it\n\nThe gate wants a\nSCHEMA-CHANGE: create_event footer, which this is not.", unnamed},
		{"a subject is not a footer", flipped, "SCHEMA-CHANGE: create_event", unnamed},
		{"an indented block is not a footer block", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event: hints\n\n    go run ./scripts/gates schema-ack", unnamed},
		{"a footer above the co-author trailer", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", flipped, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: create_event: hints", ""},
		{"a resource template by its URI", relabeled, "Rename it\n\nSCHEMA-CHANGE: gcal://calendars/{+calendar_id}: renamed", ""},
		{"a resource template unnamed", relabeled, "Rename it\n\nSCHEMA-CHANGE: create_event: renamed", "no footer names gcal://calendars/{+calendar_id}"},
		{"the instructions by their key", reworded, "Reword them\n\nSCHEMA-CHANGE: instructions: reworded", ""},
		{"a break named only as a change", broken, "Rename the field\n\nSCHEMA-CHANGE: create_event: calendar is cal", unnamedBreak},
		{"BREAKING CHANGE: acknowledges a break", broken, "Rename the field\n\nBREAKING CHANGE: create_event: calendar is cal", ""},
		{"BREAKING-CHANGE: is its synonym", broken, "Rename the field\n\nBREAKING-CHANGE: create_event: calendar is cal", ""},
		{"a removed template needs BREAKING CHANGE:", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: gcal://calendars/{+calendar_id}: gone",
			"no BREAKING CHANGE: footer names gcal://calendars/{+calendar_id}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(io.Discard, readTest(t, plain), readTest(t, tc.built), "base")
			err := checkAck(c, []string{"Tidy the docs", tc.message}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// A name in backticks is taken whole, so a URI with a comma in it can be
// named; split on its comma, it names nothing.
func TestAFooterCanNameAURIWithACommaInBackticks(t *testing.T) {
	const tmpl = "gcal://calendars/{+calendar_id}{?fields,view}"
	was := strings.Replace(plain, `"gcal://calendars/{+calendar_id}"`, `"`+tmpl+`"`, 1)
	now := strings.Replace(was, `"name":"calendar"`, `"name":"cal"`, 1)
	c := compareSurfaces(io.Discard, readTest(t, was), readTest(t, now), "base")
	if err := checkAck(c, []string{"Rename\n\nSCHEMA-CHANGE: `" + tmpl + "`: renamed"}, "base"); err != nil {
		t.Errorf("checkAck = %v, want the backticked URI to name the template", err)
	}
	if err := checkAck(c, []string{"Rename\n\nSCHEMA-CHANGE: " + tmpl + ": renamed"}, "base"); err == nil {
		t.Error("checkAck passed with the URI split on its comma, which names nothing")
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := readTest(t, plain)
	base := readTest(t, strings.Replace(plain, `"tools":[`, `"tools":[{"name":"draft_tool"},`, 1))
	head := readTest(t, strings.Replace(plain, `"properties":{"calendar"`, `"properties":{"cal"`, 1))
	var report strings.Builder
	c := releaseBreaksOnly(&report, compareSurfaces(io.Discard, base, head, "base"),
		compareSurfaces(io.Discard, release, head, "release").breaking)
	if want := "    draft_tool breaks nothing a release shipped, so it is a change\n"; report.String() != want {
		t.Errorf("the report says %q, want %q", report.String(), want)
	}
	if want := []change{{"create_event", "create_event: input field calendar removed"}}; !slices.Equal(c.breaking, want) {
		t.Errorf("breaking = %v, want %v", c.breaking, want)
	}
	if want := []change{{"draft_tool", "draft_tool"}}; !slices.Equal(c.changed, want) {
		t.Errorf("changed = %v, want %v", c.changed, want)
	}
	footer := "Drop and rename\n\nSCHEMA-CHANGE: draft_tool: never released\nBREAKING CHANGE: create_event: calendar is cal"
	if err := checkAck(c, []string{footer}, "base"); err != nil {
		t.Errorf("checkAck = %v, want both acknowledged", err)
	}
}

// The release is the baseline the base branch records. Before the first
// release there is none, and nothing breaks one.
func TestReleasedBreaksReadTheBaseBranchBaseline(t *testing.T) {
	t.Chdir(t.TempDir())
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com",
			"-c", "commit.gpgsign=false"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "--quiet", "--initial-branch=main")
	git("commit", "--quiet", "--allow-empty", "-m", "Start")
	head := readTest(t, strings.Replace(plain, `"properties":{"calendar"`, `"properties":{"cal"`, 1))

	breaks, err := releasedBreaks("main", head)
	if err != nil || len(breaks) != 0 {
		t.Fatalf("releasedBreaks with no baseline = %v, %v; want none", breaks, err)
	}

	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.FromSlash(baselineFile), []byte(plain), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", baselineFile)
	git("commit", "--quiet", "-m", "Release")
	// The working tree's baseline is a branch's, not the release's.
	if err := os.WriteFile(filepath.FromSlash(baselineFile), []byte(`{"tools":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	breaks, err = releasedBreaks("main", head)
	if want := []change{{"create_event", "create_event: input field calendar removed"}}; err != nil || !slices.Equal(breaks, want) {
		t.Errorf("releasedBreaks = %v, %v; want %v", breaks, err, want)
	}
}
