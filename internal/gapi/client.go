// Package gapi is the raw REST client for the Calendar API v3.
//
// Raw, because §4.8 forbids the generated client. Every request is built
// here, every response is decoded into internal/gcal's hand-written
// types, and every failure comes back as a classified *Error.
package gapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
)

// BaseURL is the API root. A var so caltest can point it at a fake.
var BaseURL = "https://www.googleapis.com/calendar/v3"

// Client speaks to the Calendar API.
type Client struct {
	HTTP *http.Client
	// Base overrides BaseURL for this client (tests).
	Base string
	// UserAgent identifies the server to Google.
	UserAgent string
	// MaxRetries bounds the truncated exponential backoff of §11.
	MaxRetries int
	// Sleep is the backoff sleep, injectable so tests do not wait.
	Sleep func(context.Context, time.Duration) error
	// Now supplies the clock for jitter; nil uses time.Now.
	Now func() time.Time
}

// New builds a Client with the defaults §11 fixes.
func New(hc *http.Client) *Client {
	return &Client{HTTP: hc, MaxRetries: 4, UserAgent: "google-calendar-mcp"}
}

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return BaseURL
}

// MaxBackoff caps the wait, as Google's own algorithm does.
const MaxBackoff = 32 * time.Second

// backoff is min((2^n) + jitter, MaxBackoff), which is the algorithm
// Google's quota guide publishes.
func backoff(attempt int) time.Duration {
	d := time.Duration(1<<uint(attempt)) * time.Second
	if d > MaxBackoff {
		d = MaxBackoff
	}
	// Jitter spreads retries so a fleet does not resynchronise on the
	// same second. It is a scheduling nicety, not a secret, so a
	// cryptographic source would cost entropy for nothing.
	return d + time.Duration(rand.IntN(1000))*time.Millisecond //nolint:gosec // jitter, not a secret
}

