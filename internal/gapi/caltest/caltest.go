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

	// ConferenceFails makes a conference create request come back
	// failed instead of pending, which is the arm of §17.3 a caller has
	// to be told about: the event exists and has no meeting link.
	ConferenceFails bool
	// ConferenceStaysPending keeps a pending conference pending however
	// often it is read, so the "still being made" path can be tested.
	// By default a pending conference is ready by the next read, which
	// is what Google's asynchronous generation looks like from here.
	ConferenceStaysPending bool

	// PageSize forces paging when set, so a test can prove the client
	// follows nextPageToken.
	PageSize int

	// Fail makes the next matching request fail. Key is "METHOD /path"
	// prefix; value is the status.
	Fail map[string]int
	// FailMessage is the message that failure carries, for the refusals
	// this server translates by what Google SAYS rather than by status
	// alone — "the data owner of a calendar cannot remove such a
	// calendar" is a 403 like any other until you read it.
	FailMessage map[string]string
	// Writes records every write served: the method, the path, and the
	// sendUpdates the caller asked for. §4.3 has no default, so a test
	// has to be able to assert that the server sent what the caller
	// chose and nothing when nobody chose.
	Writes []Write
	// Requests records every path served, so a test can assert how many
	// requests a fan-out spent (§11).
	//
	// Guarded by mu: the service fans out across calendars concurrently,
	// so the handler runs on several goroutines at once. Without the
	// lock this field is a data race, and the race detector found it the
	// first time `make check` ran the fan-out test.
	Requests []string

	// SyncTokenExpired makes the next sync request answer 410, which is
	// the one thing a caller holding a token has to survive: Google
	// discards tokens and the only cure is a full read with none.
	SyncTokenExpired bool

	mu sync.Mutex
	// revs counts patches per event, so an etag moves on every write and
	// a stale If-Match is refused the way Google refuses it.
	revs map[string]int
	// syncSeq is a change counter per calendar, and changed records the
	// LATEST state of every event that has moved since the calendar was
	// seeded — a cancelled stub for one that was deleted outright.
	//
	// A deleted event is removed from Events entirely, so without this
	// a sync could not report it, and reporting deletions is the whole
	// reason sync exists rather than a second way to list.
	syncSeq map[string]int
	changed map[string]map[string]syncChange

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
		FailMessage:    map[string]string{},
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
//
// The two carry DIFFERENT etags, and that is the point rather than a
// detail. A calendar is two resources — the calendar itself and one
// user's subscription to it — patched by two methods, each holding its
// own etag, and the server has to send the right one to each. When this
// fake gave both the same value the mistake was invisible: a write
// carrying the subscription's etag to calendars.patch passed every test
// and would have been a 412 on every real call.
func (s *Server) AddCalendar(id, summary, tz, role string, primary bool) {
	s.Calendars[id] = &gcal.Calendar{ID: id, Summary: summary, TimeZone: tz, ETag: etag(id, 1)}
	s.Entries[id] = &gcal.CalendarListEntry{
		ID: id, Summary: summary, TimeZone: tz, AccessRole: role,
		Primary: primary, Selected: true, ETag: etag(entryKey(id), 1),
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

// Write is one write this fake served.
type Write struct {
	Method     string
	CalendarID string
	EventID    string
	// RuleID is the ACL rule a sharing write addressed.
	RuleID      string
	SendUpdates string
	// SendNotifications is the ACL half of §4.3, and a separate field
	// because it is a separate parameter with the opposite default
	// (§2.5). Recorded as the string that went over the wire — "true",
	// "false" or "" — so a test can tell "the server chose false" from
	// "the server sent nothing and let Google's true stand".
	SendNotifications string
	IfMatch           string
}

// Wrote returns a copy of the writes served so far.
func (s *Server) Wrote() []Write {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Write, len(s.Writes))
	copy(out, s.Writes)
	return out
}

func (s *Server) recordWrite(w Write) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Writes = append(s.Writes, w)
}

// ---------------------------------------------------------------- writes

