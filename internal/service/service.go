// Package service is orchestration and policy: the layer that turns a
// tool call into API calls, applies the budgets and the guards, and
// builds the values internal/render prints.
//
// The tools below it are thin. Everything that decides something — which
// zone, which calendar, how many requests, what to refuse — lives here,
// so there is one place to test it.
package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/config"
	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/render"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// Service holds the client and the policy.
type Service struct {
	API *gapi.Client
	Cfg config.Config
	// AuthErr is set when credentials could not be resolved at startup.
	//
	// It is a field rather than a reason to refuse to start, because a
	// client launches this server and asks for tools/list BEFORE anybody
	// has logged in. Exiting there makes discovery impossible and every
	// host reports it as a crash; carrying the error and returning it
	// from each tool call gives the caller "[auth] run login", which is
	// the sentence they need.
	AuthErr error
	// Clock is injected so a test can stand at a daylight-saving
	// boundary (§13).
	Clock when.Clock

	// cached per process lifetime: the user's settings change rarely and
	// every read needs the zone.
	mu          sync.Mutex
	settings    *gcal.Settings
	calendars   []model.Calendar
	calendarsAt bool
}

// New builds a Service.
func New(api *gapi.Client, cfg config.Config) *Service {
	return &Service{API: api, Cfg: cfg, Clock: when.SystemClock()}
}

// Unauthenticated builds a Service that answers every call with err.
// The server still starts, so a client can list tools before login.
func Unauthenticated(cfg config.Config, err error) *Service {
	return &Service{Cfg: cfg, Clock: when.SystemClock(), AuthErr: err}
}

// ready is the first line of every method that reaches the API.
func (s *Service) ready() error {
	if s.AuthErr != nil {
		return gapi.Wrap(gapi.ClassAuth, s.AuthErr,
			"not signed in: run `google-calendar-mcp login`, then `google-calendar-mcp doctor` to check it")
	}
	if s.API == nil {
		return gapi.Errf(gapi.ClassAuth, "not signed in: run `google-calendar-mcp login`")
	}
	return nil
}

// ------------------------------------------------------------- settings

// Settings returns the user's settings, reading them once per process.
func (s *Service) Settings(ctx context.Context) (*gcal.Settings, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.settings != nil {
		defer s.mu.Unlock()
		return s.settings, nil
	}
	s.mu.Unlock()

	out, err := s.API.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.settings = out
	s.mu.Unlock()
	return out, nil
}

// userZone is the last resort in §4.1's resolution order.
//
// A failure to read settings is not an error here: it means the last
// resort is unavailable, and Resolve will refuse with ErrNoZone if the
// other two are also empty. Turning it into an error would make a
// missing settings scope look like a broken calendar read.
func (s *Service) userZone(ctx context.Context) string {
	st, err := s.Settings(ctx)
	if err != nil {
		return ""
	}
	tz, _ := st.Lookup(gcal.SettingTimezone)
	return tz
}

// Zone resolves the zone for a call: the caller's, then the calendar's,
// then the user's (§4.1). calendarTZ may be empty.
func (s *Service) Zone(ctx context.Context, fromCall, calendarTZ string) (when.Zone, error) {
	z, err := when.Resolve(fromCall, calendarTZ, s.userZone(ctx))
	if err != nil {
		// Classified, not passed through. An unwrapped error from
		// internal/when is not a *gapi.Error, so it fell through to the
		// default class — [unavailable], which is retryable. A zone that
		// does not exist will not start existing, and telling a caller
		// to retry a request that cannot succeed is worse than refusing
		// it. s.window classifies the same package's errors the same way.
		return when.Zone{}, gapi.Wrap(gapi.ClassInvalid, err, "%s", err.Error())
	}
	return z, nil
}

// ------------------------------------------------------------ calendars

// Calendars returns the subscribed calendar list, paging to the end.
//
// It pages to the end rather than returning the first page: a calendar
// missing from the list cannot be resolved by title (§6.1), and "not
// found" for a calendar that is simply on page two is the kind of wrong
// answer nobody thinks to check.
func (s *Service) Calendars(ctx context.Context, showHidden bool) ([]model.Calendar, error) {
	all, err := s.allCalendars(ctx)
	if err != nil {
		return nil, err
	}
	if showHidden {
		return all, nil
	}
	out := make([]model.Calendar, 0, len(all))
	for _, c := range all {
		if !c.Hidden {
			out = append(out, c)
		}
	}
	return out, nil
}

