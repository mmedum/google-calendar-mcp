package main

import (
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

// flipped is plain with create_event made destructive, and renamed is
// plain with create_event's calendar input renamed, which breaks it.
var (
	flipped = strings.Replace(plain, `"destructiveHint":false`, `"destructiveHint":true`, 1)
	renamed = strings.Replace(plain, `"properties":{"calendar"`, `"properties":{"cal"`, 1)
)

// renameBreak is the break renamed makes of plain.
var renameBreak = []change{{"create_event", "create_event: input field calendar removed"}}

// schema-ack fails a pull request unless its commits name every changed
// item in a footer, and every break of a release in a BREAKING CHANGE:
// footer.
func TestSchemaAckNeedsEveryItemNamed(t *testing.T) {
	relabeled := strings.Replace(plain, `"name":"calendar"`, `"name":"cal"`, 1)
	withoutTemplate := strings.Replace(plain, `{"uri_template":"gcal://calendars/{+calendar_id}","name":"calendar"}`, ``, 1)
	reworded := strings.Replace(plain, `"version":"v1.0.0",`, `"version":"v1.0.0","instructions":"new",`, 1)
	const (
		wantUnnamed      = "no footer names create_event"
		wantUnnamedBreak = "no BREAKING CHANGE: footer names create_event"
	)
	for _, tc := range []struct {
		name, built, message, wantErr string
	}{
		{"an unchanged surface needs nothing", plain, "Tidy the README", ""},
		{"a change needs a footer", flipped, "Mark create_event destructive", wantUnnamed},
		{"a footer naming the tool acknowledges it", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event: destructiveHint true", ""},
		{"a footer naming another tool does not", flipped, "Mark it\n\nSCHEMA-CHANGE: get_event: destructiveHint true", wantUnnamed},
		{"a name is a whole token", flipped, "Mark it\n\nSCHEMA-CHANGE: create_events: destructiveHint true", wantUnnamed},
		{"a continuation line can name it", flipped, "Mark them\n\nSCHEMA-CHANGE: get_event,\n  create_event: destructiveHint true", ""},
		{"an empty footer says nothing", flipped, "Mark it\n\nSCHEMA-CHANGE:", wantUnnamed},
		{"the next line is not the footer's text", flipped, "Mark it\n\nSCHEMA-CHANGE:\nCo-Authored-By: create_event", wantUnnamed},
		{"a footer must open its line", flipped, "Mark it, see SCHEMA-CHANGE: create_event", wantUnnamed},
		{"a line of prose is not a footer", flipped, "Mark it\n\nThe gate wants a\nSCHEMA-CHANGE: create_event footer, which this is not.", wantUnnamed},
		{"a subject is not a footer", flipped, "SCHEMA-CHANGE: create_event", wantUnnamed},
		{"an indented block is not a footer block", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event: hints\n\n    go run ./scripts/gates schema-ack", wantUnnamed},
		{"a footer above the co-author trailer", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event: hints\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a footer beside the co-author trailer", flipped, "Mark it\n\nCo-Authored-By: someone <x@example.com>\nSCHEMA-CHANGE: create_event: hints", ""},
		{"a resource template by its URI", relabeled, "Rename it\n\nSCHEMA-CHANGE: gcal://calendars/{+calendar_id}: renamed", ""},
		{"a resource template unnamed", relabeled, "Rename it\n\nSCHEMA-CHANGE: create_event: renamed", "no footer names gcal://calendars/{+calendar_id}"},
		{"the instructions by their key", reworded, "Reword them\n\nSCHEMA-CHANGE: instructions: reworded", ""},
		{"a break named only as a change", renamed, "Rename the field\n\nSCHEMA-CHANGE: create_event: calendar is cal", wantUnnamedBreak},
		{"BREAKING CHANGE: acknowledges a break", renamed, "Rename the field\n\nBREAKING CHANGE: create_event: calendar is cal", ""},
		{"BREAKING-CHANGE: is its synonym", renamed, "Rename the field\n\nBREAKING-CHANGE: create_event: calendar is cal", ""},
		{"a removed template needs BREAKING CHANGE:", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: gcal://calendars/{+calendar_id}: gone",
			"no BREAKING CHANGE: footer names gcal://calendars/{+calendar_id}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(readTest(t, plain), readTest(t, tc.built))
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
	c := compareSurfaces(readTest(t, was), readTest(t, now))
	if err := checkAck(c, []string{"Rename\n\nSCHEMA-CHANGE: `" + tmpl + "`: renamed"}, "base"); err != nil {
		t.Errorf("checkAck = %v, want the backticked URI to name the template", err)
	}
	if err := checkAck(c, []string{"Rename\n\nSCHEMA-CHANGE: " + tmpl + ": renamed"}, "base"); err == nil {
		t.Error("checkAck passed with the URI split on its comma, which names nothing")
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change, and
// the report lists it as one.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := readTest(t, plain)
	base := readTest(t, strings.Replace(plain, `"tools":[`, `"tools":[{"name":"draft_tool"},`, 1))
	head := readTest(t, renamed)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if !slices.Equal(c.breaking, renameBreak) {
		t.Errorf("breaking = %v, want %v", c.breaking, renameBreak)
	}
	if want := []change{{"draft_tool", "draft_tool removed"}}; !slices.Equal(c.changed, want) {
		t.Errorf("changed = %v, want %v", c.changed, want)
	}
	var report strings.Builder
	c.write(&report, "base")
	want := "  against base: 0 added, 1 changed, 1 breaking\n" +
		"    ~ draft_tool removed\n" +
		"    ! create_event: input field calendar removed  BREAKING\n"
	if report.String() != want {
		t.Errorf("the report is\n%s\nwant\n%s", report.String(), want)
	}
	footer := "Drop and rename\n\nSCHEMA-CHANGE: draft_tool: never released\nBREAKING CHANGE: create_event: calendar is cal"
	if err := checkAck(c, []string{footer}, "base"); err != nil {
		t.Errorf("checkAck = %v, want both acknowledged", err)
	}
}

// A break of the release is matched by its item, not its text. Here the
// release takes a string, the merge-base widened that to an integer as
// well, and the head takes only an integer: the two breaks read
// differently, and both break the same tool.
func TestABreakOfTheReleaseIsMatchedByItsItem(t *testing.T) {
	typed := func(typ string) surface {
		return readTest(t, strings.Replace(plain, `"properties":{"calendar":{"type":"string"}}`,
			`"properties":{"calendar":{"type":`+typ+`}}`, 1))
	}
	release, base, head := typed(`"string"`), typed(`["integer","string"]`), typed(`"integer"`)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if len(c.breaking) != 1 || c.breaking[0].item != "create_event" {
		t.Errorf("breaking = %v, want create_event's narrowed input kept as a break of the release", c.breaking)
	}
}

// The release is the baseline the base branch records. Before the first
// release there is none, and nothing breaks one.
func TestReleasedBreaksReadTheBaseBranchBaseline(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	git := gitIn(t, dir)
	git("init", "--quiet", "--initial-branch=main")
	git("commit", "--quiet", "--allow-empty", "-m", "Start")
	head := readTest(t, renamed)

	breaks, err := releasedBreaks("main", head)
	if err != nil || len(breaks) != 0 {
		t.Fatalf("releasedBreaks with no baseline = %v, %v; want none", breaks, err)
	}

	put(t, dir, baselineFile, plain)
	git("add", baselineFile)
	git("commit", "--quiet", "-m", "Release")
	// The working tree's baseline is a branch's, not the release's.
	put(t, dir, baselineFile, `{"tools":[]}`)
	breaks, err = releasedBreaks("main", head)
	if err != nil || !slices.Equal(breaks, renameBreak) {
		t.Errorf("releasedBreaks = %v, %v; want %v", breaks, err, renameBreak)
	}
}
