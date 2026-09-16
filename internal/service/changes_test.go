package service_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/service"
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
	// series, so it picks up whatever is already cancelled. Those are
	// not deletions "since" anything, and the text must not call them
	// that — there was no since yet.
	if strings.Contains(got.Text(), "Deleted (") {
		t.Fatalf("a baseline called already-cancelled events deletions:\n%s", got.Text())
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

// Google issues the token with the last page only, so a read that did
// not finish has none — and a caller who stored one anyway would skip
// every page it had not seen.
func TestAnUnfinishedReadHandsBackNoSyncToken(t *testing.T) {
	svc, fake := seeded(t)
	fake.PageSize = 1

	got, err := svc.ListChanges(context.Background(), service.ChangesOptions{
		Calendar: "primary", MaxEvents: 1})
	if err != nil {
		t.Fatalf("ListChanges: %v", err)
	}
	if got.Complete {
		t.Fatal("the read stopped at its budget and called itself complete")
	}
	if got.SyncToken != "" {
		t.Fatalf("an unfinished read handed back the sync token %q, which skips everything it did not read",
			got.SyncToken)
	}
	if got.NextPageToken == "" {
		t.Fatal("an unfinished read gave no page token, so it cannot be continued")
	}
	if !strings.Contains(got.Text(), "NO sync token") {
		t.Fatalf("the result does not say the token is missing:\n%s", got.Text())
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
