// Package caltest is an in-memory Google Calendar, served over HTTP, for
// tests.
//
// Everything it returns is GENERATED (§9.1). Nothing here was recorded
// from a real account, and nothing may be: a fixture copied from a live
// response is itself the leak, whatever a secret scanner says about it.
// The names below are invented, the domains are .test (RFC 2606, which
// is reserved and can never resolve), and the ids are synthetic.
//
// It models the parts of the API this server reads, including the parts
// that are easy to get wrong and therefore worth having a fake for: the
// singleEvents split (§2.9), cancelled events being hidden by default
// (§2.13), per-calendar free/busy errors (§4.6) and paging.
package caltest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
)

// Server is an in-memory Calendar.
type Server struct {
	// Calendars, keyed by id.
	Calendars map[string]*gcal.Calendar
	// List entries, keyed by calendar id.
	Entries map[string]*gcal.CalendarListEntry
	// Events, keyed by calendar id then event id.
	Events map[string]map[string]*gcal.Event
	// ACL rules, keyed by calendar id.
	ACL map[string][]gcal.AclRule
	// Busy intervals, keyed by calendar id.
	Busy map[string][]gcal.TimePeriod
	// FreeBusyErrors makes a calendar answer with an error instead of a
	// busy list, which is what §4.6 must distinguish from "free".
	FreeBusyErrors map[string]string
	// FreeBusyOmit leaves a calendar out of the response altogether,
	// which is what a query truncated by calendarExpansionMax looks
	// like: no busy list, no error, no row. It must not read as "free".
	FreeBusyOmit map[string]bool
	// Settings the user has.
	Settings []gcal.Setting

	// PageSize forces paging when set, so a test can prove the client
	// follows nextPageToken.
	PageSize int

	// Fail makes the next matching request fail. Key is "METHOD /path"
	// prefix; value is the status.
	Fail map[string]int
	// Requests records every path served, so a test can assert how many
	// requests a fan-out spent (§11).
	//
	// Guarded by mu: the service fans out across calendars concurrently,
	// so the handler runs on several goroutines at once. Without the
	// lock this field is a data race, and the race detector found it the
	// first time `make check` ran the fan-out test.
	Requests []string

	mu sync.Mutex

	// ACLScopeRequired makes acl.list refuse, which is how §2.15 is
	// exercised offline: calendar.readonly does not cover it.
	ACLScopeRequired bool

	ts *httptest.Server
}

// New returns an empty Server. Use Seed for a populated one.
func New() *Server {
	return &Server{
		Calendars:      map[string]*gcal.Calendar{},
		Entries:        map[string]*gcal.CalendarListEntry{},
		Events:         map[string]map[string]*gcal.Event{},
		ACL:            map[string][]gcal.AclRule{},
		Busy:           map[string][]gcal.TimePeriod{},
		FreeBusyErrors: map[string]string{},
		FreeBusyOmit:   map[string]bool{},
		Fail:           map[string]int{},
	}
}

// Start serves the fake and returns its base URL.
func (s *Server) Start() string {
	s.ts = httptest.NewServer(http.HandlerFunc(s.serve))
	return s.ts.URL
}

// Close shuts the fake down.
func (s *Server) Close() {
	if s.ts != nil {
		s.ts.Close()
	}
}

// AddCalendar registers a calendar and this user's subscription to it.
func (s *Server) AddCalendar(id, summary, tz, role string, primary bool) {
	s.Calendars[id] = &gcal.Calendar{ID: id, Summary: summary, TimeZone: tz, ETag: etag(id, 1)}
	s.Entries[id] = &gcal.CalendarListEntry{
		ID: id, Summary: summary, TimeZone: tz, AccessRole: role,
		Primary: primary, Selected: true, ETag: etag(id, 1),
	}
	if s.Events[id] == nil {
		s.Events[id] = map[string]*gcal.Event{}
	}
}

