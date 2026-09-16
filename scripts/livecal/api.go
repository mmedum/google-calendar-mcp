//go:build live

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/mmedum/google-calendar-mcp/internal/auth"
	"github.com/mmedum/google-calendar-mcp/internal/credentials"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/userconfig"
)

// The scratch calendar's contents. Everything here is invented: the
// driver reads only what it wrote (§9.1), so nothing from the operator's
// real calendar can reach a transcript.
const (
	scratchTitle   = "google-calendar-mcp live driver scratch"
	timedTitle     = "Livecal timed probe"
	allDayTitle    = "Livecal all-day probe"
	weeklyTitle    = "Livecal weekly probe"
	cancelledTitle = "Livecal cancelled probe"
	searchTerm     = "Livecal"
)

// Event ids are base32hex: lowercase a-v and the digits, 5 to 1024
// characters (§2.11). Not a-z — the letters w, x, y and z are NOT
// allowed, which is the kind of detail that reads as obvious and is not.
// The first run of this driver used "allday" and "weekly" and Google
// refused both with "Invalid resource id value", naming neither the
// field nor the rule; gcal.ValidEventID holds it now and caught
// "following" in this file before a request was built.
//
// Generated per run, not fixed. Deleting an event does NOT release its
// id — Google answers a re-insert with 409 — so a driver that reuses its
// scratch calendar (which is how it stops spending calendar quota, §18
// row 36) cannot reuse ids. Base 32 in Go's strconv is exactly
// base32hex's alphabet, "0123456789abcdefghijklmnopqrstuv", so a
// formatted integer is a legal id fragment by construction.
var (
	runSuffix = strconv.FormatInt(time.Now().UnixNano(), 32)

	timedID     = "livecaltimedprobe" + runSuffix
	allDayID    = "livecalfulldateprobe" + runSuffix
	weeklyID    = "livecalrepeatprobe" + runSuffix
	cancelledID = "livecalcancelledprobe" + runSuffix
)

const apiBase = "https://www.googleapis.com/calendar/v3"

// liveAPI does the driver's own setup and teardown, directly against
// REST. The server's tool surface has no writes in phase 0, and giving
// the driver its own keeps that surface honest.
type liveAPI struct {
	hc *http.Client
	ts oauth2.TokenSource
	// recordedScopes is what the profile stored at login. Spike G does
	// not use it: see liveScopes.
	recordedScopes []string
}

func newAPI(ctx context.Context, profile string) (*liveAPI, error) {
	if profile == "" {
		profile = userconfig.DefaultProfile
	}
	tokenPath, err := userconfig.TokenFilePath(profile)
	if err != nil {
		return nil, err
	}
	uc, err := userconfig.Load(profile)
	if err != nil {
		return nil, err
	}
	store := &credentials.Store{
		Profile: profile, Keyring: credentials.OSKeyring(), FilePath: tokenPath,
		ExpectKeyring: uc.TokenStore == string(credentials.SourceKeyring),
	}
	refresh, _, err := store.Resolve()
	if err != nil {
		return nil, err
	}
	secret, err := userconfig.ResolveClientSecretPath(profile)
	if err != nil {
		return nil, err
	}
	// The scopes come from what the profile RECORDED at login, not from
	// what this driver would like. A refresh exchange cannot widen a
	// grant, so asking for more here would be a lie in the config and
	// would hide the very thing spike G measures.
	oc, err := auth.LoadClientSecret(secret, uc.Scopes)
	if err != nil {
		return nil, err
	}
	ts := auth.TokenSource(ctx, oc, refresh, 60*time.Second)
	hc := oauth2.NewClient(ctx, ts)
	hc.Timeout = 60 * time.Second
	return &liveAPI{hc: hc, ts: ts, recordedScopes: uc.Scopes}, nil
}