// allCalendars reads every subscribed calendar once per process,
// hidden ones included, and filters afterwards.
//
// One list, not two. The first version cached only the list WITHOUT
// hidden calendars, and every caller that wanted the full one — which
// is every calendar resolution, because a hidden calendar still
// resolves — went to the network again. `check_availability` on 55
// addresses re-listed the account's calendars 55 times and then
// reported `api_requests: 2`, because the batches were counted and the
// resolutions were not. The visible list is the full one minus the
// hidden entries, so there is nothing a second fetch could learn.
func (s *Service) allCalendars(ctx context.Context) ([]model.Calendar, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.calendarsAt {
		out := s.calendars
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()

	var out []model.Calendar
	token := ""
	for {
		page, err := s.API.ListCalendars(ctx, token, true)
		if err != nil {
			return nil, err
		}
		for _, e := range page.Items {
			if e.Deleted {
				continue
			}
			out = append(out, model.FromCalendarList(e))
		}
		if page.NextPageToken == "" {
			break
		}
		token = page.NextPageToken
	}
	sortCalendars(out)

	s.mu.Lock()
	s.calendars, s.calendarsAt = out, true
	s.mu.Unlock()
	return out, nil
}

// sortCalendars is the order the list is held in: the account's own
// calendar first, then by title. Its own function because a row added to
// the cache after a write has to land where a re-read would have put it.
func sortCalendars(cals []model.Calendar) {
	sort.Slice(cals, func(i, j int) bool {
		if cals[i].Primary != cals[j].Primary {
			return cals[i].Primary
		}
		return strings.ToLower(cals[i].Title) < strings.ToLower(cals[j].Title)
	})
}

// ResolveCalendar turns a reference into a calendar (§6.1).
//
// "primary" is the API's own alias and passes through. An id is used as
// given. Anything else is matched against the subscribed list by title,
// and a title matching more than one calendar is [ambiguous] with the
// candidates — never the first match.
func (s *Service) ResolveCalendar(ctx context.Context, ref string) (model.Calendar, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.EqualFold(ref, "primary") {
		return s.calendarByID(ctx, "primary")
	}

	cals, err := s.Calendars(ctx, true)
	if err != nil {
		return model.Calendar{}, err
	}

	// An exact id wins outright.
	for _, c := range cals {
		if c.ID == ref {
			return c, nil
		}
	}

	var exact, partial []model.Calendar
	for _, c := range cals {
		switch {
		case strings.EqualFold(c.Title, ref):
			exact = append(exact, c)
		case strings.Contains(strings.ToLower(c.Title), strings.ToLower(ref)):
			partial = append(partial, c)
		}
	}
	hits := exact
	if len(hits) == 0 {
		hits = partial
	}

	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		// It may still be a calendar id the user is not subscribed to.
		// Saying "not found" for a calendar they can read would be a lie
		// (§6.1), so try it before refusing.
		if strings.Contains(ref, "@") {
			return s.calendarByID(ctx, ref)
		}
		return model.Calendar{}, gapi.Errf(gapi.ClassNotFound,
			"no calendar called %q. `list_calendars` shows what this account can see; "+
				"a calendar you are not subscribed to can still be reached by its id", ref)
	default:
		var b strings.Builder
		for _, c := range hits {
			b.WriteString("\n  " + c.Title + " — id " + c.ID)
		}
		return model.Calendar{}, gapi.Errf(gapi.ClassAmbiguous,
			"%q matches %d calendars; pass one of these ids:%s", ref, len(hits), b.String())
	}
}

func (s *Service) calendarByID(ctx context.Context, id string) (model.Calendar, error) {
	// "primary" is answered from the list this process has already read,
	// when it has one. Every call resolves a calendar and the list is
	// cached for the process, so going to calendarList.get for the alias
	// spends a request on a question already answered — once per tool
	// call, on the commonest reference there is. That is the shape of
	// the defect phase 1 found in check_availability, one request at a
	// time instead of 166, and invisible for the same reason: the
	// result's own count does not include it.
	if strings.EqualFold(id, "primary") {
		if c, ok := s.cachedPrimary(); ok {
			return c, nil
		}
	}
	// The list entry carries the access role and the per-user overrides,
	// which the calendar resource does not, so prefer it.
	if entry, err := s.API.GetCalendarListEntry(ctx, id); err == nil {
		return model.FromCalendarList(*entry), nil
	}
	cal, err := s.API.GetCalendar(ctx, id)
	if err != nil {
		return model.Calendar{}, err
	}
	return model.Calendar{
		ID: cal.ID, Title: cal.Summary, TimeZone: cal.TimeZone,
		Description: cal.Description, ETag: cal.ETag,
	}, nil
}

// cachedPrimary returns the account's own calendar from the list this
// process has already read.
//
// Only from the cache: it never triggers the read itself. A caller that
// asked for one calendar should not pay for the whole list, and a list
// that is not there yet means the direct read below is the cheaper
// answer.
func (s *Service) cachedPrimary() (model.Calendar, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.calendarsAt {
		return model.Calendar{}, false
	}
	for _, c := range s.calendars {
		if c.Primary {
			return c, true
		}
	}
	return model.Calendar{}, false
}

// --------------------------------------------------------------- events

// ListOptions is what a schedule read takes.
type ListOptions struct {
	// Calendars are references, resolved through ResolveCalendar.
	Calendars []string
	// TimeZone the caller asked for; may be empty.
	TimeZone string
	// From and To bound the window. Both required.
	From string
	To   string
	// Expand chooses instances over series parents (§2.9).
	Expand bool
	// Query is the free-text q. Undocumented and unscoped (§2).
	Query string
	// ShowCancelled includes cancelled events (§2.13).
	ShowCancelled bool
	// MaxEvents overrides the configured budget.
	MaxEvents int
	PageToken string
}

