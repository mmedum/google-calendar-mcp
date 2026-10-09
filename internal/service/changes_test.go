package service_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/v3/internal/gapi/caltest"
	"github.com/mmedum/google-calendar-mcp/v3/internal/render"
	"github.com/mmedum/google-calendar-mcp/v3/internal/service"
)

// Incremental sync (§17.1). The claims worth holding are not "it lists
// events" — list_events does that — but the four things a list cannot
// do: hand back a token, report a DELETION, withhold the token when the
// read did not finish, and say what to do when the token dies.

func TestChangesBaselineHandsBackAToken(t *testing.T) {
	svc, _ := seeded(t)
	got, err := svc.ListChanges(context.Background(), service.ChangesOptions{Calendar: "primary"})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if !got.Baseline {
		t.Fatal("a call with no token is a baseline and should say so")
	}
	if got.SyncToken == "" {
		t.Fatal("the baseline handed back no sync token, so nothing can be asked incrementally after it")
	}
	if !got.Complete {
		t.Fatal("the baseline read the whole calendar and should report itself complete")
	}
	// A baseline reads with showDeleted=true like every call in the
	// series, so it picks up whatever is already canceled. Those are
	// not deletions "since" anything, and the text must not call them
	// that — there was no since yet.
	if strings.Contains(got.Text(), "Deleted (") {
		t.Fatalf("a baseline called already-canceled events deletions:\n%s", got.Text())
	}
	if got.Zone.Name() == "" {
		t.Fatal("the result does not name the zone it rendered in (§4.5)")
	}
}

func TestChangesReportsOnlyWhatMoved(t *testing.T) {
	svc, fake := seeded(t)
	ctx := context.Background()

	base, err := svc.ListChanges(ctx, service.ChangesOptions{Calendar: "primary"})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Nothing has happened, so nothing changed — and the token moves on.
	quiet, err := svc.ListChanges(ctx, service.ChangesOptions{
		Calendar: "primary", SyncToken: base.SyncToken})
	if err != nil {
		t.Fatalf("quiet sync: %v", err)
	}
	if len(quiet.Changed) != 0 || len(quiet.Deleted) != 0 {
		t.Fatalf("nothing happened and the sync reported %d changed, %d deleted",
			len(quiet.Changed), len(quiet.Deleted))
	}
	if quiet.Baseline {
		t.Fatal("a call WITH a token is not a baseline")
	}

	// Now move one event and sync again.
	fake.Touch("primary", fake.AnyEventID("primary"))
	after, err := svc.ListChanges(ctx, service.ChangesOptions{
		Calendar: "primary", SyncToken: quiet.SyncToken})
	if err != nil {
		t.Fatalf("sync after a change: %v", err)
	}
	if len(after.Changed) != 1 {
		t.Fatalf("one event changed and the sync reported %d", len(after.Changed))
	}
}

// The reason this tool exists. A deleted event stops matching a window,
// so no list can report it; sync returns a tombstone.
func TestChangesReportsADeletionAListCannot(t *testing.T) {
	svc, fake := seeded(t)
	ctx := context.Background()

	base, err := svc.ListChanges(ctx, service.ChangesOptions{Calendar: "primary"})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	before := len(base.Changed)

	id := fake.AnyEventID("primary")
	fake.Remove("primary", id)

	after, err := svc.ListChanges(ctx, service.ChangesOptions{
		Calendar: "primary", SyncToken: base.SyncToken})
	if err != nil {
		t.Fatalf("sync after a delete: %v", err)
	}
	if len(after.Deleted) != 1 || after.Deleted[0] != id {
		t.Fatalf("the deletion of %s was not reported; deleted = %v", id, after.Deleted)
	}
	// And the text says so, because the structured half is not what a
	// model reads first.
	if !strings.Contains(after.Text(), "Deleted (1)") {
		t.Fatalf("the rendered result does not report the deletion:\n%s", after.Text())
	}
	if before == 0 {
		t.Fatal("the baseline saw no events, so this proved nothing")
	}
}