// insertEvent is events.insert.
//
// Two things here are the ones worth having a fake for. A client-supplied
// id is validated as base32hex, because Google answers an illegal one
// with "Invalid resource id value" naming neither the field nor the rule
// — which cost a live run (§18 row 21). And a duplicate id is a 409,
// which is what makes a retry after an ambiguous failure safe rather than
// a second meeting (§2.11, spike F).
func (s *Server) insertEvent(w http.ResponseWriter, r *http.Request, calID string) {
	if _, ok := s.Calendars[calID]; !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no calendar with that id")
		return
	}
	var e gcal.Event
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad event body")
		return
	}
	s.recordWrite(Write{Method: "insert", CalendarID: calID, EventID: e.ID,
		SendUpdates: r.URL.Query().Get("sendUpdates")})

	if e.ID != "" {
		if err := gcal.ValidEventID(e.ID); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", "Invalid resource id value.")
			return
		}
		if _, clash := s.Events[calID][e.ID]; clash {
			writeErr(w, http.StatusConflict, "duplicate", "The requested identifier already exists.")
			return
		}
	}
	if e.ID == "" {
		e.ID = fmt.Sprintf("caltest%d", len(s.Events[calID])+1)
	}
	if e.Status == "" {
		e.Status = gcal.StatusConfirmed
	}
	if len(e.ConferenceData) > 0 {
		// Version 0 — the default — "ignores conference data in the
		// event's body", so the fake drops it exactly as Google does.
		// A client that forgot the parameter otherwise passes its tests
		// and creates events with no meeting link in production.
		if r.URL.Query().Get("conferenceDataVersion") != "1" {
			e.ConferenceData = nil
		} else {
			e.ConferenceData = conferenceAnswer(e.ConferenceData, s.ConferenceFails)
		}
	}
	e.ETag = etag(e.ID, 1)
	s.mu.Lock()
	if s.Events[calID] == nil {
		s.Events[calID] = map[string]*gcal.Event{}
	}
	s.Events[calID][e.ID] = &e
	s.bumpSync(calID, e)
	s.mu.Unlock()
	writeJSON(w, e)
}

// patchEvent is events.patch, under If-Match.
//
// The etag moves on every write, so a second patch carrying the first
// one's etag is a 412 — which is the whole point of §4.4 and the thing a
// fake that ignored If-Match would let pass.
func (s *Server) patchEvent(w http.ResponseWriter, r *http.Request, calID, eventID string) {
	cur, ok := s.event(calID, eventID)
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no event with that id on that calendar")
		return
	}
	match := r.Header.Get("If-Match")
	s.recordWrite(Write{Method: "patch", CalendarID: calID, EventID: eventID,
		SendUpdates: r.URL.Query().Get("sendUpdates"), IfMatch: match})
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	var p gcal.EventPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad patch body")
		return
	}

	s.mu.Lock()
	next := *cur
	// The fold lives on the type, so this fake cannot drift from what
	// the server sends: a field added to EventPatch and forgotten here
	// would make the fake quietly not apply it, and a test green over
	// behaviour that never happened.
	p.ApplyTo(&next)
	if s.revs == nil {
		s.revs = map[string]int{}
	}
	s.revs[eventID]++
	next.ETag = etag(eventID, s.revs[eventID]+1)
	s.Events[calID][eventID] = &next
	s.bumpSync(calID, next)
	s.mu.Unlock()
	writeJSON(w, next)
}

// deleteEvent is events.delete: the event is gone, not cancelled.
func (s *Server) deleteEvent(w http.ResponseWriter, r *http.Request, calID, eventID string) {
	cur, ok := s.event(calID, eventID)
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no event with that id on that calendar")
		return
	}
	match := r.Header.Get("If-Match")
	s.recordWrite(Write{Method: "delete", CalendarID: calID, EventID: eventID,
		SendUpdates: r.URL.Query().Get("sendUpdates"), IfMatch: match})
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	s.mu.Lock()
	delete(s.Events[calID], eventID)
	// Gone from the calendar, but sync still owes the caller a
	// tombstone: that is how a client learns the event was deleted
	// rather than simply stopped matching a window.
	s.bumpSync(calID, gcal.Event{ID: eventID, Status: gcal.StatusCancelled})
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// moveEvent is events.move: the same event, a different calendar.
func (s *Server) moveEvent(w http.ResponseWriter, r *http.Request, calID, eventID string) {
	dest := s.primaryID(r.URL.Query().Get("destination"))
	cur, ok := s.event(calID, eventID)
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no event with that id on that calendar")
		return
	}
	if _, ok := s.Calendars[dest]; !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no destination calendar with that id")
		return
	}
	match := r.Header.Get("If-Match")
	s.recordWrite(Write{Method: "move", CalendarID: calID, EventID: eventID,
		SendUpdates: r.URL.Query().Get("sendUpdates"), IfMatch: match})
	// events.move honours If-Match, which nothing Google publishes says
	// and spike J established live (§18 row 49).
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	s.mu.Lock()
	moved := *cur
	delete(s.Events[calID], eventID)
	if s.Events[dest] == nil {
		s.Events[dest] = map[string]*gcal.Event{}
	}
	s.Events[dest][eventID] = &moved
	s.mu.Unlock()

	// The RESPONSE says cancelled; the stored event does not. That is
	// what Google does, found live: a successful move answers with
	// status:cancelled while the event sits confirmed on the destination
	// (§18 row 48). A fake that answered with the destination's state
	// would hide the reason the server reads the event back.
	answer := moved
	answer.Status = gcal.StatusCancelled
	writeJSON(w, answer)
}

