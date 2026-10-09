package main

import (
	"slices"
	"testing"
)

// A tool kept by name breaks a caller when it loses a field it took or
// returned, or newly requires one; adding fields does not, and neither
// does an old dump that carried no output schema.
func TestBrokenFields(t *testing.T) {
	old, err := fieldsOf([]byte(`{"tools":[
		{"name":"get_event","input_schema":{"properties":{"calendar":{},"event_id":{},"time_zone":{}},"required":["event_id"]},
		 "output_schema":{"properties":{"id":{},"title":{},"guests":{}}}},
		{"name":"gone","input_schema":{"properties":{"x":{}}}},
		{"name":"from_an_old_tag","input_schema":{"properties":{"a":{}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	cur, err := fieldsOf([]byte(`{"tools":[
		{"name":"get_event","input_schema":{"properties":{"calendar":{},"event_id":{},"etag":{}},"required":["event_id","calendar"]},
		 "output_schema":{"properties":{"id":{},"title":{},"etag":{}}}},
		{"name":"from_an_old_tag","input_schema":{"properties":{"a":{}}},"output_schema":{"properties":{"new":{}}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	got := brokenFields(old, cur)
	want := []string{
		"get_event: input field time_zone removed",
		"get_event: input field calendar newly required",
		"get_event: output field guests removed",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