// Google issues the token with the last page only, so an INCREMENTAL
// read stopped by its budget has none — and a caller who stored one
// anyway would mark changes as seen that were never delivered.
func TestAnUnfinishedIncrementalReadHandsBackNoSyncToken(t *testing.T) {
	svc, fake := seeded(t)
	ctx := context.Background()

	base, err := svc.ListChanges(ctx, service.ChangesOptions{Calendar: "primary"})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// More changes than the budget will carry.
	for _, id := range fake.EventIDs("primary") {
		fake.Touch("primary", id)
	}
	fake.PageSize = 1

	got, err := svc.ListChanges(ctx, service.ChangesOptions{
		Calendar: "primary", SyncToken: base.SyncToken, MaxEvents: 1})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if got.Complete {
		t.Fatal("the read stopped at its budget and called itself complete")
	}
	if got.SyncToken != "" {
		t.Fatalf("an unfinished incremental read handed back the token %q, which marks changes as seen "+
			"that were never delivered", got.SyncToken)
	}
	if got.NextPageToken == "" {
		t.Fatal("an unfinished read gave no page token, so it cannot be continued")
	}
	if !strings.Contains(got.Text(), "NO sync token") {
		t.Fatalf("the result does not say the token is missing:\n%s", got.Text())
	}
}

// And the other half, which a live run found the hard way: a BASELINE
// pages past its budget to reach the token.
//
// On a real calendar the scratch account had 507 rows across 3 pages,
// nearly all of them tombstones from deleted events. With the budget
// stopping the read at 250 the last page was never reached, so no token
// ever came back and incremental sync could not be started at all. The
// rows are not changes on a baseline — the token covers them — so paging
// past them loses nothing.
func TestABaselinePagesPastItsBudgetToReachTheToken(t *testing.T) {
	svc, fake := seeded(t)
	fake.PageSize = 1

	got, err := svc.ListChanges(context.Background(), service.ChangesOptions{
		Calendar: "primary", MaxEvents: 1})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if !got.Complete {
		t.Fatal("a baseline stopped before the last page, so it can never produce a token")
	}
	if got.SyncToken == "" {
		t.Fatal("a baseline reached the end and still handed back no token")
	}
	if got.Requests < 2 {
		t.Fatalf("a baseline over %d one-event pages spent %d requests; it did not page",
			fake.PageSize, got.Requests)
	}
	if got.Skipped == 0 {
		t.Fatal("the baseline carried every row despite its budget, so nothing was paged past")
	}
	// And it says so, rather than silently dropping them.
	if !strings.Contains(got.Text(), "paged past") {
		t.Fatalf("the result does not admit to skipping rows:\n%s", got.Text())
	}
}

// §17.1's invalidation story: 410 means start over, and saying "read
// again and retry" would send the caller round the same loop.
func TestAnExpiredTokenSaysToStartOver(t *testing.T) {
	svc, fake := seeded(t)
	ctx := context.Background()

	base, err := svc.ListChanges(ctx, service.ChangesOptions{Calendar: "primary"})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	fake.SyncTokenExpired = true
	_, err = svc.ListChanges(ctx, service.ChangesOptions{
		Calendar: "primary", SyncToken: base.SyncToken})
	if err == nil {
		t.Fatal("an expired sync token was accepted")
	}
	cls, ok := gapi.ClassOf(err)
	if !ok || cls != gapi.ClassStale {
		t.Fatalf("an expired token gave class %v, want stale", cls)
	}
	if !strings.Contains(err.Error(), "no sync_token") {
		t.Fatalf("the refusal does not name the cure:\n%v", err)
	}

	// And the cure works: no token, a fresh baseline.
	fresh, err := svc.ListChanges(ctx, service.ChangesOptions{Calendar: "primary"})
	if err != nil {
		t.Fatalf("the documented cure failed: %v", err)
	}
	if fresh.SyncToken == "" {
		t.Fatal("starting over produced no new token")
	}
}

// clockAt sets the fake's clock, which stamps `updated` on every write.
func clockAt(t *testing.T, fake *caltest.Server, at string) {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, at)
	if err != nil {
		t.Fatal(err)
	}
	fake.Now = func() time.Time { return ts }
}

func changedTitles(c render.Changes) string {
	var titles []string
	for _, e := range c.Changed {
		titles = append(titles, e.Title)
	}
	return strings.Join(titles, ",")
}