// ------------------------------------------- calendars and the ACL

// underETag is the preamble five write handlers share: find the
// resource, refuse if it is not there, record what was asked for, and
// apply If-Match.
//
// One copy, because the five had it written out and the etag rule is
// exactly the thing a fake must not get subtly different in one of them:
// a handler that forgot the check would accept a stale write and make
// the server's §4.4 promise untestable on that path. It returns false
// having already answered the request.
func underETag[T any](s *Server, w http.ResponseWriter, r *http.Request,
	method, id string, find func() (T, bool), missing string,
) (T, string, bool) {
	cur, ok := find()
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", missing)
		var zero T
		return zero, "", false
	}
	match := r.Header.Get("If-Match")
	s.recordWrite(Write{Method: method, CalendarID: id, IfMatch: match})
	return cur, match, true
}

// aclScopeRefused answers the three ACL handlers when the grant is
// missing, which is §2.15 exercised offline.
func (s *Server) aclScopeRefused(w http.ResponseWriter) bool {
	if !s.ACLScopeRequired {
		return false
	}
	writeErr(w, http.StatusForbidden, "insufficientPermissions",
		"Request had insufficient authentication scopes.")
	return true
}

// insertCalendar is calendars.insert: a new secondary calendar.
func (s *Server) insertCalendar(w http.ResponseWriter, r *http.Request) {
	var c gcal.Calendar
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad calendar body")
		return
	}
	if strings.TrimSpace(c.Summary) == "" {
		writeErr(w, http.StatusBadRequest, "required", "Missing title.")
		return
	}
	s.recordWrite(Write{Method: "calendars.insert", CalendarID: c.ID})

	s.mu.Lock()
	// Google mints the id, and it is not the title: a secondary
	// calendar's id is an opaque address on a group domain. A fake that
	// echoed the caller's id would make every test address a calendar by
	// a name the API never returns.
	c.ID = fmt.Sprintf("cal%d@group.calendar.example.test", len(s.Calendars)+1)
	c.ETag = etag(c.ID, 1)
	stored := c
	s.Calendars[c.ID] = &stored
	// Creating a calendar subscribes the creator to it, as Google does,
	// and makes them its owner.
	s.Entries[c.ID] = &gcal.CalendarListEntry{
		ID: c.ID, Summary: c.Summary, Description: c.Description, Location: c.Location,
		TimeZone: c.TimeZone, AccessRole: gcal.RoleOwner, Selected: true, ETag: etag(entryKey(c.ID), 1),
	}
	if s.Events[c.ID] == nil {
		s.Events[c.ID] = map[string]*gcal.Event{}
	}
	s.mu.Unlock()
	writeJSON(w, c)
}

// patchCalendar is calendars.patch, under If-Match.
func (s *Server) patchCalendar(w http.ResponseWriter, r *http.Request, calID string) {
	cur, match, ok := underETag(s, w, r, "calendars.patch", calID, s.calendar(calID),
		"no calendar with that id")
	if !ok {
		return
	}
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	var p gcal.CalendarPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad calendar patch body")
		return
	}
	s.mu.Lock()
	next := *cur
	p.ApplyTo(&next)
	s.bump(calID)
	next.ETag = etag(calID, s.revs[calID]+1)
	s.Calendars[calID] = &next
	// The calendar's own title is what an unrenamed subscription shows,
	// so the list entry follows it. A subscription that kept the old
	// title would make list_calendars disagree with get_calendar.
	if e, sub := s.Entries[calID]; sub {
		entry := *e
		entry.Summary = next.Summary
		entry.Description, entry.Location, entry.TimeZone = next.Description, next.Location, next.TimeZone
		s.Entries[calID] = &entry
	}
	s.mu.Unlock()
	writeJSON(w, next)
}