// AddEvent puts an event on a calendar.
func (s *Server) AddEvent(calendarID string, e *gcal.Event) {
	if s.Events[calendarID] == nil {
		s.Events[calendarID] = map[string]*gcal.Event{}
	}
	if e.ETag == "" {
		e.ETag = etag(e.ID, 1)
	}
	s.Events[calendarID][e.ID] = e
}

// Timed builds a timed event. Start and end are RFC3339; tz is the IANA
// name that goes alongside, as §4.1 requires on every write.
func Timed(id, summary, start, end, tz string) *gcal.Event {
	return &gcal.Event{
		ID: id, Summary: summary, Status: gcal.StatusConfirmed,
		EventType: gcal.EventTypeDefault,
		Start:     &gcal.EventDateTime{DateTime: start, TimeZone: tz},
		End:       &gcal.EventDateTime{DateTime: end, TimeZone: tz},
	}
}

// AllDay builds an all-day event. Note what it does NOT take: a zone.
// An all-day event has no instant, so there is nothing for a zone to do
// (§4.1). A fake that accepted one here would let a test pass that the
// real type system forbids.
func AllDay(id, summary, startDate, endDate string) *gcal.Event {
	return &gcal.Event{
		ID: id, Summary: summary, Status: gcal.StatusConfirmed,
		EventType: gcal.EventTypeDefault,
		Start:     &gcal.EventDateTime{Date: startDate},
		End:       &gcal.EventDateTime{Date: endDate},
	}
}

// Recurring builds a series parent.
func Recurring(id, summary, start, end, tz string, rrule ...string) *gcal.Event {
	e := Timed(id, summary, start, end, tz)
	e.Recurrence = rrule
	return e
}

// Instance builds one occurrence of a series.
func Instance(id, seriesID, summary, start, end, tz, originalStart string) *gcal.Event {
	e := Timed(id, summary, start, end, tz)
	e.RecurringEventID = seriesID
	e.OriginalStartTime = &gcal.EventDateTime{DateTime: originalStart, TimeZone: tz}
	return e
}