// ListEvents reads a schedule.
func (s *Service) ListEvents(ctx context.Context, o ListOptions) (render.Schedule, error) {
	if err := s.ready(); err != nil {
		return render.Schedule{}, err
	}
	refs := o.Calendars
	if len(refs) == 0 {
		refs = []string{"primary"}
	}
	if len(refs) > s.Cfg.MaxCalendars {
		return render.Schedule{}, gapi.Errf(gapi.ClassInvalid,
			"asked for %d calendars; this server reads at most %d in one call "+
				"(GCAL_MAX_CALENDARS). Split the request", len(refs), s.Cfg.MaxCalendars)
	}

	cals := make([]model.Calendar, 0, len(refs))
	for _, ref := range refs {
		c, err := s.ResolveCalendar(ctx, ref)
		if err != nil {
			return render.Schedule{}, err
		}
		cals = append(cals, c)
	}

	// The zone comes from the first calendar when the caller named none,
	// and the result says which (§4.5).
	firstTZ := ""
	if len(cals) > 0 {
		firstTZ = cals[0].TimeZone
	}
	zone, err := s.Zone(ctx, o.TimeZone, firstTZ)
	if err != nil {
		return render.Schedule{}, err
	}

	win, err := s.window(o.From, o.To, zone)
	if err != nil {
		return render.Schedule{}, err
	}

	budget := o.MaxEvents
	if budget <= 0 {
		budget = s.Cfg.MaxEvents
	}

	resume, err := decodeCursor(o.PageToken)
	if err != nil {
		return render.Schedule{}, err
	}
	// A continuation reads only the calendars that still have pages. One
	// that finished is absent from the cursor, and reading it again would
	// repeat its first page as though it were new.
	if resume != nil {
		still := make([]model.Calendar, 0, len(cals))
		for _, c := range cals {
			if _, more := resume[c.ID]; more {
				still = append(still, c)
			}
		}
		if len(still) == 0 {
			return render.Schedule{}, gapi.Errf(gapi.ClassInvalid,
				"that page_token was issued for a different set of calendars, and none of the "+
					"calendars asked for here has a page waiting. Pass the calendars the token came "+
					"from, or omit the token to start again")
		}
		cals = still
	}

	// The budget bounds the whole result, so it is shared out before the
	// calendars are read rather than applied to the pile afterwards. A
	// calendar with little on it leaves its share unused, which costs a
	// round trip on a continuation and never costs an event: the
	// alternative discards what has already been paged past. Computed
	// after the cursor narrows the list, so a continuation gives its
	// whole budget to the calendars that still have pages.
	perCalendar := budget / len(cals)
	if perCalendar < 1 {
		perCalendar = 1
	}

	sched := render.Schedule{Window: win, Zone: zone, Expanded: o.Expand}
	for _, c := range cals {
		sched.Calendars = append(sched.Calendars, c.Title)
	}

	type result struct {
		events   []model.Event
		requests int
		token    string
		err      error
	}
	results := make([]result, len(cals))
	sem := make(chan struct{}, s.Cfg.Concurrency)
	var wg sync.WaitGroup
	for i, c := range cals {
		wg.Add(1)
		go func(i int, c model.Calendar) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			evs, reqs, token, err := s.readCalendar(ctx, c, o, resume[c.ID], zone, win, perCalendar)
			results[i] = result{evs, reqs, token, err}
		}(i, c)
	}
	wg.Wait()

	next := map[string]string{}
	for i, r := range results {
		if r.err != nil {
			return render.Schedule{}, r.err
		}
		sched.Events = append(sched.Events, r.events...)
		sched.Requests += r.requests
		if r.token != "" {
			next[cals[i].ID] = r.token
		}
	}
	sched.NextPageToken = encodeCursor(next)

	sort.Slice(sched.Events, func(i, j int) bool {
		return sortKey(sched.Events[i]) < sortKey(sched.Events[j])
	})

	sched.Matched = len(sched.Events)
	// Nothing is cut here, and that is the point. The combined list used
	// to be truncated to the budget after every calendar had been read,
	// which threw away events whose page token had already moved past
	// them — unreachable from the cursor the caller was handed, while
	// the result called itself resumable. The budget is divided across
	// the calendars up front instead, so the total is bounded by what
	// was fetched rather than by what is discarded.
	sched.Truncated = sched.NextPageToken != ""
	return sched, nil
}

func (s *Service) readCalendar(ctx context.Context, c model.Calendar, o ListOptions, pageToken string,
	zone when.Zone, win when.Window, budget int,
) ([]model.Event, int, string, error) {
	opts := gapi.EventsListOptions{
		TimeMin:      win.Start.String(),
		TimeMax:      win.End.String(),
		SingleEvents: o.Expand,
		Query:        o.Query,
		ShowDeleted:  o.ShowCancelled,
		TimeZone:     zone.Name(),
		MaxResults:   250,
		PageToken:    pageToken,
	}
	// §2.9: orderBy=startTime is only legal with singleEvents.
	if o.Expand {
		opts.OrderBy = "startTime"
	}

	events, requests, token, err := s.drain(ctx, c.ID, zone, budget, o.ShowCancelled, opts,
		func(ctx context.Context, opts gapi.EventsListOptions) (*gcal.EventList, error) {
			return s.API.ListEvents(ctx, c.ID, opts)
		})
	return events, requests, token, err
}

