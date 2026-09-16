package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/config"
	"github.com/mmedum/google-calendar-mcp/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/service"
)

func day(from, to string) service.AvailabilityOptions {
	return service.AvailabilityOptions{From: from, To: to}
}

func TestAvailabilityReportsBusyAndFree(t *testing.T) {
	svc, _ := seeded(t)
	o := day("2026-03-16", "2026-03-16")
	o.Calendars = []string{"primary"}

	got, err := svc.Availability(context.Background(), o)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if len(got.Answers) != 1 {
		t.Fatalf("got %d answers, want 1", len(got.Answers))
	}
	if got.Answers[0].Unknown {
		t.Fatal("a calendar that answered was reported unknown")
	}
	if len(got.Answers[0].Busy) != 1 {
		t.Fatalf("got %d busy blocks, want the one seeded", len(got.Answers[0].Busy))
	}
	if len(got.Gaps) == 0 {
		t.Fatal("a day with one 15-minute meeting has free gaps")
	}
	if got.Requests != 1 {
		t.Fatalf("spent %d requests for one batch", got.Requests)
	}
}

// TestUnknownIsNeverFree is §4.6, the rule this tool exists to keep: a
// calendar that could not be read is unknown, and a model that cannot
// tell that from "free" will book over somebody.
func TestUnknownIsNeverFree(t *testing.T) {
	fake := caltest.Seed()
	fake.FreeBusyErrors["readonly@group.calendar.example.test"] = "notFound"
	svc := newService(t, fake)

	o := day("2026-03-16", "2026-03-16")
	o.Calendars = []string{"primary", "readonly@group.calendar.example.test"}
	got, err := svc.Availability(context.Background(), o)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}

	var unknown int
	for _, a := range got.Answers {
		if a.Unknown {
			unknown++
			if a.Reason == "" {
				t.Fatal("an unknown calendar does not say why")
			}
		}
	}
	if unknown != 1 {
		t.Fatalf("%d calendars reported unknown, want 1", unknown)
	}

	text := got.Text()
	if !strings.Contains(text, "UNKNOWN") || !strings.Contains(text, "Do not treat this as free") {
		t.Fatalf("the text does not keep unknown apart from free:\n%s", text)
	}
	// And the gaps have to admit what they were computed from.
	if !strings.Contains(text, "1 could not be read") {
		t.Fatalf("the free gaps do not say a calendar was missing from them:\n%s", text)
	}
}

// TestNoCalendarReadableReportsNoFreeTime: when nothing could be read,
// printing the whole window as free is the defect §4.6 is about. The
// report refuses to print gaps at all.
func TestNoCalendarReadableReportsNoFreeTime(t *testing.T) {
	fake := caltest.Seed()
	fake.FreeBusyErrors["primary"] = "internalError"
	svc := newService(t, fake)

	o := day("2026-03-16", "2026-03-16")
	o.Calendars = []string{"primary"}
	got, err := svc.Availability(context.Background(), o)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	text := got.Text()
	if strings.Contains(text, "free for the whole window") {
		t.Fatalf("an unreadable calendar was reported free:\n%s", text)
	}
	if !strings.Contains(text, "No free time can be computed") {
		t.Fatalf("the report does not refuse to compute free time:\n%s", text)
	}
}

// TestCalendarMissingFromTheResponseIsUnknown: this is what a truncated
// query looks like, and it must not read as "free".
func TestCalendarMissingFromTheResponseIsUnknown(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)

	// A calendar the fake does not answer for at all: caltest returns an
	// entry per requested id, so ask about one it has no busy list for
	// by removing it from the response.
	fake.FreeBusyOmit = map[string]bool{"team@group.calendar.example.test": true}

	o := day("2026-03-16", "2026-03-16")
	o.Calendars = []string{"primary", "team@group.calendar.example.test"}
	got, err := svc.Availability(context.Background(), o)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	for _, a := range got.Answers {
		if a.CalendarID != "team@group.calendar.example.test" {
			continue
		}
		if !a.Unknown {
			t.Fatal("a calendar Google did not answer for was not reported unknown")
		}
		if !strings.Contains(a.Reason, "no answer") {
			t.Fatalf("the reason does not say Google skipped it: %q", a.Reason)
		}
		return
	}
	t.Fatal("the calendar is missing from the answers entirely")
}