// Seed builds a small, invented calendar set covering the shapes this
// server has to get right. It is deliberately not "realistic data": it
// is the minimum that exercises all-day events, a recurrence, a
// cancelled event, a transparent event and a second calendar.
func Seed() *Server {
	s := New()
	const tz = "Europe/Copenhagen"

	s.AddCalendar("primary", "Sample Primary", tz, gcal.RoleOwner, true)
	s.AddCalendar("team@group.calendar.example.test", "Sample Team", "America/Chicago", gcal.RoleWriter, false)
	s.AddCalendar("readonly@group.calendar.example.test", "Sample Readonly", "UTC", gcal.RoleReader, false)

	s.AddEvent("primary", Timed("ev-standup", "Morning sync",
		"2026-03-16T09:00:00+01:00", "2026-03-16T09:15:00+01:00", tz))

	// An all-day event, which is the one every surveyed server gets
	// wrong. End is exclusive: a one-day event ends on the next day.
	s.AddEvent("primary", AllDay("ev-holiday", "Public holiday", "2026-03-20", "2026-03-21"))

	// A weekly series and one expanded instance of it.
	s.AddEvent("primary", Recurring("ev-weekly", "Weekly review",
		"2026-03-17T14:00:00+01:00", "2026-03-17T15:00:00+01:00", tz,
		"RRULE:FREQ=WEEKLY;BYDAY=TU;COUNT=10"))
	s.AddEvent("primary", Instance("ev-weekly_20260324T130000Z", "ev-weekly", "Weekly review",
		"2026-03-24T14:00:00+01:00", "2026-03-24T15:00:00+01:00", tz, "2026-03-24T14:00:00+01:00"))

	// One occurrence moved an hour later, and one cancelled: the two
	// exceptions a series picks up, and the two things list_instances
	// exists to show. A cancelled instance is how a single date is
	// removed, so it is hidden by default like any other cancelled
	// event (§2.13) and the result says it is hiding them.
	s.AddEvent("primary", Instance("ev-weekly_20260331T130000Z", "ev-weekly", "Weekly review",
		"2026-03-31T15:00:00+02:00", "2026-03-31T16:00:00+02:00", tz, "2026-03-31T14:00:00+02:00"))
	dropped := Instance("ev-weekly_20260407T120000Z", "ev-weekly", "Weekly review",
		"2026-04-07T14:00:00+02:00", "2026-04-07T15:00:00+02:00", tz, "2026-04-07T14:00:00+02:00")
	dropped.Status = gcal.StatusCancelled
	s.AddEvent("primary", dropped)

	// Cancelled: hidden unless showDeleted (§2.13).
	cancelled := Timed("ev-cancelled", "Cancelled thing",
		"2026-03-18T10:00:00+01:00", "2026-03-18T11:00:00+01:00", tz)
	cancelled.Status = gcal.StatusCancelled
	s.AddEvent("primary", cancelled)

	// Transparent: on the calendar, but not busy. This is why
	// availability cannot come from an event list (§4.6).
	free := Timed("ev-transparent", "Blocked but free",
		"2026-03-16T13:00:00+01:00", "2026-03-16T14:00:00+01:00", tz)
	free.Transparency = gcal.TransparencyTransparent
	s.AddEvent("primary", free)

	s.AddEvent("team@group.calendar.example.test", Timed("ev-team", "Team planning",
		"2026-03-16T15:00:00+01:00", "2026-03-16T16:00:00+01:00", "America/Chicago"))

	s.Busy["primary"] = []gcal.TimePeriod{
		{Start: "2026-03-16T09:00:00Z", End: "2026-03-16T09:15:00Z"},
	}

	s.ACL["primary"] = []gcal.AclRule{
		{ID: "user:owner@example.test", Role: gcal.RoleOwner,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "owner@example.test"}},
		{ID: "user:colleague@example.test", Role: gcal.RoleReader,
			Scope: gcal.AclScope{Type: gcal.ScopeTypeUser, Value: "colleague@example.test"}},
	}

	s.Settings = []gcal.Setting{
		{ID: gcal.SettingTimezone, Value: tz},
		{ID: gcal.SettingWeekStart, Value: "1"},
		{ID: gcal.SettingFormat24Hour, Value: "true"},
	}
	return s
}

func etag(seed string, rev int) string { return fmt.Sprintf(`"%s-%d"`, seed, rev) }

// record notes a served request.
func (s *Server) record(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, path)
}

// Served returns a copy of the requests served so far. Tests read this
// rather than the slice, which the handler goroutines are still writing.
func (s *Server) Served() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.Requests))
	copy(out, s.Requests)
	return out
}

// ---------------------------------------------------------------- serve

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	s.record(r.Method + " " + path)

	for prefix, status := range s.Fail {
		if strings.HasPrefix(r.Method+" "+path, prefix) {
			writeErr(w, status, "forced", "forced failure from caltest")
			return
		}
	}

	switch {
	case path == "/users/me/calendarList" && r.Method == http.MethodGet:
		s.listCalendars(w, r)
	case strings.HasPrefix(path, "/users/me/calendarList/") && r.Method == http.MethodGet:
		s.getEntry(w, trimID(path, "/users/me/calendarList/"))
	case path == "/users/me/settings" && r.Method == http.MethodGet:
		writeJSON(w, gcal.Settings{Items: s.Settings})
	case path == "/colors" && r.Method == http.MethodGet:
		writeJSON(w, palette())
	case path == "/freeBusy" && r.Method == http.MethodPost:
		s.freeBusy(w, r)
	case strings.HasSuffix(path, "/acl") && r.Method == http.MethodGet:
		s.listACL(w, trimID(strings.TrimSuffix(path, "/acl"), "/calendars/"))
	case strings.HasSuffix(path, "/instances") && r.Method == http.MethodGet:
		rest := strings.TrimSuffix(path, "/instances")
		cal, ev := splitEventPath(rest)
		s.listInstances(w, r, cal, ev)
	case strings.Contains(path, "/events/") && r.Method == http.MethodGet:
		cal, ev := splitEventPath(path)
		s.getEvent(w, cal, ev)
	case strings.HasSuffix(path, "/events") && r.Method == http.MethodGet:
		s.listEvents(w, r, trimID(strings.TrimSuffix(path, "/events"), "/calendars/"))
	case strings.HasPrefix(path, "/calendars/") && r.Method == http.MethodGet:
		s.getCalendar(w, trimID(path, "/calendars/"))
	default:
		writeErr(w, http.StatusNotFound, "notFound", "caltest does not serve "+r.Method+" "+path)
	}
}