// drain reads pages until the budget is reached or the pages run out.
//
// It is §4.5's completeness policy in one place: stop at the budget
// rather than draining a year of events to throw them away, and hand
// back the token so the caller can say how to continue. The two read
// paths had a copy each and had already drifted on what "truncated"
// means.
// pageCursor is what `next_page_token` actually carries.
//
// One call can read several calendars, and a Google page token is scoped
// to one calendar and one query. Handing back a single token — whichever
// calendar happened to produce one last — resumed the wrong calendar
// from an unrelated offset, or failed outright, and silently discarded
// the tokens for the rest. A caller who followed it believed they had
// seen everything.
//
// So the cursor holds one token per calendar, and names ONLY the
// calendars with more to read: one that finished is absent, and a
// continuation skips it rather than reading it again. It stays a single
// opaque string, so the tool schema is unchanged and a caller still just
// passes back what it was given.
type pageCursor struct {
	V    int               `json:"v"`
	Cals map[string]string `json:"c"`
}

const pageCursorVersion = 1

func encodeCursor(tokens map[string]string) string {
	if len(tokens) == 0 {
		return ""
	}
	b, err := json.Marshal(pageCursor{V: pageCursorVersion, Cals: tokens})
	if err != nil {
		// Unreachable for a map of strings, and a lost token is better
		// than a bad one: an empty cursor reads as "nothing more", which
		// Truncated still contradicts.
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// decodeCursor returns the per-calendar tokens, or nil for a first read.
func decodeCursor(tok string) (map[string]string, error) {
	if strings.TrimSpace(tok) == "" {
		return nil, nil
	}
	bad := gapi.Errf(gapi.ClassInvalid,
		"page_token is not one this server issued. Pass back the next_page_token from a previous "+
			"read of the same calendars, unchanged, or omit it to start again")
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		return nil, bad
	}
	var c pageCursor
	if err := json.Unmarshal(raw, &c); err != nil || c.V != pageCursorVersion || len(c.Cals) == 0 {
		return nil, bad
	}
	return c.Cals, nil
}

// showCancelled is the caller's promise, passed in rather than read off
// opts.ShowDeleted. The two are not the same knob: ShowDeleted is what
// this server asked Google for, and the defect being fixed here is
// exactly that the server treated the request field as if it were the
// promise. Phase 2 needs to ask for cancelled occurrences without
// putting them in a result — a cancelled instance is an exception, and
// §4.2's `this_and_following` has to see them.
func (s *Service) drain(ctx context.Context, calendarID string, zone when.Zone, budget int,
	showCancelled bool, opts gapi.EventsListOptions,
	fetch func(context.Context, gapi.EventsListOptions) (*gcal.EventList, error),
) (events []model.Event, requests int, nextToken string, err error) {
	// Never ask for more than the budget will keep.
	//
	// A page is the unit Google's token points past, so discarding the
	// tail of a page loses everything between the budget and the page
	// boundary: with max_events=10 the first page returned 250, ten were
	// shown, and the token resumed at 251. The other 240 vanished while
	// the result said it was resumable. Asking for the budget makes the
	// token line up with what the caller actually saw.
	if budget > 0 && (opts.MaxResults == 0 || opts.MaxResults > budget) {
		opts.MaxResults = budget
	}
	// The budget bounds what the read DRAINS, not what survives the
	// filter below. Counting kept events let every dropped row buy
	// another page: a window whose first twenty rows were cancelled cost
	// twenty-one requests to return one event, which is §4.7 failing
	// where the result cannot show it. A page that is mostly cancelled
	// now comes back short, truncated and resumable — §4.5's bargain.
	drained := 0
	for {
		page, ferr := fetch(ctx, opts)
		requests++
		if ferr != nil {
			return nil, requests, "", ferr
		}
		drained += len(page.Items)
		for _, raw := range page.Items {
			// Asked before the conversion, on the wire value: a
			// cancelled row is discarded, so parsing its three times
			// and building a model.Event is work spent on nothing — and
			// Google sends these bare, so a conversion error on one
			// would fail a read over an event nobody asked for.
			//
			// showDeleted=false does not mean Google filtered them. The
			// discovery document: "Cancelled instances of recurring
			// events (but not the underlying recurring event) will
			// still be included if showDeleted and singleEvents are
			// both False." One arrives with no start and no summary, so
			// a series read rendered a row with no date and no title
			// and counted it among the results (§18).
			if !showCancelled && raw.Status == gcal.StatusCancelled {
				continue
			}
			e, cerr := model.FromEvent(calendarID, raw, &zone)
			if cerr != nil {
				return nil, requests, "", cerr
			}
			events = append(events, e)
		}
		if page.NextPageToken == "" || drained >= budget || len(events) >= budget {
			return events, requests, page.NextPageToken, nil
		}
		opts.PageToken = page.NextPageToken
	}
}

// GetEvent reads one event.
func (s *Service) GetEvent(ctx context.Context, calRef, eventID, tz string) (model.Event, when.Zone, error) {
	if err := s.ready(); err != nil {
		return model.Event{}, when.Zone{}, err
	}
	c, err := s.ResolveCalendar(ctx, calRef)
	if err != nil {
		return model.Event{}, when.Zone{}, err
	}
	zone, err := s.Zone(ctx, tz, c.TimeZone)
	if err != nil {
		return model.Event{}, when.Zone{}, err
	}
	raw, err := s.API.GetEvent(ctx, c.ID, eventID)
	if err != nil {
		return model.Event{}, when.Zone{}, err
	}
	e, err := model.FromEvent(c.ID, *raw, &zone)
	return e, zone, err
}

// window resolves and validates the requested span.
func (s *Service) window(from, to string, zone when.Zone) (when.Window, error) {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" {
		return when.Window{}, gapi.Errf(gapi.ClassInvalid,
			"both from and to are required, as RFC3339 timestamps or yyyy-mm-dd dates. "+
				"This server does not guess a window, and it does not resolve "+
				"relative expressions like \"next week\" — resolve those before calling, "+
				"and the result will echo the absolute window it used")
	}
	start, err := parseBound(from, zone, false)
	if err != nil {
		return when.Window{}, err
	}
	end, err := parseBound(to, zone, true)
	if err != nil {
		return when.Window{}, err
	}
	w, err := when.NewWindow(start, end, zone.Loc)
	if err != nil {
		return when.Window{}, gapi.Wrap(gapi.ClassInvalid, err, "%s", err.Error())
	}
	return w, nil
}