// deleteCalendar is calendars.delete.
//
// Whether Google refuses this on a PRIMARY calendar is not modelled
// here, and deliberately: its description says it deletes a secondary
// calendar, but nobody is probing that live against a real account's own
// calendar. The server refuses the primary itself, which is what the
// tests exercise.
func (s *Server) deleteCalendar(w http.ResponseWriter, r *http.Request, calID string) {
	cur, match, ok := underETag(s, w, r, "calendars.delete", calID, s.calendar(calID),
		"no calendar with that id")
	if !ok {
		return
	}
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	s.mu.Lock()
	delete(s.Calendars, calID)
	delete(s.Entries, calID)
	delete(s.Events, calID)
	delete(s.ACL, calID)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// clearCalendar is calendars.clear: every event gone, the calendar
// itself untouched.
func (s *Server) clearCalendar(w http.ResponseWriter, r *http.Request, calID string) {
	cur, match, ok := underETag(s, w, r, "calendars.clear", calID, s.calendar(calID),
		"no calendar with that id")
	if !ok {
		return
	}
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	// Google's description says "Clears a primary calendar". Whether it
	// also clears a secondary one is spike K, and this fake empties
	// whichever it is given rather than inventing a refusal the API may
	// not make.
	s.mu.Lock()
	s.Events[calID] = map[string]*gcal.Event{}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// insertEntry is calendarList.insert: this user subscribes to a calendar
// that already exists.
func (s *Server) insertEntry(w http.ResponseWriter, r *http.Request) {
	var e gcal.CalendarListEntry
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad calendar list entry body")
		return
	}
	id := s.primaryID(e.ID)
	s.recordWrite(Write{Method: "calendarList.insert", CalendarID: id})
	s.mu.Lock()
	cal, ok := s.Calendars[id]
	s.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no calendar with that id")
		return
	}
	s.mu.Lock()
	entry := gcal.CalendarListEntry{
		ID: id, Summary: cal.Summary, Description: cal.Description, Location: cal.Location,
		TimeZone: cal.TimeZone, AccessRole: gcal.RoleReader, Selected: true,
		ColorID: e.ColorID, SummaryOverride: e.SummaryOverride, ETag: etag(entryKey(id), 1),
	}
	if prev, sub := s.Entries[id]; sub {
		// Already subscribed: keep what this user had rather than
		// resetting their overrides. The server reads the list first and
		// does not send this, so nothing depends on the choice.
		entry = *prev
	}
	s.Entries[id] = &entry
	s.mu.Unlock()
	writeJSON(w, entry)
}

// patchEntry is calendarList.patch: this user's own overrides.
func (s *Server) patchEntry(w http.ResponseWriter, r *http.Request, calID string) {
	cur, match, ok := underETag(s, w, r, "calendarList.patch", calID, s.entry(calID),
		"not subscribed to that calendar")
	if !ok {
		return
	}
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	var p gcal.CalendarListPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad calendar list patch body")
		return
	}
	s.mu.Lock()
	next := *cur
	p.ApplyTo(&next)
	s.bump(entryKey(calID))
	next.ETag = etag(entryKey(calID), s.revs[entryKey(calID)]+1)
	s.Entries[calID] = &next
	s.mu.Unlock()
	writeJSON(w, next)
}

// deleteEntry is calendarList.delete: the subscription goes, the
// calendar stays.
func (s *Server) deleteEntry(w http.ResponseWriter, r *http.Request, calID string) {
	cur, match, ok := underETag(s, w, r, "calendarList.delete", calID, s.entry(calID),
		"not subscribed to that calendar")
	if !ok {
		return
	}
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	s.mu.Lock()
	delete(s.Entries, calID)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// insertACL is acl.insert: a new sharing rule.
func (s *Server) insertACL(w http.ResponseWriter, r *http.Request, calID string) {
	if s.aclScopeRefused(w) {
		return
	}
	var rule gcal.AclRule
	if err := json.NewDecoder(r.Body).Decode(&rule); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad acl rule body")
		return
	}
	send := r.URL.Query().Get("sendNotifications")
	s.recordWrite(Write{Method: "acl.insert", CalendarID: calID, SendNotifications: send})
	if _, ok := s.Calendars[calID]; !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no calendar with that id")
		return
	}
	if rule.Role == "" || rule.Scope.Type == "" {
		writeErr(w, http.StatusBadRequest, "required", "Missing role or scope type.")
		return
	}
	rule.ID = aclRuleID(rule.Scope)
	rule.ETag = etag(rule.ID, 1)

	s.mu.Lock()
	replaced := false
	for i, existing := range s.ACL[calID] {
		if gcal.SameScope(existing.Scope, rule.Scope) {
			s.ACL[calID][i] = rule
			replaced = true
			break
		}
	}
	if !replaced {
		s.ACL[calID] = append(s.ACL[calID], rule)
	}
	s.mu.Unlock()
	writeJSON(w, rule)
}

// patchACL is acl.patch: a role change on a rule that exists.
func (s *Server) patchACL(w http.ResponseWriter, r *http.Request, calID, ruleID string) {
	if s.aclScopeRefused(w) {
		return
	}
	send := r.URL.Query().Get("sendNotifications")
	match := r.Header.Get("If-Match")
	s.recordWrite(Write{Method: "acl.patch", CalendarID: calID, RuleID: ruleID,
		SendNotifications: send, IfMatch: match})

	idx, cur, ok := s.aclRule(calID, ruleID)
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no acl rule with that id on that calendar")
		return
	}
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	var p gcal.AclPatch
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "parseError", "bad acl patch body")
		return
	}
	s.mu.Lock()
	next := cur
	if p.Role != nil {
		next.Role = *p.Role
	}
	s.bump("acl:" + ruleID)
	next.ETag = etag(ruleID, s.revs["acl:"+ruleID]+1)
	s.ACL[calID][idx] = next
	s.mu.Unlock()
	writeJSON(w, next)
}

