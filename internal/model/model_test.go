package model_test

import (
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

func zone(t *testing.T, name string) *when.Zone {
	t.Helper()
	z, err := when.Resolve(name, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return &z
}

// TestParseWhenKeepsTheHalvesApart is §4.1 at the conversion boundary:
// a Date never acquires an instant, whatever zone is in play.
func TestParseWhenKeepsTheHalvesApart(t *testing.T) {
	chicago := zone(t, "America/Chicago")

	allDay, err := model.ParseWhen(&gcal.EventDateTime{Date: "2026-03-20"}, chicago)
	if err != nil {
		t.Fatal(err)
	}
	if !allDay.AllDay {
		t.Fatal("a date did not come back as all-day")
	}
	if !allDay.At.IsZero() {
		t.Fatal("a date acquired an instant")
	}
	if allDay.Date.String() != "2026-03-20" {
		t.Fatalf("date = %s", allDay.Date)
	}

	timed, err := model.ParseWhen(&gcal.EventDateTime{
		DateTime: "2026-03-20T09:00:00+01:00", TimeZone: "Europe/Copenhagen",
	}, chicago)
	if err != nil {
		t.Fatal(err)
	}
	if timed.AllDay {
		t.Fatal("a timestamp came back as all-day")
	}
	if !timed.Date.IsZero() {
		t.Fatal("a timestamp populated the date half as well")
	}
	if timed.At.ZoneName() != "America/Chicago" {
		t.Fatalf("the requested rendering zone was ignored: %s", timed.At.ZoneName())
	}
}

func TestParseWhenHandlesNilAndEmpty(t *testing.T) {
	got, err := model.ParseWhen(nil, nil)
	if err != nil || got.AllDay || !got.At.IsZero() {
		t.Fatalf("ParseWhen(nil) = %+v, %v", got, err)
	}
	got, err = model.ParseWhen(&gcal.EventDateTime{}, nil)
	if err != nil || !got.At.IsZero() {
		t.Fatalf("ParseWhen(empty) = %+v, %v", got, err)
	}
}

func TestParseWhenRejectsRubbish(t *testing.T) {
	if _, err := model.ParseWhen(&gcal.EventDateTime{Date: "20-03-2026"}, nil); err == nil {
		t.Fatal("a malformed date was accepted")
	}
	if _, err := model.ParseWhen(&gcal.EventDateTime{DateTime: "not a time"}, nil); err == nil {
		t.Fatal("a malformed timestamp was accepted")
	}
}

func TestFromEvent(t *testing.T) {
	z := zone(t, "Europe/Copenhagen")
	raw := gcal.Event{
		ID: "ev-1", Summary: "Review", Status: gcal.StatusConfirmed,
		EventType: gcal.EventTypeDefault, ETag: `"abc"`,
		Start:     &gcal.EventDateTime{DateTime: "2026-03-16T09:00:00+01:00", TimeZone: "Europe/Copenhagen"},
		End:       &gcal.EventDateTime{DateTime: "2026-03-16T10:00:00+01:00", TimeZone: "Europe/Copenhagen"},
		Organizer: &gcal.EventPerson{Email: "organiser@example.test", Self: true},
		Attendees: []gcal.EventAttendee{
			{Email: "self@example.test", Self: true, ResponseStatus: gcal.ResponseAccepted},
			{Email: "guest@example.test", ResponseStatus: gcal.ResponseNeedsAction},
			{Email: "room@example.test", Resource: true},
		},
		Recurrence:   []string{"RRULE:FREQ=WEEKLY"},
		Transparency: gcal.TransparencyTransparent,
	}
	e, err := model.FromEvent("primary", raw, z)
	if err != nil {
		t.Fatal(err)
	}
	if e.Title != "Review" || e.CalendarID != "primary" || e.ETag != `"abc"` {
		t.Fatalf("%+v", e)
	}
	if !e.IsSeries() || !e.IsRecurring() || e.IsInstance() {
		t.Fatalf("recurrence flags wrong: %+v", e)
	}
	if !e.Transparent {
		t.Fatal("transparency was lost")
	}
	if !e.OrganizerSelf || e.Organizer != "organiser@example.test" {
		t.Fatalf("organizer = %q self=%v", e.Organizer, e.OrganizerSelf)
	}
	if len(e.Attendees) != 3 {
		t.Fatalf("got %d attendees", len(e.Attendees))
	}
}

// TestReachesPeopleExcludesSelfAndRooms is §4.3.2: a write that cannot
// reach anybody must not demand a notify decision, or the requirement
// becomes friction with no safety in it.
func TestReachesPeopleExcludesSelfAndRooms(t *testing.T) {
	cases := []struct {
		name  string
		att   []model.Attendee
		want  bool
		count int
	}{
		{"nobody", nil, false, 0},
		{"only me", []model.Attendee{{Email: "me@example.test", Self: true}}, false, 0},
		{"only a room", []model.Attendee{{Email: "room@example.test", Resource: true}}, false, 0},
		{"a real guest", []model.Attendee{{Email: "guest@example.test"}}, true, 1},
		{"me, a room and two guests", []model.Attendee{
			{Email: "me@example.test", Self: true},
			{Email: "room@example.test", Resource: true},
			{Email: "a@example.test"}, {Email: "b@example.test"},
		}, true, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := model.Event{Attendees: c.att}
			if got := e.ReachesPeople(); got != c.want {
				t.Fatalf("ReachesPeople() = %v, want %v", got, c.want)
			}
			if got := e.GuestCount(); got != c.count {
				t.Fatalf("GuestCount() = %d, want %d", got, c.count)
			}
		})
	}
}

func TestInstanceFlags(t *testing.T) {
	e := model.Event{SeriesID: "parent"}
	if !e.IsInstance() || e.IsSeries() || !e.IsRecurring() {
		t.Fatalf("%+v", e)
	}
	plain := model.Event{}
	if plain.IsRecurring() {
		t.Fatal("a one-off event reported itself as recurring")
	}
}

func TestCancelled(t *testing.T) {
	if !(model.Event{Status: gcal.StatusCancelled}).Cancelled() {
		t.Fatal("a cancelled event does not report itself")
	}
	if (model.Event{Status: gcal.StatusConfirmed}).Cancelled() {
		t.Fatal("a confirmed event reported itself cancelled")
	}
}

func TestFromCalendarListKeepsBothNames(t *testing.T) {
	c := model.FromCalendarList(gcal.CalendarListEntry{
		ID: "team@example.test", Summary: "Team", SummaryOverride: "My name for it",
		AccessRole: gcal.RoleWriter, TimeZone: "UTC",
	})
	if c.Title != "My name for it" {
		t.Fatalf("title = %q, want this user's own name", c.Title)
	}
	if c.Original != "Team" {
		t.Fatalf("original = %q; a rename is this user's alone and the other name must survive", c.Original)
	}

	// With no override, there is no second name to report.
	plain := model.FromCalendarList(gcal.CalendarListEntry{ID: "x", Summary: "Team"})
	if plain.Original != "" {
		t.Fatalf("original = %q for an un-renamed calendar", plain.Original)
	}
}

func TestCanWrite(t *testing.T) {
	cases := map[string]bool{
		gcal.RoleOwner:                    true,
		gcal.RoleWriter:                   true,
		gcal.RoleWriterWithoutPrivateData: true,
		gcal.RoleReader:                   false,
		gcal.RoleFreeBusyReader:           false,
		gcal.RoleNone:                     false,
		"somethingGoogleAddedLater":       false,
	}
	for role, want := range cases {
		if got := (model.Calendar{Role: role}).CanWrite(); got != want {
			t.Fatalf("CanWrite(%q) = %v, want %v", role, got, want)
		}
	}
}
