package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// The values below are assembled from pieces so this file does not trip
// the gate it tests, nor gitleaks.

// Every rule catches one value of its shape and lets one innocent
// value of nearly the same shape through.
func TestLeakRulesCatchTheirShapeAndAllowTheReserved(t *testing.T) {
	at := "@"
	cases := []struct {
		rule   string
		caught string
		passed string
	}{
		{"email address", "pat" + at + "acme-sample.com", "pat" + at + "example.test"},
		{"email address", "pat" + at + "corp.test.com", "pat" + at + "example.com"},
		{"email address", "pat" + at + "example.dk", "team" + at + "group.calendar.example.test"},
		// Dependabot's sign-off is allowed, and no other address there.
		{"email address", "pat" + at + "github.com", "Signed-off-by: dependabot[bot] <support" + at + "github.com>"},
		{"OAuth client id",
			"1234567890-" + strings.Repeat("a1", 16) + ".apps.googleusercontent.com",
			"1234567890-" + strings.Repeat("a1", 9) + ".apps.googleusercontent.com"},
		{"Google API key", "AIza" + strings.Repeat("x", 35), "AIza" + strings.Repeat("x", 34)},
		{"Google group calendar id",
			strings.Repeat("c0", 10) + at + "group.calendar.google.com",
			strings.Repeat("c0", 9) + "c" + at + "group.calendar.google.com"},
		{"Google Calendar event URL",
			"https://www.google.com/calendar/" + "event?eid=" + "QUJD",
			"https://www.google.com/calendar/" + "r"},
		{"OAuth refresh token", "1//" + strings.Repeat("t", 20), "1//" + strings.Repeat("t", 19)},
		{"Google Drive file link",
			"https://drive.google.com/file/d/" + strings.Repeat("f", 20) + "/view",
			"https://drive.google.com/open?id=" + strings.Repeat("f", 19)},
		{"Google Drive file link",
			"https://DOCS.google.com/document/d/" + strings.Repeat("f", 20) + "/edit",
			"https://docs.example.test/document/d/" + strings.Repeat("f", 20) + "/edit"},
	}
	rules := leakRules()
	// A group calendar id is also an address, so findings are counted
	// per rule rather than in total.
	byRule := func(value, rule string) int {
		n := 0
		for _, f := range scanOne("f", "x "+value+" x", rules) {
			if strings.HasPrefix(f, "f: "+rule+" ") {
				n++
			}
		}
		return n
	}
	for _, c := range cases {
		if got := byRule(c.caught, c.rule); got != 1 {
			t.Errorf("%s: %q produced %d findings, want 1", c.rule, redactFinding(c.caught), got)
		}
		if got := byRule(c.passed, c.rule); got != 0 {
			t.Errorf("%s: %q produced %d findings, want 0", c.rule, redactFinding(c.passed), got)
		}
	}
}

// A finding prints a masked value, never the value: CI output is
// published.
func TestLeakFindingsAreMasked(t *testing.T) {
	value := "pat" + "@" + "acme-sample.com"
	got := scanOne("f", value, leakRules())
	if len(got) != 1 {
		t.Fatalf("got %v, want one finding", got)
	}
	if strings.Contains(got[0], value) {
		t.Fatalf("the finding printed the value: %s", got[0])
	}
	if !strings.Contains(got[0], `"pat@***********.com"`) {
		t.Fatalf("got %s, want the first and last four characters kept", got[0])
	}
}