// deleteACL is acl.delete. It takes no sendNotifications: the method
// publishes none, and nobody is told they lost access.
func (s *Server) deleteACL(w http.ResponseWriter, r *http.Request, calID, ruleID string) {
	if s.aclScopeRefused(w) {
		return
	}
	match := r.Header.Get("If-Match")
	s.recordWrite(Write{Method: "acl.delete", CalendarID: calID, RuleID: ruleID, IfMatch: match})

	idx, cur, ok := s.aclRule(calID, ruleID)
	if !ok {
		writeErr(w, http.StatusNotFound, "notFound", "no acl rule with that id on that calendar")
		return
	}
	if !etagOK(match, cur.ETag) {
		writeErr(w, http.StatusPreconditionFailed, "conditionNotMet", "Precondition Failed")
		return
	}
	s.mu.Lock()
	s.ACL[calID] = append(s.ACL[calID][:idx], s.ACL[calID][idx+1:]...)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// aclRule finds one rule under the lock.
func (s *Server) aclRule(calID, ruleID string) (int, gcal.AclRule, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.ACL[calID] {
		if r.ID == ruleID {
			return i, aclDefaults(r), true
		}
	}
	return 0, gcal.AclRule{}, false
}

// aclDefaults fills in what Google always sends.
//
// Every resource the API returns carries an etag, and a fixture written
// without one makes the whole If-Match path untestable: the server sends
// no header when the read carried no etag, so the write goes out
// unprotected and the test passes. Applied to the read AND to the
// lookup a write checks against, because the two disagreeing is the same
// bug one layer down — the read hands out an etag the write then refuses.
func aclDefaults(r gcal.AclRule) gcal.AclRule {
	if r.ETag == "" {
		r.ETag = etag(r.ID, 1)
	}
	return r
}

// aclRuleID is the id Google gives a rule: the scope type, a colon and
// the address — and the bare type for the public scope, which has no
// address.
func aclRuleID(scope gcal.AclScope) string {
	if scope.Type == gcal.ScopeTypeDefault {
		return gcal.ScopeTypeDefault
	}
	return scope.Type + ":" + scope.Value
}

// bump moves a resource's revision under the caller's lock, so an etag
// changes on every write and a stale If-Match is refused.
func (s *Server) bump(key string) {
	if s.revs == nil {
		s.revs = map[string]int{}
	}
	s.revs[key]++
}

// calendar and entry are the two lookups underETag takes, each reading
// its map under the lock the write handlers mutate it behind.
func (s *Server) calendar(id string) func() (*gcal.Calendar, bool) {
	return func() (*gcal.Calendar, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		c, ok := s.Calendars[id]
		return c, ok
	}
}

func (s *Server) entry(id string) func() (*gcal.CalendarListEntry, bool) {
	return func() (*gcal.CalendarListEntry, bool) {
		s.mu.Lock()
		defer s.mu.Unlock()
		e, ok := s.Entries[id]
		return e, ok
	}
}

// event reads one event under the lock.
//
// Under the lock because the write handlers mutate the same map: the
// service issues writes one at a time today, so this is a race the
// detector would find the first time anything fanned out rather than a
// bug anybody has hit — which is the kind that arrives in the phase
// after the one that wrote it.
func (s *Server) event(calID, eventID string) (*gcal.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Events[calID][eventID]
	return e, ok
}

// etagOK applies If-Match.
//
// An absent header is allowed, because this server sends none when the
// read carried no etag. "*" matches anything, which is §4.4's explicit
// override and never a default.
func etagOK(match, current string) bool {
	return match == "" || match == "*" || match == current
}

func etag(seed string, rev int) string { return fmt.Sprintf(`"%s-%d"`, seed, rev) }

// entryKey namespaces a subscription's etag away from its calendar's, so
// the two can never come out equal by accident.
func entryKey(calendarID string) string { return "entry:" + calendarID }

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
			message := "forced failure from caltest"
			if m, ok := s.FailMessage[prefix]; ok {
				message = m
			}
			writeErr(w, status, "forced", message)
			return
		}
	}

	switch {
	case path == "/users/me/calendarList" && r.Method == http.MethodGet:
		s.listCalendars(w, r)
	case strings.HasPrefix(path, "/users/me/calendarList/") && r.Method == http.MethodGet:
		s.getEntry(w, s.primaryID(trimID(path, "/users/me/calendarList/")))
	case path == "/users/me/settings" && r.Method == http.MethodGet:
		writeJSON(w, gcal.Settings{Items: s.Settings})
	case path == "/colors" && r.Method == http.MethodGet:
		writeJSON(w, palette())
	case path == "/freeBusy" && r.Method == http.MethodPost:
		s.freeBusy(w, r)
	case strings.HasSuffix(path, "/acl") && r.Method == http.MethodGet:
		s.listACL(w, s.primaryID(trimID(strings.TrimSuffix(path, "/acl"), "/calendars/")))
	case strings.HasSuffix(path, "/move") && r.Method == http.MethodPost:
		cal, ev := s.splitEventPath(strings.TrimSuffix(path, "/move"))
		s.moveEvent(w, r, cal, ev)
	case strings.HasSuffix(path, "/instances") && r.Method == http.MethodGet:
		rest := strings.TrimSuffix(path, "/instances")
		cal, ev := s.splitEventPath(rest)
		s.listInstances(w, r, cal, ev)
	case strings.Contains(path, "/events/") && r.Method == http.MethodGet:
		cal, ev := s.splitEventPath(path)
		s.getEvent(w, cal, ev)
	case strings.HasSuffix(path, "/events") && r.Method == http.MethodGet:
		s.listEvents(w, r, s.primaryID(trimID(strings.TrimSuffix(path, "/events"), "/calendars/")))
	case strings.HasSuffix(path, "/events") && r.Method == http.MethodPost:
		s.insertEvent(w, r, s.primaryID(trimID(strings.TrimSuffix(path, "/events"), "/calendars/")))
	case strings.Contains(path, "/events/") && r.Method == http.MethodPatch:
		cal, ev := s.splitEventPath(path)
		s.patchEvent(w, r, cal, ev)
	case strings.Contains(path, "/events/") && r.Method == http.MethodDelete:
		cal, ev := s.splitEventPath(path)
		s.deleteEvent(w, r, cal, ev)
	case path == "/calendars" && r.Method == http.MethodPost:
		s.insertCalendar(w, r)
	case path == "/users/me/calendarList" && r.Method == http.MethodPost:
		s.insertEntry(w, r)
	case strings.HasPrefix(path, "/users/me/calendarList/") && r.Method == http.MethodPatch:
		s.patchEntry(w, r, s.primaryID(trimID(path, "/users/me/calendarList/")))
	case strings.HasPrefix(path, "/users/me/calendarList/") && r.Method == http.MethodDelete:
		s.deleteEntry(w, r, s.primaryID(trimID(path, "/users/me/calendarList/")))
	case strings.HasSuffix(path, "/acl") && r.Method == http.MethodPost:
		s.insertACL(w, r, s.primaryID(trimID(strings.TrimSuffix(path, "/acl"), "/calendars/")))
	case strings.Contains(path, "/acl/") && r.Method == http.MethodPatch:
		cal, rule := s.splitACLPath(path)
		s.patchACL(w, r, cal, rule)
	case strings.Contains(path, "/acl/") && r.Method == http.MethodDelete:
		cal, rule := s.splitACLPath(path)
		s.deleteACL(w, r, cal, rule)
	case strings.HasSuffix(path, "/clear") && r.Method == http.MethodPost:
		s.clearCalendar(w, r, s.primaryID(trimID(strings.TrimSuffix(path, "/clear"), "/calendars/")))
	case strings.HasPrefix(path, "/calendars/") && r.Method == http.MethodPatch:
		s.patchCalendar(w, r, s.primaryID(trimID(path, "/calendars/")))
	case strings.HasPrefix(path, "/calendars/") && r.Method == http.MethodDelete:
		s.deleteCalendar(w, r, s.primaryID(trimID(path, "/calendars/")))
	case strings.HasPrefix(path, "/calendars/") && r.Method == http.MethodGet:
		s.getCalendar(w, s.primaryID(trimID(path, "/calendars/")))
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

	// A sync request is a different question from a list, so it is
	// answered before any of the filtering below: it asks what CHANGED,
	// and the answer includes events that no longer exist.
	if token := q.Get("syncToken"); token != "" {
		s.syncPage(w, r, calID, token)
		return
	}

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
	out := gcal.EventList{
		Summary: cal.Summary, TimeZone: cal.TimeZone,
		AccessRole: s.Entries[calID].AccessRole,
		Items:      items[page[0]:page[1]], NextPageToken: next,
	}
	// "Token obtained from the nextSyncToken field returned on the LAST
	// page of results" — so a truncated read carries no token, and a
	// caller that stored one from a middle page would be storing
	// nothing. The fake has to withhold it for the server to be held to
	// that.
	if next == "" {
		out.NextSyncToken = syncTokenFor(s.currentSeq(calID))
	}
	writeJSON(w, out)
}