// TestAvailabilityAsksAboutACalendarItCannotRead is the premise of the
// whole tool: free/busy sees busy time on calendars whose events are
// invisible, so an address that is not in the subscribed list must not
// be refused.
func TestAvailabilityAsksAboutACalendarItCannotRead(t *testing.T) {
	fake := caltest.Seed()
	fake.Busy["stranger@example.test"] = []gcal.TimePeriod{
		{Start: "2026-03-16T10:00:00Z", End: "2026-03-16T11:00:00Z"},
	}
	svc := newService(t, fake)

	o := day("2026-03-16", "2026-03-16")
	o.Calendars = []string{"stranger@example.test"}
	got, err := svc.Availability(context.Background(), o)
	if err != nil {
		t.Fatalf("Availability on an unsubscribed address: %v", err)
	}
	if len(got.Answers) != 1 || got.Answers[0].Unknown {
		t.Fatalf("the answer for an unsubscribed calendar is %+v", got.Answers)
	}
	if len(got.Answers[0].Busy) != 1 {
		t.Fatal("the busy block on a calendar this account cannot open was lost")
	}
}

func TestAvailabilityMinMinutes(t *testing.T) {
	fake := caltest.Seed()
	fake.Busy["primary"] = []gcal.TimePeriod{
		{Start: "2026-03-16T08:00:00Z", End: "2026-03-16T10:00:00Z"},
		{Start: "2026-03-16T10:20:00Z", End: "2026-03-16T16:00:00Z"},
	}
	svc := newService(t, fake)

	o := day("2026-03-16T09:00:00+01:00", "2026-03-16T17:00:00+01:00")
	o.Calendars = []string{"primary"}
	o.MinMinutes = 30

	got, err := svc.Availability(context.Background(), o)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	for _, g := range got.Gaps {
		if g.Duration().Minutes() < 30 {
			t.Fatalf("a %v gap survived min_minutes=30", g.Duration())
		}
	}
	// And a caller can tell an empty list from one their filter emptied.
	out := service.NewAvailabilityResult(got)
	if out.MinMinutes != 30 {
		t.Fatalf("the result does not echo min_minutes: %+v", out.MinMinutes)
	}
}

func TestAvailabilityDefaultsToPrimaryAndDedupes(t *testing.T) {
	svc, fake := seeded(t)
	ctx := context.Background()

	if _, err := svc.Availability(ctx, day("2026-03-16", "2026-03-16")); err != nil {
		t.Fatalf("Availability with no calendars: %v", err)
	}

	o := day("2026-03-16", "2026-03-16")
	o.Calendars = []string{"primary", "primary", "Sample Primary"}
	got, err := svc.Availability(ctx, o)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if len(got.Answers) != 1 {
		t.Fatalf("the same calendar was asked about %d times", len(got.Answers))
	}
	if n := countRequests(fake.Served(), "POST /freeBusy"); n != 2 {
		t.Fatalf("spent %d free/busy requests across two calls, want 2", n)
	}
}

func TestAvailabilityRefusesTooManyCalendars(t *testing.T) {
	svc, _ := seeded(t)
	o := day("2026-03-16", "2026-03-16")
	for i := 0; i <= config.MaxFreeBusyCalendars; i++ {
		o.Calendars = append(o.Calendars, fmt.Sprintf("cal%d@example.test", i))
	}
	_, err := svc.Availability(context.Background(), o)
	if err == nil {
		t.Fatalf("%d calendars were accepted, over the ceiling", len(o.Calendars))
	}
	// The refusal has to say which limit this is: it is not the one the
	// fan-out reads use, and a caller told "too many calendars" would go
	// and change the wrong setting.
	if !strings.Contains(err.Error(), "GCAL_MAX_CALENDARS") {
		t.Fatalf("the refusal does not distinguish the two limits: %v", err)
	}
}

