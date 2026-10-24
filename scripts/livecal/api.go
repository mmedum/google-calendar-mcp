//go:build live

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/mmedum/google-calendar-mcp/v5/internal/auth"
	"github.com/mmedum/google-calendar-mcp/v5/internal/config"
	"github.com/mmedum/google-calendar-mcp/v5/internal/credentials"
	"github.com/mmedum/google-calendar-mcp/v5/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v5/internal/redact"
	"github.com/mmedum/google-calendar-mcp/v5/internal/userconfig"
)

// The scratch calendar's contents. Everything here is invented: the
// driver reads only what it wrote (§9.1), so nothing from the operator's
// real calendar can reach a transcript.
const (
	scratchTitle  = "google-calendar-mcp live driver scratch"
	timedTitle    = "Livecal timed probe"
	allDayTitle   = "Livecal all-day probe"
	weeklyTitle   = "Livecal weekly probe"
	canceledTitle = "Livecal canceled probe"
	searchTerm    = "Livecal"
	// splitKey and splitValue are the private extended property the
	// weekly series carries into a split.
	splitKey   = "livecalSplit"
	splitValue = "carried"
	// attachTitle is the title the driver gives the file it attaches.
	attachTitle = "Livecal attachment probe"
	// envAttachment names a Drive file the account can open, as its
	// link. The server asks for no Drive scope, so the driver cannot make
	// a file of its own: the person running it makes one for the purpose,
	// with an invented name and nothing in it. Unset, the attachment
	// steps are skipped and owed.
	envAttachment = "GCAL_LIVE_ATTACHMENT"
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

	timedID    = "livecaltimedprobe" + runSuffix
	allDayID   = "livecalfulldateprobe" + runSuffix
	weeklyID   = "livecalrepeatprobe" + runSuffix
	canceledID = "livecalcanceledprobe" + runSuffix
)

// destTitle is the second scratch calendar, and it exists for one tool.
//
// move_event changes which calendar an event lives on, so driving it
// needs somewhere to move to — and that somewhere may not be the
// operator's own calendar, because §9.1 says this driver writes only on
// a calendar it created. So it creates a second one, adopts it on every
// later run exactly as it adopts the first, and moves the event there
// and straight back, leaving the scratch calendar as it found it.
//
// It costs one more of the calendar-creation quota §18 row 36 is about,
// once. That is the price of driving the write path rather than
// exempting it, and §13 is explicit that green gates are not done.
const destTitle = scratchTitle + " destination"

const apiBase = "https://www.googleapis.com/calendar/v3"

// liveAPI does the driver's own setup and teardown, directly against
// REST. The server's tool surface has no writes in phase 0, and giving
// the driver its own keeps that surface honest.
type liveAPI struct {
	hc *http.Client
	ts oauth2.TokenSource
	// base is the API root every path is joined to: apiBase, or a test's
	// fake.
	base string
	// recordedScopes is what the profile stored at login. Spike G does
	// not use it: see liveScopes.
	recordedScopes []string
}

func newAPI(ctx context.Context, profile string) (*liveAPI, error) {
	if profile == "" {
		profile = config.DefaultProfile
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
	return &liveAPI{hc: hc, ts: ts, base: apiBase, recordedScopes: uc.Scopes}, nil
}

func (a *liveAPI) do(ctx context.Context, method, path string, body, out any) error {
	return a.doWithMatch(ctx, method, path, "", body, out)
}

// doWithMatch is do with an If-Match header, for the one spike that has
// to ask whether a method honors it. It also returns the HTTP status,
// because "which status" IS the answer there rather than a detail.
func (a *liveAPI) doWithMatch(ctx context.Context, method, path, ifMatch string, body, out any) error {
	_, err := a.status(ctx, method, path, ifMatch, body, out)
	return err
}

func (a *liveAPI) status(ctx context.Context, method, path, ifMatch string, body, out any) (int, error) {
	// Every REST call passes the same §9.1 guards the tool calls do.
	if err := primaryCal.rest(method, path); err != nil {
		return 0, err
	}
	if err := reach.rest(method, path, body); err != nil {
		return 0, err
	}
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, r)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ifMatch != "" {
		req.Header.Set("If-Match", ifMatch)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s %s failed", method, path)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode >= 300 {
		// The body can carry the calendar's title; the caller prints
		// through the redactor, which handles it.
		return resp.StatusCode, fmt.Errorf("%s %s returned %d: %s",
			method, path, resp.StatusCode, string(data))
	}
	if out != nil && len(data) > 0 {
		return resp.StatusCode, json.Unmarshal(data, out)
	}
	return resp.StatusCode, nil
}

// scratchMarker is written into the description of every scratch
// calendar this driver creates. A calendar is adopted only when it
// carries it and the signed-in account owns it, so a calendar somebody
// else made is never emptied, whatever its title.
const scratchMarker = "[livecal-scratch-calendar]"