// The floor: a scan that read fewer than 20 files did not read this
// repository, and says so rather than passing.
func TestLeakGateRefusesATreeBelowTheFloor(t *testing.T) {
	for _, tc := range []struct {
		files int
		ok    bool
	}{{19, false}, {20, true}} {
		dir := t.TempDir()
		for i := range tc.files {
			name := filepath.Join(dir, fmt.Sprintf("f%02d.txt", i))
			if err := os.WriteFile(name, []byte("nothing here\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		t.Chdir(dir)
		err := leakGate(false)
		if (err == nil) != tc.ok {
			t.Errorf("%d files: err = %v, want ok=%v", tc.files, err, tc.ok)
		}
	}
}

// A finding anywhere in the tree fails the gate.
func TestLeakGateFailsOnAFinding(t *testing.T) {
	dir := t.TempDir()
	for i := range 20 {
		body := "nothing here\n"
		if i == 7 {
			body = "contact: pat" + "@" + "acme-sample.com\n"
		}
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%02d.txt", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	if err := leakGate(false); err == nil {
		t.Fatal("a tree with a real-looking address passed")
	}
}

// gitIn runs git in dir as Pat, with no global or system configuration,
// so a person's hooks, signing or default branch never reach a test.
func gitIn(t *testing.T, dir string) func(args ...string) {
	t.Helper()
	return func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Pat", "-c", "user.email=pat" + "@" + "acme-sample.com"},
			args...)...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// isolatedRepo makes the working directory a fresh git repository on main
// and returns it with a git runner. The gate's own git calls read none of
// the machine's configuration either.
func isolatedRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	// git reads $XDG_CONFIG_HOME/git/ignore even with the global
	// configuration off. A gate's go command keeps its telemetry there on
	// Linux and can still be writing when the temporary directory is
	// removed; mode "off" stops it (https://go.dev/doc/telemetry#config).
	config := t.TempDir()
	if err := os.MkdirAll(filepath.Join(config, "go", "telemetry"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "go", "telemetry", "mode"), []byte("off"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", config)
	dir := t.TempDir()
	t.Chdir(dir)
	git := gitIn(t, dir)
	git("init", "--quiet", "--initial-branch=main")
	return dir, git
}

// The history scan reads an annotated tag, its message included, less
// the tagger line, which git writes from the tagger's own identity.
func TestTheHistoryScanReadsATagButNotItsTagger(t *testing.T) {
	at := "@"
	dir := t.TempDir()
	git := gitIn(t, dir)
	git("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("nothing here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git("add", "f.txt")
	git("commit", "-q", "-m", "first")
	git("tag", "-a", "v0.1.0", "-m", "a release")
	t.Chdir(dir)
	if got, _, err := scanHistory(leakRules()); err != nil || len(got) != 0 {
		t.Errorf("a tag's tagger line was read: %q, %v", got, err)
	}
	git("tag", "-a", "v0.2.0", "-m", "for carol"+at+"acme-sample.com")
	want := []string{`tag v0.2.0: email address "caro*************.com"`}
	if got, _, err := scanHistory(leakRules()); err != nil || !slices.Equal(got, want) {
		t.Errorf("a tag's message gave %q, %v; want %q", got, err, want)
	}
}

// TestIsolatedRepoTurnsGoTelemetryOff holds that the go command reads the
// mode file where isolatedRepo writes it; a moved file would leave
// telemetry writing into the temporary directory while a test removes it.
func TestIsolatedRepoTurnsGoTelemetryOff(t *testing.T) {
	isolatedRepo(t)
	if dir, err := os.UserConfigDir(); err != nil || dir != os.Getenv("XDG_CONFIG_HOME") {
		t.Skip("the go command does not read its config from XDG_CONFIG_HOME on " + runtime.GOOS)
	}
	out, err := exec.Command("go", "telemetry").CombinedOutput()
	if err != nil {
		t.Fatalf("go telemetry: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != "off" {
		t.Errorf("go telemetry under isolatedRepo = %q, want %q", got, "off")
	}
}

// A Co-authored-by trailer naming an address that authors a commit HEAD
// reaches passes, since git already publishes it in that commit's header.
// Any other address, the same one outside a trailer, and an address only
// an unmerged branch authors are still findings, and an empty address is
// never an author.
func TestTheHistoryScanPassesATrailerNamingAnAuthor(t *testing.T) {
	at := "@"
	ada := "ada" + at + "acme-sample.com"
	bea := "bea" + at + "acme-sample.com"
	setup := func(t *testing.T) func(args ...string) {
		t.Helper()
		_, git := isolatedRepo(t)
		if err := os.WriteFile("f.txt", []byte("nothing here\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		git("add", "f.txt")
		git("commit", "-q", "-m", "first")
		return git
	}

	git := setup(t)
	git("commit", "-q", "--allow-empty", "--author", "Ada <"+ada+">", "-m", "work")
	git("commit", "-q", "--allow-empty", "-m", "Squash\n\nCo-authored-by: Ada < "+strings.ToUpper(ada)+" >\r")
	got, trailers, err := scanHistory(leakRules())
	if err != nil || len(got) != 0 || trailers != 1 {
		t.Fatalf("a trailer naming an author gave %q, %d trailers, %v; want none, 1, nil", got, trailers, err)
	}

	for name, message := range map[string]string{
		"another address":        "Squash\n\nCo-authored-by: Bea <" + bea + ">",
		"the author outside one": "Thanks to " + ada,
		"another in the name":    "Squash\n\nCo-authored-by: " + bea + " <" + ada + ">",
	} {
		t.Run(name, func(t *testing.T) {
			git := setup(t)
			git("commit", "-q", "--allow-empty", "--author", "Ada <"+ada+">", "-m", "work")
			git("commit", "-q", "--allow-empty", "-m", message)
			if got, _, err := scanHistory(leakRules()); err != nil || len(got) == 0 {
				t.Errorf("%q gave %q, %v; want a finding", message, got, err)
			}
		})
	}

	t.Run("an empty address", func(t *testing.T) {
		git := setup(t)
		git("commit", "-q", "--allow-empty", "--author", "Nobody <>", "-m", "work")
		git("commit", "-q", "--allow-empty", "-m", "Squash\n\nCo-authored-by: Nobody < >")
		if _, trailers, err := scanHistory(leakRules()); err != nil || trailers != 0 {
			t.Errorf("an empty trailer address gave %d trailers, %v; want 0, nil", trailers, err)
		}
	})

	t.Run("an author only another branch reaches", func(t *testing.T) {
		git := setup(t)
		git("switch", "-q", "-c", "side")
		git("commit", "-q", "--allow-empty", "--author", "Ada <"+ada+">", "-m", "work")
		git("switch", "-q", "main")
		git("commit", "-q", "--allow-empty", "-m", "Squash\n\nCo-authored-by: Ada <"+ada+">")
		if got, _, err := scanHistory(leakRules()); err != nil || len(got) == 0 {
			t.Errorf("a trailer naming an author only side reaches gave %q, %v; want a finding", got, err)
		}
	})
}
