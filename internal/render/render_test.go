package render_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/v2/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v2/internal/model"
	"github.com/mmedum/google-calendar-mcp/v2/internal/render"
	"github.com/mmedum/google-calendar-mcp/v2/internal/when"
)

func zone(t *testing.T, name string) when.Zone {
	t.Helper()
	z, err := when.Resolve(name, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return z
}

func timed(t *testing.T, start, end string, loc string) model.Event {
	t.Helper()
	l, err := when.LoadLocation(loc)
	if err != nil {
		t.Fatal(err)
	}
	s, err := when.ParseZoned(start, l)
	if err != nil {
		t.Fatal(err)
	}
	e, err := when.ParseZoned(end, l)
	if err != nil {
		t.Fatal(err)
	}
	return model.Event{
		ID: "ev", Title: "Meeting", Status: gcal.StatusConfirmed,
		Start: model.When{At: s}, End: model.When{At: e},
	}
}

// TestAllDayNeverRendersATime is the renderer's half of §4.1. Printing
// "00:00" for an all-day event is the sentence a reader turns into a
// bug, and the next implementer turns into an instant.
func TestAllDayNeverRendersATime(t *testing.T) {
	e := model.Event{
		ID: "ev", Title: "Public holiday",
		Start: model.When{AllDay: true, Date: when.MustParseDate("2026-03-20")},
		End:   model.When{AllDay: true, Date: when.MustParseDate("2026-03-21")},
	}
	got := render.TimeRange(e, zone(t, "America/Chicago"))
	if !strings.Contains(got, "all day") {
		t.Fatalf("TimeRange = %q, want it to say all day", got)
	}
	for _, forbidden := range []string{"00:00", "0:00", "23:00", "T00"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("an all-day event rendered a time: %q", got)
		}
	}
	if !strings.Contains(got, "2026-03-20") {
		t.Fatalf("TimeRange = %q, want the date", got)
	}
}

// TestOneDayAllDayEventDoesNotReadAsTwo: Google's end date is exclusive.
func TestOneDayAllDayEventDoesNotReadAsTwo(t *testing.T) {
	e := model.Event{
		Title: "Holiday",
		Start: model.When{AllDay: true, Date: when.MustParseDate("2026-03-20")},
		End:   model.When{AllDay: true, Date: when.MustParseDate("2026-03-21")},
	}
	got := render.TimeRange(e, zone(t, "UTC"))
	if strings.Contains(got, "2026-03-21") {
		t.Fatalf("a one-day event rendered its exclusive end date, reading as two days: %q", got)
	}

	// A genuine two-day event shows both ends.
	e.End.Date = when.MustParseDate("2026-03-22")
	got = render.TimeRange(e, zone(t, "UTC"))
	if !strings.Contains(got, "2026-03-20") || !strings.Contains(got, "2026-03-21") {
		t.Fatalf("a two-day event lost an end: %q", got)
	}
}

func TestTimedEventRendersItsRange(t *testing.T) {
	e := timed(t, "2026-03-16T09:00:00+01:00", "2026-03-16T10:30:00+01:00", "Europe/Copenhagen")
	got := render.TimeRange(e, zone(t, "Europe/Copenhagen"))
	if got != "09:00-10:30" {
		t.Fatalf("TimeRange = %q, want 09:00-10:30", got)
	}
}

// TestEventCrossingMidnightShowsTheDate, or it reads as ending before it
// began.
func TestEventCrossingMidnightShowsTheDate(t *testing.T) {
	e := timed(t, "2026-03-16T23:00:00+01:00", "2026-03-17T01:00:00+01:00", "Europe/Copenhagen")
	got := render.TimeRange(e, zone(t, "Europe/Copenhagen"))
	if !strings.Contains(got, "2026-03-17") {
		t.Fatalf("an event crossing midnight does not show the end date: %q", got)
	}
}

