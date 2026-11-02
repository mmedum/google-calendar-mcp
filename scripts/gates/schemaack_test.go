package main

import (
	"fmt"
	"io"
	"os"
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

// relabeled is plain with the resource template's name changed, and
// withDraft is plain with a tool added since the release.
var (
	relabeled = strings.Replace(plain, `"name":"calendar"`, `"name":"cal"`, 1)
	withDraft = strings.Replace(plain, `"tools":[`, `"tools":[{"name":"draft_tool"},`, 1)
)

// refusalFix is the footer a refusal ends with, after its blank line.
func refusalFix(err error) string {
	_, fix, _ := strings.Cut(fmt.Sprint(err), "\n\n")
	return fix
}

// renameBreak is the break renamed makes of plain.
var renameBreak = []change{{"create_event", "create_event: input field calendar removed", "input calendar"}}

// schema-ack fails a pull request unless its commits name every changed
// item in a footer, and every break of a release in a BREAKING CHANGE:
// footer.
func TestSchemaAckNeedsEveryItemNamed(t *testing.T) {
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
		{"the instructions unnamed", reworded, "Reword them", "no footer names instructions"},
		{"a description word names nothing", flipped, "Mark it\n\nSCHEMA-CHANGE: get_event: create_event is destructive", wantUnnamed},
		{"a tab after the colon starts the description too", flipped, "Mark it\n\nSCHEMA-CHANGE: get_event:\tdestructive, create_event", wantUnnamed},
		{"a value with no description names its whole list", flipped, "Mark them\n\nSCHEMA-CHANGE: get_event, create_event", ""},
		{"a value wrapped at the margin continues it", flipped, "Mark them\n\nSCHEMA-CHANGE: get_event,\ncreate_event: destructiveHint true", ""},
		{"prose after a bare list names nothing", flipped, "Mark it\n\nSCHEMA-CHANGE: get_event\ncreate_event is destructive now", wantUnnamed},
		{"a bare list ends at its line, and its name counts", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event\nhints flipped", ""},
		{"prose after a trailing comma names nothing", flipped, "Mark them\n\nSCHEMA-CHANGE: get_event,\nwhich create_event also reads", wantUnnamed},
		{"a list is split on commas, not spaces", flipped, "Mark them\n\nSCHEMA-CHANGE: get_event create_event: hints", wantUnnamed},
		{"a Token #value footer is a footer", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event: hints\n\nCloses #12", ""},
		{"a Token #value footer acknowledges nothing", flipped, "Mark it\n\nSCHEMA-CHANGE #create_event", wantUnnamed},
		{"nor does a BREAKING CHANGE #value one", renamed, "Rename it\n\nBREAKING CHANGE #create_event", wantUnnamedBreak},
		{"a footer ends the value before it", flipped, "Mark it\n\nSCHEMA-CHANGE: get_event,\nRefs #12, create_event", wantUnnamed},
		{"three blank lines end one paragraph", flipped, "Mark it\n\nSCHEMA-CHANGE: create_event: hints\n\n\n\nCo-Authored-By: someone <x@example.com>", ""},
		{"a break named only as a change", renamed, "Rename the field\n\nSCHEMA-CHANGE: create_event: calendar is cal", wantUnnamedBreak},
		{"BREAKING CHANGE: acknowledges a break", renamed, "Rename the field\n\nBREAKING CHANGE: create_event: calendar is cal", ""},
		{"BREAKING-CHANGE: is its synonym", renamed, "Rename the field\n\nBREAKING-CHANGE: create_event: calendar is cal", ""},
		{"SCHEMA-CHANGE: in any case", flipped, "Mark it\n\nSchema-Change: create_event: destructiveHint true", ""},
		{"BREAKING CHANGE: only in uppercase", renamed, "Rename the field\n\nBreaking-Change: create_event: calendar is cal", wantUnnamedBreak},
		{"a removed template needs BREAKING CHANGE:", withoutTemplate, "Drop it\n\nSCHEMA-CHANGE: gcal://calendars/{+calendar_id}: gone",
			"no BREAKING CHANGE: footer names gcal://calendars/{+calendar_id}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := compareSurfaces(readTest(t, plain), readTest(t, tc.built))
			err := checkAck(io.Discard, c, []string{"Tidy the docs", tc.message}, "base")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("checkAck = %v, want it to pass", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("checkAck = %v, want an error containing %q", err, tc.wantErr)
			}
		})
	}
}

// A refusal ends with the footer that fixes it, bare so it can be copied:
// BREAKING CHANGE: for the breaks, SCHEMA-CHANGE: for the rest, an item
// the first names left out of the second. The footer it prints passes.
func TestARefusalNamesTheFooterThatFixesIt(t *testing.T) {
	built := strings.Replace(relabeled, `"properties":{"calendar"`, `"properties":{"cal"`, 1)
	built = strings.Replace(built, `"destructiveHint":false`, `"destructiveHint":true`, 1)
	c := compareSurfaces(readTest(t, plain), readTest(t, built))
	fix := refusalFix(checkAck(io.Discard, c, []string{"Change them"}, "base"))
	if want := "BREAKING CHANGE: create_event: what broke\nSCHEMA-CHANGE: gcal://calendars/{+calendar_id}: what changed"; fix != want {
		t.Fatalf("the refusal's fix is %q, want %q", fix, want)
	}
	if err := checkAck(io.Discard, c, []string{"Change them", "Acknowledge them\n\n" + fix}, "base"); err != nil {
		t.Errorf("the printed fix does not pass: %v", err)
	}
}

// A name the footer would split, or trim, is printed in backticks, and
// the footer printed passes: a URI with a comma, and one ending in a full
// stop, which a bare name would lose. Bare, the URI with a comma names
// nothing.
func TestARefusalQuotesANameTheFooterWouldSplit(t *testing.T) {
	for _, tmpl := range []string{"gcal://calendars/{+calendar_id}{?fields,view}", "gcal://calendars/{+calendar_id}."} {
		was := strings.Replace(plain, `"gcal://calendars/{+calendar_id}"`, `"`+tmpl+`"`, 1)
		c := compareSurfaces(readTest(t, was), readTest(t, strings.Replace(was, `"name":"calendar"`, `"name":"cal"`, 1)))
		fix := refusalFix(checkAck(io.Discard, c, nil, "base"))
		if want := "SCHEMA-CHANGE: `" + tmpl + "`: what changed"; fix != want {
			t.Errorf("the refusal's fix is %q, want %q", fix, want)
			continue
		}
		if err := checkAck(io.Discard, c, []string{"Rename it\n\n" + fix}, "base"); err != nil {
			t.Errorf("the printed fix for %s does not pass: %v", tmpl, err)
		}
		if bare := strings.ReplaceAll(fix, "`", ""); strings.Contains(tmpl, ",") && checkAck(io.Discard, c, []string{"Rename it\n\n" + bare}, "base") == nil {
			t.Errorf("%q passed, though split on its comma it names nothing", bare)
		}
	}
}

// A gate that could not run says so and exits 2; a refusal exits 1.
// Under `go run`, which exits 1 for both, the message tells them apart.
func TestSchemaAckTellsARefusalFromAFailure(t *testing.T) {
	refused := checkAck(io.Discard, compareSurfaces(readTest(t, plain), readTest(t, flipped)), nil, "base")
	if refused == nil || exitCode(refused) != 1 || strings.HasPrefix(refused.Error(), "could not run") {
		t.Errorf("a refusal (%v) exits %d, want 1", refused, exitCode(refused))
	}
	failed := schemaAck(io.Discard, "refs/heads/no-such-branch-for-a-test", "HEAD")
	if failed == nil || !strings.HasPrefix(failed.Error(), "could not run: ") || exitCode(failed) != 2 {
		t.Errorf("a gate that could not run (%v) exits %d, want 2 and a message saying so", failed, exitCode(failed))
	}
}

// A break no footer names is asked for once, as a break: the BREAKING
// CHANGE: footer that fixes it names it for both checks.
func TestARefusalAsksForAnUnnamedBreakOnce(t *testing.T) {
	err := checkAck(io.Discard, compareSurfaces(readTest(t, plain), readTest(t, renamed)), nil, "base")
	if want := "the tool surface changed since base, and no BREAKING CHANGE: footer names create_event. "; err == nil || !strings.HasPrefix(err.Error(), want) {
		t.Errorf("checkAck = %v, want it to start %q", err, want)
	}
}

// A break of the released surface needs BREAKING CHANGE:; removing a tool
// added since the release breaks no caller, so it is a plain change, and
// the report lists it as one.
func TestABreakIsABreakOfARelease(t *testing.T) {
	release := readTest(t, plain)
	base := readTest(t, withDraft)
	head := readTest(t, renamed)
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if !slices.Equal(c.breaking, renameBreak) {
		t.Errorf("breaking = %v, want %v", c.breaking, renameBreak)
	}
	if want := []change{{item: "create_event", text: "create_event (input_schema.properties)"},
		{item: "draft_tool", text: "draft_tool removed", field: "draft_tool"}}; !slices.Equal(c.changed, want) {
		t.Errorf("changed = %v, want %v", c.changed, want)
	}
	var report strings.Builder
	footer := "Drop and rename\n\nSCHEMA-CHANGE: draft_tool: never released\nBREAKING CHANGE: create_event: calendar is cal"
	if err := checkAck(&report, c, []string{footer}, "base"); err != nil {
		t.Errorf("checkAck = %v, want both acknowledged", err)
	}
	want := "  against the merge-base base: 0 added, 2 changed, 1 breaking\n" +
		"    ~ create_event (input_schema.properties)\n" +
		"    ~ draft_tool removed\n" +
		"    ! create_event: input field calendar removed  BREAKING\n" +
		"  the change since base is acknowledged\n"
	if report.String() != want {
		t.Errorf("the report is\n%s\nwant\n%s", report.String(), want)
	}
}

// A tool the base branch already broke since the release, in one field,
// does not make a pull request's break of another field, one no release
// shipped, a break of the release.
func TestABreakOfTheReleaseIsMatchedByItsField(t *testing.T) {
	withInputs := func(props string) string {
		return strings.Replace(plain, `"properties":{"calendar":{"type":"string"}}`, `"properties":{`+props+`}`, 1)
	}
	release := readTest(t, withInputs(`"calendar":{"type":"string"},"note":{"type":"string"}`))
	base := readTest(t, withInputs(`"calendar":{"type":"string"},"draft":{"type":"string"}`))
	head := readTest(t, withInputs(`"calendar":{"type":"string"}`))
	c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking)
	if len(c.breaking) != 0 {
		t.Errorf("breaking = %v, want draft, which no release shipped, as a change", c.breaking)
	}
	// Nor does a released tool of the name of a prompt the pull request
	// removes: they are two items, though a footer names both alike.
	release = readTest(t, `{"tools":[{"name":"get_event"},{"name":"summarize"}]}`)
	base = readTest(t, `{"tools":[{"name":"get_event"}],"prompts":[{"name":"summarize"}]}`)
	head = readTest(t, `{"tools":[{"name":"get_event"}],"prompts":[]}`)
	if c := releaseBreaksOnly(compareSurfaces(base, head), compareSurfaces(release, head).breaking); len(c.breaking) != 0 {
		t.Errorf("breaking = %v, want the unreleased prompt's removal as a change", c.breaking)
	}
}

