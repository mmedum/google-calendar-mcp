package gapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/v2/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v2/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v2/internal/gcal"
)

func client(base string) *gapi.Client {
	c := gapi.New(nil)
	c.Base = base
	c.MaxRetries = 0
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestReadsAgainstTheFake(t *testing.T) {
	fake := caltest.Seed()
	c := client(fake.Start())
	defer fake.Close()
	ctx := context.Background()

	if got, err := c.ListCalendars(ctx, "", false); err != nil || len(got.Items) != 3 {
		t.Fatalf("ListCalendars = %v, %v", got, err)
	}
	if got, err := c.GetCalendar(ctx, "primary"); err != nil || got.ID != "primary" {
		t.Fatalf("GetCalendar = %v, %v", got, err)
	}
	if got, err := c.GetCalendarListEntry(ctx, "primary"); err != nil || !got.Primary {
		t.Fatalf("GetCalendarListEntry = %v, %v", got, err)
	}
	if got, err := c.GetEvent(ctx, "primary", "ev-standup"); err != nil || got.Summary != "Morning sync" {
		t.Fatalf("GetEvent = %v, %v", got, err)
	}
	if got, err := c.ListEvents(ctx, "primary", gapi.EventsListOptions{}); err != nil || len(got.Items) == 0 {
		t.Fatalf("ListEvents = %v, %v", got, err)
	}
	if got, err := c.ListInstances(ctx, "primary", "ev-weekly", gapi.EventsListOptions{}); err != nil || len(got.Items) != 2 {
		t.Fatalf("ListInstances = %v, %v", got, err)
	}
	if got, err := c.ListACL(ctx, "primary", ""); err != nil || len(got.Items) != 2 {
		t.Fatalf("ListACL = %v, %v", got, err)
	}
	if got, err := c.ListSettings(ctx); err != nil || len(got.Items) == 0 {
		t.Fatalf("ListSettings = %v, %v", got, err)
	}
	if got, err := c.GetColors(ctx); err != nil || len(got.Event) == 0 {
		t.Fatalf("GetColors = %v, %v", got, err)
	}
}

// TestCalendarIdWithAnAtSignSurvivesEscaping: a primary calendar's id is
// an email address, so a path segment that mangles @ reaches the wrong
// calendar or none.
func TestCalendarIdWithAnAtSignSurvivesEscaping(t *testing.T) {
	fake := caltest.Seed()
	c := client(fake.Start())
	defer fake.Close()

	got, err := c.GetCalendar(context.Background(), "team@group.calendar.example.test")
	if err != nil {
		t.Fatalf("GetCalendar with an @ in the id: %v", err)
	}
	if got.ID != "team@group.calendar.example.test" {
		t.Fatalf("got %q", got.ID)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name   string
		status int
		reason string
		want   gapi.Class
	}{
		{"bad request", 400, "", gapi.ClassInvalid},
		{"unauthorized", 401, "", gapi.ClassAuth},
		{"forbidden by role", 403, "forbidden", gapi.ClassForbidden},
		{"missing scope is auth, not forbidden", 403, "insufficientPermissions", gapi.ClassAuth},
		{"rate limited with a 403", 403, "rateLimitExceeded", gapi.ClassRateLimited},
		{"user rate limit", 403, "userRateLimitExceeded", gapi.ClassRateLimited},
		{"not found", 404, "", gapi.ClassNotFound},
		{"conflict", 409, "duplicate", gapi.ClassConflict},
		{"gone is stale", 410, "", gapi.ClassStale},
		{"precondition failed is stale", 412, "", gapi.ClassStale},
		{"too many requests", 429, "", gapi.ClassRateLimited},
		{"server error", 500, "", gapi.ClassUnavailable},
		{"bad gateway", 502, "", gapi.ClassUnavailable},
		{"unavailable", 503, "", gapi.ClassUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`{"error":{"code":` + itoa(c.status) +
					`,"message":"nope","errors":[{"reason":"` + c.reason + `"}]}}`))
			}))
			defer srv.Close()

			_, err := client(srv.URL).GetCalendar(context.Background(), "primary")
			got, ok := gapi.ClassOf(err)
			if !ok {
				t.Fatalf("error carried no class: %v", err)
			}
			if got != c.want {
				t.Fatalf("status %d reason %q classified as %q, want %q", c.status, c.reason, got, c.want)
			}
			if !strings.HasPrefix(err.Error(), "["+string(c.want)+"]") {
				t.Fatalf("error is not formatted [class] message: %q", err)
			}
		})
	}
}