func TestEventLineTags(t *testing.T) {
	base := timed(t, "2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", "Europe/Copenhagen")
	z := zone(t, "Europe/Copenhagen")

	cases := []struct {
		name string
		edit func(e *model.Event)
		want string
	}{
		{"canceled", func(e *model.Event) { e.Status = gcal.StatusCanceled }, "canceled"},
		{"transparent says free", func(e *model.Event) { e.Transparent = true }, "free"},
		{"series shows its rule", func(e *model.Event) {
			e.Recurrence = []string{"RRULE:FREQ=WEEKLY;BYDAY=TU"}
		}, "every week on Tuesday"},
		{"instance", func(e *model.Event) { e.SeriesID = "parent" }, "one occurrence"},
		{"guest count", func(e *model.Event) {
			e.Attendees = []model.Attendee{{Email: "a@example.test"}, {Email: "b@example.test"}}
		}, "2 guests"},
		{"truncated guest list", func(e *model.Event) { e.AttendeesTruncated = true }, "truncated"},
		{"invented end", func(e *model.Event) { e.EndInvented = true }, "no end time set"},
		{"birthday is not a meeting", func(e *model.Event) { e.Type = gcal.EventTypeBirthday }, "birthday"},
		{"out of office", func(e *model.Event) { e.Type = gcal.EventTypeOutOfOffice }, "out of office"},
		{"from gmail cannot be edited", func(e *model.Event) { e.Type = gcal.EventTypeFromGmail }, "cannot be edited"},
		{"location", func(e *model.Event) { e.Location = "Room 4" }, "at Room 4"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := base
			c.edit(&e)
			got := render.EventLine(e, z)
			if !strings.Contains(got, c.want) {
				t.Fatalf("EventLine = %q, want it to mention %q", got, c.want)
			}
		})
	}
}

func TestRecurrenceIsExplained(t *testing.T) {
	cases := []struct{ rule, want string }{
		{"RRULE:FREQ=DAILY", "every day"},
		{"RRULE:FREQ=DAILY;INTERVAL=3", "every 3 days"},
		{"RRULE:FREQ=WEEKLY;BYDAY=TU", "every week on Tuesday"},
		{"RRULE:FREQ=WEEKLY;INTERVAL=2;BYDAY=MO,WE", "every 2 weeks on Monday and Wednesday"},
		{"RRULE:FREQ=MONTHLY;BYDAY=2TU", "2nd Tuesday"},
		{"RRULE:FREQ=MONTHLY;BYMONTHDAY=-1", "last day"},
		{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10", "10 times"},
		{"RRULE:FREQ=MONTHLY", "every month"},
		{"RRULE:FREQ=YEARLY", "every year"},
		{"RRULE:FREQ=WEEKLY;BYDAY=2TU", "Tuesday"},
		{"RRULE:FREQ=WEEKLY;UNTIL=20261231T000000Z", "until"},
	}
	for _, c := range cases {
		t.Run(c.rule, func(t *testing.T) {
			if got := render.Recurrence([]string{c.rule}); !strings.Contains(got, c.want) {
				t.Fatalf("Recurrence(%q) = %q, want it to mention %q", c.rule, got, c.want)
			}
		})
	}
	// An unparseable rule comes back as itself rather than as a wrong
	// explanation.
	odd := "RRULE:FREQ=HOURLY;INTERVAL=6"
	if got := render.Recurrence([]string{odd}); got != odd {
		t.Fatalf("Recurrence(%q) = %q; an unknown rule must be shown verbatim", odd, got)
	}
	if got := render.Recurrence(nil); got == "" {
		t.Fatal("Recurrence(nil) returned an empty string")
	}
}

func TestScheduleStatesItsWindowZoneAndCompleteness(t *testing.T) {
	z := zone(t, "Europe/Copenhagen")
	start, _ := when.ParseZoned("2026-03-16T00:00:00+01:00", z.Loc)
	end, _ := when.ParseZoned("2026-03-17T00:00:00+01:00", z.Loc)
	w, err := when.NewWindow(start, end, z.Loc)
	if err != nil {
		t.Fatal(err)
	}

	s := render.Schedule{
		Window: w, Zone: z, Calendars: []string{"Sample Primary"},
		Events:    []model.Event{timed(t, "2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", "Europe/Copenhagen")},
		Matched:   5,
		Truncated: true, NextPageToken: "next", Requests: 3, Expanded: true,
	}
	got := s.Text()
	for _, want := range []string{
		"2026-03-16", "Europe/Copenhagen", "Sample Primary",
		"expanded", "budget", "page_token", "3 API requests", "of 5",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the schedule does not state %q:\n%s", want, got)
		}
	}
}

