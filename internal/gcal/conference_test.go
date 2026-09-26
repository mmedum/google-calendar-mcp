package gcal_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v2/internal/gcal"
)

// TestConferenceRequestAsksForMeetWithTheEventID: the request id is the
// event id, so a retry of an insert whose answer was never seen cannot
// produce a second conference — Google ignores a repeated request id.
func TestConferenceRequestAsksForMeetWithTheEventID(t *testing.T) {
	raw := gcal.NewConferenceRequest("abcdef0123456789")
	var body struct {
		CreateRequest struct {
			RequestID             string `json:"requestId"`
			ConferenceSolutionKey struct {
				Type string `json:"type"`
			} `json:"conferenceSolutionKey"`
		} `json:"createRequest"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("the request is not JSON: %v", err)
	}
	if body.CreateRequest.RequestID != "abcdef0123456789" {
		t.Fatalf("request id is %q, want the event id", body.CreateRequest.RequestID)
	}
	if got := body.CreateRequest.ConferenceSolutionKey.Type; got != gcal.ConferenceSolutionMeet {
		t.Fatalf("solution type is %q, want %q", got, gcal.ConferenceSolutionMeet)
	}
}

func TestReadConference(t *testing.T) {
	cases := []struct {
		name                            string
		raw                             string
		present, pending, failed, ready bool
		uri                             string
	}{
		{name: "no conference at all"},
		{
			name:    "the answer an insert usually carries",
			raw:     `{"createRequest":{"requestId":"a1","status":{"statusCode":"pending"}}}`,
			present: true, pending: true,
		},
		{
			name: "made, with a link to join",
			raw: `{"createRequest":{"requestId":"a1","status":{"statusCode":"success"}},` +
				`"conferenceSolution":{"key":{"type":"hangoutsMeet"},"name":"Google Meet"},` +
				`"entryPoints":[{"entryPointType":"phone","uri":"tel:+1-000-000"},` +
				`{"entryPointType":"video","uri":"https://meet.google.com/aaa-bbbb-ccc"}]}`,
			present: true, ready: true, uri: "https://meet.google.com/aaa-bbbb-ccc",
		},
		{
			name:    "asked for and refused",
			raw:     `{"createRequest":{"requestId":"a1","status":{"statusCode":"failure"}}}`,
			present: true, failed: true,
		},
		{
			// A union this package does not model must not lose the
			// event it is attached to.
			name:    "a shape from somewhere else",
			raw:     `{"somethingNobodyModeled":{"nested":[1,2,3]}}`,
			present: true,
		},
		{
			name:    "not even JSON",
			raw:     `{{{`,
			present: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gcal.ReadConference(json.RawMessage(c.raw))
			if got.Present != c.present {
				t.Fatalf("Present = %v, want %v", got.Present, c.present)
			}
			if got.Pending() != c.pending || got.Failed() != c.failed || got.Ready() != c.ready {
				t.Fatalf("pending/failed/ready = %v/%v/%v, want %v/%v/%v",
					got.Pending(), got.Failed(), got.Ready(), c.pending, c.failed, c.ready)
			}
			if got.URI != c.uri {
				t.Fatalf("URI = %q, want %q", got.URI, c.uri)
			}
		})
	}
}

// TestAllowsMeetFailsOpenOnSilence: Google documents the list as
// optional and leaves it off calendars that do create conferences, so
// absence must not be read as a refusal.
func TestAllowsMeetFailsOpenOnSilence(t *testing.T) {
	var absent *gcal.ConferenceProperties
	if !absent.AllowsMeet() {
		t.Fatal("a calendar that published no conference properties was treated as forbidding Meet")
	}
	if !(&gcal.ConferenceProperties{}).AllowsMeet() {
		t.Fatal("an empty list was treated as a refusal")
	}
	if !(&gcal.ConferenceProperties{AllowedConferenceSolutionTypes: []string{"eventHangout", "hangoutsMeet"}}).AllowsMeet() {
		t.Fatal("a list naming hangoutsMeet was read as forbidding it")
	}
	if (&gcal.ConferenceProperties{AllowedConferenceSolutionTypes: []string{"eventHangout"}}).AllowsMeet() {
		t.Fatal("a list that does not name hangoutsMeet was read as allowing it")
	}
}

// TestConferenceRoundTripKeepsWhatItDoesNotModel is why the field is
// held as raw JSON: an event read and written back must not lose a
// provider's own fields.
func TestConferenceRoundTripKeepsWhatItDoesNotModel(t *testing.T) {
	raw := `{"conferenceId":"x","signature":"s","parameters":{"addOnParameters":{"k":"v"}}}`
	e := gcal.Event{ConferenceData: json.RawMessage(raw)}
	out, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"addOnParameters"`) {
		t.Fatalf("a round trip dropped a field this package does not model: %s", out)
	}
}