// updated_since asks Google for what was written at or after a moment:
// an event written before it is not reported, one written after it is,
// and so is one deleted after it. The read is not a baseline, and it
// hands back no sync token, because nothing yet shows that a token from
// such a read chains (§18 row 91).
func TestUpdatedSinceReportsWhatChangedAfterIt(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)
	clockAt(t, fake, "2026-03-10T08:00:00Z")
	fake.Touch("primary", "ev-holiday")
	clockAt(t, fake, "2026-03-12T08:00:00Z")
	fake.Touch("primary", "ev-standup")
	fake.Remove("primary", "ev-transparent")

	got, err := svc.ListChanges(context.Background(), service.ChangesOptions{
		Calendar: "primary", UpdatedSince: "2026-03-11T00:00:00Z"})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if titles := changedTitles(got); titles != "Morning sync" {
		t.Fatalf("changed = %q, want only the event written after the moment", titles)
	}
	if strings.Join(got.Deleted, ",") != "ev-transparent" {
		t.Fatalf("deleted = %v, want the event deleted after the moment", got.Deleted)
	}
	if got.Baseline || got.SyncToken != "" || !got.Complete {
		t.Fatalf("baseline=%v sync_token=%q complete=%v, want a complete change list with no token",
			got.Baseline, got.SyncToken, got.Complete)
	}
	text := got.Text()
	for _, want := range []string{
		"Changes on Sample Primary since 2026-03-11T01:00:00+01:00",
		"an event whose only change was its reminders is not here",
		"Deleted (1)",
		"hands back no sync token",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the text does not say %q:\n%s", want, text)
		}
	}
}

// Google says changing reminders "does not also change the updated
// property", so a reminders-only update is not reported by a read since
// a moment, while a change to the event itself is.
func TestARemindersOnlyChangeIsNotReportedSinceAMoment(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)
	clockAt(t, fake, "2026-03-10T08:00:00Z")
	fake.Touch("primary", "ev-standup")
	clockAt(t, fake, "2026-03-12T08:00:00Z")
	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "ev-standup", PopupReminders: ptr([]int{10}),
	}); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	since := service.ChangesOptions{Calendar: "primary", UpdatedSince: "2026-03-11T00:00:00Z"}
	got, err := svc.ListChanges(context.Background(), since)
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if titles := changedTitles(got); titles != "" {
		t.Fatalf("changed = %q, want nothing: only reminders changed", titles)
	}

	if _, err := svc.UpdateEvent(context.Background(), service.UpdateOptions{
		Calendar: "primary", EventID: "ev-standup", Title: ptr("Morning sync, moved"),
	}); err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if got, err = svc.ListChanges(context.Background(), since); err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if titles := changedTitles(got); titles != "Morning sync, moved" {
		t.Fatalf("changed = %q, want the retitled event", titles)
	}
}

// A bare date is the start of that day in the resolved zone, as a
// window's from is, and the result echoes the instant it used.
// Copenhagen is an hour ahead of UTC in March, so 16 March starts at
// 23:00 UTC on the 15th.
func TestUpdatedSinceADateIsTheStartOfThatDayInTheZone(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)
	clockAt(t, fake, "2026-03-15T22:30:00Z")
	fake.Touch("primary", "ev-holiday")
	clockAt(t, fake, "2026-03-15T23:30:00Z")
	fake.Touch("primary", "ev-standup")

	got, err := svc.ListChanges(context.Background(), service.ChangesOptions{
		Calendar: "primary", UpdatedSince: "2026-03-16"})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if titles := changedTitles(got); titles != "Morning sync" {
		t.Fatalf("changed = %q, want only the event written after local midnight", titles)
	}
	if echo := service.NewChangesResult(got).UpdatedSince; echo != "2026-03-16T00:00:00+01:00" {
		t.Fatalf("updated_since echoed %q, want 2026-03-16T00:00:00+01:00", echo)
	}
}

// Google refuses updatedMin alongside a sync token, so the server
// refuses the pair before it spends a request.
func TestUpdatedSinceIsRefusedAlongsideASyncToken(t *testing.T) {
	svc, fake := seeded(t)
	_, err := svc.ListChanges(context.Background(), service.ChangesOptions{
		Calendar: "primary", SyncToken: "caltest-sync-0", UpdatedSince: "2026-03-16"})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class = %s, want invalid", cls)
	}
	if !strings.Contains(err.Error(), "updated_since and sync_token cannot be used together") {
		t.Fatalf("the refusal does not name the pair: %v", err)
	}
	if n := len(fake.Served()); n != 0 {
		t.Fatalf("spent %d requests on a pair Google refuses", n)
	}
}