// parseBound accepts a date or a timestamp. A bare date means the start
// of that day in the resolved zone, and for the upper bound the start of
// the following day, so "2026-03-16" to "2026-03-16" is that whole day
// rather than an empty window.
func parseBound(v string, zone when.Zone, upper bool) (when.Zoned, error) {
	v = strings.TrimSpace(v)
	if d, err := when.ParseDate(v); err == nil {
		if upper {
			return when.NewZoned(d.EndExclusiveIn(zone.Loc), zone.Loc), nil
		}
		return when.NewZoned(d.StartIn(zone.Loc), zone.Loc), nil
	}
	z, err := when.ParseZoned(v, zone.Loc)
	if err != nil {
		return when.Zoned{}, gapi.Wrap(gapi.ClassInvalid, err,
			"%q is neither a yyyy-mm-dd date nor an RFC3339 timestamp with an offset", v)
	}
	return z, nil
}

// sortKey orders a schedule the way the renderer groups it: by LOCAL
// date, in the zone the read resolved.
//
// It used to sort timed events by their UTC instant and all-day events
// by their local date, while `render.dayKey` grouped both by local date.
// East of UTC the two disagree — in Asia/Tokyo an 08:00 event carries a
// UTC key on the previous day — so an all-day event landed in the middle
// of its own day instead of at the front, which is what this function
// promises. Every event here has already been converted to the one
// resolved zone, so a local key is consistent across calendars.
func sortKey(e model.Event) string {
	if e.Start.AllDay {
		// All-day events sort to the front of their day, which is where
		// a reader expects them.
		return e.Start.Date.String() + "T00:00:00"
	}
	if e.Start.At.IsZero() {
		return "9999"
	}
	return e.Start.At.T.Format("2006-01-02T15:04:05")
}

// CalendarDetail is get_calendar: the calendar plus who it is shared
// with.
//
// A missing ACL scope is reported as a note rather than an error. §2.15
// makes it a separate grant, so an account that never consented to it
// would otherwise see the whole tool fail — and an empty sharing list
// would read as "shared with nobody", which is the wrong answer rather
// than a missing one.
func (s *Service) CalendarDetail(ctx context.Context, ref string) (CalendarResult, error) {
	if err := s.ready(); err != nil {
		return CalendarResult{}, err
	}
	c, err := s.ResolveCalendar(ctx, ref)
	if err != nil {
		return CalendarResult{}, err
	}
	out := CalendarResult{
		Calendar:    NewCalendarsResult([]model.Calendar{c}).Calendars[0],
		Description: c.Description,
	}
	if !s.Cfg.Sharing {
		out.Note = "Sharing tools are off in this server (GCAL_SHARING=off), so the sharing list was not read."
		return out, nil
	}
	rules, err := s.sharingRules(ctx, c.ID)
	if err != nil {
		if !missingACLScope(err) {
			return CalendarResult{}, err
		}
		// Reported as a note rather than as a failure: the rest of this
		// card is a successful read, and an empty sharing list would say
		// "shared with nobody", which is the wrong answer rather than a
		// missing one. The sentence is the one list_sharing raises.
		out.Note = render.Sentence(MissingACLScope)
		return out, nil
	}
	out.Sharing = sharingOut(rules)
	return out, nil
}