func (a *liveAPI) createScratchCalendar(ctx context.Context, title string) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	err := a.do(ctx, http.MethodPost, "/calendars", map[string]any{
		"summary":     title,
		"description": "Created by the google-calendar-mcp live driver. Safe to delete. " + scratchMarker,
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

// ensureScratchCalendar adopts a scratch calendar an earlier run of this
// driver left, and creates one otherwise. kept counts the events an
// adopted calendar still holds because they have guests besides this
// account.
//
// Creating one per run is what phase 1 did, and it is what spent this
// account's calendar quota: the limit counts calendars CREATED and
// deleting them does not refund it (§18 row 36). Phase 2 needs the most
// live runs of any phase, so a run that can reuse `-keep`'s leftover
// costs the quota nothing.
//
// The lookup reads the account's calendar list, which is the one place
// this driver looks past what it wrote. Nothing from the list is
// printed, and §9.1's rule about the transcript is enforced separately,
// on what steps may show.
func (a *liveAPI) ensureScratchCalendar(ctx context.Context, title, self string) (id string, created bool, kept int, err error) {
	found, err := a.findScratchCalendar(ctx, title, self)
	if err != nil {
		return "", false, 0, err
	}
	if found != "" {
		// An earlier run left it, so it may still hold that run's events.
		kept, err := a.clearEvents(ctx, found, self)
		if err != nil {
			return "", false, 0, fmt.Errorf("adopting the scratch calendar: %w", err)
		}
		return found, false, kept, nil
	}
	id, err = a.createScratchCalendar(ctx, title)
	return id, true, 0, err
}

// calendarEntry is the part of a calendar-list entry that decides
// adoption.
type calendarEntry struct {
	ID          string `json:"id"`
	Summary     string `json:"summary"`
	Description string `json:"description"`
	AccessRole  string `json:"accessRole"`
	DataOwner   string `json:"dataOwner"`
}

// driversOwn reports whether e is a scratch calendar this driver made
// for this account: its title, the driver's marker in its description,
// and the signed-in account as both an owner and its data owner. A
// title alone proves nothing, and the owner role alone can be granted
// by somebody else. Any field missing fails closed, which costs a
// calendar from the quota and never somebody else's events.
func (e calendarEntry) driversOwn(title, self string) bool {
	return e.Summary == title && strings.Contains(e.Description, scratchMarker) &&
		e.AccessRole == "owner" && self != "" && strings.EqualFold(e.DataOwner, self)
}

// findScratchCalendar returns the id of a calendar this driver made for
// this account, or an empty string.
func (a *liveAPI) findScratchCalendar(ctx context.Context, title, self string) (string, error) {
	var out struct {
		Items         []calendarEntry `json:"items"`
		NextPageToken string          `json:"nextPageToken"`
	}
	path := "/users/me/calendarList?maxResults=250&showHidden=true"
	for {
		out.Items, out.NextPageToken = nil, ""
		if err := a.do(ctx, http.MethodGet, path, nil, &out); err != nil {
			return "", err
		}
		for _, it := range out.Items {
			if it.driversOwn(title, self) {
				return it.ID, nil
			}
		}
		if out.NextPageToken == "" {
			return "", nil
		}
		path = "/users/me/calendarList?maxResults=250&showHidden=true&pageToken=" +
			url.QueryEscape(out.NextPageToken)
	}
}

// clearEvents empties a scratch calendar this driver made, so an adopted
// one seeds as cleanly as a fresh one, and returns how many events it
// left. calendars.clear is not usable here: it only works on the primary
// calendar, which this driver never writes.
//
// An event with a guest besides this account is left where it is,
// unless -sweep-spikes asked for it. Only an armed run puts one there,
// as spikes A and B do on purpose, and removing it either mails the
// guest or, with sendUpdates=none, leaves the meeting on their calendar
// (§18 row 43). A default run mails nobody, so it does neither.
func (a *liveAPI) clearEvents(ctx context.Context, cal, self string) (kept int, err error) {
	// Bounded, because the exit condition depends on Google agreeing
	// that what was deleted is gone. A driver that spins forever on a
	// calendar it cannot empty is worse than one that says so.
	for pass := 0; pass < 20; pass++ {
		var out struct {
			Items []struct {
				ID        string `json:"id"`
				Attendees []struct {
					Email string `json:"email"`
				} `json:"attendees"`
			} `json:"items"`
		}
		if err := a.do(ctx, http.MethodGet,
			"/calendars/"+cal+"/events?maxResults=250&showDeleted=false", nil, &out); err != nil {
			return 0, err
		}
		deleted := 0
		kept = 0
		for _, it := range out.Items {
			others := false
			for _, at := range it.Attendees {
				if !strings.EqualFold(at.Email, self) {
					others = true
				}
			}
			// With guests, the event is canceled WITH notification: a
			// delete with sendUpdates=none takes it off this calendar
			// and leaves it on theirs.
			updates := "none"
			if others {
				if !clearSpikeEvents {
					kept++
					continue
				}
				updates = "all"
			}
			// A 410 means it is already gone, which is the state wanted.
			if derr := a.do(ctx, http.MethodDelete,
				"/calendars/"+cal+"/events/"+it.ID+"?sendUpdates="+updates, nil, nil); derr != nil &&
				!strings.Contains(derr.Error(), "returned 410") &&
				!strings.Contains(derr.Error(), "returned 404") {
				return 0, derr
			}
			deleted++
		}
		// Nothing left that this run may remove.
		if deleted == 0 {
			return kept, nil
		}
	}
	return 0, fmt.Errorf("the scratch calendar still lists events after 20 passes; empty it by hand")
}

// insertEvent posts one event and returns Google's answer.
//
// sendUpdates=none is correct for every event this driver writes except
// spikes A and B: they carry no guests, so nothing can be sent, and
// saying so keeps the driver from ever mailing a real person (§9.1).
func (a *liveAPI) insertEvent(ctx context.Context, cal string, body map[string]any) error {
	return a.insertWithUpdates(ctx, cal, body, "none")
}

// insertWithUpdates posts one event under an explicit sendUpdates value.
//
// The only path in this driver that may mail a real person, which is why
// the value is a required argument rather than a default.
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

// attachmentURL is the Drive file the attachment steps use, or "".
func attachmentURL() string { return strings.TrimSpace(os.Getenv(envAttachment)) }

// attach puts that file on an event under the driver's own title. The
// server writes no attachment, so the driver does, and does nothing when
// no file was named.
func (a *liveAPI) attach(ctx context.Context, cal, id string) error {
	link := attachmentURL()
	if link == "" {
		return nil
	}
	return a.do(ctx, http.MethodPatch,
		"/calendars/"+cal+"/events/"+id+"?sendUpdates=none&supportsAttachments=true",
		map[string]any{"attachments": []map[string]any{{"fileUrl": link, "title": attachTitle}}}, nil)
}

// privateProperty reads one private extended property off an event. No
// tool shows one, so this is the only way to see whether a split kept
// it.
func (a *liveAPI) privateProperty(ctx context.Context, cal, id, key string) (string, error) {
	var out struct {
		ExtendedProperties struct {
			Private map[string]string `json:"private"`
		} `json:"extendedProperties"`
	}
	err := a.do(ctx, http.MethodGet, "/calendars/"+cal+"/events/"+id, nil, &out)
	return out.ExtendedProperties.Private[key], err
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
			// carrying its zone so the wall clock holds. The private
			// extended property is a field the server does not model,
			// so the split step can check the new series kept it.
			id: weeklyID,
			body: map[string]any{
				"id": weeklyID, "summary": weeklyTitle,
				"start":      zoned("2026-03-17T14:00:00+01:00"),
				"end":        zoned("2026-03-17T15:00:00+01:00"),
				"recurrence": []string{"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=4"},
				"extendedProperties": map[string]any{
					"private": map[string]any{splitKey: splitValue},
				},
			},
			// The attachment, when a file was named, so a read shows it
			// and the split step can check the new series kept it.
			after: func(ctx context.Context, a *liveAPI, cal string) error {
				return a.attach(ctx, cal, weeklyID)
			},
		},
		{
			id: canceledID,
			body: map[string]any{
				"id": canceledID, "summary": canceledTitle,
				"start": zoned("2026-03-18T11:00:00+01:00"),
				"end":   zoned("2026-03-18T12:00:00+01:00"),
			},
			after: func(ctx context.Context, a *liveAPI, cal string) error {
				return a.do(ctx, http.MethodDelete, "/calendars/"+cal+"/events/"+canceledID, nil, nil)
			},
		},
	}
}