func TestUpdatedSinceThatIsNotATimeIsRefused(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.ListChanges(context.Background(), service.ChangesOptions{
		Calendar: "primary", UpdatedSince: "last Monday"})
	if cls := classOf(t, err); cls != gapi.ClassInvalid {
		t.Fatalf("class = %s, want invalid", cls)
	}
}

// Google answers a moment further back than it keeps changes for with
// 410 updatedMinTooLongAgo. The cure is a later moment or a baseline,
// not the sync token's "start over with no token".
func TestUpdatedSinceTooFarBackSaysToPassALaterOne(t *testing.T) {
	svc, fake := seeded(t)
	fake.UpdatedMinTooLongAgo = true
	_, err := svc.ListChanges(context.Background(), service.ChangesOptions{
		Calendar: "primary", UpdatedSince: "2000-01-01"})
	if cls := classOf(t, err); cls != gapi.ClassStale {
		t.Fatalf("class = %s, want stale", cls)
	}
	if !strings.Contains(err.Error(), "as far back as 2000-01-01T00:00:00+01:00") ||
		!strings.Contains(err.Error(), "Pass a later updated_since") {
		t.Fatalf("the refusal does not name the moment and the cure: %v", err)
	}
}

// A read since a moment that stops early continues only with that same
// moment. Without it the page token was taken for a baseline's, and the
// chain's last page handed back the sync token a read since a moment
// withholds. No page of the chain carries one.
func TestAReadSinceAMomentContinuesOnlyWithThatMoment(t *testing.T) {
	fake := caltest.Seed()
	svc := newService(t, fake)
	ctx := context.Background()
	clockAt(t, fake, "2026-03-12T08:00:00Z")
	for _, id := range []string{"ev-holiday", "ev-standup", "ev-transparent"} {
		fake.Touch("primary", id)
	}
	o := service.ChangesOptions{Calendar: "primary", UpdatedSince: "2026-03-11T00:00:00Z", MaxEvents: 1}
	first, err := svc.ListChanges(ctx, o)
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if first.Complete || first.NextPageToken == "" {
		t.Fatalf("complete=%v next=%q, want a read that stopped at its budget", first.Complete, first.NextPageToken)
	}

	for _, c := range []struct{ since, says string }{
		{"", "continues a read since 2026-03-11T00:00:00Z, and this call has no updated_since"},
		{"2026-03-10T00:00:00Z", "continues a read since 2026-03-11T00:00:00Z, not since 2026-03-10T00:00:00Z"},
		{"2026-03-11", "not since 2026-03-10T23:00:00Z"},
	} {
		_, err := svc.ListChanges(ctx, service.ChangesOptions{
			Calendar: "primary", UpdatedSince: c.since, PageToken: first.NextPageToken, MaxEvents: 1})
		if got := classOf(t, err); got != gapi.ClassInvalid || !strings.Contains(err.Error(), c.says) {
			t.Fatalf("updated_since %q: got [%s] %v, want [invalid] saying %q", c.since, got, err, c.says)
		}
	}

	next, pages := first, 1
	for next.NextPageToken != "" {
		if pages++; pages > 10 {
			t.Fatal("the chain did not end")
		}
		o.PageToken = next.NextPageToken
		if next, err = svc.ListChanges(ctx, o); err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if next.Baseline || next.SyncToken != "" {
			t.Fatalf("page %d: baseline=%v sync_token=%q, want neither", pages, next.Baseline, next.SyncToken)
		}
	}
	if !next.Complete || pages != 3 {
		t.Fatalf("complete=%v after %d pages, want the three changes over three pages", next.Complete, pages)
	}
}