// syncPage answers events.list?syncToken=…
//
// Three rules from the discovery document, each of which a fake that
// simply filtered by time would let a caller get wrong:
// deletions are ALWAYS in the result, showDeleted=false is refused
// outright, and a token the server has discarded is a 410 rather than an
// empty page.
func (s *Server) syncPage(w http.ResponseWriter, r *http.Request, calID, token string) {
	q := r.URL.Query()
	if s.SyncTokenExpired {
		writeErr(w, http.StatusGone, "fullSyncRequired",
			"Sync token is no longer valid, a full sync is required.")
		return
	}
	if q.Get("showDeleted") == "false" {
		writeErr(w, http.StatusBadRequest, "invalid",
			"showDeleted must not be false when syncToken is set")
		return
	}
	for _, forbidden := range []string{"timeMin", "timeMax", "q", "orderBy", "updatedMin",
		"iCalUID", "privateExtendedProperty", "sharedExtendedProperty"} {
		if q.Get(forbidden) != "" {
			writeErr(w, http.StatusBadRequest, "invalid",
				"cannot specify "+forbidden+" together with syncToken")
			return
		}
	}
	seq, ok := seqFromSyncToken(token)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid", "malformed syncToken")
		return
	}

	items := s.syncSince(calID, seq)
	page, next := s.paginate(len(items), q.Get("pageToken"), q.Get("maxResults"))
	cal := s.Calendars[calID]
	out := gcal.EventList{
		Summary: cal.Summary, TimeZone: cal.TimeZone,
		AccessRole: s.Entries[calID].AccessRole,
		Items:      items[page[0]:page[1]], NextPageToken: next,
	}
	if next == "" {
		out.NextSyncToken = syncTokenFor(s.currentSeq(calID))
	}
	writeJSON(w, out)
}