// SettingsSummary is get_settings.
func (s *Service) SettingsSummary(ctx context.Context) (SettingsResult, error) {
	if err := s.ready(); err != nil {
		return SettingsResult{}, err
	}
	st, err := s.Settings(ctx)
	if err != nil {
		return SettingsResult{}, err
	}
	out := SettingsResult{}
	out.TimeZone, _ = st.Lookup(gcal.SettingTimezone)
	out.WeekStart, _ = st.Lookup(gcal.SettingWeekStart)
	out.Format24Hour, _ = st.Lookup(gcal.SettingFormat24Hour)
	out.Locale, _ = st.Lookup(gcal.SettingLocale)

	// Colours are a separate call and a nicety: a failure here must not
	// take down the tool whose real job is reporting the time zone.
	if colors, err := s.API.GetColors(ctx); err == nil {
		if len(colors.Event) > 0 {
			out.EventColors = map[string]string{}
			for id, pair := range colors.Event {
				out.EventColors[id] = pair.Background
			}
		}
		// The calendar palette is a different set of ids from the event
		// one, and manage_calendar's color_id indexes THIS one. Reporting
		// only the event colours left that parameter with no published
		// source for its values.
		if len(colors.Calendar) > 0 {
			out.CalendarColors = map[string]string{}
			for id, pair := range colors.Calendar {
				out.CalendarColors[id] = pair.Background
			}
		}
	}

	var b strings.Builder
	if out.TimeZone == "" {
		b.WriteString("This account's Calendar settings name no time zone.\n")
	} else {
		fmt.Fprintf(&b, "Time zone: %s\n", out.TimeZone)
	}
	if out.WeekStart != "" {
		fmt.Fprintf(&b, "Week starts on: %s\n", weekdayName(out.WeekStart))
	}
	if out.Format24Hour != "" {
		fmt.Fprintf(&b, "24-hour clock: %s\n", out.Format24Hour)
	}
	if out.Locale != "" {
		fmt.Fprintf(&b, "Locale: %s\n", out.Locale)
	}
	if n := len(out.EventColors); n > 0 {
		fmt.Fprintf(&b, "%d event colours available, ids %s.\n", n, colourIDs(out.EventColors))
	}
	if n := len(out.CalendarColors); n > 0 {
		fmt.Fprintf(&b, "%d calendar colours available, ids %s — these are what manage_calendar's "+
			"color_id takes.\n", n, colourIDs(out.CalendarColors))
	}
	out.text = b.String()
	return out, nil
}

// colourIDs lists a palette's ids in numeric order, because a map
// iterates in none and a caller needs to know which ids exist.
func colourIDs(palette map[string]string) string {
	ids := make([]string, 0, len(palette))
	for id := range palette {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, aerr := strconv.Atoi(ids[i])
		b, berr := strconv.Atoi(ids[j])
		if aerr == nil && berr == nil {
			return a < b
		}
		return ids[i] < ids[j]
	})
	return strings.Join(ids, ", ")
}

func weekdayName(v string) string {
	switch v {
	case "0":
		return "Sunday"
	case "1":
		return "Monday"
	case "6":
		return "Saturday"
	default:
		return v
	}
}

// ------------------------------------------------------------ instances

// InstanceOptions is what list_instances takes.
type InstanceOptions struct {
	// Calendar is a reference, resolved through ResolveCalendar.
	Calendar string
	// EventID is the SERIES id. An instance id is not a series id, and
	// the refusal below says so.
	EventID  string
	TimeZone string
	// From and To bound the occurrences. Both or neither: a half window
	// cannot be stated absolutely, and §4.5 says every read states its
	// window.
	From string
	To   string
	// ShowCancelled reveals the occurrences that were removed from the
	// series, which is what a cancelled instance is (§2.13).
	ShowCancelled bool
	MaxEvents     int
	PageToken     string
}

