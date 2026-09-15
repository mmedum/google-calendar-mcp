package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/config"
	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/service"
)

func newService(t *testing.T, s *caltest.Server) *service.Service {
	t.Helper()
	base := s.Start()
	t.Cleanup(s.Close)
	api := gapi.New(nil)
	api.Base = base
	api.MaxRetries = 0
	cfg := config.Config{
		MaxEvents: 250, MaxCalendars: 25, Concurrency: 4, Sharing: true,
	}
	return service.New(api, cfg)
}

func seeded(t *testing.T) (*service.Service, *caltest.Server) {
	t.Helper()
	fake := caltest.Seed()
	return newService(t, fake), fake
}

func TestCalendarsListsAndSorts(t *testing.T) {
	svc, _ := seeded(t)
	cals, err := svc.Calendars(context.Background(), false)
	if err != nil {
		t.Fatalf("Calendars: %v", err)
	}
	if len(cals) != 3 {
		t.Fatalf("got %d calendars, want 3", len(cals))
	}
	// The primary calendar sorts first, because it is the one a caller
	// means by default.
	if !cals[0].Primary {
		t.Fatalf("first calendar is %q, not the primary one", cals[0].Title)
	}
	if cals[0].TimeZone == "" {
		t.Fatal("the list must carry each calendar's zone; §4.1 resolves against it")
	}
}

func TestResolveCalendar(t *testing.T) {
	svc, _ := seeded(t)
	ctx := context.Background()

	for _, c := range []struct{ name, ref, wantID string }{
		{"primary alias", "primary", "primary"},
		{"empty means primary", "", "primary"},
		{"by id", "team@group.calendar.example.test", "team@group.calendar.example.test"},
		{"by exact title", "Sample Team", "team@group.calendar.example.test"},
		{"by partial title", "Readonly", "readonly@group.calendar.example.test"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, err := svc.ResolveCalendar(ctx, c.ref)
			if err != nil {
				t.Fatalf("ResolveCalendar(%q): %v", c.ref, err)
			}
			if got.ID != c.wantID {
				t.Fatalf("ResolveCalendar(%q) = %q, want %q", c.ref, got.ID, c.wantID)
			}
		})
	}
}

// TestResolveCalendarRefusesAmbiguity is §6.1: never take the first
// match. Two calendars sharing a word is the ordinary case, and a server
// that guesses will read the wrong person's schedule.
func TestResolveCalendarRefusesAmbiguity(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)

	_, err := svc.ResolveCalendar(context.Background(), "Sample")
	if err == nil {
		t.Fatal("a title matching three calendars resolved to one of them")
	}
	cls, ok := gapi.ClassOf(err)
	if !ok || cls != gapi.ClassAmbiguous {
		t.Fatalf("class = %q, want ambiguous", cls)
	}
	// The refusal must carry the ids, or the caller cannot act on it.
	for _, id := range []string{"primary", "team@group.calendar.example.test"} {
		if !strings.Contains(err.Error(), id) {
			t.Fatalf("the ambiguity refusal does not name %q: %v", id, err)
		}
	}
}

func TestResolveCalendarNotFound(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.ResolveCalendar(context.Background(), "No Such Calendar")
	cls, _ := gapi.ClassOf(err)
	if cls != gapi.ClassNotFound {
		t.Fatalf("class = %q, want not_found (%v)", cls, err)
	}
	// It must say that an unsubscribed calendar is still reachable by id,
	// because "not found" for a calendar the caller can read is a lie.
	if !strings.Contains(err.Error(), "id") {
		t.Fatalf("the refusal does not mention reaching a calendar by id: %v", err)
	}
}

func TestListEventsWindowAndZone(t *testing.T) {
	svc, _ := seeded(t)
	sched, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-03-16", To: "2026-03-16",
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	// A bare date on both ends is that whole day, not an empty window.
	if sched.Window.Duration() <= 0 {
		t.Fatalf("window is empty: %s", sched.Window)
	}
	if sched.Zone.Name() != "Europe/Copenhagen" {
		t.Fatalf("zone = %q, want the calendar's own zone", sched.Zone.Name())
	}
	if sched.Zone.Explain() == "" {
		t.Fatal("every read must be able to say which zone it used and why")
	}
	if len(sched.Events) == 0 {
		t.Fatal("no events on a day the fixture puts two on")
	}
}