func (s *Server) listCalendars(w http.ResponseWriter, r *http.Request) {
	ids := make([]string, 0, len(s.Entries))
	for id := range s.Entries {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	showHidden := r.URL.Query().Get("showHidden") == "true"
	var items []gcal.CalendarListEntry
	for _, id := range ids {
		e := s.Entries[id]
		if e.Hidden && !showHidden {
			continue
		}
		items = append(items, *e)
	}

	page, next := s.paginate(len(items), r.URL.Query().Get("pageToken"), r.URL.Query().Get("maxResults"))
	writeJSON(w, gcal.CalendarList{Items: items[page[0]:page[1]], NextPageToken: next})
}

func (s *Server) getCalendar(w http.ResponseWriter, id string) {
	c, ok := s.Calendars[id]
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no calendar with that id")
		return
	}
	writeJSON(w, c)
}

func (s *Server) getEntry(w http.ResponseWriter, id string) {
	e, ok := s.Entries[id]
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "not subscribed to that calendar")
		return
	}
	writeJSON(w, e)
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request, calID string) {
	if _, ok := s.Calendars[calID]; !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no calendar with that id")
		return
	}
	q := r.URL.Query()
	single := q.Get("singleEvents") == "true"
	showDeleted := q.Get("showDeleted") == "true"
	search := strings.ToLower(q.Get("q"))

	var items []gcal.Event
	for _, e := range s.Events[calID] {
		// §2.9: singleEvents decides which of the two shapes comes back.
		// Without it, parents; with it, instances.
		isInstance := e.RecurringEventID != ""
		isParent := len(e.Recurrence) > 0
		// A cancelled instance is the exception to both rules below,
		// and this fake used to hide it where Google does not. The
		// discovery document: "Cancelled instances of recurring events
		// (but not the underlying recurring event) will still be
		// included if showDeleted and singleEvents are both False."
		//
		// Google sends it bare: an id, a status, the series it belongs
		// to and the date it was, with no start and no summary. That is
		// why the server rendered one as a row with no date and no
		// title. The shape is not modelled here; that it arrives at all
		// is what the server has to handle.
		keptCancelledInstance := !single && isInstance && e.Status == gcal.StatusCancelled
		switch {
		case keptCancelledInstance:
			// Returned regardless of showDeleted, which is the point.
		case e.Status == gcal.StatusCancelled && !showDeleted:
			continue // §2.13
		case single && isParent, !single && isInstance:
			continue // §2.9: one shape or the other, never both
		}
		if search != "" && !strings.Contains(strings.ToLower(e.Summary), search) &&
			!strings.Contains(strings.ToLower(e.Description), search) {
			continue
		}
		if tm := q.Get("timeMin"); tm != "" && endsBefore(e, tm) {
			continue
		}
		if tx := q.Get("timeMax"); tx != "" && startsAfter(e, tx) {
			continue
		}
		items = append(items, *e)
	}
	sort.Slice(items, func(i, j int) bool { return startKey(items[i]) < startKey(items[j]) })

	page, next := s.paginate(len(items), q.Get("pageToken"), q.Get("maxResults"))
	cal := s.Calendars[calID]
	writeJSON(w, gcal.EventList{
		Summary: cal.Summary, TimeZone: cal.TimeZone,
		AccessRole: s.Entries[calID].AccessRole,
		Items:      items[page[0]:page[1]], NextPageToken: next,
	})
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request, calID, eventID string) {
	// events.instances wants the SERIES id. A parent with no instances
	// is an empty list; an id that is not a parent at all is not found,
	// which is what a caller passing an occurrence's own id gets.
	parent, ok := s.Events[calID][eventID]
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no event with that id on that calendar")
		return
	}
	q := r.URL.Query()
	showDeleted := q.Get("showDeleted") == "true"

	// An occurrence's own id is not refused: the live driver probed this
	// and Google answered 200, expanding the occurrence the id names
	// (§18). A CANCELLED occurrence expands to nothing, so the answer is
	// an empty list and a success — which is why the server has to
	// recognise the id itself rather than wait to be told.
	if parent.RecurringEventID != "" {
		items := []gcal.Event{}
		if parent.Status != gcal.StatusCancelled || showDeleted {
			items = append(items, *parent)
		}
		writeJSON(w, gcal.EventList{Items: items})
		return
	}
	if len(parent.Recurrence) == 0 {
		// Still a guess, and still unprobed: what Google does for a
		// plain non-recurring event is not documented, and the live
		// driver has not asked. The server's own behaviour does not
		// depend on which it is — it explains the mistake for both 400
		// and 404.
		writeErr(w, http.StatusBadRequest, "invalid", "the requested event is not a recurring event")
		return
	}

	var items []gcal.Event
	for _, e := range s.Events[calID] {
		if e.RecurringEventID != eventID {
			continue
		}
		if e.Status == gcal.StatusCancelled && !showDeleted {
			continue
		}
		if tm := q.Get("timeMin"); tm != "" && endsBefore(e, tm) {
			continue
		}
		if tx := q.Get("timeMax"); tx != "" && startsAfter(e, tx) {
			continue
		}
		items = append(items, *e)
	}
	sort.Slice(items, func(i, j int) bool { return startKey(items[i]) < startKey(items[j]) })
	page, next := s.paginate(len(items), q.Get("pageToken"), q.Get("maxResults"))
	writeJSON(w, gcal.EventList{Items: items[page[0]:page[1]], NextPageToken: next})
}

