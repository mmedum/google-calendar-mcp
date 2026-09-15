//go:build live

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/oauth2"

	"github.com/mmedum/google-calendar-mcp/internal/auth"
	"github.com/mmedum/google-calendar-mcp/internal/credentials"
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

	// Event ids are base32hex: lowercase a-v and the digits, 5 to 1024
	// characters (§2.11). Not a-z — the letters w, x, y and z are NOT
	// allowed, which is the kind of detail that reads as obvious and is
	// not. The first run of this driver used "allday" and "weekly" and
	// Google refused both with "Invalid resource id value", naming
	// neither the field nor the rule.
	timedID     = "livecaltimedprobe00000000000001"
	allDayID    = "livecalfulldateprobe000000001"
	weeklyID    = "livecalrepeatprobe0000000000001"
	cancelledID = "livecalcancelledprobe000000001"
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

func (a *liveAPI) deleteCalendar(ctx context.Context, id string) error {
	return a.do(ctx, http.MethodDelete, "/calendars/"+id, nil, nil)
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

// validEventID holds §2.11's rule locally, so a bad id is caught before
// a request is built rather than as Google's "Invalid resource id
// value", which names neither the field nor the constraint.
func validEventID(id string) error {
	if len(id) < 5 || len(id) > 1024 {
		return fmt.Errorf("event id %q is %d characters; Google requires 5 to 1024", id, len(id))
	}
	for i, r := range id {
		ok := (r >= 'a' && r <= 'v') || (r >= '0' && r <= '9')
		if !ok {
			return fmt.Errorf("event id %q has %q at position %d; base32hex allows only a-v and 0-9 "+
				"(not w, x, y or z)", id, r, i)
		}
	}
	return nil
}

func (a *liveAPI) seed(ctx context.Context, cal string) error {
	for _, e := range seedEvents() {
		if err := validEventID(e.id); err != nil {
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
