package service_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v5/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v5/internal/model"
	"github.com/mmedum/google-calendar-mcp/v5/internal/service"
	"github.com/mmedum/google-calendar-mcp/v5/internal/when"
)

func testZone(t *testing.T) when.Zone {
	t.Helper()
	z, err := when.Resolve("Europe/Copenhagen", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return z
}

// TestEventOutKeepsTheDateAndTimeHalvesApart: a date is not a time, in
// the JSON a client actually sees too. A structured event must not offer both a
// date and a timestamp for the same edge, or the consumer picks.
func TestEventOutKeepsTheDateAndTimeHalvesApart(t *testing.T) {
	allDay := service.NewEventOut(model.Event{
		ID: "a", Title: "Holiday",
		Start: model.When{AllDay: true, Date: when.MustParseDate("2026-03-20")},
		End:   model.When{AllDay: true, Date: when.MustParseDate("2026-03-21")},
	})
	if !allDay.AllDay {
		t.Fatal("the all-day flag was lost")
	}
	if allDay.Start != "" || allDay.End != "" {
		t.Fatalf("an all-day event carries a timestamp: %+v", allDay)
	}
	if allDay.StartDate != "2026-03-20" {
		t.Fatalf("start_date = %q", allDay.StartDate)
	}

	z := testZone(t)
	start, _ := when.ParseZoned("2026-03-16T09:00:00+01:00", z.Loc)
	end, _ := when.ParseZoned("2026-03-16T10:00:00+01:00", z.Loc)
	timed := service.NewEventOut(model.Event{
		ID: "b", Title: "Meeting",
		Start: model.When{At: start}, End: model.When{At: end},
	})
	if timed.AllDay {
		t.Fatal("a timed event claims to be all-day")
	}
	if timed.StartDate != "" || timed.EndDate != "" {
		t.Fatalf("a timed event carries a bare date: %+v", timed)
	}
	if timed.Start == "" {
		t.Fatal("a timed event has no start")
	}
}

func TestEventOutExplainsRecurrence(t *testing.T) {
	out := service.NewEventOut(model.Event{
		ID: "s", Recurrence: []string{"RRULE:FREQ=WEEKLY;BYDAY=TU"},
	})
	if out.RecurrenceMeans == "" {
		t.Fatal("a series carries its rule and no explanation of it")
	}
	if !strings.Contains(out.RecurrenceMeans, "Tuesday") {
		t.Fatalf("recurrence_means = %q", out.RecurrenceMeans)
	}
	plain := service.NewEventOut(model.Event{ID: "p"})
	if plain.RecurrenceMeans != "" {
		t.Fatal("a one-off event got a recurrence explanation")
	}
}

// TestBothHalvesDiffer: content is the readable presentation and
// structuredContent the machine one, and the specification asks for two
// forms rather than the same bytes twice.
func TestBothHalvesDiffer(t *testing.T) {
	z := testZone(t)
	start, _ := when.ParseZoned("2026-03-16T09:00:00+01:00", z.Loc)
	end, _ := when.ParseZoned("2026-03-16T10:00:00+01:00", z.Loc)
	e := model.Event{
		ID: "ev", CalendarID: "primary", Title: "Review",
		Start: model.When{At: start}, End: model.When{At: end},
		Attendees: []model.Attendee{
			{Email: "guest@example.test", Response: gcal.ResponseNeedsAction},
		},
	}
	res := service.NewEventResult(e, z)

	text := res.Render()
	structured, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	if text == "" {
		t.Fatal("the readable half is empty")
	}
	if text == string(structured) {
		t.Fatal("both halves are the same bytes")
	}
	if !strings.Contains(text, "Review") {
		t.Fatalf("the readable half lost the title:\n%s", text)
	}
	if !strings.Contains(text, "guest@example.test") {
		t.Fatalf("the readable half lost the guest:\n%s", text)
	}
	if !strings.Contains(text, z.Name()) {
		t.Fatalf("the readable half does not name the zone:\n%s", text)
	}
}

func TestEventResultDescribesRecurrenceShape(t *testing.T) {
	z := testZone(t)
	series := service.NewEventResult(model.Event{
		ID: "s", Recurrence: []string{"RRULE:FREQ=WEEKLY;BYDAY=TU"},
	}, z)
	if !strings.Contains(series.Render(), "series") {
		t.Fatalf("a series does not say so:\n%s", series.Render())
	}
	instance := service.NewEventResult(model.Event{ID: "i", SeriesID: "s"}, z)
	if !strings.Contains(instance.Render(), "occurrence") {
		t.Fatalf("an instance does not say so:\n%s", instance.Render())
	}
}

func TestEventResultReportsATruncatedGuestList(t *testing.T) {
	z := testZone(t)
	res := service.NewEventResult(model.Event{
		ID: "e", Attendees: []model.Attendee{{Email: "a@example.test"}},
		AttendeesTruncated: true,
	}, z)
	if !strings.Contains(res.Render(), "truncated") {
		t.Fatalf("a truncated guest list is not reported:\n%s", res.Render())
	}
}

func TestCalendarsResultRendersAndCarriesBothHalves(t *testing.T) {
	res := service.NewCalendarsResult([]model.Calendar{
		{ID: "primary", Title: "Mine", TimeZone: "UTC", Role: gcal.RoleOwner, Primary: true},
		{ID: "r@example.test", Title: "Read", TimeZone: "UTC", Role: gcal.RoleReader},
	})
	if res.Count != 2 {
		t.Fatalf("count = %d", res.Count)
	}
	if !res.Calendars[0].CanWrite || res.Calendars[1].CanWrite {
		t.Fatalf("can_write is wrong: %+v", res.Calendars)
	}
	for _, c := range res.Calendars {
		if c.RoleMeans == "" {
			t.Fatalf("role %q not explained", c.Role)
		}
	}
	if !strings.Contains(res.Render(), "Mine") {
		t.Fatalf("the rendering lost a calendar:\n%s", res.Render())
	}
}

func TestCalendarResultWithNoSharing(t *testing.T) {
	res := service.NewCalendarResult(model.Calendar{ID: "primary", Title: "Mine", Role: gcal.RoleOwner}, nil)
	if !strings.Contains(res.Render(), "nobody else") {
		t.Fatalf("an unshared calendar does not say so:\n%s", res.Render())
	}
}

// TestPublicSharingIsShouted: scope type "default" means the whole
// internet, and it is the one ACL value that cannot be undone from the
// other side.
func TestPublicSharingIsShouted(t *testing.T) {
	res := service.NewCalendarResult(model.Calendar{ID: "primary", Title: "Mine", Role: gcal.RoleOwner},
		[]model.Sharing{{ScopeType: gcal.ScopeTypeDefault, Role: gcal.RoleReader}})
	got := res.Render()
	if !strings.Contains(got, "ANYONE") {
		t.Fatalf("a public calendar is not called out:\n%s", got)
	}
}

// get_calendar's text, whole, for a shared calendar, an unshared one and
// one whose sharing was not read: the text both halves are built for,
// held line for line rather than by a phrase in it.
func TestCalendarDetailTextIsWhole(t *testing.T) {
	svc, fake := calendarSeed(t)
	fake.ACL["team@group.calendar.example.test"] = append(fake.ACL["team@group.calendar.example.test"],
		gcal.AclRule{ID: "default", Role: gcal.RoleFreeBusyReader, Scope: gcal.AclScope{Type: gcal.ScopeTypeDefault}},
		gcal.AclRule{ID: "user:colleague@example.test", Role: gcal.RoleWriter,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "colleague@example.test"}})
	primary := "Sample Primary\n  id: me@example.test\n  time zone: Europe/Copenhagen\n" +
		"  your access: owner — can see and change everything, including who else has access\n" +
		"  your primary calendar\n  default reminders: none\n\n"
	for _, c := range []struct {
		ref, want string
		off       bool
	}{
		{"team@group.calendar.example.test", "Sample Team\n  id: team@group.calendar.example.test\n" +
			"  time zone: America/Chicago\n" +
			"  your access: owner — can see and change everything, including who else has access\n" +
			"  default reminders: none\n\nwho can see it:\n" +
			"  ANYONE, signed in or not — freeBusyReader (can see only whether the time is busy, never what the event is)\n" +
			"  me@example.test — owner (can see and change everything, including who else has access)\n" +
			"  colleague@example.test — writer (can see and change events, including private ones)\n", false},
		{"primary", primary + "who can see it:\n  nobody else.\n", false},
		{"primary", primary + "Sharing tools are off in this server (GCAL_SHARING=off), so the sharing list was not read.\n", true},
	} {
		svc.Cfg.Sharing = !c.off
		got, err := svc.CalendarDetail(context.Background(), c.ref)
		if err != nil {
			t.Fatal(err)
		}
		if got.Render() != c.want {
			t.Errorf("get_calendar %s (sharing off: %v) renders\n%s\nwant\n%s", c.ref, c.off, got.Render(), c.want)
		}
	}
}
