//go:build live

package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/v3/internal/redact"
)

// spikeO — what does a read by updatedMin give back (§18 row 91)?
//
// list_changes' updated_since sends updatedMin and hands back no sync
// token, because nothing showed that a token from such a read chains.
// This asks Google directly:
//
//   - does the last page of an updatedMin read carry a nextSyncToken;
//   - if it does, does a sync with it report a change made after it;
//   - how does Google answer a moment far in the past;
//   - does the bound include an event written at exactly that moment.
//
// Everything is on the scratch calendar this run filled (§9.1). The one
// write is a description change on the driver's own timed event, sent
// with sendUpdates=none to an event with no guests.
func spikeO(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	if scratch == "" {
		return undetermined, "no scratch calendar this run"
	}
	events := "/calendars/" + url.PathEscape(scratch) + "/events"

	// The seed wrote every event in this run, so an hour back covers
	// them and nothing from an earlier run.
	since := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	token, found, pages, err := pageUpdatedMin(ctx, api, events, since, timedID)
	if err != nil {
		return undetermined, "the updatedMin read failed: " + redact.String(err.Error())
	}
	out.Printf("      updatedMin an hour back: %d page(s), seeded event found=%v, nextSyncToken on the last page=%v\n",
		pages, found, token != "")

	// How far back Google allows is not documented. One request with a
	// moment decades ago, reported by status and reason only.
	var none struct{}
	status, ferr := api.status(ctx, http.MethodGet,
		events+"?"+url.Values{"updatedMin": {"2000-01-01T00:00:00Z"}, "maxResults": {"1"}}.Encode(), "", nil, &none)
	reason := ""
	if ferr != nil && strings.Contains(ferr.Error(), "updatedMinTooLongAgo") {
		reason = " updatedMinTooLongAgo"
	}
	out.Printf("      updatedMin 2000-01-01: status %d%s\n", status, reason)

	// Whether the bound includes its own moment: the timed event's
	// `updated`, passed back exactly.
	var ev struct {
		Updated string `json:"updated"`
	}
	if err := api.do(ctx, http.MethodGet, events+"/"+timedID, nil, &ev); err == nil && ev.Updated != "" {
		_, atBound, _, berr := pageUpdatedMin(ctx, api, events, ev.Updated, timedID)
		if berr == nil {
			out.Printf("      updatedMin equal to the event's own updated: event included=%v\n", atBound)
		}
	}

	if token == "" {
		return pass, "an updatedMin read issues no sync token, so withholding one costs nothing"
	}

	// It issued one. Does it chain? Change the event, then sync.
	if err := api.patchEvent(ctx, scratch, timedID, map[string]any{"description": "spike O " + runSuffix}); err != nil {
		return undetermined, "could not change the timed event to sync over: " + redact.String(err.Error())
	}
	var synced struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	q := url.Values{"syncToken": {token}, "showDeleted": {"true"}}
	status, serr := api.status(ctx, http.MethodGet, events+"?"+q.Encode(), "", nil, &synced)
	switch {
	case serr == nil:
	case status >= 400 && status < 500:
		// Never the error: it carries the URL, and the URL the token.
		return pass, fmt.Sprintf("Google issued a token from an updatedMin read and REFUSED it as a sync "+
			"token (HTTP %d), so withholding it is right", status)
	default:
		// No answer, or a 5xx, says nothing about the token.
		return undetermined, fmt.Sprintf("the sync with the token got no answer to read (HTTP %d); "+
			"run the spike again", status)
	}
	for _, it := range synced.Items {
		if it.ID == timedID {
			return pass, "a token from an updatedMin read chains: a sync with it reported the change made " +
				"after it. list_changes could hand it back (§18 row 91)"
		}
	}
	return pass, fmt.Sprintf("Google accepted the token but the sync missed the change made after it "+
		"(%d rows), so withholding it is right", len(synced.Items))
}

// pageUpdatedMin pages an updatedMin read to its end, as list_changes
// does, and reports the token on the last page and whether want was
// among the rows. The token is returned, never printed.
func pageUpdatedMin(ctx context.Context, api *liveAPI, events, since, want string) (token string, found bool, pages int, err error) {
	const maxPages = 40
	pageTk := ""
	for pages < maxPages {
		var list struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
			NextSyncToken string `json:"nextSyncToken"`
		}
		q := url.Values{"updatedMin": {since}, "showDeleted": {"true"}, "maxResults": {"250"}}
		if pageTk != "" {
			q.Set("pageToken", pageTk)
		}
		if err := api.do(ctx, http.MethodGet, events+"?"+q.Encode(), nil, &list); err != nil {
			return "", false, pages, err
		}
		pages++
		for _, it := range list.Items {
			if it.ID == want {
				found = true
			}
		}
		token, pageTk = list.NextSyncToken, list.NextPageToken
		if pageTk == "" {
			return token, found, pages, nil
		}
	}
	return "", found, pages, fmt.Errorf("still not the last page after %d pages", maxPages)
}