// syncTokenFor and seqFromSyncToken keep the token opaque to the server,
// which is the point: it is Google's to mint and the caller's to hand
// back, and nothing in this repository may read meaning out of one.
func syncTokenFor(seq int) string { return fmt.Sprintf("caltest-sync-%d", seq) }

func seqFromSyncToken(token string) (int, bool) {
	var seq int
	if _, err := fmt.Sscanf(token, "caltest-sync-%d", &seq); err != nil {
		return 0, false
	}
	return seq, true
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
	// Under the lock from the lookup to the copy. This read MUTATES —
	// Google generates the conference asynchronously, so the link an
	// insert did not carry is there on a later read, and the fake
	// models that rather than answering success at insert, which would
	// hide the one thing create_event has to say about a conference it
	// asked for. A lock around the assignment alone protected nothing a
	// concurrent reader could see, and this server already fans out
	// across calendars.
	s.mu.Lock()
	e, ok := s.Events[calID][eventID]
	if !ok {
		s.mu.Unlock()
		writeErr(w, http.StatusNotFound, "notFound", "no event with that id on that calendar")
		return
	}
	if !s.ConferenceStaysPending {
		e.ConferenceData = conferenceReady(e.ConferenceData)
	}
	out := *e
	s.mu.Unlock()

	writeJSON(w, out)
}