// seed fills the calendar, and returns the iCalendar UID Google gave each
// event, by event id, from the insert's own answer.
func (a *liveAPI) seed(ctx context.Context, cal string) (map[string]string, error) {
	uids := map[string]string{}
	for _, e := range seedEvents() {
		if err := gcal.ValidEventID(e.id); err != nil {
			return nil, err
		}
		// sendUpdates=none is correct here and nowhere else: these events
		// have no guests, so nothing can be sent, and saying so keeps the
		// driver from ever mailing a real person.
		var made struct {
			ICalUID string `json:"iCalUID"`
		}
		if err := a.do(ctx, http.MethodPost,
			"/calendars/"+cal+"/events?sendUpdates=none", e.body, &made); err != nil {
			return nil, err
		}
		uids[e.id] = made.ICalUID
		if e.after != nil {
			if err := e.after(ctx, a, cal); err != nil {
				return nil, err
			}
		}
	}
	return uids, nil
}

// primaryAddress is the signed-in account's own calendar id, which is
// its email address (§6.1).
//
// tokeninfo cannot supply it: it returns an email only when an email
// scope was granted, and this server asks for none — which is what phase
// 0's `login` reported as a field that could never populate (§18). The
// primary calendar's id is the same fact, from a scope this server
// already has.
//
// It reaches the transcript only through the redactor, which matches an
// address by shape.
func (a *liveAPI) primaryAddress(ctx context.Context) (string, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := a.do(ctx, http.MethodGet, "/calendars/primary", nil, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("Google returned a primary calendar with no id")
	}
	return out.ID, nil
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

	// runMark goes in those events' titles. They survive the next run's
	// clearing on purpose, so several rounds sit in the same inbox at the
	// same times — and without a marker the reader cannot tell which run
	// an invitation came from, which is how a second round of a spike
	// becomes unreadable.
	runMark = runSuffix[len(runSuffix)-4:]
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
	// ETag is what spike J needs a stale copy of, to ask whether
	// events.move honors If-Match.
	ETag string `json:"etag"`
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
// canceled instance is.
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
	// canceledOccurrence is the date of the occurrence removed from the
	// weekly series, as Google returned it.
	canceledOccurrence string
	// timedUID and weeklyUID are the iCalendar UIDs Google gave the
	// timed event and the weekly series. No tool shows one; a caller
	// holds one from an invitation email.
	timedUID, weeklyUID string
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
		return "", fmt.Errorf("the canceled occurrence carried no start date")
	}
	return target.Start.DateTime[:10], nil
}