// TestListEventsRequiresBothBounds: the server does not guess a window,
// and says so in a way that tells the caller what to do instead.
func TestListEventsRequiresBothBounds(t *testing.T) {
	svc, _ := seeded(t)
	for _, c := range []service.ListOptions{
		{From: "", To: "2026-03-16"},
		{From: "2026-03-16", To: ""},
		{},
	} {
		_, err := svc.ListEvents(context.Background(), c)
		if err == nil {
			t.Fatalf("ListEvents(%+v) succeeded without a full window", c)
		}
		if cls, _ := gapi.ClassOf(err); cls != gapi.ClassInvalid {
			t.Fatalf("class = %q, want invalid", cls)
		}
	}
}

func TestListEventsRejectsABackwardsWindow(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-03-20T10:00:00+01:00", To: "2026-03-20T09:00:00+01:00",
	})
	if err == nil {
		t.Fatal("a window that ends before it starts was accepted")
	}
}

// TestCancelledEventsAreHiddenByDefault is §2.13.
func TestCancelledEventsAreHiddenByDefault(t *testing.T) {
	svc, _ := seeded(t)
	ctx := context.Background()
	opts := service.ListOptions{From: "2026-03-18", To: "2026-03-19"}

	hidden, err := svc.ListEvents(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range hidden.Events {
		if e.Cancelled() {
			t.Fatalf("a cancelled event appeared without show_cancelled: %s", e.Title)
		}
	}

	opts.ShowCancelled = true
	shown, err := svc.ListEvents(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range shown.Events {
		if e.Cancelled() {
			found = true
		}
	}
	if !found {
		t.Fatal("show_cancelled did not reveal the cancelled event")
	}
}

// TestExpandChoosesBetweenSeriesAndInstances is §2.9: the two return
// genuinely different things, and the caller picks.
func TestExpandChoosesBetweenSeriesAndInstances(t *testing.T) {
	svc, _ := seeded(t)
	ctx := context.Background()
	window := service.ListOptions{From: "2026-03-16", To: "2026-03-31"}

	window.Expand = false
	series, err := svc.ListEvents(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	sawSeries := false
	for _, e := range series.Events {
		if e.IsSeries() {
			sawSeries = true
		}
		if e.IsInstance() {
			t.Fatalf("an instance appeared with expand off: %s", e.ID)
		}
	}
	if !sawSeries {
		t.Fatal("no series parent with expand off")
	}
	if series.Expanded {
		t.Fatal("the result claims it expanded when it did not")
	}

	window.Expand = true
	instances, err := svc.ListEvents(ctx, window)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range instances.Events {
		if e.IsSeries() {
			t.Fatalf("a series parent appeared with expand on: %s", e.ID)
		}
	}
	if !instances.Expanded {
		t.Fatal("the result does not say it expanded")
	}
}

// TestAllDayEventStaysADate is the defect from §3, end to end.
func TestAllDayEventStaysADate(t *testing.T) {
	svc, _ := seeded(t)
	// Read it from a zone far west of the calendar's own. A server that
	// turned the date into an instant would move it a day here.
	sched, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-03-20", To: "2026-03-21", TimeZone: "America/Chicago",
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found bool
	for _, e := range sched.Events {
		if e.ID != "ev-holiday" {
			continue
		}
		found = true
		if !e.Start.AllDay {
			t.Fatal("the all-day event lost its all-day flag")
		}
		if got := e.Start.Date.String(); got != "2026-03-20" {
			t.Fatalf("all-day event moved to %s when read from America/Chicago; want 2026-03-20", got)
		}
		if !e.Start.At.IsZero() {
			t.Fatal("the all-day event acquired an instant; §4.1 forbids the conversion")
		}
	}
	if !found {
		t.Fatal("the all-day event was not returned")
	}
}

func TestSearchEventsFindsByText(t *testing.T) {
	svc, _ := seeded(t)
	sched, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-03-16", To: "2026-03-31", Query: "sync", Expand: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sched.Events) != 1 || !strings.Contains(sched.Events[0].Title, "sync") {
		t.Fatalf("search returned %d events: %+v", len(sched.Events), sched.Events)
	}
}

func TestFanOutCountsItsRequests(t *testing.T) {
	svc, _ := seeded(t)
	sched, err := svc.ListEvents(context.Background(), service.ListOptions{
		Calendars: []string{"primary", "Sample Team"},
		From:      "2026-03-16", To: "2026-03-17",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sched.Requests < 2 {
		t.Fatalf("reading two calendars reported %d requests", sched.Requests)
	}
	if len(sched.Calendars) != 2 {
		t.Fatalf("the result names %d calendars, want 2", len(sched.Calendars))
	}
}

func TestFanOutIsBounded(t *testing.T) {
	fake := caltest.Seed()
	base := fake.Start()
	t.Cleanup(fake.Close)
	api := gapi.New(nil)
	api.Base = base
	svc := service.New(api, config.Config{MaxEvents: 250, MaxCalendars: 2, Concurrency: 2})

	_, err := svc.ListEvents(context.Background(), service.ListOptions{
		Calendars: []string{"a", "b", "c"}, From: "2026-03-16", To: "2026-03-17",
	})
	if err == nil {
		t.Fatal("the fan-out limit was not enforced")
	}
	if !strings.Contains(err.Error(), "at most 2") {
		t.Fatalf("the refusal does not name the limit: %v", err)
	}
}

func TestBudgetTruncatesAndSaysSo(t *testing.T) {
	fake := caltest.Seed()
	base := fake.Start()
	t.Cleanup(fake.Close)
	api := gapi.New(nil)
	api.Base = base
	svc := service.New(api, config.Config{MaxEvents: 1, MaxCalendars: 25, Concurrency: 2})

	sched, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-03-16", To: "2026-03-31", Expand: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sched.Truncated {
		t.Fatal("a read past its budget did not report truncation")
	}
	if len(sched.Events) != 1 {
		t.Fatalf("a budget of 1 returned %d events", len(sched.Events))
	}
	// A read that stops at its budget with more to come has to say so
	// and hand back a way to continue. It used to report itself
	// complete, because truncation was decided from the overflow alone.
	if sched.NextPageToken == "" {
		t.Fatal("a truncated read gave no page token to continue from")
	}
	if !strings.Contains(sched.Text(), "budget") {
		t.Fatalf("the rendered text does not mention truncation:\n%s", sched.Text())
	}
	if !strings.Contains(sched.Text(), "there are more") {
		t.Fatalf("the text does not say the list is incomplete:\n%s", sched.Text())
	}
}

func TestGetEvent(t *testing.T) {
	svc, _ := seeded(t)
	e, z, err := svc.GetEvent(context.Background(), "primary", "ev-standup", "")
	if err != nil {
		t.Fatalf("GetEvent: %v", err)
	}
	if e.Title != "Morning sync" {
		t.Fatalf("title = %q", e.Title)
	}
	if z.Name() == "" {
		t.Fatal("GetEvent returned no zone")
	}
}

func TestGetEventNotFound(t *testing.T) {
	svc, _ := seeded(t)
	_, _, err := svc.GetEvent(context.Background(), "primary", "no-such-event", "")
	if cls, _ := gapi.ClassOf(err); cls != gapi.ClassNotFound {
		t.Fatalf("class = %q, want not_found", cls)
	}
}

// TestCalendarDetailReportsAMissingACLScope is §2.15: an empty sharing
// list and an unreadable one are different answers, and conflating them
// tells the caller the calendar is private when it may not be.
func TestCalendarDetailReportsAMissingACLScope(t *testing.T) {
	fake := caltest.Seed()
	fake.ACLScopeRequired = true
	svc := newService(t, fake)

	res, err := svc.CalendarDetail(context.Background(), "primary")
	if err != nil {
		t.Fatalf("CalendarDetail must not fail when only the ACL scope is missing: %v", err)
	}
	if len(res.Sharing) != 0 {
		t.Fatal("sharing was reported despite the scope being refused")
	}
	if res.Note == "" {
		t.Fatal("a refused sharing read produced no note; an empty list would read as `shared with nobody`")
	}
	if !strings.Contains(res.Note, "acls.readonly") {
		t.Fatalf("the note does not name the missing scope: %q", res.Note)
	}
	if !strings.Contains(res.Rendered(), "Could not read") {
		t.Fatalf("the rendered text hides the problem:\n%s", res.Rendered())
	}
}

func TestCalendarDetailListsSharing(t *testing.T) {
	svc, _ := seeded(t)
	res, err := svc.CalendarDetail(context.Background(), "primary")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sharing) != 2 {
		t.Fatalf("got %d sharing rules, want 2", len(res.Sharing))
	}
	// A role must be explained, not echoed.
	for _, s := range res.Sharing {
		if s.RoleMeans == "" {
			t.Fatalf("role %q has no explanation", s.Role)
		}
	}
}

func TestSettingsSummary(t *testing.T) {
	svc, _ := seeded(t)
	res, err := svc.SettingsSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.TimeZone != "Europe/Copenhagen" {
		t.Fatalf("time zone = %q", res.TimeZone)
	}
	if !strings.Contains(res.Render(), "Europe/Copenhagen") {
		t.Fatalf("the rendering omits the zone:\n%s", res.Render())
	}
}

// TestZoneFallsBackToTheAccountSettings is §4.1's third source.
func TestZoneFallsBackToTheAccountSettings(t *testing.T) {
	fake := caltest.Seed()
	// A calendar with no zone of its own.
	fake.Entries["primary"].TimeZone = ""
	fake.Calendars["primary"].TimeZone = ""
	svc := newService(t, fake)

	sched, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-03-16", To: "2026-03-17",
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if sched.Zone.Name() != "Europe/Copenhagen" {
		t.Fatalf("zone = %q, want the account setting", sched.Zone.Name())
	}
	if !strings.Contains(sched.Zone.Explain(), "settings") {
		t.Fatalf("the result does not say the zone came from settings: %q", sched.Zone.Explain())
	}
}

// TestNoZoneAnywhereRefuses: §4.1 refuses rather than picking UTC or the
// machine's own zone.
func TestNoZoneAnywhereRefuses(t *testing.T) {
	fake := caltest.Seed()
	fake.Entries["primary"].TimeZone = ""
	fake.Calendars["primary"].TimeZone = ""
	fake.Settings = nil
	svc := newService(t, fake)

	_, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-03-16", To: "2026-03-17",
	})
	if err == nil {
		t.Fatal("with no zone anywhere the read succeeded; it must refuse rather than assume one")
	}
	if !strings.Contains(err.Error(), "time zone") {
		t.Fatalf("the refusal does not explain: %v", err)
	}
}

