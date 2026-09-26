package caltest_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v2/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v2/internal/gcal"
)

// TestSeedIsSynthetic is §9.1 held as a test rather than as a promise.
//
// Everything this fake returns must be invented. The check is on the
// DOMAIN, because that is the one shape a recorded fixture cannot fake:
// RFC 2606 reserves .test, .example and .invalid, and they can never
// resolve. A real address copied from a live response fails here.
func TestSeedIsSynthetic(t *testing.T) {
	s := caltest.Seed()

	addresses := 0
	check := func(where, value string) {
		t.Helper()
		if !strings.Contains(value, "@") {
			return
		}
		addresses++
		at := strings.LastIndex(value, "@")
		domain := value[at+1:]
		ok := strings.HasSuffix(domain, ".test") ||
			strings.HasSuffix(domain, ".example") ||
			strings.HasSuffix(domain, ".invalid") ||
			domain == "primary"
		if !ok {
			t.Fatalf("%s carries %q, whose domain is not reserved by RFC 2606; "+
				"a fixture copied from a live response is itself the leak", where, value)
		}
	}

	for id := range s.Calendars {
		check("a calendar id", id)
	}
	for cal, rules := range s.ACL {
		for _, r := range rules {
			check("an ACL rule on "+cal, r.Scope.Value)
		}
	}
	for cal, events := range s.Events {
		for _, e := range events {
			for _, a := range e.Attendees {
				check("an attendee on "+cal, a.Email)
			}
			if e.Organizer != nil {
				check("an organizer on "+cal, e.Organizer.Email)
			}
		}
	}
	if addresses < 2 {
		t.Fatalf("checked only %d addresses; the seed is not exercising this rule", addresses)
	}
}

// TestSeedCoversTheShapesThatGoWrong. A fake that only holds ordinary
// meetings lets every interesting bug through.
func TestSeedCoversTheShapesThatGoWrong(t *testing.T) {
	s := caltest.Seed()
	primary := s.Events["primary"]

	var allDay, series, instance, canceled, transparent bool
	for _, e := range primary {
		switch {
		case e.Start != nil && e.Start.IsAllDay():
			allDay = true
		}
		if len(e.Recurrence) > 0 {
			series = true
		}
		if e.RecurringEventID != "" {
			instance = true
		}
		if e.Status == gcal.StatusCanceled {
			canceled = true
		}
		if e.Transparency == gcal.TransparencyTransparent {
			transparent = true
		}
	}
	for name, got := range map[string]bool{
		"an all-day event": allDay, "a recurring series": series,
		"an expanded instance": instance, "a canceled event": canceled,
		"a transparent event": transparent,
	} {
		if !got {
			t.Fatalf("the seed has no %s, so nothing tests it", name)
		}
	}

	if len(s.Calendars) < 2 {
		t.Fatal("the seed has one calendar, so nothing tests a fan-out")
	}
}

// TestAllDayHelperTakesNoZone: an all-day event has no instant, so
// there is nothing for a zone to do. A fake that accepted one would let
// a test pass that the real type system forbids.
func TestAllDayHelperTakesNoZone(t *testing.T) {
	e := caltest.AllDay("id", "Title", "2026-03-20", "2026-03-21")
	if e.Start.DateTime != "" || e.Start.TimeZone != "" {
		t.Fatalf("AllDay produced a timed start: %+v", e.Start)
	}
	if e.Start.Date != "2026-03-20" {
		t.Fatalf("start = %q", e.Start.Date)
	}
}

// TestTimedHelperAlwaysCarriesAZone is §4.1: a timed event on the wire
// carries its IANA zone alongside the offset.
func TestTimedHelperAlwaysCarriesAZone(t *testing.T) {
	e := caltest.Timed("id", "Title", "2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", "Europe/Copenhagen")
	if e.Start.TimeZone != "Europe/Copenhagen" || e.End.TimeZone != "Europe/Copenhagen" {
		t.Fatalf("the zone was not carried: %+v / %+v", e.Start, e.End)
	}
}

func TestAddCalendarAndEvent(t *testing.T) {
	s := caltest.New()
	s.AddCalendar("x@example.test", "X", "UTC", gcal.RoleOwner, false)
	s.AddEvent("x@example.test", caltest.Timed("e", "E", "2026-03-16T09:00:00Z", "2026-03-16T10:00:00Z", "UTC"))

	if s.Calendars["x@example.test"] == nil {
		t.Fatal("AddCalendar did not register the calendar")
	}
	if s.Entries["x@example.test"] == nil {
		t.Fatal("AddCalendar did not register the subscription")
	}
	if s.Events["x@example.test"]["e"] == nil {
		t.Fatal("AddEvent did not store the event")
	}
	if s.Events["x@example.test"]["e"].ETag == "" {
		t.Fatal("AddEvent stored an event with no etag; §4.4 writes need one")
	}
}

func TestRecurringAndInstanceHelpers(t *testing.T) {
	parent := caltest.Recurring("p", "P", "2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00",
		"Europe/Copenhagen", "RRULE:FREQ=WEEKLY")
	if len(parent.Recurrence) != 1 {
		t.Fatalf("Recurring did not set the rule: %+v", parent)
	}
	inst := caltest.Instance("p_1", "p", "P", "2026-03-24T14:00:00+01:00", "2026-03-24T15:00:00+01:00",
		"Europe/Copenhagen", "2026-03-24T14:00:00+01:00")
	if inst.RecurringEventID != "p" {
		t.Fatalf("Instance did not point at its series: %+v", inst)
	}
	if inst.OriginalStartTime == nil {
		t.Fatal("Instance has no originalStartTime; §6.2 addresses instances by it")
	}
}

// TestConferenceDataIsIgnoredWithoutTheVersion holds the trap the fake
// exists to reproduce: Google's conferenceDataVersion defaults to 0,
// which "ignores conference data in the event's body". A server that
// forgets the parameter gets a 200, an event, and no meeting link — and
// a fake that accepted it would let that ship.
func TestConferenceDataIsIgnoredWithoutTheVersion(t *testing.T) {
	s := caltest.Seed()
	base := s.Start()
	defer s.Close()

	body := `{"id":"abcdef0123456789","summary":"probe",` +
		`"start":{"dateTime":"2026-03-16T09:00:00+01:00","timeZone":"Europe/Copenhagen"},` +
		`"end":{"dateTime":"2026-03-16T10:00:00+01:00","timeZone":"Europe/Copenhagen"},` +
		`"conferenceData":{"createRequest":{"requestId":"abcdef0123456789",` +
		`"conferenceSolutionKey":{"type":"hangoutsMeet"}}}}`

	post := func(query string) gcal.Event {
		t.Helper()
		resp, err := http.Post(base+"/calendars/primary/events"+query, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var out gcal.Event
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	if got := post(""); len(got.ConferenceData) != 0 {
		t.Fatalf("an insert with no conferenceDataVersion kept the conference: %s", got.ConferenceData)
	}
	s.Events["primary"] = nil
	got := post("?conferenceDataVersion=1")
	if c := gcal.ReadConference(got.ConferenceData); !c.Pending() {
		t.Fatalf("an insert with the version did not come back pending: %+v", c)
	}
}