func TestEmptyScheduleSaysSo(t *testing.T) {
	z := zone(t, "UTC")
	start, _ := when.ParseZoned("2026-03-16T00:00:00Z", z.Loc)
	end, _ := when.ParseZoned("2026-03-17T00:00:00Z", z.Loc)
	w, _ := when.NewWindow(start, end, z.Loc)
	got := render.Schedule{Window: w, Zone: z}.Text()
	if !strings.Contains(got, "No events") {
		t.Fatalf("an empty schedule does not say so:\n%s", got)
	}
	// Even empty, it must still say which window it looked at.
	if !strings.Contains(got, "2026-03-16") {
		t.Fatalf("an empty schedule hides its window:\n%s", got)
	}
}

func TestScheduleSaysWhichRecurrenceShapeItReturned(t *testing.T) {
	z := zone(t, "UTC")
	start, _ := when.ParseZoned("2026-03-16T00:00:00Z", z.Loc)
	end, _ := when.ParseZoned("2026-03-17T00:00:00Z", z.Loc)
	w, _ := when.NewWindow(start, end, z.Loc)

	expanded := render.Schedule{Window: w, Zone: z, Expanded: true}.Text()
	series := render.Schedule{Window: w, Zone: z, Expanded: false}.Text()
	if expanded == series {
		t.Fatal("the two recurrence shapes render identically; §2.9 makes them different answers")
	}
	if !strings.Contains(series, "series") {
		t.Fatalf("the unexpanded read does not say so:\n%s", series)
	}
}

// TestAvailabilityNeverCallsAnUnreadableCalendarFree is §4.6.
func TestAvailabilityNeverCallsAnUnreadableCalendarFree(t *testing.T) {
	z := zone(t, "UTC")
	start, _ := when.ParseZoned("2026-03-16T00:00:00Z", z.Loc)
	end, _ := when.ParseZoned("2026-03-17T00:00:00Z", z.Loc)
	w, _ := when.NewWindow(start, end, z.Loc)

	r := render.AvailabilityReport{
		Window: w, Zone: z,
		Answers: []model.Availability{
			{CalendarID: "readable", Busy: nil},
			{CalendarID: "unreadable", Unknown: true, Reason: "notFound"},
		},
	}
	got := r.Text()
	if !strings.Contains(got, "UNKNOWN") {
		t.Fatalf("an unreadable calendar was not marked unknown:\n%s", got)
	}
	if !strings.Contains(got, "Do not treat this as free") {
		t.Fatalf("the report does not warn against reading unknown as free:\n%s", got)
	}
	// And a genuinely free calendar still says free.
	if !strings.Contains(got, "free for the whole window") {
		t.Fatalf("a readable, empty calendar was not reported free:\n%s", got)
	}
}

func TestCalendarLineExplainsTheRole(t *testing.T) {
	c := model.Calendar{
		ID: "primary", Title: "Mine", TimeZone: "Europe/Copenhagen",
		Role: gcal.RoleWriterWithoutPrivateData, Primary: true, Selected: true,
	}
	got := render.CalendarLine(c)
	if !strings.Contains(got, gcal.RoleMeans(gcal.RoleWriterWithoutPrivateData)) {
		t.Fatalf("the role was echoed without being explained:\n%s", got)
	}
	if !strings.Contains(got, "primary") {
		t.Fatalf("the primary calendar is not marked:\n%s", got)
	}

	// A renamed calendar says what everyone else sees.
	c.Original = "Shared team calendar"
	got = render.CalendarLine(c)
	if !strings.Contains(got, "Shared team calendar") {
		t.Fatalf("a renamed calendar hides its original name:\n%s", got)
	}
}

func TestCalendarListCounts(t *testing.T) {
	got := render.CalendarList([]model.Calendar{
		{ID: "a", Title: "A", Role: gcal.RoleOwner},
		{ID: "b", Title: "B", Role: gcal.RoleReader},
	})
	if !strings.Contains(got, "2 calendars") {
		t.Fatalf("the count is wrong or missing:\n%s", got)
	}
	one := render.CalendarList([]model.Calendar{{ID: "a", Title: "A", Role: gcal.RoleOwner}})
	if !strings.Contains(one, "1 calendar.") {
		t.Fatalf("singular not handled:\n%s", one)
	}
}