// TestUnauthenticatedAnswersEveryCall: a client lists tools and may call
// one before anybody logs in. Each must come back classified.
func TestUnauthenticatedAnswersEveryCall(t *testing.T) {
	svc := service.Unauthenticated(config.Config{MaxEvents: 10, MaxCalendars: 5, Concurrency: 1},
		gapi.Errf(gapi.ClassAuth, "no refresh token"))
	ctx := context.Background()

	checks := map[string]error{}
	_, checks["Calendars"] = svc.Calendars(ctx, false)
	_, checks["ListEvents"] = svc.ListEvents(ctx, service.ListOptions{From: "2026-03-16", To: "2026-03-17"})
	_, checks["CalendarDetail"] = svc.CalendarDetail(ctx, "primary")
	_, checks["SettingsSummary"] = svc.SettingsSummary(ctx)
	_, _, checks["GetEvent"] = svc.GetEvent(ctx, "primary", "x", "")

	for name, err := range checks {
		if err == nil {
			t.Fatalf("%s succeeded with no credentials", name)
		}
		cls, ok := gapi.ClassOf(err)
		if !ok || cls != gapi.ClassAuth {
			t.Fatalf("%s returned class %q, want auth", name, cls)
		}
		if !strings.Contains(err.Error(), "login") {
			t.Fatalf("%s does not say what to do: %v", name, err)
		}
	}
}