// A break of the release is matched by its field, not its text. Here the
// release takes a string, the merge-base widened that to an integer as
// well, and the head takes only an integer: the two breaks read
// differently, and both break the same tool.
func TestABreakOfTheReleaseIsNotMatchedByItsText(t *testing.T) {
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
	r := newDumpRepo(t)
	r.git("commit", "--quiet", "--allow-empty", "-m", "Start")
	head := readTest(t, renamed)

	breaks, err := releasedBreaks("main", head)
	if err != nil || len(breaks) != 0 {
		t.Fatalf("releasedBreaks with no baseline = %v, %v; want none", breaks, err)
	}

	put(t, r.root, baselineFile, plain)
	r.git("add", baselineFile)
	r.git("commit", "--quiet", "-m", "Release")
	// The working tree's baseline is a branch's, not the release's.
	put(t, r.root, baselineFile, `{"tools":[]}`)
	breaks, err = releasedBreaks("main", head)
	if err != nil || !slices.Equal(breaks, renameBreak) {
		t.Errorf("releasedBreaks = %v, %v; want %v", breaks, err, renameBreak)
	}
}

// Both sides are built from commits: the merge-base and the head. The
// checkout is neither, in CI the merge commit and here main with a change
// nobody committed, so what it would serve is not what is judged.
func TestSchemaAckBuildsBothSidesFromCommits(t *testing.T) {
	r := newDumpRepo(t)
	r.commit(plain, "Base")
	r.git("switch", "--quiet", "-c", "feature")
	r.commit(flipped, "Flip create_event")
	r.git("switch", "--quiet", "main")
	r.write(renamed)

	var out strings.Builder
	err := schemaAck(&out, "main", "feature")
	if err == nil || !strings.HasSuffix(err.Error(), "\n\nSCHEMA-CHANGE: create_event: what changed") {
		t.Fatalf("schemaAck = %v, want a refusal naming the flip alone", err)
	}
	if !strings.Contains(out.String(), "~ create_event (annotations.destructiveHint)\n") || strings.Contains(out.String(), "BREAKING") {
		t.Errorf("the report is\n%s\nwant the flip and no break", out.String())
	}
	r.git("checkout", "--", ".")
	r.git("switch", "--quiet", "feature")
	r.git("commit", "--quiet", "--allow-empty", "-m", "Acknowledge it\n\nSCHEMA-CHANGE: create_event: destructive")
	if err := schemaAck(io.Discard, "main", "HEAD"); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
	if n := r.worktrees(); n != 1 {
		t.Errorf("%d worktrees left, want the checkout alone", n)
	}
}

