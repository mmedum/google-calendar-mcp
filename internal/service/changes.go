package service

import (
	"context"
	"fmt"

	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/render"
)

// Incremental sync (§17.1).
//
// This is the one read that answers "what changed", and it is the only
// correct way to ask: a list with a window cannot tell you an event was
// DELETED, because a deleted event simply stops matching. Sync reports
// it as a cancelled tombstone, which is the whole reason the tool exists
// rather than being a second way to list.
//
// The server holds no replica (§1) and does not store the token either.
// The token is handed back to the caller and passed in next time, the
// same contract as a page token: it is Google's to mint and opaque to
// everything here. Storing it would make the server stateful about a
// question — "since when?" — whose answer belongs to whoever is asking.
//
// Three rules come straight from the discovery document (revision
// 20260826) and each is enforced here rather than left to Google's 400:
//
//  1. deletions are always in the result, and `showDeleted` may not be
//     false, so this read forces it true;
//  2. a window, a search, an ordering or `updatedMin` cannot be combined
//     with a token, so this tool offers none of them;
//  3. `nextSyncToken` arrives on the LAST page only — so a truncated
//     read has no token to give, and saying otherwise would hand the
//     caller a token that skips everything it did not see.
//
// And the invalidation story §17.1 asked for: an expired token is 410,
// which reaches the caller as `[stale]` naming the one cure — ask again
// with no token at all.

// ChangesOptions is one call to ListChanges.
type ChangesOptions struct {
	Calendar string
	// SyncToken is empty on the first call, which establishes a
	// baseline rather than reporting changes.
	SyncToken string
	// PageToken continues a read that did not finish.
	PageToken string
	TimeZone  string
	MaxEvents int
}

// ListChanges reports what changed on a calendar since a token.
func (s *Service) ListChanges(ctx context.Context, o ChangesOptions) (render.Changes, error) {
	if err := s.ready(); err != nil {
		return render.Changes{}, err
	}
	c, err := s.ResolveCalendar(ctx, o.Calendar)
	if err != nil {
		return render.Changes{}, err
	}
	zone, err := s.Zone(ctx, o.TimeZone, c.TimeZone)
	if err != nil {
		return render.Changes{}, err
	}

	budget := o.MaxEvents
	if budget <= 0 {
		budget = s.Cfg.MaxEvents
	}

	out := render.Changes{
		CalendarID:   c.ID,
		CalendarName: c.Title,
		Zone:         zone,
		Baseline:     o.SyncToken == "",
	}

	opts := gapi.EventsListOptions{
		// Forced, not offered: the API refuses false alongside a token,
		// and a sync that hid deletions would be a slower list.
		ShowDeleted: true,
		SyncToken:   o.SyncToken,
		PageToken:   o.PageToken,
		MaxResults:  budget,
	}

	for {
		page, perr := s.API.ListEvents(ctx, c.ID, opts)
		out.Requests++
		if perr != nil {
			return render.Changes{}, changesError(perr, o.SyncToken)
		}
		for _, raw := range page.Items {
			// A tombstone is the answer, not a row to skip. Google
			// sends it bare — an id and a status, with no start and no
			// summary — so it is never put through model.FromEvent,
			// which would fail on the missing times.
			if raw.Status == gcal.StatusCancelled {
				out.Deleted = append(out.Deleted, raw.ID)
				continue
			}
			e, cerr := model.FromEvent(c.ID, raw, &zone)
			if cerr != nil {
				return render.Changes{}, cerr
			}
			out.Changed = append(out.Changed, e)
		}
		// The token arrives with the last page and only there.
		if page.NextSyncToken != "" {
			out.SyncToken = page.NextSyncToken
		}
		out.NextPageToken = page.NextPageToken
		done := page.NextPageToken == ""
		if done || len(out.Changed)+len(out.Deleted) >= budget {
			out.Complete = done
			break
		}
		opts.PageToken = page.NextPageToken
	}

	// Said rather than implied: an incomplete read carries no token, so
	// a caller that stored one anyway would skip everything still
	// unread. Belt and braces — Google withholds it too.
	if !out.Complete {
		out.SyncToken = ""
	}
	return out, nil
}

// changesError says what to do about a token that no longer works.
//
// gapi maps 410 to [stale] already; what it cannot know is that the cure
// here is not "read again and retry" but "ask again with no token", and
// that everything the caller believes about the calendar is now
// unreliable rather than merely out of date.
func changesError(err error, token string) error {
	cls, ok := gapi.ClassOf(err)
	if !ok || cls != gapi.ClassStale || token == "" {
		return err
	}
	return gapi.Wrap(gapi.ClassStale, err,
		"Google has discarded this sync token, so what changed since it was issued cannot be "+
			"recovered. Call again with no sync_token to read the calendar afresh and get a new one; "+
			"treat anything you were holding from before as unreliable rather than merely stale")
}

// ChangeCount is the one-line summary a result leads with.
func ChangeCount(c render.Changes) string {
	return fmt.Sprintf("%d changed, %d deleted", len(c.Changed), len(c.Deleted))
}