func TestPagingFollowsTheToken(t *testing.T) {
	fake := caltest.Seed()
	fake.PageSize = 1
	svc := newService(t, fake)

	cals, err := svc.Calendars(context.Background(), false)
	if err != nil {
		t.Fatalf("Calendars: %v", err)
	}
	// Three calendars at one per page: a client that stopped at the
	// first page would resolve titles against a third of the list.
	if len(cals) != 3 {
		t.Fatalf("got %d calendars with a page size of 1; paging did not follow the token", len(cals))
	}
}

func TestCalendarDetailWithSharingOff(t *testing.T) {
	fake := caltest.Seed()
	base := fake.Start()
	t.Cleanup(fake.Close)
	api := gapi.New(nil)
	api.Base = base
	svc := service.New(api, config.Config{
		MaxEvents: 250, MaxCalendars: 25, Concurrency: 2, Sharing: false,
	})

	res, err := svc.CalendarDetail(context.Background(), "primary")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sharing) != 0 {
		t.Fatal("sharing was read with GCAL_SHARING=off")
	}
	if !strings.Contains(res.Note, "off") {
		t.Fatalf("the note does not explain why sharing is absent: %q", res.Note)
	}
}

func TestResolveCalendarFallsBackToAnUnsubscribedId(t *testing.T) {
	fake := caltest.Seed()
	// Known to the API, absent from this user's subscribed list.
	fake.Calendars["other@group.calendar.example.test"] = &gcal.Calendar{
		ID: "other@group.calendar.example.test", Summary: "Other", TimeZone: "UTC",
	}
	svc := newService(t, fake)

	got, err := svc.ResolveCalendar(context.Background(), "other@group.calendar.example.test")
	if err != nil {
		t.Fatalf("a calendar reachable by id but not subscribed was refused: %v", err)
	}
	if got.ID != "other@group.calendar.example.test" {
		t.Fatalf("resolved to %q", got.ID)
	}
}

