//go:build live

package main

import (
	"context"
	"fmt"
	"net/url"

	"github.com/mmedum/google-calendar-mcp/v2/internal/redact"
)

// spikeN — which parameters suppress `nextSyncToken`?
//
// Phase 6 built `list_changes` against a fake written from the discovery
// document, and the first live run found the baseline coming back with
// NO sync token at all. That is the failure §18 keeps recording: the
// fake encodes one reading of the documentation, the code agrees with
// the fake, and both are wrong together.
//
// The documentation says only that the token arrives "on the last page
// of results" and lists the parameters that cannot accompany a token. It
// does not say which parameters stop one being ISSUED, which is a
// different question and the one that matters for the first call.
//
// So this asks the API directly, one parameter set at a time, and
// reports which of them come back with a token. Read-only, on the
// calendar this driver created and filled itself (§9.1).
func spikeN(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	if scratch == "" {
		return undetermined, "no scratch calendar this run"
	}

	// Each row is one parameter set. The first is what `list_changes`
	// actually sends; the rest narrow down whatever the answer is.
	probes := []struct {
		name  string
		query url.Values
	}{
		{"bare (no parameters)", url.Values{}},
		{"showDeleted=true", url.Values{"showDeleted": {"true"}}},
		{"maxResults=250", url.Values{"maxResults": {"250"}}},
		{"showDeleted=true&maxResults=250", url.Values{
			"showDeleted": {"true"}, "maxResults": {"250"}}},
		{"singleEvents=true&showDeleted=true", url.Values{
			"singleEvents": {"true"}, "showDeleted": {"true"}}},
		{"timeMin set (expected to suppress)", url.Values{
			"showDeleted": {"true"}, "timeMin": {"2026-01-01T00:00:00Z"}}},
	}

	type answer struct {
		name  string
		token bool
		page  bool
		items int
		err   string
	}
	var results []answer
	for _, p := range probes {
		var list struct {
			Items         []map[string]any `json:"items"`
			NextPageToken string           `json:"nextPageToken"`
			NextSyncToken string           `json:"nextSyncToken"`
		}
		path := "/calendars/" + url.PathEscape(scratch) + "/events"
		if q := p.query.Encode(); q != "" {
			path += "?" + q
		}
		a := answer{name: p.name}
		if err := api.do(ctx, "GET", path, nil, &list); err != nil {
			// Never the whole error: a request URL carries the query,
			// and §9 keeps search terms out of a transcript.
			a.err = redact.String(err.Error())
		} else {
			a.token = list.NextSyncToken != ""
			a.page = list.NextPageToken != ""
			a.items = len(list.Items)
		}
		results = append(results, a)
		// The token itself is never printed. Whether one arrived is the
		// finding; the value is a credential-shaped string belonging to
		// this account.
		switch {
		case a.err != "":
			out.Printf("      %-36s error: %s\n", p.name, a.err)
		default:
			out.Printf("      %-36s nextSyncToken=%v nextPageToken=%v items=%d\n",
				p.name, a.token, a.page, a.items)
		}
	}

	// The decisive half. Every single-request probe above came back with
	// a nextPageToken, and the documentation says the sync token arrives
	// on the LAST page — so none of them could have had one, and
	// "no token" says nothing until somebody reaches the end.
	//
	// This pages through to the real last page and reports what is
	// there. It is bounded: a calendar that needs more pages than this
	// is itself the finding.
	const maxPages = 40
	var (
		pages  int
		items  int
		token  bool
		pageTk string
	)
	for pages < maxPages {
		var list struct {
			Items         []map[string]any `json:"items"`
			NextPageToken string           `json:"nextPageToken"`
			NextSyncToken string           `json:"nextSyncToken"`
		}
		q := url.Values{"showDeleted": {"true"}, "maxResults": {"250"}}
		if pageTk != "" {
			q.Set("pageToken", pageTk)
		}
		path := "/calendars/" + url.PathEscape(scratch) + "/events?" + q.Encode()
		if err := api.do(ctx, "GET", path, nil, &list); err != nil {
			return undetermined, "paging to the last page failed: " + redact.String(err.Error())
		}
		pages++
		items += len(list.Items)
		token = list.NextSyncToken != ""
		pageTk = list.NextPageToken
		if pageTk == "" {
			break
		}
	}
	out.Printf("      paged to the end: %d pages, %d rows, nextSyncToken on the last page=%v\n",
		pages, items, token)

	switch {
	case pageTk != "":
		return undetermined, fmt.Sprintf(
			"still not the last page after %d pages and %d rows. A baseline on this calendar costs more "+
				"requests than any sane budget allows, which is itself the thing list_changes has to say "+
				"out loud rather than returning a silent half-answer", maxPages, items)
	case !token:
		return fail, fmt.Sprintf(
			"reached the last page after %d pages and %d rows and STILL no nextSyncToken, so incremental "+
				"sync cannot be built on this calendar at all", pages, items)
	default:
		return pass, fmt.Sprintf(
			"the token arrives on the LAST page and only there: %d pages and %d rows were needed to reach "+
				"it here, none of the single-request probes above could have had one, and a calendar with a "+
				"long deletion history therefore costs many requests to baseline", pages, items)
	}
}
