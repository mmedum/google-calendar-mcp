package model_test

import (
	"testing"
	"time"

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
			if got := len(e.Guests()) > 0; got != c.want {
				t.Fatalf("Guests() non-empty = %v, want %v", got, c.want)
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

// zoned is a helper for the interval arithmetic below.
func zonedAt(t *testing.T, s string, loc *time.Location) when.Zoned {
	t.Helper()
	z, err := when.ParseZoned(s, loc)
	if err != nil {
		t.Fatalf("ParseZoned(%q): %v", s, err)
	}
	return z
}

func busyAt(t *testing.T, start, end string, loc *time.Location) model.Busy {
	t.Helper()
	return model.Busy{Start: zonedAt(t, start, loc), End: zonedAt(t, end, loc)}
}

func gapStrings(gaps []when.Window) []string {
	out := make([]string, 0, len(gaps))
	for _, g := range gaps {
		out = append(out, g.Start.T.Format("15:04")+"-"+g.End.T.Format("15:04"))
	}
	return out
}

func TestMergeBusy(t *testing.T) {
	loc, err := when.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   []model.Busy
		want []string
	}{
		{"nothing", nil, nil},
		{
			name: "two people busy at once is one busy block",
			in: []model.Busy{
				busyAt(t, "2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", loc),
				busyAt(t, "2026-03-16T09:30:00+01:00", "2026-03-16T10:30:00+01:00", loc),
			},
			want: []string{"09:00-10:30"},
		},
		{
			name: "touching blocks join, because there is no gap between them",
			in: []model.Busy{
				busyAt(t, "2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", loc),
				busyAt(t, "2026-03-16T10:00:00+01:00", "2026-03-16T11:00:00+01:00", loc),
			},
			want: []string{"09:00-11:00"},
		},
		{
			name: "a block inside another disappears into it",
			in: []model.Busy{
				busyAt(t, "2026-03-16T09:00:00+01:00", "2026-03-16T12:00:00+01:00", loc),
				busyAt(t, "2026-03-16T10:00:00+01:00", "2026-03-16T11:00:00+01:00", loc),
			},
			want: []string{"09:00-12:00"},
		},
		{
			name: "separate blocks stay separate, in order",
			in: []model.Busy{
				busyAt(t, "2026-03-16T14:00:00+01:00", "2026-03-16T15:00:00+01:00", loc),
				busyAt(t, "2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", loc),
			},
			want: []string{"09:00-10:00", "14:00-15:00"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := model.Merge(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("got %d blocks, want %d", len(got), len(c.want))
			}
			for i, b := range got {
				s := b.Start.T.Format("15:04") + "-" + b.End.T.Format("15:04")
				if s != c.want[i] {
					t.Fatalf("block %d = %s, want %s", i, s, c.want[i])
				}
			}
		})
	}
}

func TestFreeGaps(t *testing.T) {
	loc, err := when.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	window, err := when.NewWindow(
		zonedAt(t, "2026-03-16T09:00:00+01:00", loc),
		zonedAt(t, "2026-03-16T17:00:00+01:00", loc), loc)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		busy []model.Busy
		min  time.Duration
		want []string
	}{
		{"nobody is busy", nil, 0, []string{"09:00-17:00"}},
		{
			name: "one meeting leaves two gaps",
			busy: []model.Busy{busyAt(t, "2026-03-16T12:00:00+01:00", "2026-03-16T13:00:00+01:00", loc)},
			want: []string{"09:00-12:00", "13:00-17:00"},
		},
		{
			name: "busy from the start leaves one gap",
			busy: []model.Busy{busyAt(t, "2026-03-16T09:00:00+01:00", "2026-03-16T12:00:00+01:00", loc)},
			want: []string{"12:00-17:00"},
		},
		{
			name: "busy all window leaves none",
			busy: []model.Busy{busyAt(t, "2026-03-16T08:00:00+01:00", "2026-03-16T18:00:00+01:00", loc)},
			want: nil,
		},
		{
			name: "a meeting outside the window changes nothing",
			busy: []model.Busy{busyAt(t, "2026-03-16T19:00:00+01:00", "2026-03-16T20:00:00+01:00", loc)},
			want: []string{"09:00-17:00"},
		},
		{
			name: "overlapping meetings do not produce a gap between them",
			busy: []model.Busy{
				busyAt(t, "2026-03-16T10:00:00+01:00", "2026-03-16T12:00:00+01:00", loc),
				busyAt(t, "2026-03-16T11:00:00+01:00", "2026-03-16T13:00:00+01:00", loc),
			},
			want: []string{"09:00-10:00", "13:00-17:00"},
		},
		{
			name: "min drops the gaps too short to use",
			busy: []model.Busy{
				busyAt(t, "2026-03-16T09:15:00+01:00", "2026-03-16T12:00:00+01:00", loc),
				busyAt(t, "2026-03-16T12:20:00+01:00", "2026-03-16T17:00:00+01:00", loc),
			},
			min:  30 * time.Minute,
			want: nil,
		},
		{
			name: "min keeps the gaps that are long enough",
			busy: []model.Busy{busyAt(t, "2026-03-16T09:15:00+01:00", "2026-03-16T12:00:00+01:00", loc)},
			min:  30 * time.Minute,
			want: []string{"12:00-17:00"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := gapStrings(model.FreeGaps(window, c.busy, c.min))
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("gap %d = %s, want %s (all: %v)", i, got[i], c.want[i], got)
				}
			}
		})
	}
}

// TestFreeGapsAcrossADaylightSavingTransition: the 29 March day is 23
// hours long in Copenhagen, and a free gap over it must be 23 hours
// rather than the 24 an arithmetic on dates would report.
func TestFreeGapsAcrossADaylightSavingTransition(t *testing.T) {
	loc, err := when.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	day := when.DayWindow(when.MustParseDate("2026-03-29"), loc)
	gaps := model.FreeGaps(day, nil, 0)
	if len(gaps) != 1 {
		t.Fatalf("got %d gaps over an empty day", len(gaps))
	}
	if got := gaps[0].Duration(); got != 23*time.Hour {
		t.Fatalf("the day of the spring-forward transition was reported as %v, want 23h", got)
	}
}