func (s *Server) getEvent(w http.ResponseWriter, calID, eventID string) {
	e, ok := s.Events[calID][eventID]
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no event with that id on that calendar")
		return
	}
	writeJSON(w, e)
}

func (s *Server) listACL(w http.ResponseWriter, calID string) {
	// §2.15, offline: acl.list is not covered by calendar.readonly, and
	// Google answers a missing scope with 403 insufficientPermissions.
	if s.ACLScopeRequired {
		writeErr(w, http.StatusForbidden, "insufficientPermissions",
			"Request had insufficient authentication scopes.")
		return
	}
	writeJSON(w, gcal.Acl{Items: s.ACL[calID]})
}

func (s *Server) freeBusy(w http.ResponseWriter, r *http.Request) {
	var req gcal.FreeBusyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad freeBusy body")
		return
	}
	// §2.10: the API caps expansion at 50.
	if len(req.Items) > 50 {
		writeErr(w, http.StatusBadRequest, "invalid", "too many calendars in one freeBusy query")
		return
	}
	out := gcal.FreeBusyResponse{
		TimeMin: req.TimeMin, TimeMax: req.TimeMax,
		Calendars: map[string]gcal.FreeBusyCalendar{},
	}
	for _, it := range req.Items {
		if s.FreeBusyOmit[it.ID] {
			continue
		}
		if reason, bad := s.FreeBusyErrors[it.ID]; bad {
			out.Calendars[it.ID] = gcal.FreeBusyCalendar{
				Errors: []gcal.FreeBusyError{{Domain: "calendar", Reason: reason}},
			}
			continue
		}
		out.Calendars[it.ID] = gcal.FreeBusyCalendar{Busy: s.Busy[it.ID]}
	}
	writeJSON(w, out)
}