func (s *Server) listACL(w http.ResponseWriter, calID string) {
	// §2.15, offline: acl.list is not covered by calendar.readonly, and
	// Google answers a missing scope with 403 insufficientPermissions.
	if s.aclScopeRefused(w) {
		return
	}
	items := make([]gcal.AclRule, 0, len(s.ACL[calID]))
	for _, r := range s.ACL[calID] {
		items = append(items, aclDefaults(r))
	}
	writeJSON(w, gcal.Acl{Items: items})
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

// primaryID resolves Google's "primary" alias to the calendar it names.
//
// The API accepts `primary` wherever a calendar id goes, and this fake
// did not — which was invisible only because Seed gave a calendar the
// literal id "primary". No real account has one: a primary calendar's id
// is the account's email address, which is also what §4.3.5 splits the
// guest count on. A fixture that dodges the alias makes the alias
// untestable.
func (s *Server) primaryID(id string) string {
	if id != "primary" {
		return id
	}
	for cid, e := range s.Entries {
		if e.Primary {
			return cid
		}
	}
	return id
}

func trimID(path, prefix string) string {
	id := strings.TrimPrefix(path, prefix)
	if unesc, err := decodeSegment(id); err == nil {
		return unesc
	}
	return id
}

// splitACLPath separates the calendar from the rule id. A rule id
// carries a colon and an address — "user:someone@example.test" — so it
// is escaped on the way out and has to be unescaped here.
func (s *Server) splitACLPath(path string) (calID, ruleID string) {
	rest := strings.TrimPrefix(path, "/calendars/")
	i := strings.Index(rest, "/acl/")
	if i < 0 {
		return "", ""
	}
	cal, _ := decodeSegment(rest[:i])
	rule, _ := decodeSegment(rest[i+len("/acl/"):])
	return s.primaryID(cal), rule
}

func (s *Server) splitEventPath(path string) (calID, eventID string) {
	rest := strings.TrimPrefix(path, "/calendars/")
	i := strings.Index(rest, "/events/")
	if i < 0 {
		return "", ""
	}
	cal, _ := decodeSegment(rest[:i])
	ev, _ := decodeSegment(rest[i+len("/events/"):])
	return s.primaryID(cal), ev
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

// conferenceAnswer is what Google puts in an event whose insert asked
// for a conference: the request echoed back with a status, and no entry
// points yet.
//
// Generated, never recorded (§9.1): the meeting code below is made up
// here, in the shape Meet uses, and no real one appears in this
// repository.
func conferenceAnswer(request json.RawMessage, fails bool) json.RawMessage {
	var body struct {
		CreateRequest struct {
			RequestID             string `json:"requestId,omitempty"`
			ConferenceSolutionKey struct {
				Type string `json:"type,omitempty"`
			} `json:"conferenceSolutionKey,omitempty"`
			Status struct {
				StatusCode string `json:"statusCode,omitempty"`
			} `json:"status"`
		} `json:"createRequest"`
	}
	if err := json.Unmarshal(request, &body); err != nil {
		return request
	}
	body.CreateRequest.Status.StatusCode = gcal.ConferencePending
	if fails {
		body.CreateRequest.Status.StatusCode = gcal.ConferenceFailure
	}
	out, err := json.Marshal(body)
	if err != nil {
		return request
	}
	return out
}

// conferenceReady turns a pending conference into a finished one, the
// way Google's asynchronous generation does between two reads.
func conferenceReady(current json.RawMessage) json.RawMessage {
	if len(current) == 0 {
		return current
	}
	c := gcal.ReadConference(current)
	if c.Status != gcal.ConferencePending {
		return current
	}
	var body map[string]any
	if err := json.Unmarshal(current, &body); err != nil {
		return current
	}
	if cr, ok := body["createRequest"].(map[string]any); ok {
		cr["status"] = map[string]any{"statusCode": gcal.ConferenceSuccess}
	}
	body["conferenceId"] = "abc-defg-hij"
	body["conferenceSolution"] = map[string]any{
		"key":  map[string]any{"type": gcal.ConferenceSolutionMeet},
		"name": "Google Meet",
	}
	body["entryPoints"] = []any{map[string]any{
		"entryPointType": "video",
		"uri":            "https://meet.google.com/abc-defg-hij",
		"label":          "meet.google.com/abc-defg-hij",
	}}
	out, err := json.Marshal(body)
	if err != nil {
		return current
	}
	return out
}

// syncChange is one event's latest state and when it changed.
type syncChange struct {
	seq   int
	event gcal.Event
}

// bumpSync records that an event changed. The caller holds mu.
func (s *Server) bumpSync(calID string, e gcal.Event) {
	if s.syncSeq == nil {
		s.syncSeq = map[string]int{}
		s.changed = map[string]map[string]syncChange{}
	}
	if s.changed[calID] == nil {
		s.changed[calID] = map[string]syncChange{}
	}
	s.syncSeq[calID]++
	s.changed[calID][e.ID] = syncChange{seq: s.syncSeq[calID], event: e}
}

// syncSince returns the events that changed after seq, oldest first.
func (s *Server) syncSince(calID string, seq int) []gcal.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []syncChange
	for _, c := range s.changed[calID] {
		if c.seq > seq {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	events := make([]gcal.Event, 0, len(out))
	for _, c := range out {
		events = append(events, c.event)
	}
	return events
}

// currentSeq is the token a sync request hands back.
func (s *Server) currentSeq(calID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.syncSeq[calID]
}

// Touch records that an event changed, the way a write through the API
// would, so a test can make something for a sync to find without going
// through the write path's own guards.
func (s *Server) Touch(calID, eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Events[calID][eventID]
	if !ok {
		return
	}
	s.bumpSync(calID, *e)
}

// Remove deletes an event and leaves the tombstone sync owes, which is
// what makes a deletion reportable at all.
func (s *Server) Remove(calID, eventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Events[calID], eventID)
	s.bumpSync(calID, gcal.Event{ID: eventID, Status: gcal.StatusCancelled})
}

// AnyEventID returns some event id on a calendar, for a test that needs
// one and does not care which.
func (s *Server) AnyEventID(calID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]string, 0, len(s.Events[calID]))
	for id, e := range s.Events[calID] {
		if e.Status != gcal.StatusCancelled && e.RecurringEventID == "" {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return ""
	}
	return ids[0]
}

// EventIDs lists the live events on a calendar, for a test that needs to
// change several of them.
func (s *Server) EventIDs(calID string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, e := range s.Events[calID] {
		if e.Status != gcal.StatusCancelled {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}
