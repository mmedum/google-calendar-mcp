package service_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/service"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

func testZone(t *testing.T) when.Zone {
	t.Helper()
	z, err := when.Resolve("Europe/Copenhagen", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return z
}

// TestEventOutKeepsTheDateAndTimeHalvesApart is §4.1 carried into the
// JSON a client actually sees. A structured event must not offer both a
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
	res := service.CalendarResult{
		Calendar: service.CalendarOut{ID: "primary", Title: "Mine", Role: gcal.RoleOwner},
	}
	if !strings.Contains(res.Render(), "Shared with nobody") {
		t.Fatalf("an unshared calendar does not say so:\n%s", res.Render())
	}
}

// TestPublicSharingIsShouted: scope type "default" means the whole
// internet, and it is the one ACL value that cannot be undone from the
// other side.
func TestPublicSharingIsShouted(t *testing.T) {
	res := service.CalendarResult{
		Calendar: service.CalendarOut{ID: "primary", Title: "Mine", Role: gcal.RoleOwner},
		Sharing: []service.SharingOut{
			{Who: "anyone", ScopeType: gcal.ScopeTypeDefault, Role: gcal.RoleReader,
				RoleMeans: gcal.RoleMeans(gcal.RoleReader), Public: true},
		},
	}
	got := res.Render()
	if !strings.Contains(got, "ANYONE") {
		t.Fatalf("a public calendar is not called out:\n%s", got)
	}
}