// paginate returns [lo,hi) and the next token.
//
// The page is the smaller of PageSize and the caller's maxResults.
// Honouring maxResults is what the real API does, and a fake that
// ignores it hides a whole class of defect: a server that asks for 250
// and keeps 10 loses the 240 in between, because the token it gets back
// points past the page rather than past what it kept.
func (s *Server) paginate(n int, token, maxResults string) ([2]int, string) {
	size := s.PageSize
	if v, err := strconv.Atoi(maxResults); err == nil && v > 0 && (size <= 0 || v < size) {
		size = v
	}
	if size <= 0 || n == 0 {
		return [2]int{0, n}, ""
	}
	lo := 0
	if token != "" {
		if v, err := strconv.Atoi(token); err == nil {
			lo = v
		}
	}
	if lo > n {
		lo = n
	}
	hi := lo + size
	if hi >= n {
		return [2]int{lo, n}, ""
	}
	return [2]int{lo, hi}, strconv.Itoa(hi)
}

// ---------------------------------------------------------------- helpers

func startKey(e gcal.Event) string {
	if e.Start == nil {
		return ""
	}
	if e.Start.Date != "" {
		return e.Start.Date
	}
	return e.Start.DateTime
}

func endsBefore(e *gcal.Event, timeMin string) bool {
	if e.End == nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, timeMin)
	if err != nil {
		return false
	}
	if e.End.Date != "" {
		d, err := time.Parse("2006-01-02", e.End.Date)
		return err == nil && !d.After(t)
	}
	end, err := time.Parse(time.RFC3339, e.End.DateTime)
	return err == nil && !end.After(t)
}

func startsAfter(e *gcal.Event, timeMax string) bool {
	if e.Start == nil {
		return false
	}
	t, err := time.Parse(time.RFC3339, timeMax)
	if err != nil {
		return false
	}
	if e.Start.Date != "" {
		d, err := time.Parse("2006-01-02", e.Start.Date)
		return err == nil && !d.Before(t)
	}
	start, err := time.Parse(time.RFC3339, e.Start.DateTime)
	return err == nil && !start.Before(t)
}

func trimID(path, prefix string) string {
	id := strings.TrimPrefix(path, prefix)
	if unesc, err := decodeSegment(id); err == nil {
		return unesc
	}
	return id
}

func splitEventPath(path string) (calID, eventID string) {
	rest := strings.TrimPrefix(path, "/calendars/")
	i := strings.Index(rest, "/events/")
	if i < 0 {
		return "", ""
	}
	cal, _ := decodeSegment(rest[:i])
	ev, _ := decodeSegment(rest[i+len("/events/"):])
	return cal, ev
}

func decodeSegment(s string) (string, error) {
	// net/url's PathUnescape, without importing it twice.
	return urlPathUnescape(s)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, reason, message string) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code": status, "message": message,
			"errors": []map[string]string{{"domain": "global", "reason": reason, "message": message}},
		},
	})
}

func palette() gcal.Colors {
	return gcal.Colors{
		Event: map[string]gcal.ColorPair{
			"1": {Background: "#a4bdfc", Foreground: "#1d1d1d"},
			"2": {Background: "#7ae7bf", Foreground: "#1d1d1d"},
			"5": {Background: "#fbd75b", Foreground: "#1d1d1d"},
		},
		Calendar: map[string]gcal.ColorPair{
			"1": {Background: "#ac725e", Foreground: "#1d1d1d"},
		},
	}
}