func (a *liveAPI) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, r)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s failed", method, path)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		// The body can carry the calendar's title; the caller prints
		// through the redactor, which handles it.
		return fmt.Errorf("%s %s returned %d: %s", method, path, resp.StatusCode, string(data))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (a *liveAPI) createScratchCalendar(ctx context.Context) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := a.do(ctx, http.MethodPost, "/calendars", map[string]any{
		"summary":     scratchTitle,
		"description": "Created by the google-calendar-mcp live driver. Safe to delete.",
		"timeZone":    scratchZone,
	}, &out)
	if err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("Google returned no calendar id")
	}
	return out.ID, nil
}

// ensureScratchCalendar adopts the driver's own scratch calendar if one
// is already there, and creates one otherwise.
//
// Creating one per run is what phase 1 did, and it is what spent this
// account's calendar quota: the limit counts calendars CREATED and
// deleting them does not refund it (§18 row 36). Phase 2 needs the most
// live runs of any phase, so a run that can reuse `-keep`'s leftover
// costs the quota nothing.
//
// The lookup reads the account's calendar list, which is the one place
// this driver looks past what it wrote. It matches its own title and
// returns an id; nothing from the list is printed, and §9.1's rule about
// the transcript is enforced separately, on what steps may show.
func (a *liveAPI) ensureScratchCalendar(ctx context.Context) (id string, created bool, err error) {
	found, err := a.findScratchCalendar(ctx)
	if err != nil {
		return "", false, err
	}
	if found != "" {
		// Somebody else's run left it, so it may still hold their
		// events, and the seed ids are fixed: a duplicate id is a 409.
		if err := a.clearEvents(ctx, found); err != nil {
			return "", false, fmt.Errorf("adopting the scratch calendar: %w", err)
		}
		return found, false, nil
	}
	id, err = a.createScratchCalendar(ctx)
	return id, true, err
}

// findScratchCalendar returns the id of a calendar this driver made, or
// an empty string.
func (a *liveAPI) findScratchCalendar(ctx context.Context) (string, error) {
	var out struct {
		Items []struct {
			ID      string `json:"id"`
			Summary string `json:"summary"`
		} `json:"items"`
		NextPageToken string `json:"nextPageToken"`
	}
	path := "/users/me/calendarList?maxResults=250&showHidden=true"
	for {
		out.Items, out.NextPageToken = nil, ""
		if err := a.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return "", err
		}
		for _, it := range out.Items {
			if it.Summary == scratchTitle {
				return it.ID, nil
			}
		}
		if out.NextPageToken == "" {
			return "", nil
		}
		path = "/users/me/calendarList?maxResults=250&showHidden=true&pageToken=" + out.NextPageToken
	}
}

