package render_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/plan"
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

// The write report is the one result a caller acts on twice: once to see
// what happened, and again to decide whether to tell somebody. §4.9 says
// what it has to carry, and a golden file is how a change to any of it
// arrives as a diff rather than as a surprise.
func TestGoldenWrite(t *testing.T) {
	z := goldenZone(t)
	before := goldenEvent(t, "abcdef0123", "Project review",
		"2026-03-18T10:00:00+01:00", "2026-03-18T11:00:00+01:00")
	before.Location = "Room 1"
	before.Attendees = []model.Attendee{
		{Email: "colleague@example.test", Response: gcal.ResponseAccepted},
		{Email: "partner@elsewhere.test", Response: gcal.ResponseNeedsAction},
	}
	after := before
	after.Location = "Room 2"
	after.ETag = `"abcdef0123-2"`

	w := render.WriteReport{
		Verb: render.VerbUpdate, Calendar: "Sample Primary", Zone: z,
		Before: &before, After: &after,
		Changes: []plan.Change{{Field: "location", From: "Room 1", To: "Room 2"}},
		Notify: "Asked Google to notify all 2 guests, 1 of them outside your organisation." +
			" That is what was asked for, not what arrived: the API reports nothing about delivery.",
		Requests: 2,
	}
	golden(t, "write-update", w.Text())
}

// A cancellation with nothing left afterwards, and a scope: the two
// shapes the update golden does not cover.
func TestGoldenWriteCancelled(t *testing.T) {
	z := goldenZone(t)
	before := goldenEvent(t, "abcdef0123", "Weekly review",
		"2026-03-24T14:00:00+01:00", "2026-03-24T15:00:00+01:00")
	before.SeriesID = "abcdef0123"

	w := render.WriteReport{
		Verb: render.VerbCancel, Calendar: "Sample Primary", Zone: z, Scope: "instance",
		Before:  &before,
		Changes: []plan.Change{{Field: "status", From: "confirmed", To: "cancelled"}},
		Notify:  "Nobody to notify: this write reaches no guests, so no notification was requested.",
		Notes: []string{
			"This event has no guests, so nobody else is holding it.",
			"One occurrence, cancelled with a status patch rather than deleted.",
		},
		Requests: 1,
	}
	golden(t, "write-cancel", w.Text())
}

// A dry run says so first, and says it wrote nothing.
func TestGoldenWriteDryRun(t *testing.T) {
	z := goldenZone(t)
	after := goldenEvent(t, "abcdef0123", "Not really", "2026-04-01T09:00:00+02:00",
		"2026-04-01T10:00:00+02:00")
	w := render.WriteReport{
		Verb: render.VerbCreate, DryRun: true, Calendar: "Sample Primary", Zone: z,
		After:  &after,
		Notify: "Asked Google to notify nobody, of 1 guest. Google says some mail may still be sent, so this is not a promise of silence.",
	}
	golden(t, "write-dry-run", w.Text())
}

// The calendar write report, which carries the distinction §7.5 is
// about: what changed on the calendar everybody sees, and what changed
// only for this account.
func TestGoldenCalendarWrite(t *testing.T) {
	before := model.Calendar{
		ID: "team@group.calendar.example.test", Title: "Sample Team",
		TimeZone: "Europe/Copenhagen", Role: gcal.RoleOwner, Selected: true,
	}
	after := before
	after.Title = "Sample Team — planning"
	after.ETag = `"team-2"`

	c := render.CalendarReport{
		Verb: render.VerbUpdate, Calendar: after, Before: &before,
		Changes: []plan.Change{
			{Field: "title", From: "Sample Team", To: "Sample Team — planning"},
			{Field: "color_id", From: "3", To: "7"},
		},
		Notes: []string{
			"That changes the calendar for everybody it is shared with, not only for you. " +
				"To change only your own view of it, pass my_name rather than title.",
			"Those are your own settings for this calendar. Nobody else sees any of them change.",
		},
		Requests: 4,
	}
	golden(t, "calendar-update", c.Text())
}

// Unsubscribing is the result most likely to be misread, so the golden
// holds the sentence that says what it did not do.
func TestGoldenCalendarUnsubscribe(t *testing.T) {
	cal := model.Calendar{
		ID: "team@group.calendar.example.test", Title: "Sample Team",
		TimeZone: "Europe/Copenhagen", Role: gcal.RoleReader, Selected: true,
	}
	c := render.CalendarReport{
		Verb: render.VerbUnsubscribe, Calendar: cal,
		Notes: []string{
			"This removes the calendar from YOUR list. The calendar itself is untouched, every event on it " +
				"stays, and nobody else sees any difference. Subscribe again with subscribe:true and the id above.",
		},
		Requests: 2,
	}
	golden(t, "calendar-unsubscribe", c.Text())
}

// A share, with exposure on both sides: §7.6's rule is that the result
// answers "who can see this now", and the golden is how a change to that
// answer arrives as a diff.
func TestGoldenSharing(t *testing.T) {
	before := []model.Sharing{
		{RuleID: "user:owner@example.test", ScopeType: gcal.ScopeTypeUser,
			Value: "owner@example.test", Role: gcal.RoleOwner},
	}
	added := model.Sharing{
		RuleID: "user:colleague@example.test", ScopeType: gcal.ScopeTypeUser,
		Value: "colleague@example.test", Role: gcal.RoleReader,
	}
	s := render.SharingReport{
		Verb: render.VerbShare, CalendarID: "team@group.calendar.example.test", Title: "Sample Team",
		Before: before, After: append(append([]model.Sharing{}, before...), added), Changed: &added,
		Notify: "Asked Google to email the person this rule names about the change. That is what was asked " +
			"for, not what arrived: the API reports nothing about delivery.",
		Requests: 2,
	}
	golden(t, "sharing-share", s.Text())
}

// A public calendar, listed: the public rule comes first and is shouted,
// because a reader scanning addresses will not notice a word in the
// middle of the list.
func TestGoldenSharingPublic(t *testing.T) {
	rules := []model.Sharing{
		{RuleID: "user:owner@example.test", ScopeType: gcal.ScopeTypeUser,
			Value: "owner@example.test", Role: gcal.RoleOwner},
		{RuleID: "default", ScopeType: gcal.ScopeTypeDefault, Role: gcal.RoleFreeBusyReader},
		{RuleID: "domain:example.test", ScopeType: gcal.ScopeTypeDomain,
			Value: "example.test", Role: gcal.RoleReader},
	}
	s := render.SharingReport{
		CalendarID: "team@group.calendar.example.test", Title: "Sample Team", After: rules,
		Notes: []string{
			"This calendar is PUBLIC: anybody at all can see only whether the time is busy, never what the " +
				"event is. unshare_calendar with who:anyone removes that rule — which stops new readers and " +
				"takes nothing back from whoever has already looked.",
		},
		Requests: 1,
	}
	golden(t, "sharing-public", s.Text())
}