// Instances expands one series into its occurrences.
//
// One API request per page, and no read of the parent: the occurrences
// are what was asked for, and fetching the series as well to quote its
// rule would double the cost of every call for a line the caller can get
// from get_event.
func (s *Service) Instances(ctx context.Context, o InstanceOptions) (render.Instances, error) {
	if err := s.ready(); err != nil {
		return render.Instances{}, err
	}
	if strings.TrimSpace(o.EventID) == "" {
		return render.Instances{}, gapi.Errf(gapi.ClassInvalid,
			"event_id is required: it is the id of the repeating event, which list_events reports as "+
				"series_id on each occurrence")
	}
	if series, start, ok := gcal.SplitOccurrenceID(o.EventID); ok {
		return render.Instances{}, gapi.Errf(gapi.ClassInvalid,
			"%s names the occurrence starting %s, not the series. Pass %s — the series_id list_events "+
				"reports on each occurrence — and list_instances expands the whole series",
			o.EventID, start, series)
	}
	c, err := s.ResolveCalendar(ctx, o.Calendar)
	if err != nil {
		return render.Instances{}, err
	}
	zone, err := s.Zone(ctx, o.TimeZone, c.TimeZone)
	if err != nil {
		return render.Instances{}, err
	}

	out := render.Instances{
		SeriesID: o.EventID, CalendarID: c.ID, Zone: zone,
		ShowCancelled: o.ShowCancelled,
	}
	opts := gapi.EventsListOptions{
		TimeZone: zone.Name(), MaxResults: 250,
		ShowDeleted: o.ShowCancelled, PageToken: o.PageToken,
	}

	hasFrom, hasTo := strings.TrimSpace(o.From) != "", strings.TrimSpace(o.To) != ""
	switch {
	case hasFrom != hasTo:
		return render.Instances{}, gapi.Errf(gapi.ClassInvalid,
			"pass both from and to, or neither. Neither means the whole series, as far as the "+
				"event budget reaches; the result says if it stopped early")
	case hasFrom:
		win, werr := s.window(o.From, o.To, zone)
		if werr != nil {
			return render.Instances{}, werr
		}
		out.Window = &win
		opts.TimeMin, opts.TimeMax = win.Start.String(), win.End.String()
	}

	budget := o.MaxEvents
	if budget <= 0 {
		budget = s.Cfg.MaxEvents
	}

	events, requests, token, err := s.drain(ctx, c.ID, zone, budget, o.ShowCancelled, opts,
		func(ctx context.Context, opts gapi.EventsListOptions) (*gcal.EventList, error) {
			return s.API.ListInstances(ctx, c.ID, o.EventID, opts)
		})
	out.Requests = requests
	if err != nil {
		return render.Instances{}, instancesError(err, o.EventID)
	}
	out.Events, out.NextPageToken = events, token
	for _, e := range events {
		if out.Title == "" {
			out.Title = e.Title
		}
		if e.SeriesID != "" {
			out.SeriesID = e.SeriesID
		}
	}
	if len(out.Events) > budget {
		out.Events = out.Events[:budget]
	}
	// Ordered AFTER the cut, never before.
	//
	// Google does not return occurrences in date order — the live run
	// got a cancelled 24 March after 7 April — and a list of dates out
	// of order is hard to read for the question this tool answers, which
	// is "which dates does this series have". But sorting before the
	// budget cut would keep a different SET than the page token accounts
	// for, and the ones dropped would be reachable from nowhere. That is
	// the defect phase 1 fixed in the schedule read; this is the same
	// trap one tool over.
	sort.Slice(out.Events, func(i, j int) bool {
		return sortKey(out.Events[i]) < sortKey(out.Events[j])
	})
	// Truncated means the caller is not looking at the whole series:
	// either the budget cut the list, or a page is still waiting.
	out.Truncated = len(events) > budget || out.NextPageToken != ""
	return out, nil
}

// instancesError says where the id should have come from.
//
// It no longer claims the id names an occurrence: seriesOf catches every
// occurrence-shaped id before the request is built, so anything reaching
// here is by construction NOT one, and the old wording could only
// misdirect. What is left is the part that stays true — this call wants
// the series id, and the result that carries it.
func instancesError(err error, id string) error {
	cls, ok := gapi.ClassOf(err)
	if !ok || (cls != gapi.ClassNotFound && cls != gapi.ClassInvalid) {
		return err
	}
	return gapi.Wrap(cls, err,
		"no repeating event with id %s on that calendar. list_events reports the id this call wants "+
			"as series_id on each occurrence", id)
}

// --------------------------------------------------------- availability

// AvailabilityOptions is what check_availability takes.
type AvailabilityOptions struct {
	Calendars []string
	From      string
	To        string
	TimeZone  string
	// MinMinutes drops free gaps shorter than this. Zero keeps them all.
	MinMinutes int
}

// FreeBusyBatch is the API's ceiling on calendars per query (§2.10).
const FreeBusyBatch = 50