// ------------------------------------------------- phase 3's own calls

// countEvents is how spike K tells "cleared" from "accepted and did
// nothing". A 2xx on its own says neither.
func (a *liveAPI) countEvents(ctx context.Context, cal string) (int, error) {
	var out struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := a.do(ctx, http.MethodGet,
		"/calendars/"+cal+"/events?maxResults=250&showDeleted=false", nil, &out); err != nil {
		return 0, err
	}
	return len(out.Items), nil
}

// calendarETag reads the calendar resource's own etag, which is not the
// list entry's: two resources, two etags, and using one where the other
// belongs is a 412 that reads as somebody else's edit.
func (a *liveAPI) calendarETag(ctx context.Context, cal string) (string, error) {
	var out struct {
		ETag string `json:"etag"`
	}
	if err := a.do(ctx, http.MethodGet, "/calendars/"+cal, nil, &out); err != nil {
		return "", err
	}
	return out.ETag, nil
}

func (a *liveAPI) patchCalendar(ctx context.Context, cal string, body map[string]any) error {
	return a.do(ctx, http.MethodPatch, "/calendars/"+cal, body, nil)
}

// probeACLIfMatch asks whether acl.patch honors a stale If-Match.
//
// It creates a rule, moves its etag with a role change, and then patches
// again with the etag it read before that. The rule names an address in
// a domain that cannot resolve and is inserted with
// sendNotifications=false, so nothing is sent and nobody can receive it;
// the rule is removed at the end whatever happens.
//
// Returns the status of the stale write, or a reason it could not be
// asked.
func (a *liveAPI) probeACLIfMatch(ctx context.Context, cal string) (int, string) {
	var rule struct {
		ID   string `json:"id"`
		ETag string `json:"etag"`
	}
	err := a.do(ctx, http.MethodPost, "/calendars/"+cal+"/acl?sendNotifications=false",
		map[string]any{
			"role":  "reader",
			"scope": map[string]any{"type": "user", "value": probeGuest},
		}, &rule)
	if err != nil {
		return 0, "the probe rule could not be created: " + redact.String(err.Error())
	}
	defer func() {
		_ = a.do(context.Background(), http.MethodDelete,
			"/calendars/"+cal+"/acl/"+url.PathEscape(rule.ID), nil, nil)
	}()
	if rule.ETag == "" {
		return 0, "Google returned no etag on the new rule, so there is nothing to go stale"
	}
	stale := rule.ETag

	// Move the etag on.
	var fresh struct {
		ETag string `json:"etag"`
	}
	if err := a.do(ctx, http.MethodPatch,
		"/calendars/"+cal+"/acl/"+url.PathEscape(rule.ID)+"?sendNotifications=false",
		map[string]any{"role": "writer"}, &fresh); err != nil {
		return 0, "the probe rule could not be patched: " + redact.String(err.Error())
	}
	if fresh.ETag == stale {
		return 0, "the rule's etag did not move after a patch, so a stale one cannot be built"
	}

	status, _ := a.status(ctx, http.MethodPatch,
		"/calendars/"+cal+"/acl/"+url.PathEscape(rule.ID)+"?sendNotifications=false",
		stale, map[string]any{"role": "reader"}, nil)
	return status, ""
}
