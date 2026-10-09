package main

import (
	"slices"
	"strings"
	"testing"
)

// A tool kept by name breaks a caller when it loses a field it took or
// returned, at any depth, when a field changes type, or when an input is
// newly required; adding fields does not.
func TestBrokenFields(t *testing.T) {
	old, err := fieldsOf([]byte(`{"tools":[
		{"name":"get_event","input_schema":{"type":"object","properties":{"calendar":{"type":"string"},"event_id":{"type":"string"},"time_zone":{"type":"string"}},"required":["event_id"]},
		 "output_schema":{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},"guests":{"type":"integer"},
		   "start":{"type":"object","properties":{"date":{"type":"string"},"at":{"type":"string"}}}}}},
		{"name":"list_events","input_schema":{"type":"object","properties":{"calendar":{"type":"string"}}},
		 "output_schema":{"type":"object","properties":{"events":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"},"start":{"type":"string"}}}},
		   "dropped":{"type":"object","properties":{"a":{"type":"string"},"b":{"type":"string"}}}}}},
		{"name":"gone","input_schema":{"type":"object","properties":{"x":{"type":"string"}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cur, err := fieldsOf([]byte(`{"tools":[
		{"name":"get_event","input_schema":{"type":"object","properties":{"calendar":{"type":"string"},"event_id":{"type":"string"},"etag":{"type":"string"}},"required":["event_id","calendar"]},
		 "output_schema":{"type":"object","properties":{"id":{"type":"string"},"title":{"type":["null","string"]},"etag":{"type":"string"},
		   "start":{"type":"object","properties":{"date":{"type":"string"}}}}}},
		{"name":"list_events","input_schema":{"type":"object","properties":{"calendar":{"type":"string"},
		   "reminders":{"type":"object","properties":{"minutes":{"type":"integer"}},"required":["minutes"]}}},
		 "output_schema":{"type":"object","properties":{"events":{"type":"array","items":{"type":"object","properties":{"id":{"type":"string"}}}}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := brokenFields(old, cur)
	want := []string{
		`get_event: input field time_zone removed`,
		`get_event: output field guests removed`,
		`get_event: output field start.at removed`,
		`get_event: output field title changed type from "string" to ["null","string"]`,
		`get_event: input field calendar newly required`,
		`list_events: output field dropped removed`,
		`list_events: output field events[].start removed`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// The baseline must be the newest release's surface, which is the
// CHANGELOG's first heading after [Unreleased].
func TestBaselineVersion(t *testing.T) {
	for changelog, want := range map[string]string{
		"# Changelog\n\n## [Unreleased]\n\n### Added\n\n- x\n\n## [3.0.1] - 2026-10-01\n\n## [3.0.0] - 2026-09-30\n": "v3.0.1",
		"# Changelog\n\n## [3.1.0] - 2026-10-10\n\n- x\n\n## [3.0.1] - 2026-10-01\n":                                 "v3.1.0",
		"# Changelog\n\n## [Unreleased]\n\n- first\n":                                                                "",
	} {
		if got := baselineVersion(changelog); got != want {
			t.Errorf("baselineVersion(%q) = %q, want %q", changelog, got, want)
		}
	}
}

// A break is recorded only as a new major version.
func TestNewMajor(t *testing.T) {
	for _, c := range []struct {
		from, to string
		want     bool
	}{
		{"v3.0.1", "v4.0.0", true},
		{"v3.0.1", "v3.1.0", false},
		{"v3.0.1", "v3.0.1", false},
		{"", "v4.0.0", false},
		{"dev", "v4.0.0", false},
	} {
		if got := newMajor(c.from, c.to); got != c.want {
			t.Errorf("newMajor(%q, %q) = %t, want %t", c.from, c.to, got, c.want)
		}
	}
}

// Two dumps of one surface are the same whatever their stamps and key
// order; a changed description is not.
func TestSameSurface(t *testing.T) {
	a := []byte(`{"version":"v3.0.1","sdk_version":"v1.8.0","tools":[{"name":"t","description":"d","input_schema":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
	b := []byte(`{"tools":[{"input_schema":{"properties":{"a":{"type":"string"}},"type":"object"},"description":"d","name":"t"}],"version":"dev","sdk_version":"v1.9.0"}`)
	c := []byte(`{"version":"v3.0.1","tools":[{"name":"t","description":"changed","input_schema":{"type":"object","properties":{"a":{"type":"string"}}}}]}`)
	if !sameSurface(a, b) {
		t.Error("the same surface under other stamps and key order differs")
	}
	if sameSurface(a, c) {
		t.Error("a changed description is the same surface")
	}
}

// dumpOf is a dump of one tool, get_event, with the input and output
// schemas given.
func dumpOf(t *testing.T, input, output string) map[string]toolFields {
	t.Helper()
	f, err := fieldsOf([]byte(`{"tools":[{"name":"get_event","input_schema":` + input + `,"output_schema":` + output + `}]}`))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A type change breaks a caller in one direction only: an input that
// takes fewer types, or an output that may return more. An input that
// takes more, or an output that returns fewer, breaks nobody.
func TestATypeChangeBreaksOneWay(t *testing.T) {
	const empty = `{"type":"object"}`
	field := func(typ string) string {
		if typ == "" {
			return `{"type":"object","properties":{"x":{}}}`
		}
		return `{"type":"object","properties":{"x":{"type":` + typ + `}}}`
	}
	for _, c := range []struct {
		side, was, now, want string
	}{
		{"input", `"boolean"`, `["null","boolean"]`, ""},
		{"input", `"integer"`, `"number"`, ""},
		{"input", `"string"`, ``, ""},
		{"input", `"number"`, `"integer"`, `get_event: input field x changed type from "number" to "integer"`},
		{"input", `["null","string"]`, `"string"`, `get_event: input field x changed type from ["null","string"] to "string"`},
		{"input", ``, `"string"`, `get_event: input field x changed type from any to "string"`},
		{"output", `["null","string"]`, `"string"`, ""},
		{"output", `"number"`, `"integer"`, ""},
		{"output", `"string"`, `["null","string"]`, `get_event: output field x changed type from "string" to ["null","string"]`},
		{"output", `"integer"`, `"number"`, `get_event: output field x changed type from "integer" to "number"`},
		{"output", `"string"`, ``, `get_event: output field x changed type from "string" to any`},
	} {
		was, now := dumpOf(t, field(c.was), empty), dumpOf(t, field(c.now), empty)
		if c.side == "output" {
			was, now = dumpOf(t, empty, field(c.was)), dumpOf(t, empty, field(c.now))
		}
		got := strings.Join(brokenFields(was, now), "\n")
		if got != c.want {
			t.Errorf("%s %s to %s: got %q, want %q", c.side, c.was, c.now, got, c.want)
		}
	}
}

// An output field a caller read as always there breaks it when it may
// now be missing, at any depth. One removed is reported once, as removed.
// An input no longer required breaks nobody.
func TestAnOutputNoLongerRequiredBreaks(t *testing.T) {
	was := dumpOf(t, `{"type":"object","properties":{"calendar":{"type":"string"}},"required":["calendar"]}`,
		`{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},
		"etag":{"type":"string"},"link":{"type":"string"},
		"start":{"type":"object","properties":{"at":{"type":"string"}},"required":["at"]}},
		"required":["id","title","etag","start"]}`)
	now := dumpOf(t, `{"type":"object","properties":{"calendar":{"type":"string"}}}`,
		`{"type":"object","properties":{"id":{"type":"string"},"title":{"type":"string"},
		"link":{"type":"string"},
		"start":{"type":"object","properties":{"at":{"type":"string"}}}},
		"required":["id","start","link"]}`)
	got := brokenFields(was, now)
	want := []string{
		`get_event: output field etag removed`,
		`get_event: output field start.at no longer required`,
		`get_event: output field title no longer required`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// An input value a caller sent breaks it when the field no longer takes
// it, in a list's elements too. A value added, or the list dropped so
// any value goes, breaks nobody.
func TestAnInputThatLosesAValueBreaks(t *testing.T) {
	const output = `{"type":"object"}`
	was := dumpOf(t, `{"type":"object","properties":{
		"scope":{"type":"string","enum":["instance","series","this_and_following"]},
		"notify":{"type":"string","enum":["none","all"]},
		"order":{"type":"string","enum":["start","updated"]},
		"event_types":{"type":"array","items":{"type":"string","enum":["default","focusTime"]}}}}`, output)
	now := dumpOf(t, `{"type":"object","properties":{
		"scope":{"type":"string","enum":["instance","series"]},
		"notify":{"type":"string","enum":["none","external_only","all"]},
		"order":{"type":"string"},
		"event_types":{"type":"array","items":{"type":"string","enum":["focusTime"]}}}}`, output)
	got := brokenFields(was, now)
	want := []string{
		`get_event: input field event_types[] no longer takes "default"`,
		`get_event: input field scope no longer takes "this_and_following"`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// Listed values that may or may not break a caller are reported, not
// failed: an output that may carry a value it did not, and an input
// newly limited to a list. An output that lists fewer, or newly lists
// its values, is neither, and a field removed is a break, said once.
func TestValuesThatMayBreakAreReported(t *testing.T) {
	was := dumpOf(t, `{"type":"object","properties":{"scope":{"type":"string"},
		"notify":{"type":"string","enum":["none","all"]}}}`,
		`{"type":"object","properties":{"id":{"type":"string"},"kind_code":{"type":"string"},
		"visibility":{"type":"string","enum":["public","private"]},
		"status":{"type":"string","enum":["confirmed","tentative"]},
		"kind":{"type":"string","enum":["default","focusTime"]},
		"response":{"type":"string","enum":["accepted","declined"]}}}`)
	now := dumpOf(t, `{"type":"object","properties":{"scope":{"type":"string","enum":["instance","series"]},
		"notify":{"type":"string","enum":["none","all"]}}}`,
		`{"type":"object","properties":{"id":{"type":"string"},"kind_code":{"type":"string","enum":["a","b"]},
		"status":{"type":"string","enum":["confirmed","tentative","cancelled"]},
		"kind":{"type":"string"},
		"response":{"type":"string","enum":["accepted"]}}}`)
	if b := brokenFields(was, now); !slices.Equal(b, []string{"get_event: output field visibility removed"}) {
		t.Fatalf("got breaks %q, want only the field removed", b)
	}
	got := valueNotes(was, now)
	want := []string{
		`get_event: output field kind may now be any value`,
		`get_event: output field status may now be "cancelled"`,
		`get_event: input field scope now takes only "instance", "series"`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