// TestAvailabilityBatchesAtFifty is §2.10: the API caps one query at 50
// calendars, so more than that is more than one request — and §4.7 says
// the result reports how many it made.
func TestAvailabilityBatchesAtFifty(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)

	o := day("2026-03-16", "2026-03-16")
	for i := 0; i < 55; i++ {
		o.Calendars = append(o.Calendars, fmt.Sprintf("cal%d@example.test", i))
	}
	got, err := svc.Availability(context.Background(), o)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if got.Requests != 2 {
		t.Fatalf("55 calendars took %d requests; the API caps a query at 50", got.Requests)
	}
	if len(got.Answers) != 55 {
		t.Fatalf("got %d answers for 55 calendars", len(got.Answers))
	}
	if !strings.Contains(got.Text(), "2 API requests") {
		t.Fatalf("the result does not say what it spent:\n%s", got.Text())
	}
}

func TestAvailabilityRequiresAWindow(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.Availability(context.Background(), service.AvailabilityOptions{From: "2026-03-16"})
	if err == nil {
		t.Fatal("a query with no end was accepted")
	}
	if !strings.Contains(err.Error(), "[invalid]") {
		t.Fatalf("the refusal is not classified invalid: %v", err)
	}
}

func countRequests(served []string, prefix string) int {
	n := 0
	for _, r := range served {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

// TestAvailabilityDoesNotResolveEveryAddress is the cost half of §4.7:
// the result says what it spent, so what it spends has to be what the
// result says.
//
// The first version of this path resolved every reference through
// ResolveCalendar. That re-listed the account's calendars once per
// reference — the full list is never cached — and then spent two more
// failing round trips per address that was not in it. 55 colleagues
// cost 168 requests to set up a query the result reported as 2.
func TestAvailabilityDoesNotResolveEveryAddress(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)

	o := day("2026-03-16", "2026-03-16")
	for i := 0; i < 55; i++ {
		o.Calendars = append(o.Calendars, fmt.Sprintf("cal%d@example.test", i))
	}
	got, err := svc.Availability(context.Background(), o)
	if err != nil {
		t.Fatalf("Availability: %v", err)
	}
	if got.Requests != 2 {
		t.Fatalf("the result claims %d requests", got.Requests)
	}

	served := fake.Served()
	// Every HTTP call the whole tool made, not only the ones it counted.
	if len(served) > 6 {
		t.Fatalf("asking about 55 calendars took %d HTTP requests while reporting %d:\n  %s",
			len(served), got.Requests, strings.Join(served, "\n  "))
	}
	if n := countRequests(served, "GET /users/me/calendarList/"); n > 0 {
		t.Fatalf("resolved %d addresses one at a time; free/busy works on calendars this account "+
			"cannot open, so an address is an id (§4.6)", n)
	}
}

// TestCalendarListIsReadOnce: the full list, hidden calendars included,
// is what every resolution wants, and it was the one the cache never
// held.
func TestCalendarListIsReadOnce(t *testing.T) {
	svc, fake := seeded(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		if _, err := svc.Calendars(ctx, true); err != nil {
			t.Fatalf("Calendars: %v", err)
		}
		if _, err := svc.Calendars(ctx, false); err != nil {
			t.Fatalf("Calendars: %v", err)
		}
	}
	if n := countRequests(fake.Served(), "GET /users/me/calendarList"); n != 1 {
		t.Fatalf("ten calls listed the calendars %d times", n)
	}
}

// TestHiddenCalendarsAreFilteredNotRefetched: the visible list is the
// full one minus the hidden entries, so filtering locally must give the
// same answer the API would.
func TestHiddenCalendarsAreFilteredNotRefetched(t *testing.T) {
	fake := caltest.Seed()
	fake.Entries["readonly@group.calendar.example.test"].Hidden = true
	svc := newService(t, fake)
	ctx := context.Background()

	visible, err := svc.Calendars(ctx, false)
	if err != nil {
		t.Fatalf("Calendars: %v", err)
	}
	all, err := svc.Calendars(ctx, true)
	if err != nil {
		t.Fatalf("Calendars: %v", err)
	}
	if len(all) != len(visible)+1 {
		t.Fatalf("%d calendars in all, %d visible; the hidden one is not accounted for", len(all), len(visible))
	}
	for _, c := range visible {
		if c.Hidden {
			t.Fatalf("a hidden calendar came back in the visible list: %s", c.ID)
		}
	}
}
