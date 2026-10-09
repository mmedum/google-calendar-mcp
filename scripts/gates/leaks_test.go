package main

import (
	"fmt"
	"os"
	"path/filepath"
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