// TestEveryEmittedClassIsInTheVocabulary keeps the enum closed from the
// runtime side as well as from the gate's static side.
func TestEveryEmittedClassIsInTheVocabulary(t *testing.T) {
	for _, c := range gapi.Classes {
		if !c.Valid() {
			t.Fatalf("%q is in Classes and fails Valid()", c)
		}
	}
	if gapi.Class("invented").Valid() {
		t.Fatal("an unknown class passed Valid()")
	}
	if len(gapi.Classes) != 12 {
		t.Fatalf("%d classes; §6.5 tabulates twelve", len(gapi.Classes))
	}
}

// TestOnlyTransientClassesRetry: stale needs a re-read and
// ambiguous_outcome needs somebody to look, so neither may be retried
// silently.
func TestOnlyTransientClassesRetry(t *testing.T) {
	retryable := map[gapi.Class]bool{gapi.ClassUnavailable: true, gapi.ClassRateLimited: true}
	for _, c := range gapi.Classes {
		if got := c.Retryable(); got != retryable[c] {
			t.Fatalf("%q.Retryable() = %v, want %v", c, got, retryable[c])
		}
	}
}

func TestRetriesTransientFailures(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"code":503,"message":"try later"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"primary","summary":"ok"}`))
	}))
	defer srv.Close()

	c := client(srv.URL)
	c.MaxRetries = 4
	got, err := c.GetCalendar(context.Background(), "primary")
	if err != nil {
		t.Fatalf("GetCalendar after retries: %v", err)
	}
	if got.Summary != "ok" {
		t.Fatalf("got %+v", got)
	}
	if hits.Load() != 3 {
		t.Fatalf("made %d attempts, expected 3", hits.Load())
	}
}

func TestDoesNotRetryAClientError(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"gone"}}`))
	}))
	defer srv.Close()

	c := client(srv.URL)
	c.MaxRetries = 4
	if _, err := c.GetCalendar(context.Background(), "primary"); err == nil {
		t.Fatal("expected an error")
	}
	if hits.Load() != 1 {
		t.Fatalf("a 404 was retried %d times", hits.Load())
	}
}

// TestTransportErrorsCarryNoURL is §9: a search term travels in a query
// string, and the obvious fmt of a *url.Error quotes the whole URL.
func TestTransportErrorsCarryNoURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	base := srv.URL
	srv.Close() // nothing is listening now

	c := client(base)
	_, err := c.ListEvents(context.Background(), "primary", gapi.EventsListOptions{
		Query: "salary-review-with-alex",
	})
	if err == nil {
		t.Fatal("expected a transport failure")
	}
	msg := err.Error()
	for _, forbidden := range []string{"salary-review", "http://", "https://", base} {
		if strings.Contains(msg, forbidden) {
			t.Fatalf("a transport error leaked %q into its message: %s", forbidden, msg)
		}
	}
}

func TestContextCancellationIsNotAMysteriousFailure(t *testing.T) {
	fake := caltest.Seed()
	c := client(fake.Start())
	defer fake.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.GetCalendar(ctx, "primary")
	if err == nil {
		t.Fatal("expected an error")
	}
	if cls, _ := gapi.ClassOf(err); cls != gapi.ClassUnavailable {
		t.Fatalf("class = %q, want unavailable", cls)
	}
	if !strings.Contains(err.Error(), "cancel") {
		t.Fatalf("a canceled request does not say so: %v", err)
	}
}