// clearEvents empties a calendar this driver owns, so an adopted one
// seeds as cleanly as a fresh one. calendars.clear is not usable here:
// it only works on the primary calendar, which this driver never writes.
func (a *liveAPI) clearEvents(ctx context.Context, cal string) error {
	// Bounded, because the exit condition depends on Google agreeing
	// that what was deleted is gone. A driver that spins forever on a
	// calendar it cannot empty is worse than one that says so.
	for pass := 0; pass < 20; pass++ {
		var out struct {
			Items []struct {
				ID string `json:"id"`
			} `json:"items"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := a.do(ctx, http.MethodGet,
			"/calendars/"+cal+"/events?maxResults=250&showDeleted=false", nil, &out); err != nil {
			return err
		}
		if len(out.Items) == 0 {
			return nil
		}
		for _, it := range out.Items {
			// A 410 means it is already gone, which is the state wanted.
			if derr := a.do(ctx, http.MethodDelete,
				"/calendars/"+cal+"/events/"+it.ID+"?sendUpdates=none", nil, nil); derr != nil &&
				!strings.Contains(derr.Error(), "returned 410") &&
				!strings.Contains(derr.Error(), "returned 404") {
				return derr
			}
		}
	}
	return fmt.Errorf("the scratch calendar still lists events after 20 passes; empty it by hand")
}

// insertEvent posts one event and returns Google's answer.
//
// sendUpdates=none is correct for every event this driver writes: they
// carry no guests, so nothing can be sent, and saying so keeps the
// driver from ever mailing a real person (§9.1).
func (a *liveAPI) insertEvent(ctx context.Context, cal string, body map[string]any) error {
	if id, ok := body["id"].(string); ok {
		if err := gcal.ValidEventID(id); err != nil {
			return err
		}
	}
	return a.do(ctx, http.MethodPost, "/calendars/"+cal+"/events?sendUpdates=none", body, nil)
}

// insertWithUpdates posts one event under an explicit sendUpdates value.
//
// The only place in this driver that may mail a real person, which is
// why the value is a required argument rather than a default: every
// other write goes through insertEvent, which hardcodes none because
// those events have no guests at all.
func (a *liveAPI) insertWithUpdates(ctx context.Context, cal string, body map[string]any, updates string) error {
	if id, ok := body["id"].(string); ok {
		if err := gcal.ValidEventID(id); err != nil {
			return err
		}
	}
	return a.do(ctx, http.MethodPost,
		"/calendars/"+cal+"/events?sendUpdates="+updates, body, nil)
}

// patchEvent patches one event. Patch, never PUT (§4.4).
func (a *liveAPI) patchEvent(ctx context.Context, cal, id string, body map[string]any) error {
	return a.do(ctx, http.MethodPatch,
		"/calendars/"+cal+"/events/"+id+"?sendUpdates=none", body, nil)
}

// getEvent reads one event back.
func (a *liveAPI) getEvent(ctx context.Context, cal, id string) (instanceRow, error) {
	var row instanceRow
	err := a.do(ctx, http.MethodGet, "/calendars/"+cal+"/events/"+id, nil, &row)
	return row, err
}

func (a *liveAPI) deleteCalendar(ctx context.Context, id string) error {
	return a.do(ctx, http.MethodDelete, "/calendars/"+id, nil, nil)
}

// createFillerCalendars makes n real, readable, empty calendars, and
// returns what it managed to create even when it then fails.
//
// Only spike I needs these, and only to answer whether the free/busy
// ceiling counts calendars it can actually expand. They are the driver's
// own, so §9.1 holds, but 51 calendars on somebody's real account is not
// something to do by default — `-spike-ceiling` gates it, and the caller
// deletes what comes back whatever the error.
func (a *liveAPI) createFillerCalendars(ctx context.Context, n int) ([]string, error) {
	ids := make([]string, 0, n)
	for i := range n {
		var out struct {
			ID string `json:"id"`
		}
		err := a.do(ctx, http.MethodPost, "/calendars", map[string]any{
			"summary":     fmt.Sprintf("%s ceiling %02d", scratchTitle, i),
			"description": "Created by the google-calendar-mcp live driver. Safe to delete.",
			"timeZone":    scratchZone,
		}, &out)
		if err != nil {
			return ids, err
		}
		if out.ID == "" {
			return ids, fmt.Errorf("Google returned no calendar id")
		}
		ids = append(ids, out.ID)
	}
	return ids, nil
}

type seedEvent struct {
	id    string
	body  map[string]any
	after func(ctx context.Context, a *liveAPI, cal string) error
}

func seedEvents() []seedEvent {
	zoned := func(s string) map[string]any {
		return map[string]any{"dateTime": s, "timeZone": scratchZone}
	}
	return []seedEvent{
		{
			id: timedID,
			body: map[string]any{
				"id": timedID, "summary": timedTitle,
				"start": zoned(timedStart), "end": zoned("2026-03-16T10:00:00+01:00"),
			},
		},
		{
			// An all-day event: a DATE at both ends, and the end is
			// exclusive. No zone, because there is no instant to place.
			id: allDayID,
			body: map[string]any{
				"id": allDayID, "summary": allDayTitle,
				"start": map[string]any{"date": allDayDate},
				"end":   map[string]any{"date": "2026-03-21"},
			},
		},
		{
			// A weekly series crossing the 29 March European transition,
			// carrying its zone so the wall clock holds.
			id: weeklyID,
			body: map[string]any{
				"id": weeklyID, "summary": weeklyTitle,
				"start":      zoned("2026-03-17T14:00:00+01:00"),
				"end":        zoned("2026-03-17T15:00:00+01:00"),
				"recurrence": []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"},
			},
		},
		{
			id: cancelledID,
			body: map[string]any{
				"id": cancelledID, "summary": cancelledTitle,
				"start": zoned("2026-03-18T11:00:00+01:00"),
				"end":   zoned("2026-03-18T12:00:00+01:00"),
			},
			after: func(ctx context.Context, a *liveAPI, cal string) error {
				return a.do(ctx, http.MethodDelete, "/calendars/"+cal+"/events/"+cancelledID, nil, nil)
			},
		},
	}
}

func (a *liveAPI) seed(ctx context.Context, cal string) error {
	for _, e := range seedEvents() {
		if err := gcal.ValidEventID(e.id); err != nil {
			return err
		}
		// sendUpdates=none is correct here and nowhere else: these events
		// have no guests, so nothing can be sent, and saying so keeps the
		// driver from ever mailing a real person.
		if err := a.do(ctx, http.MethodPost,
			"/calendars/"+cal+"/events?sendUpdates=none", e.body, nil); err != nil {
			return err
		}
		if e.after != nil {
			if err := e.after(ctx, a, cal); err != nil {
				return err
			}
		}
	}
	return nil
}

// liveScopes asks Google what the current access token actually carries.
//
// The profile records what login was told at the time; this is what the
// token has now. Spike G turns on the difference.
func (a *liveAPI) liveScopes(ctx context.Context) ([]string, error) {
	tok, err := a.ts.Token()
	if err != nil {
		return nil, err
	}
	info, err := auth.Inspect(ctx, nil, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	return info.Scopes, nil
}

// listACL is spike G's probe: acl.list is not covered by
// calendar.readonly (§2.15).
func (a *liveAPI) listACL(ctx context.Context, cal string) error {
	return a.do(ctx, http.MethodGet, "/calendars/"+cal+"/acl", nil, nil)
}

// ---------------------------------------------------- phase 1 additions

// Ids for the probes phase 1 and 2 add, generated per run for the reason
// the id block above gives: a deleted id is not released.
var (
	// unzonedID is spike C's negative half: a weekly series written with
	// no timeZone alongside its dateTime.
	unzonedID = "livecalnozoneprobe" + runSuffix
	// Spike E's series: a weekly run long enough to have a target with
	// occurrences on both sides of it, in May so it cannot collide with
	// the March window every step reads.
	spikeESeriesID = "livecaltrailingprobe" + runSuffix
	spikeENewID    = "livecaltrailingsecond" + runSuffix
	// Spike F's id: one client-generated id, sent twice at once.
	spikeFID = "livecalduplicateprobe" + runSuffix

	// Spike A makes three events, one per sendUpdates value; spike B one.
	spikeAIDBase = "livecalnotifiprobe" + runSuffix
	spikeBID     = "livecallosseventprobe" + runSuffix
)

const (
	// unzonedTitle is what spike C's series is called, so a step can
	// find it.
	unzonedTitle = "Livecal unzoned probe"

	spikeETitle = "Livecal this-and-following probe"
	spikeEStart = "2026-05-05T10:00:00+02:00"
	spikeEEnd   = "2026-05-05T11:00:00+02:00"
	spikeERule  = "RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=8"

	spikeFTitle = "Livecal duplicate-insert probe"

	spikeATitle = "Livecal notification probe"
	spikeBTitle = "Livecal none-on-insert probe"

	// noSuchCalendar is spike H's target: a calendar that cannot exist.
	//
	// The domain is .test, which RFC 2606 reserves and which can never
	// resolve — not a google.com address shaped like a real secondary
	// calendar. The leak gate refused the first version of this line for
	// exactly that reason, and it was right to: an invented id that
	// looks real is indistinguishable, to every later reader and every
	// scanner, from one that is.
	noSuchCalendar = "livecal-no-such-calendar@example.test"

	// unknownCalendarRef is the reference the "unknown calendar refused"
	// step passes. It is not an id at all, which is the point: the
	// server must refuse it rather than resolve it to something.
	unknownCalendarRef = "no-such-calendar-here"
)

// instanceRow is one occurrence as Google returns it.
type instanceRow struct {
	ID    string `json:"id"`
	Start struct {
		DateTime string `json:"dateTime"`
		TimeZone string `json:"timeZone"`
	} `json:"start"`
	Status string `json:"status"`
}

// listInstances reads a series' occurrences directly, for the setup that
// needs a real instance id rather than one composed from a convention.
//
// The id format is not documented, so composing one would be adopting a
// convention on a reference page's silence — which §18 says not to do.
func (a *liveAPI) listInstances(ctx context.Context, cal, event string) ([]instanceRow, error) {
	var out struct {
		Items []instanceRow `json:"items"`
	}
	err := a.do(ctx, http.MethodGet,
		"/calendars/"+cal+"/events/"+event+"/instances?maxResults=10", nil, &out)
	return out.Items, err
}

// cancelInstance removes one occurrence from a series, which is what a
// cancelled instance is.
func (a *liveAPI) cancelInstance(ctx context.Context, cal, instance string) error {
	return a.do(ctx, http.MethodDelete,
		"/calendars/"+cal+"/events/"+instance+"?sendUpdates=none", nil, nil)
}

// createUnzonedSeries writes a recurring event whose start carries a
// dateTime and NO timeZone. §2.2 says the zone is required on a
// recurring event; whether Google refuses it, or accepts it and lets the
// series drift across a transition, is spike C's question.
func (a *liveAPI) createUnzonedSeries(ctx context.Context, cal string) error {
	return a.do(ctx, http.MethodPost, "/calendars/"+cal+"/events?sendUpdates=none", map[string]any{
		"id": unzonedID, "summary": unzonedTitle,
		"start":      map[string]any{"dateTime": "2026-03-17T14:00:00+01:00"},
		"end":        map[string]any{"dateTime": "2026-03-17T15:00:00+01:00"},
		"recurrence": []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"},
	}, nil)
}

// freeBusy asks about a list of calendars directly, which is how spike I
// can send 51: the server batches at 50 and would never produce the
// request the spike is about.
func (a *liveAPI) freeBusy(ctx context.Context, ids []string, expansionMax int) (map[string]gcal.FreeBusyCalendar, error) {
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, map[string]any{"id": id})
	}
	body := map[string]any{
		"timeMin": "2026-03-16T00:00:00Z", "timeMax": "2026-03-17T00:00:00Z",
		"items": items,
	}
	if expansionMax > 0 {
		body["calendarExpansionMax"] = expansionMax
	}
	var out gcal.FreeBusyResponse
	err := a.do(ctx, http.MethodPost, "/freeBusy", body, &out)
	return out.Calendars, err
}

// seedState is what filling the scratch calendar learned. The steps
// assert against facts only the setup can know — an instance id is
// Google's to invent, so a step cannot hardcode one.
type seedState struct {
	// cancelledOccurrence is the date of the occurrence removed from the
	// weekly series, as Google returned it.
	cancelledOccurrence string
}

// removeOneOccurrence cancels the second occurrence of the weekly
// series, which is how a single date leaves a series.
//
// It reads the instances first rather than composing an instance id from
// the series id and a timestamp: that format is not documented, and §18
// says a convention gets verified before it is adopted.
func (a *liveAPI) removeOneOccurrence(ctx context.Context, cal string) (string, error) {
	rows, err := a.listInstances(ctx, cal, weeklyID)
	if err != nil {
		return "", err
	}
	if len(rows) < 2 {
		return "", fmt.Errorf("the weekly series expanded to %d occurrences; expected at least 2", len(rows))
	}
	target := rows[1]
	if err := a.cancelInstance(ctx, cal, target.ID); err != nil {
		return "", err
	}
	if len(target.Start.DateTime) < 10 {
		return "", fmt.Errorf("the cancelled occurrence carried no start date")
	}
	return target.Start.DateTime[:10], nil
}