// An incremental read continues only with the sync token it started
// from, on its own calendar. Without the token its page token was taken
// for a baseline's, which pages past its budget and counts changes
// rather than reporting them.
func TestAnIncrementalReadContinuesOnlyFromItsSyncToken(t *testing.T) {
	svc, fake := seeded(t)
	ctx := context.Background()
	base, err := svc.ListChanges(ctx, service.ChangesOptions{Calendar: "primary"})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	for _, id := range fake.EventIDs("primary") {
		fake.Touch("primary", id)
	}
	fake.PageSize = 1
	o := service.ChangesOptions{Calendar: "primary", SyncToken: base.SyncToken, MaxEvents: 1}
	first, err := svc.ListChanges(ctx, o)
	if err != nil || first.NextPageToken == "" {
		t.Fatalf("got %v and next %q, want a read that stopped at its budget", err, first.NextPageToken)
	}

	for _, c := range []struct {
		name string
		o    service.ChangesOptions
		says string
	}{
		{"no sync token", service.ChangesOptions{Calendar: "primary"},
			"continues a read from a sync_token, and this call does not pass the same one"},
		{"another sync token", service.ChangesOptions{Calendar: "primary", SyncToken: "caltest-sync-1"},
			"continues a read from a sync_token, and this call does not pass the same one"},
		{"a moment instead", service.ChangesOptions{Calendar: "primary", UpdatedSince: "2026-03-11"},
			"continues a read without updated_since"},
		{"another calendar", service.ChangesOptions{Calendar: "team@group.calendar.example.test",
			SyncToken: base.SyncToken}, "continues a read of another calendar"},
	} {
		c.o.PageToken, c.o.MaxEvents = first.NextPageToken, 1
		_, err := svc.ListChanges(ctx, c.o)
		if got := classOf(t, err); got != gapi.ClassInvalid || !strings.Contains(err.Error(), c.says) {
			t.Fatalf("%s: got [%s] %v, want [invalid] saying %q", c.name, got, err, c.says)
		}
	}

	o.PageToken = first.NextPageToken
	next, err := svc.ListChanges(ctx, o)
	if err != nil {
		t.Fatalf("continuing: %v", err)
	}
	if next.Baseline || len(next.Changed)+len(next.Deleted) != 1 || next.Skipped != 0 {
		t.Fatalf("baseline=%v rows=%d skipped=%d, want one more change reported",
			next.Baseline, len(next.Changed)+len(next.Deleted), next.Skipped)
	}
}

// A baseline too long for one call continues as a baseline: with a sync
// token its page token is refused, and alone it goes on.
func TestABaselineContinuesAsABaseline(t *testing.T) {
	svc, fake := seeded(t)
	for i := range 30 {
		fake.AddEvent("primary", caltest.Timed(fmt.Sprintf("ev-extra-%02d", i), "Extra",
			"2026-03-16T09:00:00+01:00", "2026-03-16T09:15:00+01:00", "Europe/Copenhagen"))
	}
	fake.PageSize = 1
	ctx := context.Background()
	first, err := svc.ListChanges(ctx, service.ChangesOptions{Calendar: "primary"})
	if err != nil || first.NextPageToken == "" || !first.Baseline {
		t.Fatalf("got %v, next %q, baseline %v: want a baseline cut short", err, first.NextPageToken, first.Baseline)
	}
	_, err = svc.ListChanges(ctx, service.ChangesOptions{
		Calendar: "primary", SyncToken: "caltest-sync-1", PageToken: first.NextPageToken})
	if got := classOf(t, err); got != gapi.ClassInvalid || !strings.Contains(err.Error(),
		"continues a baseline, which has no sync_token. Pass it without sync_token") {
		t.Fatalf("got [%s] %v, want [invalid] naming the baseline", got, err)
	}
	next, err := svc.ListChanges(ctx, service.ChangesOptions{Calendar: "primary", PageToken: first.NextPageToken})
	if err != nil || !next.Baseline || next.SyncToken == "" {
		t.Fatalf("got %v, baseline %v, sync token %q: want the baseline finished with its token",
			err, next.Baseline, next.SyncToken)
	}
}

// A page token list_changes did not issue is refused rather than handed
// to Google.
func TestAForeignPageTokenIsRefusedByListChanges(t *testing.T) {
	svc, _ := seeded(t)
	_, err := svc.ListChanges(context.Background(), service.ChangesOptions{Calendar: "primary", PageToken: "2"})
	if got := classOf(t, err); got != gapi.ClassInvalid || !strings.Contains(err.Error(), "not one this server issued") {
		t.Fatalf("got [%s] %v, want [invalid]", got, err)
	}
}