// Through the whole gate: a tool no release shipped, removed, is a plain
// change, and an input the released baseline lists, renamed, is a break.
func TestSchemaAckAsksForBreakingChangeOnlyForAReleasedBreak(t *testing.T) {
	r := newDumpRepo(t)
	r.release(plain, "Release")
	r.commit(withDraft, "Add draft_tool")
	r.git("switch", "--quiet", "-c", "feature")
	r.commit(renamed, "Drop draft_tool and rename calendar")
	c, messages, base, err := ackInputs("main", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	fix := refusalFix(checkAck(io.Discard, c, messages, base))
	if want := "BREAKING CHANGE: create_event: what broke\nSCHEMA-CHANGE: draft_tool: what changed"; fix != want {
		t.Fatalf("the fix is %q, want %q", fix, want)
	}
	if err := checkAck(io.Discard, c, append(messages, "Acknowledge them\n\n"+fix), base); err != nil {
		t.Fatalf("acknowledged: %v", err)
	}
}

// The release is the baseline the base branch records, not the head's:
// a branch cut before the last release, renaming what that release
// already renamed, breaks no caller of it.
func TestTheReleaseIsTheBaseBranchsBaseline(t *testing.T) {
	r := newDumpRepo(t)
	r.release(plain, "Release 1")
	r.git("branch", "feature")
	r.release(renamed, "Release 2")
	r.git("switch", "--quiet", "feature")
	r.commit(renamed, "Rename calendar")
	fix := refusalFix(schemaAck(io.Discard, "main", "HEAD"))
	if want := "SCHEMA-CHANGE: create_event: what changed"; fix != want {
		t.Errorf("the fix is %q, want %q, since release 2 shipped the rename", fix, want)
	}
}

// dumpRepo is a git repository, the working directory for the test,
// whose cmd/google-calendar-mcp prints a fixed dump, so the gate's
// worktree builds are exercised without building the real server. The
// gate's own git reads none of the machine's configuration either.
type dumpRepo struct {
	t    *testing.T
	root string
	git  func(args ...string)
}

func newDumpRepo(t *testing.T) *dumpRepo {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	t.Chdir(dir)
	r := &dumpRepo{t: t, root: dir, git: gitIn(t, dir)}
	r.git("init", "--quiet", "--initial-branch=main")
	put(t, dir, "go.mod", "module example.invalid/fake\n\ngo 1.21\n")
	r.git("add", "go.mod")
	return r
}

// commit commits a main that prints dump.
func (r *dumpRepo) commit(dump, message string) {
	r.t.Helper()
	r.write(dump)
	r.git("add", "-A")
	r.git("commit", "--quiet", "-m", message)
}

// release commits a main that prints dump with dump as the baseline, as
// a release commit records it.
func (r *dumpRepo) release(dump, message string) {
	r.t.Helper()
	put(r.t, r.root, baselineFile, dump)
	r.commit(dump, message)
}

// write puts a main that prints dump in the working tree, uncommitted.
func (r *dumpRepo) write(dump string) {
	r.t.Helper()
	put(r.t, r.root, "cmd/google-calendar-mcp/main.go",
		fmt.Sprintf("package main\n\nimport \"os\"\n\nfunc main() { os.Stdout.WriteString(%q) }\n", dump))
}

func (r *dumpRepo) worktrees() int {
	r.t.Helper()
	out, err := gitOutput("worktree", "list", "--porcelain")
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.Count(out, "worktree ")
}