func TestWindowAcceptsTimestampsAndDates(t *testing.T) {
	svc, _ := seeded(t)
	ctx := context.Background()
	for _, c := range []struct{ name, from, to string }{
		{"dates", "2026-03-16", "2026-03-17"},
		{"timestamps", "2026-03-16T08:00:00+01:00", "2026-03-16T18:00:00+01:00"},
		{"mixed", "2026-03-16", "2026-03-16T18:00:00+01:00"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := svc.ListEvents(ctx, service.ListOptions{From: c.from, To: c.to}); err != nil {
				t.Fatalf("ListEvents(%s, %s): %v", c.from, c.to, err)
			}
		})
	}
	if _, err := svc.ListEvents(ctx, service.ListOptions{From: "tomorrow", To: "2026-03-17"}); err == nil {
		t.Fatal("a relative expression was accepted; the server does not guess")
	}
}

func TestRejectsAnUnknownZoneOnTheCall(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-03-16", To: "2026-03-17", TimeZone: "Mars/Olympus_Mons",
	})
	if err == nil {
		t.Fatal("an invented zone named on the call was accepted")
	}
}

func TestSettingsAreReadOnce(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := svc.Settings(ctx); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, r := range fake.Served() {
		if strings.Contains(r, "/users/me/settings") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("settings were read %d times; every read needs the zone, so they are cached", n)
	}
}

// TestHiddenCalendarStillResolvesByTitle.
//
// Resolution looks at every calendar, hidden ones included: a calendar
// hidden in the Calendar UI is still one this account can read, and
// "not found" for it would be a lie (§6.1). The list is now read once
// and filtered, so this is the assertion that the filter is applied
// where it should be and not to the resolution.
func TestHiddenCalendarStillResolvesByTitle(t *testing.T) {
	fake := caltest.Seed()
	fake.Entries["readonly@group.calendar.example.test"].Hidden = true
	svc := newService(t, fake)
	ctx := context.Background()

	got, err := svc.ResolveCalendar(ctx, "Sample Readonly")
	if err != nil {
		t.Fatalf("a hidden calendar must still resolve by title: %v", err)
	}
	if got.ID != "readonly@group.calendar.example.test" {
		t.Fatalf("resolved to %q", got.ID)
	}

	visible, err := svc.Calendars(ctx, false)
	if err != nil {
		t.Fatalf("Calendars: %v", err)
	}
	for _, c := range visible {
		if c.ID == got.ID {
			t.Fatal("the hidden calendar appeared in the visible list")
		}
	}
}

