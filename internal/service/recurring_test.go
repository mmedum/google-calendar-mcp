package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/service"
)

func TestInstancesReturnsTheOccurrences(t *testing.T) {
	svc, _ := seeded(t)
	got, err := svc.Instances(context.Background(), service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly",
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(got.Events) != 2 {
		t.Fatalf("got %d occurrences, want the 2 that are not cancelled", len(got.Events))
	}
	if got.SeriesID != "ev-weekly" {
		t.Fatalf("series id = %q", got.SeriesID)
	}
	if got.Requests != 1 {
		t.Fatalf("spent %d requests; one series is one request (§4.7)", got.Requests)
	}
	if got.Window != nil {
		t.Fatal("no window was asked for, so none should be reported")
	}
	if got.Zone.Name() == "" {
		t.Fatal("the result does not name the zone it rendered in (§4.5)")
	}
}

// TestInstancesHidesCancelledUntilAsked. A cancelled occurrence is how a
// single date is removed from a series, so its absence is a fact the
// result has to admit to rather than a detail (§2.13).
func TestInstancesHidesCancelledUntilAsked(t *testing.T) {
	svc, _ := seeded(t)
	ctx := context.Background()

	hidden, err := svc.Instances(ctx, service.InstanceOptions{Calendar: "primary", EventID: "ev-weekly"})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if !strings.Contains(hidden.Text(), "Cancelled occurrences are hidden") {
		t.Fatalf("the result does not say it is hiding cancelled occurrences:\n%s", hidden.Text())
	}

	shown, err := svc.Instances(ctx, service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly", ShowCancelled: true,
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(shown.Events) != len(hidden.Events)+1 {
		t.Fatalf("show_cancelled returned %d occurrences, hidden returned %d",
			len(shown.Events), len(hidden.Events))
	}
	if !strings.Contains(shown.Text(), "CANCELLED") {
		t.Fatalf("a cancelled occurrence is not marked:\n%s", shown.Text())
	}
}

// TestInstancesMarksAMovedOccurrence: originalStartTime is the stable
// identity of an instance (§6.2), and the difference between it and the
// start is somebody's exception.
func TestInstancesMarksAMovedOccurrence(t *testing.T) {
	svc, _ := seeded(t)
	got, err := svc.Instances(context.Background(), service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly",
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	out := service.NewInstancesResult(got)
	moved := 0
	for _, occ := range out.Instances {
		if occ.Moved {
			moved++
			if occ.OriginalStart == "" {
				t.Fatal("a moved occurrence does not report where it was")
			}
		}
	}
	if moved != 1 {
		t.Fatalf("%d occurrences reported as moved, want 1", moved)
	}
	if !strings.Contains(got.Text(), "moved from") {
		t.Fatalf("the text does not say an occurrence moved:\n%s", got.Text())
	}
}

func TestInstancesWindow(t *testing.T) {
	svc, _ := seeded(t)
	got, err := svc.Instances(context.Background(), service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly",
		From: "2026-03-20", To: "2026-03-26",
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(got.Events) != 1 {
		t.Fatalf("got %d occurrences in the window, want 1", len(got.Events))
	}
	if got.Window == nil {
		t.Fatal("the result does not state the window it used (§4.5)")
	}
}

func TestInstancesRefusesAHalfWindow(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.Instances(context.Background(), service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly", From: "2026-03-20",
	})
	if err == nil {
		t.Fatal("a half window was accepted")
	}
	if !strings.Contains(err.Error(), "[invalid]") {
		t.Fatalf("the refusal is not classified invalid: %v", err)
	}
}

func TestInstancesRefusesWithoutAnEventID(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.Instances(context.Background(), service.InstanceOptions{Calendar: "primary"})
	if err == nil {
		t.Fatal("a call with no event id was accepted")
	}
	if !strings.Contains(err.Error(), "series_id") {
		t.Fatalf("the refusal does not say where the id comes from: %v", err)
	}
}

// TestInstancesRefusesAnOccurrenceID is the error path a model will
// actually hit: it has an occurrence's id from list_events and passes
// that.
//
// The live run settled what Google does with one, and it is not a
// refusal — it answers 200 and expands the occurrence the id names. A
// CANCELLED occurrence expands to nothing, so the call succeeded with
// an empty list and the tool reported "No occurrences" for a series
// that has three. The server reads the id's shape now, so all three of
// these are refused before a request is built.
func TestInstancesRefusesAnOccurrenceID(t *testing.T) {
	for _, tc := range []struct {
		name string
		id   string
	}{
		{"a live occurrence, which Google expands", "ev-weekly_20260324T130000Z"},
		{"a cancelled one, which expands to nothing", "ev-weekly_20260407T120000Z"},
		{"lowercased, the same mistake", "ev-weekly_20260407t120000z"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := seeded(t)
			_, err := svc.Instances(context.Background(), service.InstanceOptions{
				Calendar: "primary", EventID: tc.id,
			})
			if err == nil {
				t.Fatal("an occurrence id was accepted as a series id")
			}
			// [invalid] rather than [not_found] is what distinguishes
			// the shape rule from Google merely failing to find the id.
			if !strings.Contains(err.Error(), "[invalid]") {
				t.Fatalf("the id shape was not recognised: %v", err)
			}
			for _, want := range []string{"series_id", "ev-weekly"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

func TestInstancesOfSomethingThatDoesNotRepeat(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.Instances(context.Background(), service.InstanceOptions{
		Calendar: "primary", EventID: "ev-standup",
	})
	if err == nil {
		t.Fatal("a non-recurring event was expanded")
	}
}

func TestInstancesPagesToItsBudget(t *testing.T) {
	fake := caltest.Seed()
	fake.PageSize = 1
	svc := newService(t, fake)

	got, err := svc.Instances(context.Background(), service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly", ShowCancelled: true,
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(got.Events) != 3 {
		t.Fatalf("got %d occurrences across pages, want 3", len(got.Events))
	}
	if got.Requests < 3 {
		t.Fatalf("paged with %d requests; three one-event pages need at least three", got.Requests)
	}
}

func TestInstancesStopsAtTheBudgetAndSaysSo(t *testing.T) {
	svc, _ := seeded(t)
	got, err := svc.Instances(context.Background(), service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly", MaxEvents: 1, ShowCancelled: true,
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(got.Events) != 1 || !got.Truncated {
		t.Fatalf("got %d occurrences, truncated=%v", len(got.Events), got.Truncated)
	}
	if !strings.Contains(got.Text(), "budget") {
		t.Fatalf("a truncated read does not say so:\n%s", got.Text())
	}
}

// TestInstancesPagingDoesNotSkipOccurrences.
//
// A page is the unit Google's next-page token points past. Reading a
// 250-event page, showing ten and handing back that page's token loses
// the 240 in between — while the result says it is resumable, which is
// the worst combination: a caller who follows the token believes they
// have seen everything.
func TestInstancesPagingDoesNotSkipOccurrences(t *testing.T) {
	fake := caltest.Seed()
	// Pages of three, and a budget of one: the case where the two
	// disagree. A server that asks for a whole page and keeps one
	// occurrence gets back a token pointing past all three.
	fake.PageSize = 3
	svc := newService(t, fake)
	ctx := context.Background()

	first, err := svc.Instances(ctx, service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly", MaxEvents: 1, ShowCancelled: true,
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(first.Events) != 1 || !first.Truncated || first.NextPageToken == "" {
		t.Fatalf("got %d occurrences, truncated=%v, token=%q",
			len(first.Events), first.Truncated, first.NextPageToken)
	}

	// Following the token must produce the occurrence that comes next,
	// not one further on.
	seen := map[string]bool{first.Events[0].ID: true}
	token := first.NextPageToken
	for i := 0; i < 5 && token != ""; i++ {
		next, err := svc.Instances(ctx, service.InstanceOptions{
			Calendar: "primary", EventID: "ev-weekly", MaxEvents: 1,
			ShowCancelled: true, PageToken: token,
		})
		if err != nil {
			t.Fatalf("Instances(page %d): %v", i, err)
		}
		for _, e := range next.Events {
			seen[e.ID] = true
		}
		token = next.NextPageToken
	}

	all, err := svc.Instances(ctx, service.InstanceOptions{
		Calendar: "primary", EventID: "ev-weekly", ShowCancelled: true,
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	for _, e := range all.Events {
		if !seen[e.ID] {
			t.Fatalf("paging one occurrence at a time never produced %s; %d of %d seen",
				e.ID, len(seen), len(all.Events))
		}
	}
}

// Google does not return occurrences in date order: the live run got a
// cancelled 24 March after 7 April, which reads badly for the question
// this tool answers — which dates does this series have.
//
// The order is applied AFTER the budget cut, never before. Sorting first
// would keep a different set than the page token accounts for, and the
// dropped ones would be reachable from nowhere; that is the defect phase
// 1 fixed in the schedule read, one tool over.
func TestInstancesComeBackInDateOrder(t *testing.T) {
	const tz = "Europe/Copenhagen"
	fake := caltest.New()
	fake.AddCalendar("primary", "Sample Primary", tz, gcal.RoleOwner, true)
	fake.AddEvent("primary", caltest.Recurring("evorder0001", "Weekly",
		"2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00", tz,
		"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"))
	// Added last, dated first: the fake stores them in a map, so this
	// asserts the server's order rather than the fixture's.
	for _, c := range []struct{ id, start, end string }{
		{"evorder0001_20260407T120000Z", "2026-04-07T14:00:00+02:00", "2026-04-07T15:00:00+02:00"},
		{"evorder0001_20260331T120000Z", "2026-03-31T14:00:00+02:00", "2026-03-31T15:00:00+02:00"},
		{"evorder0001_20260324T130000Z", "2026-03-24T14:00:00+01:00", "2026-03-24T15:00:00+01:00"},
	} {
		fake.AddEvent("primary", caltest.Instance(c.id, "evorder0001", "Weekly",
			c.start, c.end, tz, c.start))
	}
	svc := newService(t, fake)

	out, err := svc.Instances(context.Background(), service.InstanceOptions{
		Calendar: "primary", EventID: "evorder0001", ShowCancelled: true,
	})
	if err != nil {
		t.Fatalf("Instances: %v", err)
	}
	if len(out.Events) < 3 {
		t.Fatalf("got %d occurrences, want at least 3", len(out.Events))
	}
	for i := 1; i < len(out.Events); i++ {
		prev, cur := out.Events[i-1].Start.At, out.Events[i].Start.At
		if cur.T.Before(prev.T) {
			t.Fatalf("occurrence %d (%s) comes before %d (%s)", i, cur, i-1, prev)
		}
	}
}