func (c *Client) sleep(ctx context.Context, d time.Duration) error {
	if c.Sleep != nil {
		return c.Sleep(ctx, d)
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// request describes one call.
type request struct {
	method string
	path   string
	query  url.Values
	body   any
	// etag, when set, becomes If-Match. "*" forces the write through
	// (§4.4) and is never the default.
	etag string
	// idempotent says this call may be retried after an ambiguous
	// failure. It is set per call site rather than derived from the
	// method, because §11 has exceptions in both directions:
	// freebusy.query is a POST that is safe, and events.move is a POST
	// that is not.
	idempotent bool
}

// do performs a request with retries and decodes into out.
func (c *Client) do(ctx context.Context, r request, out any) error {
	var last error
	attempts := c.MaxRetries
	if attempts < 0 {
		attempts = 0
	}
	for attempt := 0; ; attempt++ {
		err := c.once(ctx, r, out)
		if err == nil {
			return nil
		}
		last = err

		cls, ok := ClassOf(err)
		if !ok || !cls.Retryable() || !r.idempotent || attempt >= attempts {
			return err
		}
		if werr := c.sleep(ctx, backoff(attempt)); werr != nil {
			return last
		}
	}
}

func (c *Client) once(ctx context.Context, r request, out any) error {
	u := c.base() + r.path
	if len(r.query) > 0 {
		u += "?" + r.query.Encode()
	}

	var body io.Reader
	if r.body != nil {
		buf, err := json.Marshal(r.body)
		if err != nil {
			return Wrap(ClassInvalid, err, "could not encode the request")
		}
		body = bytes.NewReader(buf)
	}

	// Counted here rather than at the call sites: every request this
	// client makes passes through, retries included, so a result's
	// api_requests cannot drift from what Google was actually asked.
	count(ctx)

	req, err := http.NewRequestWithContext(ctx, r.method, u, body)
	if err != nil {
		// Never fmt the error: it carries the URL, and the URL carries
		// the query string (§9).
		return Errf(ClassInvalid, "could not build the request: %s", stripURL(err))
	}
	if r.body != nil {
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	}
	if r.etag != "" {
		req.Header.Set("If-Match", r.etag)
	}
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	req.Header.Set("Accept", "application/json")

	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return classifyTransport(err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 1 MiB is generous for a page of events and small enough that a
	// runaway response cannot exhaust the process.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Wrap(ClassUnavailable, err, "could not read Google's response")
	}

	if resp.StatusCode >= 300 {
		return decodeError(resp.StatusCode, data)
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return Wrap(ClassUnavailable, err, "Google's response was not the JSON this server expected")
	}
	return nil
}

func decodeError(status int, data []byte) *Error {
	var ge googleError
	_ = json.Unmarshal(data, &ge)

	reason, message := "", strings.TrimSpace(ge.Error.Message)
	if len(ge.Error.Errors) > 0 {
		reason = ge.Error.Errors[0].Reason
	}
	if message == "" {
		message = strings.TrimSpace(string(data))
	}
	if message == "" {
		message = http.StatusText(status)
	}

	cls, msg := classify(status, reason, message)
	return &Error{Class: cls, Message: msg, Status: status, Reason: reason}
}

// ---------------------------------------------------------------- reads

// ListCalendars returns one page of the subscribed calendar list.
func (c *Client) ListCalendars(ctx context.Context, pageToken string, showHidden bool) (*gcal.CalendarList, error) {
	q := url.Values{}
	q.Set("maxResults", "250")
	if showHidden {
		q.Set("showHidden", "true")
	}
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	var out gcal.CalendarList
	if err := c.do(ctx, request{
		method: http.MethodGet, path: "/users/me/calendarList", query: q, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCalendar returns the calendar resource.
func (c *Client) GetCalendar(ctx context.Context, calendarID string) (*gcal.Calendar, error) {
	var out gcal.Calendar
	if err := c.do(ctx, request{
		method: http.MethodGet, path: "/calendars/" + esc(calendarID), idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetCalendarListEntry returns this user's subscription to a calendar,
// which is where the per-user overrides live.
func (c *Client) GetCalendarListEntry(ctx context.Context, calendarID string) (*gcal.CalendarListEntry, error) {
	var out gcal.CalendarListEntry
	if err := c.do(ctx, request{
		method: http.MethodGet, path: "/users/me/calendarList/" + esc(calendarID), idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// EventsListOptions are the query parameters events.list accepts that
// this server uses.
type EventsListOptions struct {
	TimeMin string
	TimeMax string
	// SingleEvents expands a series into instances. §2.9: this changes
	// what the method returns, and orderBy=startTime requires it.
	SingleEvents bool
	OrderBy      string
	Query        string
	MaxResults   int
	PageToken    string
	ShowDeleted  bool
	TimeZone     string
	EventTypes   []string
	ICalUID      string
	UpdatedMin   string
}

// ListEvents returns one page of events.
func (c *Client) ListEvents(ctx context.Context, calendarID string, o EventsListOptions) (*gcal.EventList, error) {
	q := url.Values{}
	if o.TimeMin != "" {
		q.Set("timeMin", o.TimeMin)
	}
	if o.TimeMax != "" {
		q.Set("timeMax", o.TimeMax)
	}
	if o.SingleEvents {
		q.Set("singleEvents", "true")
	}
	if o.OrderBy != "" {
		q.Set("orderBy", o.OrderBy)
	}
	if o.Query != "" {
		q.Set("q", o.Query)
	}
	if o.MaxResults > 0 {
		q.Set("maxResults", itoa(o.MaxResults))
	}
	if o.PageToken != "" {
		q.Set("pageToken", o.PageToken)
	}
	if o.ShowDeleted {
		q.Set("showDeleted", "true")
	}
	if o.TimeZone != "" {
		q.Set("timeZone", o.TimeZone)
	}
	if o.ICalUID != "" {
		q.Set("iCalUID", o.ICalUID)
	}
	if o.UpdatedMin != "" {
		q.Set("updatedMin", o.UpdatedMin)
	}
	for _, t := range o.EventTypes {
		q.Add("eventTypes", t)
	}
	var out gcal.EventList
	if err := c.do(ctx, request{
		method: http.MethodGet, path: "/calendars/" + esc(calendarID) + "/events", query: q, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetEvent returns one event.
func (c *Client) GetEvent(ctx context.Context, calendarID, eventID string) (*gcal.Event, error) {
	var out gcal.Event
	if err := c.do(ctx, request{
		method:     http.MethodGet,
		path:       "/calendars/" + esc(calendarID) + "/events/" + esc(eventID),
		idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListInstances expands one recurring series.
func (c *Client) ListInstances(ctx context.Context, calendarID, eventID string, o EventsListOptions) (*gcal.EventList, error) {
	q := url.Values{}
	if o.TimeMin != "" {
		q.Set("timeMin", o.TimeMin)
	}
	if o.TimeMax != "" {
		q.Set("timeMax", o.TimeMax)
	}
	if o.MaxResults > 0 {
		q.Set("maxResults", itoa(o.MaxResults))
	}
	if o.PageToken != "" {
		q.Set("pageToken", o.PageToken)
	}
	if o.ShowDeleted {
		q.Set("showDeleted", "true")
	}
	if o.TimeZone != "" {
		q.Set("timeZone", o.TimeZone)
	}
	var out gcal.EventList
	if err := c.do(ctx, request{
		method: http.MethodGet,
		path:   "/calendars/" + esc(calendarID) + "/events/" + esc(eventID) + "/instances",
		query:  q, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListACL returns the sharing rules on a calendar.
//
// Needs calendar.acls or calendar.acls.readonly; calendar.readonly does
// NOT cover it (§2.15).
func (c *Client) ListACL(ctx context.Context, calendarID, pageToken string) (*gcal.Acl, error) {
	q := url.Values{}
	q.Set("maxResults", "250")
	if pageToken != "" {
		q.Set("pageToken", pageToken)
	}
	var out gcal.Acl
	if err := c.do(ctx, request{
		method: http.MethodGet, path: "/calendars/" + esc(calendarID) + "/acl", query: q, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// QueryFreeBusy asks which intervals are busy.
//
// A POST that is retried, and the exception is deliberate: it creates
// nothing and changes nothing, so §11's "repeatability follows the HTTP
// method" rule is overridden here with the reason stated at the call
// site, as that rule requires.
func (c *Client) QueryFreeBusy(ctx context.Context, req *gcal.FreeBusyRequest) (*gcal.FreeBusyResponse, error) {
	var out gcal.FreeBusyResponse
	if err := c.do(ctx, request{
		method: http.MethodPost, path: "/freeBusy", body: req, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListSettings returns the user's settings, which is where §4.1's last
// resort — the user's own time zone — lives.
func (c *Client) ListSettings(ctx context.Context) (*gcal.Settings, error) {
	q := url.Values{}
	q.Set("maxResults", "250")
	var out gcal.Settings
	if err := c.do(ctx, request{
		method: http.MethodGet, path: "/users/me/settings", query: q, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetColors returns the colour palette, so a result can name a colour
// instead of printing an id.
func (c *Client) GetColors(ctx context.Context) (*gcal.Colors, error) {
	var out gcal.Colors
	if err := c.do(ctx, request{
		method: http.MethodGet, path: "/colors", idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// --------------------------------------------------------------- writes

// The four write methods share three rules, held at the call sites
// below rather than in a comment on each:
//
//   - sendUpdates is sent only when the caller made a choice. §4.3.2
//     says a write that reaches nobody has no notification decision to
//     make, and inventing one would put this server's guess where
//     Google's inconsistent default already is (§2.5).
//   - If-Match carries the etag from the read that produced the plan.
//     A 412 becomes [stale] and is never retried: a retry applies the
//     caller's intent to a resource somebody else has since changed.
//   - Repeatability follows §11 and the exceptions are named. PATCH and
//     DELETE are retried. events.insert and events.move are POSTs that
//     are not.

// InsertEvent creates an event (§2.11).
//
// Never retried, and this is the one the rule exists for: a
// client-supplied id makes the insert NEARLY idempotent, and Google
// declines to guarantee the collision is caught, so a retry can
// double-book. The service reports [ambiguous_outcome] with the id
// instead, so a caller can look rather than guess.
func (c *Client) InsertEvent(ctx context.Context, calendarID string, e *gcal.Event, sendUpdates string) (*gcal.Event, error) {
	q := url.Values{}
	if sendUpdates != "" {
		q.Set("sendUpdates", sendUpdates)
	}
	if len(e.ConferenceData) > 0 {
		// Without this the request succeeds and the conference is
		// SILENTLY DROPPED: the discovery document says version 0 —
		// the default — "ignores conference data in the event's body".
		// So it is set here, from the body, rather than passed in by
		// each caller: a 200 with no meeting link and no error is
		// exactly the failure this server is built to refuse.
		q.Set("conferenceDataVersion", "1")
	}
	var out gcal.Event
	if err := c.do(ctx, request{
		method: http.MethodPost, path: "/calendars/" + esc(calendarID) + "/events",
		query: q, body: e, idempotent: false,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchEvent applies a partial update under If-Match (§4.4).
//
// events.update is never called: it is a PUT, and omitting attendees
// there deletes every guest and every RSVP. etag is required by the
// caller's own discipline rather than by this signature — "*" forces the
// write through and is an explicit choice, never a default.
func (c *Client) PatchEvent(ctx context.Context, calendarID, eventID string,
	p *gcal.EventPatch, sendUpdates, etag string,
) (*gcal.Event, error) {
	q := url.Values{}
	if sendUpdates != "" {
		q.Set("sendUpdates", sendUpdates)
	}
	var out gcal.Event
	if err := c.do(ctx, request{
		method: http.MethodPatch,
		path:   "/calendars/" + esc(calendarID) + "/events/" + esc(eventID),
		query:  q, body: p, etag: etag, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteEvent removes an event whole.
//
// Retried, because a repeat of a delete that already landed answers 404
// or 410 rather than removing something else. The service turns that
// into "already gone" rather than a failure.
func (c *Client) DeleteEvent(ctx context.Context, calendarID, eventID, sendUpdates, etag string) error {
	q := url.Values{}
	if sendUpdates != "" {
		q.Set("sendUpdates", sendUpdates)
	}
	return c.do(ctx, request{
		method: http.MethodDelete,
		path:   "/calendars/" + esc(calendarID) + "/events/" + esc(eventID),
		query:  q, etag: etag, idempotent: true,
	}, nil)
}

// MoveEvent changes which calendar an event belongs to.
//
// A POST that is not idempotent and is not retried (§11): the second
// call cannot tell "the move did not happen" from "the move happened and
// the event is no longer here".
//
// It DOES carry If-Match. That is not obvious from anything Google
// publishes — events.move is a POST with no body, and neither the
// reference nor the discovery document says the header applies — so this
// server sent none and told callers the protection was absent. Spike J
// asked: a stale etag is refused with 412 (§18 row 49). §4.4 has no
// exception after all.
func (c *Client) MoveEvent(ctx context.Context, calendarID, eventID, destination, sendUpdates, etag string) (*gcal.Event, error) {
	q := url.Values{}
	q.Set("destination", destination)
	if sendUpdates != "" {
		q.Set("sendUpdates", sendUpdates)
	}
	var out gcal.Event
	if err := c.do(ctx, request{
		method: http.MethodPost,
		path:   "/calendars/" + esc(calendarID) + "/events/" + esc(eventID) + "/move",
		query:  q, etag: etag, idempotent: false,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ------------------------------------------- calendars and the ACL

// The calendar and sharing writes. Three rules run through them, and
// only the third is new:
//
//   - If-Match carries the etag from the read that produced the plan,
//     exactly as on an event. Whether Google honours it here is spike K:
//     until that is answered, the result says the header was sent and
//     does not claim it protected anything.
//   - sendNotifications is sent on EVERY acl.insert and acl.patch, true
//     or false, never left out. Google's default is true (§2.5), so
//     omitting it is a decision to email somebody.
//   - acl.delete takes no sendNotifications at all, and the discovery
//     document says there are no notifications on access removal. The
//     result says so rather than implying a choice existed.

// InsertCalendar creates a secondary calendar (calendars.insert).
//
// Never retried: it creates, and unlike events.insert there is no
// client-supplied id to make a repeat collide rather than duplicate. A
// failure nobody saw the answer to becomes [ambiguous_outcome] naming
// the title to look for.
func (c *Client) InsertCalendar(ctx context.Context, cal *gcal.Calendar) (*gcal.Calendar, error) {
	var out gcal.Calendar
	if err := c.do(ctx, request{
		method: http.MethodPost, path: "/calendars", body: cal, idempotent: false,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchCalendar changes the calendar itself: what everybody subscribed
// to it sees. calendars.update is a PUT and is never called (§4.4).
func (c *Client) PatchCalendar(ctx context.Context, calendarID string, p *gcal.CalendarPatch, etag string) (*gcal.Calendar, error) {
	var out gcal.Calendar
	if err := c.do(ctx, request{
		method: http.MethodPatch, path: "/calendars/" + esc(calendarID),
		body: p, etag: etag, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCalendar removes a secondary calendar and everything on it.
//
// Google's own description is "Deletes a secondary calendar": the
// primary cannot be deleted, and calendars.clear is what empties one.
func (c *Client) DeleteCalendar(ctx context.Context, calendarID, etag string) error {
	return c.do(ctx, request{
		method: http.MethodDelete, path: "/calendars/" + esc(calendarID),
		etag: etag, idempotent: true,
	}, nil)
}

// ClearCalendar deletes every event on a calendar (calendars.clear).
//
// Retried, and the reason is the end state rather than the verb: a
// repeat of a clear that already landed clears a calendar that is
// already empty, which removes nothing a first call had not. That makes
// it safe in the way a second events.insert is not.
func (c *Client) ClearCalendar(ctx context.Context, calendarID, etag string) error {
	return c.do(ctx, request{
		method: http.MethodPost, path: "/calendars/" + esc(calendarID) + "/clear",
		etag: etag, idempotent: true,
	}, nil)
}

// InsertCalendarListEntry subscribes this user to an existing calendar.
//
// It adds the calendar to one person's list. It does not create a
// calendar and it grants nobody access: an account that cannot read the
// calendar gets a subscription it cannot use.
func (c *Client) InsertCalendarListEntry(ctx context.Context, e *gcal.CalendarListEntry) (*gcal.CalendarListEntry, error) {
	var out gcal.CalendarListEntry
	if err := c.do(ctx, request{
		method: http.MethodPost, path: "/users/me/calendarList", body: e, idempotent: false,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchCalendarListEntry changes this user's own overrides.
//
// No colorRgbFormat, and that is the whole decision about colour on the
// wire: the parameter tells Google to read backgroundColor and
// foregroundColor instead of the indexed colorId, and this server writes
// the indexed one. Sending it would ask Google to read two fields this
// patch never sets.
func (c *Client) PatchCalendarListEntry(ctx context.Context, calendarID string, p *gcal.CalendarListPatch, etag string) (*gcal.CalendarListEntry, error) {
	var out gcal.CalendarListEntry
	if err := c.do(ctx, request{
		method: http.MethodPatch, path: "/users/me/calendarList/" + esc(calendarID),
		body: p, etag: etag, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteCalendarListEntry unsubscribes this user from a calendar. The
// calendar itself is untouched and nobody else notices.
func (c *Client) DeleteCalendarListEntry(ctx context.Context, calendarID, etag string) error {
	return c.do(ctx, request{
		method: http.MethodDelete, path: "/users/me/calendarList/" + esc(calendarID),
		etag: etag, idempotent: true,
	}, nil)
}

// InsertACL grants access to a calendar (acl.insert).
//
// sendNotifications is always sent. Google documents its default as
// true, which means a sharing change emails somebody unless this server
// says otherwise — the opposite of the events default, from the same API
// (§2.5). That inconsistency is why §4.3 makes the choice the caller's.
func (c *Client) InsertACL(ctx context.Context, calendarID string, rule *gcal.AclRule, sendNotifications bool) (*gcal.AclRule, error) {
	q := url.Values{}
	q.Set("sendNotifications", strconv.FormatBool(sendNotifications))
	var out gcal.AclRule
	if err := c.do(ctx, request{
		method: http.MethodPost, path: "/calendars/" + esc(calendarID) + "/acl",
		query: q, body: rule, idempotent: false,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PatchACL changes an existing rule's role (acl.patch).
//
// acl.update is a PUT and would replace the rule, scope included, so it
// is never called (§4.4).
func (c *Client) PatchACL(ctx context.Context, calendarID, ruleID string, p *gcal.AclPatch,
	sendNotifications bool, etag string,
) (*gcal.AclRule, error) {
	q := url.Values{}
	q.Set("sendNotifications", strconv.FormatBool(sendNotifications))
	var out gcal.AclRule
	if err := c.do(ctx, request{
		method: http.MethodPatch,
		path:   "/calendars/" + esc(calendarID) + "/acl/" + esc(ruleID),
		query:  q, body: p, etag: etag, idempotent: true,
	}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteACL removes a rule, taking that access away (acl.delete).
//
// It takes no sendNotifications, and that is not an omission here: the
// method publishes no such parameter, and acl.patch's own description
// says there are no notifications on access removal. Nobody is told they
// lost access; they discover it.
func (c *Client) DeleteACL(ctx context.Context, calendarID, ruleID, etag string) error {
	return c.do(ctx, request{
		method: http.MethodDelete,
		path:   "/calendars/" + esc(calendarID) + "/acl/" + esc(ruleID),
		etag:   etag, idempotent: true,
	}, nil)
}

// esc escapes one path segment. A calendar id is an email address for a
// primary calendar, so the @ and any + must survive as themselves.
func esc(s string) string { return url.PathEscape(s) }

func itoa(n int) string { return fmt.Sprintf("%d", n) }
