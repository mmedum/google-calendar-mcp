package render_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/render"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// update rewrites the golden files instead of comparing against them.
//
//	go test ./internal/render -update
//
// The point of a golden file is that a change to how a schedule reads
// shows up as a diff somebody has to look at (§13). Regenerating is one
// flag, and reviewing the diff is the part that cannot be automated.
var update = flag.Bool("update", false, "rewrite the golden files")

// goldenDir is the repository's testdata, not the package's: §9.1 keeps
// every fixture in one place, where the leak scan looks.
const goldenDir = "../../testdata/golden"

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join(goldenDir, name+".txt")
	if *update {
		if err := os.MkdirAll(goldenDir, 0o750); err != nil {
			t.Fatalf("create %s: %v", goldenDir, err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // a test fixture path built from a literal
	if err != nil {
		t.Fatalf("read %s: %v (run `go test ./internal/render -update` to create it)", path, err)
	}
	if got != string(want) {
		t.Fatalf("%s does not match the golden file.\n--- got ---\n%s\n--- want ---\n%s\n"+
			"If the change is deliberate, run `go test ./internal/render -update` and read the diff.",
			path, got, want)
	}
}

// goldenTZ is fixed so a golden file does not depend on where the tests
// run — which is the same rule §4.1 applies to the server itself.
const goldenTZ = "Europe/Copenhagen"

// goldenZone is that zone, resolved as a calendar's own rather than as
// one the caller asked for, because that is what a schedule reports.
func goldenZone(t *testing.T) when.Zone {
	t.Helper()
	loc, err := when.LoadLocation(goldenTZ)
	if err != nil {
		t.Fatal(err)
	}
	return when.Zone{Loc: loc, Source: when.ZoneFromCalendar}
}

func goldenWindow(t *testing.T, z when.Zone, from, to string) when.Window {
	t.Helper()
	start, err := when.ParseZoned(from, z.Loc)
	if err != nil {
		t.Fatal(err)
	}
	end, err := when.ParseZoned(to, z.Loc)
	if err != nil {
		t.Fatal(err)
	}
	w, err := when.NewWindow(start, end, z.Loc)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// goldenEvent is `timed` with an id and a title, which the golden files
// need so two occurrences of one series can be told apart.
func goldenEvent(t *testing.T, id, title, start, end string) model.Event {
	t.Helper()
	e := timed(t, start, end, goldenTZ)
	e.ID, e.Title, e.CalendarID = id, title, "primary"
	return e
}

func TestGoldenSchedule(t *testing.T) {
	z := goldenZone(t)
	allDay := model.Event{
		ID: "ev-holiday", CalendarID: "primary", Title: "Public holiday",
		Status: gcal.StatusConfirmed,
		Start:  model.When{AllDay: true, Date: when.MustParseDate("2026-03-20")},
		End:    model.When{AllDay: true, Date: when.MustParseDate("2026-03-21")},
	}
	series := goldenEvent(t, "ev-weekly", "Weekly review",
		"2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00")
	series.Recurrence = []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10"}

	standup := goldenEvent(t, "ev-standup", "Morning sync",
		"2026-03-16T09:00:00+01:00", "2026-03-16T09:15:00+01:00")
	standup.Attendees = []model.Attendee{{Email: "a@example.test"}, {Email: "b@example.test"}}

	free := goldenEvent(t, "ev-transparent", "Blocked but free",
		"2026-03-16T13:00:00+01:00", "2026-03-16T14:00:00+01:00")
	free.Transparent = true

	s := render.Schedule{
		Window:    goldenWindow(t, z, "2026-03-16T00:00:00+01:00", "2026-03-21T00:00:00+01:00"),
		Zone:      z,
		Calendars: []string{"Sample Primary"},
		Events:    []model.Event{standup, free, series, allDay},
		Matched:   4,
		Expanded:  false,
		Requests:  1,
	}
	golden(t, "schedule", s.Text())
}

func TestGoldenScheduleTruncated(t *testing.T) {
	z := goldenZone(t)
	s := render.Schedule{
		Window:        goldenWindow(t, z, "2026-03-16T00:00:00+01:00", "2026-03-17T00:00:00+01:00"),
		Zone:          z,
		Calendars:     []string{"Sample Primary", "Sample Team"},
		Events:        []model.Event{goldenEvent(t, "ev-standup", "Morning sync", "2026-03-16T09:00:00+01:00", "2026-03-16T09:15:00+01:00")},
		Matched:       40,
		Truncated:     true,
		NextPageToken: "next",
		Expanded:      true,
		Requests:      2,
	}
	golden(t, "schedule-truncated", s.Text())
}

func TestGoldenCalendarList(t *testing.T) {
	cals := []model.Calendar{
		{ID: "primary", Title: "Sample Primary", TimeZone: "Europe/Copenhagen", Role: gcal.RoleOwner, Primary: true, Selected: true},
		{
			ID: "team@group.calendar.example.test", Title: "Team (renamed)", Original: "Sample Team",
			TimeZone: "America/Chicago", Role: gcal.RoleWriter, Selected: true,
		},
		{
			ID: "readonly@group.calendar.example.test", Title: "Sample Readonly",
			TimeZone: "UTC", Role: gcal.RoleFreeBusyReader, Hidden: true,
		},
	}
	golden(t, "calendar-list", render.CalendarList(cals))
}

func TestGoldenInstances(t *testing.T) {
	z := goldenZone(t)
	first := goldenEvent(t, "ev-weekly_20260317T130000Z", "Weekly review",
		"2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00")
	first.SeriesID = "ev-weekly"
	first.OriginalStart = first.Start

	moved := goldenEvent(t, "ev-weekly_20260331T130000Z", "Weekly review",
		"2026-03-31T15:00:00+02:00", "2026-03-31T16:00:00+02:00")
	moved.SeriesID = "ev-weekly"
	moved.OriginalStart = goldenEvent(t, "x", "x",
		"2026-03-31T14:00:00+02:00", "2026-03-31T15:00:00+02:00").Start

	dropped := goldenEvent(t, "ev-weekly_20260407T120000Z", "Weekly review",
		"2026-04-07T14:00:00+02:00", "2026-04-07T15:00:00+02:00")
	dropped.SeriesID = "ev-weekly"
	dropped.OriginalStart = dropped.Start
	dropped.Status = gcal.StatusCancelled

	in := render.Instances{
		SeriesID: "ev-weekly", Title: "Weekly review", CalendarID: "primary",
		Zone: z, Events: []model.Event{first, moved, dropped},
		ShowCancelled: true, Requests: 1,
	}
	golden(t, "instances", in.Text())
}

func TestGoldenInstancesHidingCancelled(t *testing.T) {
	z := goldenZone(t)
	w := goldenWindow(t, z, "2026-03-16T00:00:00+01:00", "2026-04-01T00:00:00+02:00")
	first := goldenEvent(t, "ev-weekly_20260317T130000Z", "Weekly review",
		"2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00")
	first.SeriesID = "ev-weekly"
	first.OriginalStart = first.Start

	in := render.Instances{
		SeriesID: "ev-weekly", Title: "Weekly review", CalendarID: "primary",
		Window: &w, Zone: z, Events: []model.Event{first}, Requests: 1,
	}
	golden(t, "instances-window", in.Text())
}

func TestGoldenAvailability(t *testing.T) {
	z := goldenZone(t)
	w := goldenWindow(t, z, "2026-03-16T09:00:00+01:00", "2026-03-16T17:00:00+01:00")
	busy := func(start, end string) model.Busy {
		s, err := when.ParseZoned(start, z.Loc)
		if err != nil {
			t.Fatal(err)
		}
		e, err := when.ParseZoned(end, z.Loc)
		if err != nil {
			t.Fatal(err)
		}
		return model.Busy{Start: s, End: e}
	}
	answers := []model.Availability{
		{CalendarID: "primary", Busy: []model.Busy{
			busy("2026-03-16T09:00:00+01:00", "2026-03-16T09:15:00+01:00"),
			busy("2026-03-16T13:00:00+01:00", "2026-03-16T14:00:00+01:00"),
		}},
		{CalendarID: "team@group.calendar.example.test", Busy: []model.Busy{
			busy("2026-03-16T13:30:00+01:00", "2026-03-16T15:00:00+01:00"),
		}},
		{CalendarID: "nobody@example.test", Unknown: true, Reason: "no such calendar, or this account cannot see it"},
	}
	var all []model.Busy
	for _, a := range answers {
		all = append(all, a.Busy...)
	}
	rep := render.AvailabilityReport{
		Window: w, Zone: z, Answers: answers,
		Gaps:     model.FreeGaps(w, all, 30*time.Minute),
		GapsFrom: 2,
		MinGap:   30 * time.Minute,
		Requests: 1,
	}
	golden(t, "availability", rep.Text())
}

func TestGoldenAvailabilityAllUnknown(t *testing.T) {
	z := goldenZone(t)
	w := goldenWindow(t, z, "2026-03-16T09:00:00+01:00", "2026-03-16T17:00:00+01:00")
	rep := render.AvailabilityReport{
		Window: w, Zone: z,
		Answers: []model.Availability{
			{CalendarID: "nobody@example.test", Unknown: true, Reason: "no such calendar, or this account cannot see it"},
		},
		Requests: 1,
	}
	golden(t, "availability-unknown", rep.Text())
}