func TestFreeBusyRoundTrip(t *testing.T) {
	fake := caltest.Seed()
	fake.FreeBusyErrors["unreadable@example.test"] = "notFound"
	c := client(fake.Start())
	defer fake.Close()

	req := &gcal.FreeBusyRequest{
		TimeMin: "2026-03-16T00:00:00Z", TimeMax: "2026-03-17T00:00:00Z",
		TimeZone: "UTC", CalendarExpansionMax: 50,
		Items: []gcal.FreeBusyRequestItem{{ID: "primary"}, {ID: "unreadable@example.test"}},
	}
	res, err := c.QueryFreeBusy(context.Background(), req)
	if err != nil {
		t.Fatalf("QueryFreeBusy: %v", err)
	}
	if len(res.Calendars["primary"].Busy) != 1 {
		t.Fatalf("primary busy = %+v", res.Calendars["primary"])
	}
	if len(res.Calendars["unreadable@example.test"].Errors) != 1 {
		t.Fatal("a per-calendar error was not reported; §4.6 must not fold it into `free`")
	}
}

func TestErrorsWrapAndUnwrap(t *testing.T) {
	cause := errors.New("underneath")
	err := gapi.Wrap(gapi.ClassInvalid, cause, "something %s", "happened")
	if !errors.Is(err, cause) {
		t.Fatal("Wrap lost the cause")
	}
	if !strings.Contains(err.Error(), "something happened") {
		t.Fatalf("message = %q", err.Error())
	}
	if _, ok := gapi.ClassOf(errors.New("plain")); ok {
		t.Fatal("a plain error reported a class")
	}
}

func TestURLErrorIsStripped(t *testing.T) {
	ue := &url.Error{Op: "Get", URL: "https://example.test/x?q=secret", Err: errors.New("dial tcp: refused")}
	wrapped := gapi.Wrap(gapi.ClassUnavailable, ue, "could not reach Google")
	if strings.Contains(wrapped.Error(), "secret") {
		t.Fatalf("the query string survived: %s", wrapped.Error())
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestListInstancesPassesItsOptions(t *testing.T) {
	fake := caltest.Seed()
	c := client(fake.Start())
	defer fake.Close()

	got, err := c.ListInstances(context.Background(), "primary", "ev-weekly", gapi.EventsListOptions{
		TimeMin: "2026-03-01T00:00:00Z", TimeMax: "2026-04-01T00:00:00Z",
		MaxResults: 10, TimeZone: "UTC", ShowDeleted: true,
	})
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	// Two of the seeded occurrences fall in March; the canceled one is
	// in April, so showDeleted does not add it here.
	if len(got.Items) != 2 {
		t.Fatalf("got %d instances", len(got.Items))
	}
	if got.Items[0].RecurringEventID != "ev-weekly" {
		t.Fatalf("instance does not point at its series: %+v", got.Items[0])
	}
}

func TestListEventsPassesEveryOption(t *testing.T) {
	fake := caltest.Seed()
	c := client(fake.Start())
	defer fake.Close()

	_, err := c.ListEvents(context.Background(), "primary", gapi.EventsListOptions{
		TimeMin: "2026-03-01T00:00:00Z", TimeMax: "2026-04-01T00:00:00Z",
		SingleEvents: true, OrderBy: "startTime", Query: "sync",
		MaxResults: 5, PageToken: "", ShowDeleted: true, TimeZone: "UTC",
		EventTypes: []string{"default"}, ICalUID: "uid", UpdatedMin: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("ListEvents with every option: %v", err)
	}
}

func TestPagingTokensAreFollowedByTheCaller(t *testing.T) {
	fake := caltest.Seed()
	fake.PageSize = 1
	c := client(fake.Start())
	defer fake.Close()

	page, err := c.ListCalendars(context.Background(), "", false)
	if err != nil {
		t.Fatal(err)
	}
	if page.NextPageToken == "" {
		t.Fatal("a page size of 1 over three calendars produced no continuation token")
	}
	next, err := c.ListCalendars(context.Background(), page.NextPageToken, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) == 0 {
		t.Fatal("the second page is empty")
	}
	if next.Items[0].ID == page.Items[0].ID {
		t.Fatal("the continuation token returned the same page")
	}
}

func TestHiddenCalendarsAreOptional(t *testing.T) {
	fake := caltest.Seed()
	fake.Entries["readonly@group.calendar.example.test"].Hidden = true
	c := client(fake.Start())
	defer fake.Close()
	ctx := context.Background()

	visible, err := c.ListCalendars(ctx, "", false)
	if err != nil {
		t.Fatal(err)
	}
	all, err := c.ListCalendars(ctx, "", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) <= len(visible.Items) {
		t.Fatalf("showHidden returned %d, default returned %d", len(all.Items), len(visible.Items))
	}
}

func TestBackoffIsBoundedAndJittered(t *testing.T) {
	// Google's own algorithm: min((2^n) + jitter, 32s). The cap matters
	// more than the curve — an unbounded backoff turns a rate limit into
	// a hang.
	if gapi.MaxBackoff != 32*time.Second {
		t.Fatalf("MaxBackoff = %s; Google's guide caps at 32s", gapi.MaxBackoff)
	}
}

func TestRetriesGiveUpAndReportTheLastFailure(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":{"code":503,"message":"still down"}}`))
	}))
	defer srv.Close()

	c := client(srv.URL)
	c.MaxRetries = 2
	_, err := c.GetCalendar(context.Background(), "primary")
	if err == nil {
		t.Fatal("expected a failure after the retries ran out")
	}
	if cls, _ := gapi.ClassOf(err); cls != gapi.ClassUnavailable {
		t.Fatalf("class = %q", cls)
	}
	if hits.Load() != 3 { // the first attempt plus two retries
		t.Fatalf("made %d attempts with MaxRetries=2", hits.Load())
	}
}