func TestDuration(t *testing.T) {
	for _, c := range []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Minute, "30m"},
		{time.Hour, "1h"},
		{90 * time.Minute, "1h30m"},
		{0, "0m"},
		{-time.Hour, "0m"},
	} {
		if got := render.Duration(c.in); got != c.want {
			t.Fatalf("Duration(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestBothLineRenderersCarryTheSameTags.
//
// The two views had their own tag lists, and the instances view had
// quietly stopped showing four of them. A reader looking at one series
// would not have been told that an occurrence is out of office, that
// Google truncated its guest list, that nobody set an end time, or that
// it does not make anybody busy.
func TestBothLineRenderersCarryTheSameTags(t *testing.T) {
	z := zone(t, "Europe/Copenhagen")
	e := timed(t, "2026-03-16T09:00:00+01:00", "2026-03-16T10:00:00+01:00", "Europe/Copenhagen")
	e.Type = gcal.EventTypeOutOfOffice
	e.EndInvented = true
	e.AttendeesTruncated = true
	e.Transparent = true
	e.Location = "Room 4"
	e.Attendees = []model.Attendee{{Email: "a@example.test"}}

	schedule := render.EventLine(e, z)
	instance := render.InstanceLine(e, z)

	for _, want := range []string{
		"out of office", "no end time set", "guest list truncated by Google",
		"free", "at Room 4", "1 guest",
	} {
		if !strings.Contains(schedule, want) {
			t.Fatalf("EventLine does not mention %q:\n%s", want, schedule)
		}
		if !strings.Contains(instance, want) {
			t.Fatalf("InstanceLine does not mention %q:\n%s", want, instance)
		}
	}
	// And the one deliberate difference: an occurrence says which date
	// was removed, not merely that something was canceled.
	e.Status = gcal.StatusCanceled
	if got := render.InstanceLine(e, z); !strings.Contains(got, "removed from the series") {
		t.Fatalf("a canceled occurrence reads as an ordinary cancellation:\n%s", got)
	}
	if got := render.EventLine(e, z); !strings.Contains(got, "canceled") {
		t.Fatalf("EventLine lost its canceled tag:\n%s", got)
	}
}

// TestAFreeGapAcrossMidnightShowsBothDates. A multi-day window is the
// ordinary case for availability, and "2026-03-20 17:00-09:00" reads as
// ending before it began.
func TestAFreeGapAcrossMidnightShowsBothDates(t *testing.T) {
	z := zone(t, "Europe/Copenhagen")
	start, err := when.ParseZoned("2026-03-20T17:00:00+01:00", z.Loc)
	if err != nil {
		t.Fatal(err)
	}
	end, err := when.ParseZoned("2026-03-21T09:00:00+01:00", z.Loc)
	if err != nil {
		t.Fatal(err)
	}
	w, err := when.NewWindow(start, end, z.Loc)
	if err != nil {
		t.Fatal(err)
	}
	rep := render.AvailabilityReport{
		Window: w, Zone: z, GapsFrom: 1,
		Answers: []model.Availability{{CalendarID: "primary"}},
		Gaps:    []when.Window{w},
	}
	got := rep.Text()
	if !strings.Contains(got, "2026-03-20 17:00 to 2026-03-21 09:00") {
		t.Fatalf("a gap across midnight does not show both dates:\n%s", got)
	}
}

// TestAnAllDayBusyBlockDoesNotReadAsZeroLength: an all-day event comes
// back from free/busy as midnight to midnight, and printing the end
// time alone rendered it "00:00-00:00" — a block of no length, on the
// one kind of event that occupies the whole day. Found by reading a
// live transcript, which is the only place it showed.
func TestAnAllDayBusyBlockDoesNotReadAsZeroLength(t *testing.T) {
	loc, err := when.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	zone := when.Zone{Loc: loc, Source: when.ZoneFromCalendar}
	window, err := when.NewWindow(
		when.Wall(2026, time.March, 20, 0, 0, loc),
		when.Wall(2026, time.March, 21, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	rep := render.AvailabilityReport{
		Window: window, Zone: zone, GapsFrom: 1,
		Answers: []model.Availability{{CalendarID: "primary", Busy: []model.Busy{{
			Start: when.Wall(2026, time.March, 20, 0, 0, loc),
			End:   when.Wall(2026, time.March, 21, 0, 0, loc),
		}}}},
	}
	text := rep.Text()
	if strings.Contains(text, "00:00-00:00") {
		t.Fatalf("a whole-day busy block rendered as zero length:\n%s", text)
	}
	if !strings.Contains(text, "2026-03-20 00:00 to 2026-03-21 00:00") {
		t.Fatalf("a busy block crossing midnight does not carry the end's date:\n%s", text)
	}
}