// Availability answers "when is this person free" from freebusy.query.
//
// Never from a list of events: a list misses everything whose details
// the caller cannot see and ignores transparency, so it answers "free"
// for somebody who is busy (§4.6). The two rules that follow from that
// are here: a calendar that errored is reported unknown rather than
// free, and the free gaps say how many calendars they were computed
// from.
func (s *Service) Availability(ctx context.Context, o AvailabilityOptions) (render.AvailabilityReport, error) {
	if err := s.ready(); err != nil {
		return render.AvailabilityReport{}, err
	}
	refs := o.Calendars
	if len(refs) == 0 {
		refs = []string{"primary"}
	}
	if len(refs) > config.MaxFreeBusyCalendars {
		return render.AvailabilityReport{}, gapi.Errf(gapi.ClassInvalid,
			"asked about %d calendars; this server answers for at most %d in one availability call. "+
				"That is not GCAL_MAX_CALENDARS, which bounds the reads that cost a request per calendar: "+
				"free/busy answers for %d calendars per request (§2.10). Split the request",
			len(refs), config.MaxFreeBusyCalendars, FreeBusyBatch)
	}

	ids, firstTZ, err := s.freeBusyTargets(ctx, refs)
	if err != nil {
		return render.AvailabilityReport{}, err
	}
	zone, err := s.Zone(ctx, o.TimeZone, firstTZ)
	if err != nil {
		return render.AvailabilityReport{}, err
	}
	win, err := s.window(o.From, o.To, zone)
	if err != nil {
		return render.AvailabilityReport{}, err
	}

	report := render.AvailabilityReport{
		Window: win, Zone: zone,
		MinGap: time.Duration(o.MinMinutes) * time.Minute,
	}

	// §2.10 caps one query at 50 calendars, so more than that is more
	// than one request — and §4.7 says the result reports how many.
	for batch := range slices.Chunk(ids, FreeBusyBatch) {
		req := &gcal.FreeBusyRequest{
			TimeMin: win.Start.String(), TimeMax: win.End.String(),
			TimeZone: zone.Name(), CalendarExpansionMax: FreeBusyBatch,
		}
		for _, id := range batch {
			req.Items = append(req.Items, gcal.FreeBusyRequestItem{ID: id})
		}
		resp, err := s.API.QueryFreeBusy(ctx, req)
		report.Requests++
		if err != nil {
			return render.AvailabilityReport{}, err
		}
		for _, id := range batch {
			report.Answers = append(report.Answers, answerFor(id, resp, zone))
		}
	}

	var busy []model.Busy
	for _, a := range report.Answers {
		if a.Unknown {
			continue
		}
		report.GapsFrom++
		busy = append(busy, a.Busy...)
	}
	// Gaps computed from nothing are the whole window, which is the one
	// answer §4.6 forbids: "nobody could be read" must not arrive as
	// "everybody is free". The decision is here rather than in the
	// renderer, so the text and the structured half cannot disagree —
	// they did, and the JSON was the one offering the window.
	if report.GapsFrom > 0 {
		report.Gaps = model.FreeGaps(win, busy, report.MinGap)
	}
	return report, nil
}

// answerFor turns one calendar's slot in the response into an answer,
// and a missing slot into "unknown" rather than into "free".
//
// A calendar Google did not answer for is the case that matters:
// calendarExpansionMax truncating the query looks exactly like this, and
// the difference between "no busy blocks" and "no answer" is somebody's
// meeting.
func answerFor(id string, resp *gcal.FreeBusyResponse, zone when.Zone) model.Availability {
	out := model.Availability{CalendarID: id}
	cal, ok := resp.Calendars[id]
	if !ok {
		out.Unknown = true
		out.Reason = "Google returned no answer for this calendar"
		return out
	}
	if len(cal.Errors) > 0 {
		out.Unknown = true
		out.Reason = freeBusyReason(cal.Errors[0])
		return out
	}
	for _, p := range cal.Busy {
		start, serr := when.ParseZoned(p.Start, zone.Loc)
		end, eerr := when.ParseZoned(p.End, zone.Loc)
		if serr != nil || eerr != nil {
			// Unknown rather than partial: half a busy list read as a
			// whole one is the shape §4.6 refuses.
			out.Busy = nil
			out.Unknown, out.Reason = true, "Google returned a busy period this server could not read"
			return out
		}
		out.Busy = append(out.Busy, model.Busy{Start: start, End: end})
	}
	return out
}

// freeBusyReason says what a per-calendar error means, in the words the
// caller needs. Google's reasons here are short and unexplained.
func freeBusyReason(e gcal.FreeBusyError) string {
	switch e.Reason {
	case "notFound":
		return "no such calendar, or this account cannot see it"
	case "internalError":
		return "Google failed to read it; try again"
	case "rateLimitExceeded", "quotaExceeded":
		return "Google is rate limiting this account"
	case "":
		return "Google reported an error without a reason"
	default:
		return e.Reason
	}
}

// freeBusyTargets turns the caller's references into calendar ids.
//
// It deliberately does NOT resolve an address. Free/busy is the one read
// that works on a calendar this account cannot open (§4.6), so resolving
// one first would refuse exactly the query the tool exists for — and it
// would cost two failing round trips per colleague, because a calendar
// nobody is subscribed to answers neither calendarList.get nor
// calendars.get.
//
// A title is still resolved, because a typo that silently became an id
// would come back "unknown" and read as a real answer.
func (s *Service) freeBusyTargets(ctx context.Context, refs []string) (ids []string, firstTZ string, err error) {
	// The subscribed list, read once, for the zone. A calendar this
	// account knows carries one; one it cannot read does not, and that
	// is not a reason to refuse the query.
	known := map[string]model.Calendar{}
	if cals, cerr := s.Calendars(ctx, true); cerr == nil {
		for _, c := range cals {
			known[c.ID] = c
		}
	}

	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		id := ref
		if ref == "" || strings.EqualFold(ref, "primary") || !strings.Contains(ref, "@") {
			c, rerr := s.ResolveCalendar(ctx, ref)
			if rerr != nil {
				return nil, "", rerr
			}
			id = c.ID
			known[c.ID] = c
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
		if firstTZ == "" {
			firstTZ = known[id].TimeZone
		}
	}
	return ids, firstTZ, nil
}
