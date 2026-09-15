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
	"fmt"
	"sort"
	"strings"
	"sync"

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
	return when.Resolve(fromCall, calendarTZ, s.userZone(ctx))
}

// ------------------------------------------------------------ calendars

// Calendars returns the subscribed calendar list, paging to the end.
//
// It pages to the end rather than returning the first page: a calendar
// missing from the list cannot be resolved by title (§6.1), and "not
// found" for a calendar that is simply on page two is the kind of wrong
// answer nobody thinks to check.
func (s *Service) Calendars(ctx context.Context, showHidden bool) ([]model.Calendar, error) {
	if err := s.ready(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.calendarsAt && !showHidden {
		out := s.calendars
		s.mu.Unlock()
		return out, nil
	}
	s.mu.Unlock()

	var out []model.Calendar
	token := ""
	for {
		page, err := s.API.ListCalendars(ctx, token, showHidden)
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
	sort.Slice(out, func(i, j int) bool {
		if out[i].Primary != out[j].Primary {
			return out[i].Primary
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})

	if !showHidden {
		s.mu.Lock()
		s.calendars, s.calendarsAt = out, true
		s.mu.Unlock()
	}
	return out, nil
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
			evs, reqs, token, err := s.readCalendar(ctx, c, o, zone, win, budget)
			results[i] = result{evs, reqs, token, err}
		}(i, c)
	}
	wg.Wait()

	for _, r := range results {
		if r.err != nil {
			return render.Schedule{}, r.err
		}
		sched.Events = append(sched.Events, r.events...)
		sched.Requests += r.requests
		if r.token != "" {
			sched.NextPageToken = r.token
		}
	}

	sort.Slice(sched.Events, func(i, j int) bool {
		return sortKey(sched.Events[i]) < sortKey(sched.Events[j])
	})

	sched.Matched = len(sched.Events)
	if len(sched.Events) > budget {
		sched.Events = sched.Events[:budget]
		sched.Truncated = true
	}
	return sched, nil
}

func (s *Service) readCalendar(ctx context.Context, c model.Calendar, o ListOptions,
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
		PageToken:    o.PageToken,
	}
	// §2.9: orderBy=startTime is only legal with singleEvents.
	if o.Expand {
		opts.OrderBy = "startTime"
	}

	var out []model.Event
	requests := 0
	token := ""
	for {
		page, err := s.API.ListEvents(ctx, c.ID, opts)
		requests++
		if err != nil {
			return nil, requests, "", err
		}
		for _, raw := range page.Items {
			e, err := model.FromEvent(c.ID, raw, &zone)
			if err != nil {
				return nil, requests, "", err
			}
			out = append(out, e)
		}
		// Stop at the budget rather than draining a year of events to
		// throw them away.
		if page.NextPageToken == "" || len(out) >= budget {
			token = page.NextPageToken
			break
		}
		opts.PageToken = page.NextPageToken
	}
	return out, requests, token, nil
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

func sortKey(e model.Event) string {
	if e.Start.AllDay {
		// All-day events sort to the front of their day, which is where
		// a reader expects them.
		return e.Start.Date.String() + "T00:00:00"
	}
	if e.Start.At.IsZero() {
		return "9999"
	}
	return e.Start.At.T.UTC().Format("2006-01-02T15:04:05")
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
	acl, err := s.API.ListACL(ctx, c.ID, "")
	if err != nil {
		cls, _ := gapi.ClassOf(err)
		if cls == gapi.ClassAuth || cls == gapi.ClassForbidden {
			out.Note = "Could not read who this calendar is shared with: the signed-in account has not granted " +
				"the calendar.acls.readonly scope. Run `google-calendar-mcp login` again. " +
				"This is not the same as the calendar being shared with nobody."
			return out, nil
		}
		return CalendarResult{}, err
	}
	for _, r := range acl.Items {
		who := r.Scope.Value
		if r.Scope.IsPublic() {
			who = "anyone"
		}
		out.Sharing = append(out.Sharing, SharingOut{
			Who: who, ScopeType: r.Scope.Type, Role: r.Role,
			RoleMeans: gcal.RoleMeans(r.Role), Public: r.Scope.IsPublic(),
		})
	}
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
	if colors, err := s.API.GetColors(ctx); err == nil && len(colors.Event) > 0 {
		out.EventColors = map[string]string{}
		for id, pair := range colors.Event {
			out.EventColors[id] = pair.Background
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
		fmt.Fprintf(&b, "%d event colours available.\n", n)
	}
	out.text = b.String()
	return out, nil
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