func TestMalformedResponseIsNotAPanic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id": [this is not json`))
	}))
	defer srv.Close()

	_, err := client(srv.URL).GetCalendar(context.Background(), "primary")
	if err == nil {
		t.Fatal("malformed JSON was accepted")
	}
	if cls, _ := gapi.ClassOf(err); cls != gapi.ClassUnavailable {
		t.Fatalf("class = %q, want unavailable", cls)
	}
}

func TestErrorWithNoBodyStillClassifies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := client(srv.URL).GetCalendar(context.Background(), "primary")
	if cls, _ := gapi.ClassOf(err); cls != gapi.ClassNotFound {
		t.Fatalf("class = %q", cls)
	}
	if !strings.Contains(err.Error(), "Not Found") {
		t.Fatalf("an empty error body produced no message: %q", err)
	}
}

// TestListInstancesOfAnOccurrenceID records what the live driver found,
// at the layer that talks to Google.
//
// events.instances does not refuse an occurrence's own id: it answers
// 200 and expands the occurrence that id names. A canceled occurrence
// expands to nothing, so the call succeeds with an empty list — which is
// why internal/service refuses the id shape rather than waiting to be
// told. The refusal means nothing here reaches this method in
// production, so this is the only place the fake's behavior is held to
// the probe that established it (§18 row 31).
func TestListInstancesOfAnOccurrenceID(t *testing.T) {
	fake := caltest.Seed()
	c := client(fake.Start())
	defer fake.Close()
	ctx := context.Background()

	live, err := c.ListInstances(ctx, "primary", "ev-weekly_20260324T130000Z",
		gapi.EventsListOptions{})
	if err != nil {
		t.Fatalf("an occurrence id was refused: %v", err)
	}
	if len(live.Items) != 1 || live.Items[0].ID != "ev-weekly_20260324T130000Z" {
		t.Fatalf("expanding a live occurrence returned %d items, want the occurrence itself",
			len(live.Items))
	}

	// The canceled one, which is the case that produced the defect.
	gone, err := c.ListInstances(ctx, "primary", "ev-weekly_20260407T120000Z",
		gapi.EventsListOptions{})
	if err != nil {
		t.Fatalf("a canceled occurrence id was refused rather than expanded: %v", err)
	}
	if len(gone.Items) != 0 {
		t.Fatalf("a canceled occurrence expanded to %d items, want an empty list", len(gone.Items))
	}

	// And it is reachable when asked for, so the emptiness is the
	// filter's doing rather than the id being unknown.
	shown, err := c.ListInstances(ctx, "primary", "ev-weekly_20260407T120000Z",
		gapi.EventsListOptions{ShowDeleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(shown.Items) != 1 {
		t.Fatalf("showDeleted returned %d items, want the canceled occurrence", len(shown.Items))
	}
}