// TestCancelledInstancesAreHiddenInSeriesMode is the live run's second
// defect, as the reproduction that found it.
//
// The server passed showDeleted=false and trusted Google to filter.
// The discovery document says otherwise: "Cancelled instances of
// recurring events (but not the underlying recurring event) will still
// be included if showDeleted and singleEvents are both False." One came
// back with no start and no summary, so a series listing rendered a row
// with no date and no title, and counted it among the results.
func TestCancelledInstancesAreHiddenInSeriesMode(t *testing.T) {
	fake := caltest.Seed()
	// The shape Google actually sends, which is the half a populated
	// fixture cannot show: an id, a status, the series it belongs to and
	// the date it was — no start, no end, no summary. This is what
	// rendered as a row with no date and no title. Built here rather
	// than in Seed() because an event with no start falls outside no
	// window, so it would land in every other test's counts.
	fake.AddEvent("primary", &gcal.Event{
		ID:                "ev-weekly_20260414T120000Z",
		Status:            gcal.StatusCancelled,
		RecurringEventID:  "ev-weekly",
		OriginalStartTime: &gcal.EventDateTime{DateTime: "2026-04-14T14:00:00+02:00"},
	})
	svc := newService(t, fake)
	ctx := context.Background()
	opts := service.ListOptions{From: "2026-03-15", To: "2026-04-30"}

	got, err := svc.ListEvents(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got.Events {
		if e.Cancelled() {
			t.Fatalf("a cancelled instance survived a series read without show_cancelled: %q", e.ID)
		}
	}
	if got.Matched != len(got.Events) {
		t.Fatalf("matched %d but returned %d: a filtered event was still counted",
			got.Matched, len(got.Events))
	}

	// And it is still reachable when asked for, from the same read.
	opts.ShowCancelled = true
	shown, err := svc.ListEvents(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	found, bare := false, false
	for _, e := range shown.Events {
		if e.Cancelled() && e.SeriesID != "" {
			found = true
		}
		if e.ID == "ev-weekly_20260414T120000Z" {
			bare = true
		}
	}
	if !found {
		t.Fatal("show_cancelled did not reveal the cancelled occurrence")
	}
	// The bare row survives conversion and rendering when it is asked
	// for. A start-less event must not fail the whole read.
	if !bare {
		t.Fatal("the bare cancelled instance did not survive show_cancelled")
	}
	// Rendering it must not panic on the missing start either.
	if txt := shown.Text(); txt == "" {
		t.Fatal("the schedule rendered empty")
	}
}

// TestFilteringCancelledDoesNotBuyExtraPages.
//
// The budget is a bound on what the read SPENDS, not on what survives a
// filter. Counting kept events made every filtered row leave the budget
// one short, so the loop fetched another page: a window whose first
// twenty rows are cancelled cost twenty-one requests to return one
// event. That is §4.7 failing in the direction the result cannot show,
// and it is the second time in this phase — check_availability spent 168
// requests to report 2.
func TestFilteringCancelledDoesNotBuyExtraPages(t *testing.T) {
	fake := caltest.Seed()
	tz := "Europe/Copenhagen"
	// Twenty cancelled occurrences, all sorting before anything the
	// caller wants. Google returns these in a series read whatever
	// showDeleted says, so the server is the one that drops them.
	for i := range 20 {
		day := fmt.Sprintf("2026-05-%02d", i+1)
		inst := caltest.Instance(
			fmt.Sprintf("ev-noise_2026050%02dT080000Z", i+1), "ev-noise", "Noise",
			day+"T10:00:00+02:00", day+"T11:00:00+02:00", tz, day+"T10:00:00+02:00")
		inst.Status = gcal.StatusCancelled
		fake.AddEvent("primary", inst)
	}
	fake.AddEvent("primary", caltest.Timed("ev-real", "The one event",
		"2026-05-25T10:00:00+02:00", "2026-05-25T11:00:00+02:00", tz))

	svc := newService(t, fake)
	got, err := svc.ListEvents(context.Background(), service.ListOptions{
		From: "2026-05-01", To: "2026-05-31", MaxEvents: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Requests > 2 {
		t.Fatalf("spent %d requests to read one event past twenty cancelled rows; "+
			"the budget bounds what is drained, not what survives (§4.7)", got.Requests)
	}
}
