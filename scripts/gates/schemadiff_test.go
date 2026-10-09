package main

import (
	"slices"
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
