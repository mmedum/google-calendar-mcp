package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/plan"
	"github.com/mmedum/google-calendar-mcp/internal/recur"
	"github.com/mmedum/google-calendar-mcp/internal/render"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// The write path. Five tools, one shape: resolve the calendar and the
// zone, read what is there, let internal/plan decide, spend the
// requests, and report what changed and who was told (§4.9).
//
// Nothing here invents a default. The scope (§4.2) and the notification
// (§4.3) are the caller's, the etag is the one the read produced (§4.4),
// and a refusal says which of the three it wants.

// classifyPlan turns a guard's sentinel into a classified error.
//
// internal/plan has no idea what a gapi.Class is, deliberately: a guard
// package that imports the REST client to name a class has the layering
// backwards. This is the one place the three sentinels become the three
// classes, so the mapping cannot drift between call sites.
func classifyPlan(err error) error {
	if err == nil {
		return nil
	}
	for _, m := range []struct {
		sentinel error
		class    gapi.Class
	}{
		{plan.ErrBlocked, gapi.ClassBlocked},
		{plan.ErrUnsupported, gapi.ClassUnsupported},
		{plan.ErrInvalid, gapi.ClassInvalid},
		// recur's failures reach the write path directly as well as
		// through plan — parsing a series' own rule, and splitting it —
		// and had four hand-written wrappings, two of which stripped the
		// package prefix and two of which did not.
		{recur.ErrInvalid, gapi.ClassInvalid},
		// when's failures reach the read path directly: a window whose
		// end is not after its start, and the working-hours mask of
		// §17.2.
		{when.ErrInvalid, gapi.ClassInvalid},
	} {
		if !errors.Is(err, m.sentinel) {
			continue
		}
		// The sentinel names its own prefix rather than a literal here
		// repeating it: a literal stops matching silently if the
		// sentinel is ever reworded, and the caller then reads
		// "plan: invalid: …" in a message that already says [invalid].
		return gapi.Wrap(m.class, err, "%s",
			strings.TrimPrefix(err.Error(), m.sentinel.Error()+": "))
	}
	return err
}

// writeEnv is what every write resolves before it touches anything.
type writeEnv struct {
	cal  model.Calendar
	zone when.Zone
	// organiser is the address §4.3.5 splits the guest count on. For a
	// new event it is this account; for an existing one it is whoever
	// organises it, which is not always the same person.
	organiser string
}

// §4.7's api_requests is read with gapi.Requests off the context prepare
// returns, which counts every request the call made — the shared setup
// reads included, and the retries of §11. It used to be a field on this
// struct incremented by hand at each call site, which missed all of the
// setup: a create reported 1 and made 4, and a dry run reported 0 while
// spending three. A counter kept by hand is a counter that drifts.

// prepare resolves the calendar and the zone, and refuses a write to a
// calendar this account is known not to be able to write to.
//
// "Known" is the operative word: the access role comes from the
// subscribed list, and a calendar reached by id alone has none. An
// unknown role is not treated as a refusal — Google is the authority,
// and refusing on a field this server could not read would deny a write
// that would have succeeded.
func (s *Service) prepare(ctx context.Context, calRef, tz string) (context.Context, *writeEnv, error) {
	if err := s.ready(); err != nil {
		return ctx, nil, err
	}
	ctx = gapi.WithCounter(ctx)
	// The organiser is read first, and the order is the point: it fills
	// the calendar-list cache, so resolving "primary" below costs
	// nothing rather than a second request for the same fact.
	organiser := s.primaryID(ctx)
	c, err := s.ResolveCalendar(ctx, calRef)
	if err != nil {
		return ctx, nil, err
	}
	// Through needRole, which is the one place the "role known and too
	// weak" refusal is written. The copy this replaced misstated its own
	// threshold — it said "at least writer access" while checking
	// CanWrite, which is writerWithoutPrivateAccess — because a sentence
	// and the check beneath it were separate things to keep true.
	if err := needRole(c, gcal.RoleWriterWithoutPrivateData, "writing an event"); err != nil {
		return ctx, nil, err
	}
	zone, err := s.Zone(ctx, tz, c.TimeZone)
	if err != nil {
		return ctx, nil, err
	}
	return ctx, &writeEnv{cal: c, zone: zone, organiser: organiser}, nil
}

// primaryID is this account's own calendar id, which is its address.
//
// Read from the list that is already cached rather than from
// calendarList.get, which would spend a request on every write to learn
// something the list said once.
func (s *Service) primaryID(ctx context.Context) string {
	cals, err := s.Calendars(ctx, true)
	if err != nil {
		return ""
	}
	for _, c := range cals {
		if c.Primary {
			return c.ID
		}
	}
	return ""
}

// readEvent fetches one event as both halves: the raw resource, which is
// what a patch is built against, and the model, which is what a guard
// and a renderer read.
//
// Both, and not one: §4.4's read-modify-write on the guest list needs
// the wire attendees, because model.Attendee drops a guest's comment and
// their extra-guest count, and rebuilding the array from the model would
// erase those for every guest on any write that touched the list.
func (s *Service) readEvent(ctx context.Context, env *writeEnv, eventID string) (gcal.Event, model.Event, error) {
	raw, err := s.API.GetEvent(ctx, env.cal.ID, eventID)
	if err != nil {
		return gcal.Event{}, model.Event{}, err
	}
	e, cerr := model.FromEvent(env.cal.ID, *raw, &env.zone)
	if cerr != nil {
		return gcal.Event{}, model.Event{}, cerr
	}
	return *raw, e, nil
}

// address turns the caller's event_id, and an optional original_start,
// into the id to read (§6.2).
//
// A series id plus the occurrence's scheduled start becomes the
// occurrence id Google itself uses. That address is the stable one:
// originalStartTime identifies an instance even after somebody moves it,
// while the instance's own id is what a caller has only if they listed
// the instances first.
func address(eventID, originalStart string) (id, note string, err error) {
	eventID = strings.TrimSpace(eventID)
	originalStart = strings.TrimSpace(originalStart)
	if eventID == "" {
		return "", "", gapi.Errf(gapi.ClassInvalid,
			"event_id is required. list_events and search_events report it on every event")
	}
	if originalStart == "" {
		return eventID, "", nil
	}
	if _, _, isOccurrence := gcal.SplitOccurrenceID(eventID); isOccurrence {
		return "", "", gapi.Errf(gapi.ClassInvalid,
			"%s already names one occurrence, so original_start has nothing to pick. Pass the series id "+
				"with original_start, or the occurrence id on its own", eventID)
	}
	var start gcal.EventDateTime
	if d, derr := when.ParseDate(originalStart); derr == nil {
		start.Date = d.String()
	} else if z, zerr := when.ParseZoned(originalStart, nil); zerr == nil {
		start.DateTime = z.String()
	} else {
		return "", "", gapi.Errf(gapi.ClassInvalid,
			"original_start %q is neither a yyyy-mm-dd date nor an RFC3339 timestamp. It is the occurrence's "+
				"SCHEDULED start, which list_instances reports as original_start", originalStart)
	}
	occ, oerr := gcal.OccurrenceID(eventID, start)
	if oerr != nil {
		return "", "", gapi.Wrap(gapi.ClassInvalid, oerr, "%s", oerr.Error())
	}
	return occ, fmt.Sprintf("Addressed the occurrence starting %s of series %s.", originalStart, eventID), nil
}

// aim resolves which event a scoped write lands on, reading the series
// parent when the scope points at it.
//
// The decision is plan.Target's; this only spends the request it asks
// for. Every operation that addresses an existing event goes through
// here, which is the point: the three copies this replaced had already
// drifted into two different behaviours and one missing refusal.
func (s *Service) aim(ctx context.Context, env *writeEnv, scope recur.Scope,
	raw gcal.Event, e model.Event, eventID string,
) (gcal.Event, model.Event, string, error) {
	at, err := plan.Target(scope, e, eventID)
	if err != nil {
		return gcal.Event{}, model.Event{}, "", classifyPlan(err)
	}
	if at != plan.AimSeries {
		return raw, e, "", nil
	}
	parentRaw, parent, err := s.readEvent(ctx, env, e.SeriesID)
	if err != nil {
		return gcal.Event{}, model.Event{}, "", err
	}
	return parentRaw, parent, "Wrote the series " + e.SeriesID + ", not the one occurrence.", nil
}

// seriesReach counts a series' occurrences for the refusal §4.2 wants.
//
// Returned as a closure and called only on the refusal path: counting
// the occurrences of an instance's series means reading its parent, and
// spending that request on every successful write to improve a message
// nobody sees is §4.7 failing where the result cannot show it.
func (s *Service) seriesReach(ctx context.Context, env *writeEnv, raw gcal.Event, e model.Event) func() string {
	return func() string {
		parent, pe := raw, e
		if e.IsInstance() {
			p, perr := s.API.GetEvent(ctx, env.cal.ID, e.SeriesID)
			if perr != nil {
				return ""
			}
			m, merr := model.FromEvent(env.cal.ID, *p, &env.zone)
			if merr != nil {
				return ""
			}
			parent, pe = *p, m
		}
		set, serr := recur.Parse(parent.Recurrence)
		if serr != nil {
			return ""
		}
		if pe.Start.AllDay {
			return set.ReachDates(pe.Start.Date, 0)
		}
		return set.Reach(pe.Start.At, 0)
	}
}

// ifMatch decides what goes in the If-Match header (§4.4).
//
// The etag from the read that produced the plan, unless the caller
// explicitly asked for "whatever it says now". A caller who quotes an
// etag of their own is held to it: they read the event, decided, and the
// decision is about the version they read — if it moved in between, that
// is exactly the case [stale] exists for, and discovering it here costs
// nothing.
func ifMatch(addressedETag, targetETag, callerETag string, force bool) (string, error) {
	if force {
		return "*", nil
	}
	// The caller's etag is a statement about the event THEY read, which
	// is the one they addressed — not the one a scope redirected the
	// write to. Comparing it against the series parent made an etag plus
	// scope:series refuse every time with "this changed since the
	// version you passed", which was untrue and unescapable: re-reading
	// the occurrence returns the same etag it just rejected.
	if callerETag != "" && addressedETag != "" && callerETag != addressedETag {
		return "", gapi.Errf(gapi.ClassStale,
			"this event changed since the version you passed as etag. Read it again with get_event, "+
				"check the change is still the one you meant, and write with the new etag")
	}
	// If-Match goes on the event actually being written, which is what
	// concurrency control is about.
	if targetETag == "" {
		// Nothing to be optimistic about. Saying so is better than
		// sending If-Match: * and calling it concurrency control.
		return "", nil
	}
	return targetETag, nil
}

// ------------------------------------------------------------- creating

// CreateOptions is what create_event takes.
type CreateOptions struct {
	Calendar    string
	Title       string
	Start       string
	End         string
	TimeZone    string
	Description string
	Location    string
	Guests      []string
	Recurrence  []string
	Transparent bool
	// Conference asks for a Google Meet link on the new event (§17.3).
	Conference bool
	Notify     string
	DryRun     bool
}

// CreateEvent inserts an event with a client-generated id (§2.11).
//
// The id is minted here so a failure nobody saw the answer to is
// recoverable: a retry either lands or collides with a 409, where an id
// Google chose would have created a second event. The server does not
// retry it itself — §11 — and reports [ambiguous_outcome] with the id
// instead, so a caller can look rather than double-book.
func (s *Service) CreateEvent(ctx context.Context, o CreateOptions) (render.WriteReport, error) {
	ctx, env, err := s.prepare(ctx, o.Calendar, o.TimeZone)
	if err != nil {
		return render.WriteReport{}, err
	}

	if o.Conference && !env.cal.Conference.AllowsMeet() {
		// Refused here rather than by Google, which answers a 200 and a
		// failed create request: the event would exist without the link
		// the caller asked for. An absent list is not a refusal, so this
		// only fires when the calendar published its types and Meet is
		// not among them.
		return render.WriteReport{}, gapi.Errf(gapi.ClassUnsupported,
			"this calendar does not allow Google Meet conferences, so the event was not created. "+
				"Create it without conference, or use a calendar that allows them")
	}

	title := o.Title
	draft := plan.Draft{
		Title: &title, Start: o.Start, End: o.End, Zone: env.zone,
		AddGuests: o.Guests, Conference: o.Conference,
	}
	if o.Description != "" {
		draft.Description = &o.Description
	}
	if o.Location != "" {
		draft.Location = &o.Location
	}
	if o.Transparent {
		draft.Transparent = &o.Transparent
	}
	if o.Recurrence != nil {
		lines := o.Recurrence
		draft.Recurrence = &lines
	}

	decision, err := plan.Notification(o.Notify, plan.ReachOfAddresses(env.organiser, o.Guests))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}

	id, err := newEventID()
	if err != nil {
		return render.WriteReport{}, err
	}
	body, err := plan.Insert(id, draft)
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}

	report := render.WriteReport{
		Verb: render.VerbCreate, Calendar: env.cal.Title, Zone: env.zone,
		Notify: decision.Report(), DryRun: o.DryRun,
	}
	if len(body.Recurrence) > 0 {
		report.Notes = append(report.Notes,
			"This is a series: "+render.Recurrence(body.Recurrence)+".")
	}
	if o.Conference && o.DryRun {
		report.Notes = append(report.Notes,
			"A Google Meet link would be requested. Nothing is written by a dry run, so there is "+
				"no link to report here.")
	}

	if o.DryRun {
		after, cerr := model.FromEvent(env.cal.ID, body, &env.zone)
		if cerr != nil {
			return render.WriteReport{}, cerr
		}
		report.After, report.Requests = &after, gapi.Requests(ctx)
		return report, nil
	}

	created, err := s.API.InsertEvent(ctx, env.cal.ID, &body, decision.SendUpdatesFor())
	if err != nil {
		return render.WriteReport{}, insertError(err, id, env.cal.ID)
	}
	after, err := model.FromEvent(env.cal.ID, *created, &env.zone)
	if err != nil {
		return render.WriteReport{}, err
	}
	if note := conferenceNote(o.Conference, after); note != "" {
		report.Notes = append(report.Notes, note)
	}
	report.After, report.Requests = &after, gapi.Requests(ctx)
	return report, nil
}

// conferenceNote says what actually happened to a requested Meet link.
//
// A live run saw the link arrive with the insert (§18 row 60), and
// Google documents the conference as generated asynchronously, so
// "pending" with no entry point is a published answer as well. This
// reports what came back rather than what was asked for: a result that
// said "with a Google Meet link" on the strength of having asked would
// be promising something that may not be there — the same failure as
// reporting `none` as silence (§4.3 rule 3).
func conferenceNote(asked bool, e model.Event) string {
	if !asked {
		return ""
	}
	switch {
	case e.Conference.Ready():
		return "Google Meet: " + e.Conference.URI
	case e.Conference.Failed():
		return "The Google Meet link could NOT be created, and the event exists without one. " +
			"Google gave no reason. Add a link in Google Calendar, or ask again on another calendar."
	case e.Conference.Pending():
		return "The Google Meet link was requested and Google is still making it, so there is no link " +
			"to give out yet. This is the slower of Google's two answers — read this event again " +
			"with get_event in a moment."
	default:
		// Asked for, and the answer carries nothing about it. Reported
		// as absent rather than as pending: a caller told to wait for a
		// link nobody is making would wait forever.
		return "No Google Meet link came back with this event, and Google said nothing about one. " +
			"Read the event again with get_event before promising anybody a link."
	}
}

// maybeLanded is the boundary of the ambiguous_outcome class (§2.11): a
// 4xx definitely did not land, while a failure that never reached Google
// or one Google answered with a 5xx may have.
//
// One predicate, because it IS the class rather than a detail of one
// tool. Two copies would let an event insert tell a caller to go and
// look while a calendar insert told them to retry, under the same
// failure, and the closed-class gate checks that the classes exist
// rather than that they are reached under the same conditions.
func maybeLanded(err error) (*gapi.Error, bool) {
	var e *gapi.Error
	if !errors.As(err, &e) || (e.Status != 0 && e.Status < 500) {
		return nil, false
	}
	return e, true
}

// insertError produces [ambiguous_outcome] for an event insert (§2.11).
//
// The event can exist with the id this call chose while the caller was
// told the write failed. Retrying that blindly is how a meeting gets
// created twice, so the class says "go and look" and names the id to
// look for.
func insertError(err error, id, calendarID string) error {
	e, maybe := maybeLanded(err)
	if !maybe {
		return err
	}
	return gapi.Wrap(gapi.ClassAmbiguousOutcome, err,
		"this event may or may not have been created: the request failed without an answer this server can "+
			"trust (%s). It used the id %s on calendar %s — read that with get_event before trying again, "+
			"because a blind retry is how one meeting becomes two",
		e.Message, id, calendarID)
}

// ------------------------------------------------------------- updating

// UpdateOptions is what update_event takes.
type UpdateOptions struct {
	Calendar      string
	EventID       string
	OriginalStart string
	Scope         string
	TimeZone      string

	Title        *string
	Description  *string
	Location     *string
	Start        string
	End          string
	Recurrence   *[]string
	AddGuests    []string
	RemoveGuests []string
	Transparent  *bool

	Notify string
	ETag   string
	Force  bool
	DryRun bool
}

// UpdateEvent patches an event under If-Match (§4.4).
func (s *Service) UpdateEvent(ctx context.Context, o UpdateOptions) (render.WriteReport, error) {
	// Checked before anything is read: it depends on nothing but the
	// caller's own arguments, and refusing after four requests spends
	// somebody's quota to say "you asked for nothing".
	draft := plan.Draft{
		Title: o.Title, Description: o.Description, Location: o.Location,
		Start: o.Start, End: o.End,
		Recurrence: o.Recurrence, AddGuests: o.AddGuests, RemoveGuests: o.RemoveGuests,
		Transparent: o.Transparent,
	}
	if draft.Empty() {
		return render.WriteReport{}, gapi.Errf(gapi.ClassInvalid,
			"update_event was given nothing to change. Pass at least one of title, description, location, "+
				"start, end, recurrence, guests or free_not_busy")
	}
	ctx, env, err := s.prepare(ctx, o.Calendar, o.TimeZone)
	if err != nil {
		return render.WriteReport{}, err
	}
	id, note, err := address(o.EventID, o.OriginalStart)
	if err != nil {
		return render.WriteReport{}, err
	}
	raw, before, err := s.readEvent(ctx, env, id)
	if err != nil {
		return render.WriteReport{}, notFoundHint(err, id, o.OriginalStart)
	}
	scope, err := plan.Scope(o.Scope, before, s.seriesReach(ctx, env, raw, before))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}

	draft.Zone = env.zone

	if scope == recur.ScopeThisAndFollowing {
		return s.thisAndFollowing(ctx, env, before, draft, o, note)
	}

	target, targetModel, aimed, err := s.aim(ctx, env, scope, raw, before, o.EventID)
	if err != nil {
		return render.WriteReport{}, err
	}
	note = strings.TrimSpace(note + " " + aimed)

	patch, changes, err := plan.Patch(target, draft)
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	decision, err := plan.Notification(o.Notify, plan.ReachOfEvent(organiserOf(targetModel, env), env.organiser, targetModel))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	etag, err := ifMatch(raw.ETag, target.ETag, o.ETag, o.Force)
	if err != nil {
		return render.WriteReport{}, err
	}

	report := render.WriteReport{
		Verb: render.VerbUpdate, Calendar: env.cal.Title, Zone: env.zone,
		Before: &targetModel, Changes: changes, Scope: string(scope),
		Notify: decision.Report(), DryRun: o.DryRun,
	}
	if note != "" {
		report.Notes = append(report.Notes, note)
	}
	if o.Force {
		report.Notes = append(report.Notes, forcedNote)
	}
	if o.DryRun {
		after, perr := project(target, patch, env)
		if perr != nil {
			return render.WriteReport{}, perr
		}
		report.After, report.Requests = &after, gapi.Requests(ctx)
		return report, nil
	}

	updated, err := s.API.PatchEvent(ctx, env.cal.ID, target.ID, &patch, decision.SendUpdatesFor(), etag)
	if err != nil {
		return render.WriteReport{}, err
	}
	after, err := model.FromEvent(env.cal.ID, *updated, &env.zone)
	if err != nil {
		return render.WriteReport{}, err
	}
	report.After, report.Requests = &after, gapi.Requests(ctx)
	return report, nil
}

// project is what the event would look like after a patch, for a dry
// run to show.
//
// A dry run used to render "after" as the event unchanged, so an update
// showed the same line twice while the change list said otherwise, and a
// cancellation showed the event alive under the words "Deleted the
// event". create's dry run always projected; the three disagreed, and
// the tool description promises all of them report what WOULD change.
func project(before gcal.Event, patch gcal.EventPatch, env *writeEnv) (model.Event, error) {
	after := before
	patch.ApplyTo(&after)
	return model.FromEvent(env.cal.ID, after, &env.zone)
}

// forcedNote says a write went through with If-Match: *, which is §4.4's
// explicit override and never a default.
const forcedNote = "Written with If-Match: *, so a change somebody else made since this was read was " +
	"overwritten rather than reported."

// organiserOf is whose domain the guest count is split on (§4.3.5).
//
// The event's organiser, not the signed-in account: an event on a shared
// calendar can be organised by somebody else, and `external_only` splits
// on the ORGANISER's Workspace domain (§18 row 40). The account is the
// fallback for an event that names none.
func organiserOf(e model.Event, env *writeEnv) string {
	// An event on a secondary calendar is organised by the CALENDAR, and
	// its id is an address with a domain of its own. Splitting the guest
	// count on that made every colleague "outside your organisation", so
	// notify:none on a team calendar was refused with a sentence that
	// was simply false and that no caller could work around.
	//
	// The test is exact rather than a guess at Google's resource
	// domains: if the organiser IS the calendar being written, it is not
	// a person, and this account is the nearest thing to one.
	if e.Organizer != "" && !strings.EqualFold(e.Organizer, env.cal.ID) {
		return e.Organizer
	}
	return env.organiser
}

// notFoundHint says what an id that did not resolve was probably meant
// to be.
func notFoundHint(err error, id, originalStart string) error {
	cls, ok := gapi.ClassOf(err)
	if !ok || cls != gapi.ClassNotFound {
		return err
	}
	if originalStart != "" {
		return gapi.Wrap(cls, err,
			"no occurrence of that series starts at %s. original_start is the occurrence's SCHEDULED start, "+
				"which list_instances reports as original_start — an occurrence somebody moved still carries "+
				"the one it was scheduled for", originalStart)
	}
	return gapi.Wrap(cls, err, "no event %s on that calendar. An event id is unique per calendar, not "+
		"globally, so check the calendar as well as the id", id)
}

// thisAndFollowing performs the two-call pattern of §2.8.
//
// Google has no such operation: the original series is truncated before
// the target and a new one is inserted starting at it. Two consequences
// the result has to state, because no caller expects either — exceptions
// after the target are RESET (spike E confirmed it against Google), and
// the later occurrences now belong to a DIFFERENT event with a different
// id.
func (s *Service) thisAndFollowing(ctx context.Context, env *writeEnv, target model.Event,
	draft plan.Draft, o UpdateOptions, note string,
) (render.WriteReport, error) {
	if target.IsSeries() {
		return render.WriteReport{}, gapi.Errf(gapi.ClassInvalid,
			"%s is the series itself, so \"this and following\" does not say where to split it. Pass "+
				"original_start to name the occurrence it starts at, or the occurrence's own id", o.EventID)
	}
	if !target.IsInstance() {
		return render.WriteReport{}, gapi.Errf(gapi.ClassInvalid,
			"this event does not repeat, so there is nothing following it. Use scope:instance")
	}

	parentRaw, parent, err := s.readEvent(ctx, env, target.SeriesID)
	if err != nil {
		return render.WriteReport{}, err
	}
	set, err := recur.Parse(parentRaw.Recurrence)
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}

	// The occurrence's SCHEDULED start is where the split falls, not
	// where somebody moved it to: the rule generated the scheduled one,
	// and a COUNT counts what the rule generated (§18).
	split := scheduledStart(target)

	var beforeRule, afterRule string
	if parent.Start.AllDay {
		beforeRule, afterRule, err = set.SplitDates(parent.Start.Date, split.Date)
	} else {
		beforeRule, afterRule, err = set.Split(parent.Start.At, split.At)
	}
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}

	// The new series is the parent with this draft applied, starting at
	// the target occurrence and carrying the remainder of the rule.
	newBody, changes, droppedConference, err := splitBody(parentRaw, target, draft, afterRule, env.zone)
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	decision, err := plan.Notification(o.Notify, plan.ReachOfEvent(organiserOf(parent, env), env.organiser, parent))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	etag, err := ifMatch(target.ETag, parentRaw.ETag, o.ETag, o.Force)
	if err != nil {
		return render.WriteReport{}, err
	}

	report := render.WriteReport{
		Verb: render.VerbUpdate, Calendar: env.cal.Title, Zone: env.zone,
		Before: &target, Changes: changes, Scope: string(recur.ScopeThisAndFollowing),
		Notify: decision.Report(), DryRun: o.DryRun,
	}
	if note != "" {
		report.Notes = append(report.Notes, note)
	}
	report.Notes = append(report.Notes,
		fmt.Sprintf("\"This and following\" is two calls, because Google has no such operation (§2.8). "+
			"The original series %s now ends before this occurrence, with the rule %s, and the occurrences "+
			"from here on belong to a NEW event with the rule %s.",
			// The rule the new series actually carries, which is not
			// always the one the split computed: a caller who passed
			// `recurrence` alongside the scope replaces it, and quoting
			// the computed one described a series that does not exist.
			parent.ID, beforeRule, strings.Join(newBody.Recurrence, " ")),
		"Any exception after this occurrence — a moved or cancelled date — has been RESET to the series' own "+
			"schedule. Google does that, not this server, and no caller expects it. Check list_instances on "+
			"the new series if somebody had moved a later date.")
	if droppedConference {
		report.Notes = append(report.Notes,
			"The original series had a conference attached and the NEW series does not. This server does "+
				"not write conference data yet, so the later occurrences have no meeting link — add one in "+
				"Calendar if people were joining that way.")
	}

	if o.DryRun {
		after, cerr := model.FromEvent(env.cal.ID, newBody, &env.zone)
		if cerr != nil {
			return render.WriteReport{}, cerr
		}
		report.After, report.Requests = &after, gapi.Requests(ctx)
		return report, nil
	}

	// Truncate first. If the insert then fails, the caller has a series
	// that stops early and nothing duplicated — recoverable. The other
	// order leaves two overlapping series on the calendar.
	// BOTH calls carry the notification decision. The truncate is the one
	// that removes the later occurrences from everybody's calendar, so a
	// result reading "asked Google to notify all 4 guests" while the
	// truncate asked for nothing described a decision applied to half the
	// operation (§4.3.3).
	lines := []string{beforeRule}
	_, err = s.API.PatchEvent(ctx, env.cal.ID, parent.ID,
		&gcal.EventPatch{Recurrence: &lines}, decision.SendUpdatesFor(), etag)
	if err != nil {
		return render.WriteReport{}, err
	}

	created, err := s.API.InsertEvent(ctx, env.cal.ID, &newBody, decision.SendUpdatesFor())
	if err != nil {
		return render.WriteReport{}, gapi.Wrap(gapi.ClassAmbiguousOutcome, err,
			"the original series %s was truncated before this occurrence, and the new series that should "+
				"carry the later occurrences was NOT created: %s. Those occurrences are gone from the "+
				"calendar until somebody recreates them. The new series would have used the id %s — read "+
				"that with get_event before retrying",
			parent.ID, insertMessage(err), newBody.ID)
	}
	after, err := model.FromEvent(env.cal.ID, *created, &env.zone)
	if err != nil {
		return render.WriteReport{}, err
	}
	report.After, report.Requests = &after, gapi.Requests(ctx)
	return report, nil
}

// newEventID mints the client-generated id of §2.11, classified once.
//
// It had two classifications: [unavailable] on the create path and
// nothing at all on the split path, where it fell through unclassified
// — one failure reading two ways depending on which call reached it.
// A system random source that cannot produce bytes is a transient
// failure of the machine, so [unavailable] is right, and retrying is
// what the caller should do.
func newEventID() (string, error) {
	id, err := gcal.NewEventID()
	if err != nil {
		return "", gapi.Wrap(gapi.ClassUnavailable, err, "%s", err.Error())
	}
	return id, nil
}

// scheduledStart is where the recurrence rule put this occurrence.
//
// Not where somebody moved it to: originalStartTime is the stable
// address (§6.2) and it is what the RULE generated, which is what a
// COUNT counts — and counting the moved one was one of the six defects
// phase 1's review found in the split arithmetic. An occurrence carrying
// no original start is one nobody has moved, so its own start is the
// scheduled one.
func scheduledStart(e model.Event) model.When {
	if e.OriginalStart.IsZero() {
		return e.Start
	}
	return e.OriginalStart
}

func insertMessage(err error) string {
	var e *gapi.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

// splitBody builds the new series a this_and_following write inserts.
//
// It carries the parent's content forward — the guests, the description,
// the transparency — because "this and following" means the same event
// from here on, with the change applied. What it does NOT carry is the
// parent's id, its etag or its instance exceptions: those belong to the
// event being left behind.
func splitBody(parent gcal.Event, target model.Event, draft plan.Draft, rule string,
	zone when.Zone,
) (gcal.Event, []plan.Change, bool, error) {
	id, err := newEventID()
	if err != nil {
		return gcal.Event{}, nil, false, err
	}

	// Start from the parent, then move it to the target occurrence: the
	// new series begins where the split does.
	body := parent
	body.ID, body.ETag, body.HTMLLink = id, "", ""
	body.Created, body.Updated, body.ICalUID, body.Sequence = "", "", "", 0
	body.RecurringEventID, body.OriginalStartTime = "", nil
	body.Recurrence = []string{rule}
	// The conference is NOT carried, and the result says so rather than
	// letting it vanish.
	//
	// The reason is no longer "this server cannot write one" — since
	// §17.3 it can, and the client would send the version parameter for
	// this body as readily as for a create. It is that copying the value
	// points TWO series at one conference, which is a decision about
	// somebody's meeting rather than about this write, and minting a
	// second conference for a split is a write nobody asked for. So the
	// split leaves the new series without a link and says so, which is
	// the honest half of a limitation.
	droppedConference := len(body.ConferenceData) > 0
	body.ConferenceData = nil

	start, end, err := occurrenceSpan(parent, target, zone)
	if err != nil {
		return gcal.Event{}, nil, false, err
	}
	body.Start, body.End = start, end

	// Then the caller's change, applied to the new series exactly as it
	// would have been applied to the old one.
	patch, changes, err := plan.Patch(body, draft)
	if err != nil {
		return gcal.Event{}, nil, false, err
	}
	patch.ApplyTo(&body)
	return body, changes, droppedConference, nil
}

// occurrenceSpan is where the new series starts and ends: the target
// occurrence's scheduled slot, keeping the series' own duration.
func occurrenceSpan(parent gcal.Event, target model.Event, zone when.Zone) (*gcal.EventDateTime, *gcal.EventDateTime, error) {
	scheduled := scheduledStart(target)
	if scheduled.AllDay {
		days := 1
		if parent.Start != nil && parent.End != nil && parent.Start.IsAllDay() && parent.End.IsAllDay() {
			from, ferr := when.ParseDate(parent.Start.Date)
			to, terr := when.ParseDate(parent.End.Date)
			if ferr == nil && terr == nil {
				days = daysBetween(from, to)
			}
		}
		return &gcal.EventDateTime{Date: scheduled.Date.String()},
			&gcal.EventDateTime{Date: scheduled.Date.AddDays(days).String()}, nil
	}
	if scheduled.At.IsZero() {
		return nil, nil, gapi.Errf(gapi.ClassInvalid,
			"this occurrence has no start, so the new series has nowhere to begin")
	}
	span := time.Hour
	if parent.Start != nil && parent.End != nil {
		from, ferr := when.ParseZoned(parent.Start.DateTime, zone.Loc)
		to, terr := when.ParseZoned(parent.End.DateTime, zone.Loc)
		if ferr == nil && terr == nil && to.T.After(from.T) {
			span = to.T.Sub(from.T)
		}
	}
	start := scheduled.At.In(zone.Loc)
	end := when.NewZoned(start.T.Add(span), zone.Loc)
	return &gcal.EventDateTime{DateTime: start.String(), TimeZone: zone.Name()},
		&gcal.EventDateTime{DateTime: end.String(), TimeZone: zone.Name()}, nil
}

// daysBetween is how long an all-day event lasts, in days.
//
// Measured in UTC deliberately: a day is 24 hours there and nowhere
// else, and the question is a count of dates rather than an elapsed
// time. A zero or negative span is one day, which is what a single-day
// event's exclusive end produces.
func daysBetween(from, to when.Date) int {
	n := int(to.StartIn(time.UTC).Sub(from.StartIn(time.UTC)) / (24 * time.Hour))
	if n < 1 {
		return 1
	}
	return n
}

// ----------------------------------------------------------- cancelling

// CancelOptions is what cancel_event takes.
type CancelOptions struct {
	Calendar      string
	EventID       string
	OriginalStart string
	Scope         string
	TimeZone      string
	Notify        string
	ETag          string
	Force         bool
	DryRun        bool
}

// CancelEvent removes an event or one occurrence of a series.
//
// Two API shapes behind one honest verb, and the result names which
// happened: events.delete for a whole event, and a status:cancelled
// patch for one instance, because deleting an instance id is not how an
// occurrence leaves a series (§7.4).
//
// Not behind the destructive flag, and §9 argues why: cancelling a
// meeting is the most ordinary write there is, a gate everybody turns on
// protects nobody, and turning it on would also arm clear_calendar. What
// protects it instead is the required scope and the required notify.
func (s *Service) CancelEvent(ctx context.Context, o CancelOptions) (render.WriteReport, error) {
	ctx, env, err := s.prepare(ctx, o.Calendar, o.TimeZone)
	if err != nil {
		return render.WriteReport{}, err
	}
	id, note, err := address(o.EventID, o.OriginalStart)
	if err != nil {
		return render.WriteReport{}, err
	}
	raw, before, err := s.readEvent(ctx, env, id)
	if err != nil {
		return render.WriteReport{}, notFoundHint(err, id, o.OriginalStart)
	}
	if before.Cancelled() {
		return render.WriteReport{}, gapi.Errf(gapi.ClassConflict,
			"that event is already cancelled. list_events and list_instances show cancelled events with "+
				"show_cancelled, which is how you can tell this from a missing one")
	}
	scope, err := plan.Scope(o.Scope, before, s.seriesReach(ctx, env, raw, before))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}

	// "this and following" truncates the series, so it aims at the
	// parent exactly as `series` does — the difference is the rule it
	// writes there, not the event it writes to.
	aimAs := scope
	if scope == recur.ScopeThisAndFollowing {
		aimAs = recur.ScopeSeries
	}
	target, targetModel, aimed, err := s.aim(ctx, env, aimAs, raw, before, o.EventID)
	if err != nil {
		return render.WriteReport{}, err
	}
	if aimed != "" && scope != recur.ScopeThisAndFollowing {
		note = strings.TrimSpace(note + " " + aimed)
	}

	decision, err := plan.Notification(o.Notify, plan.ReachOfEvent(organiserOf(targetModel, env), env.organiser, targetModel))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	etag, err := ifMatch(raw.ETag, target.ETag, o.ETag, o.Force)
	if err != nil {
		return render.WriteReport{}, err
	}

	report := render.WriteReport{
		Verb: render.VerbCancel, Calendar: env.cal.Title, Zone: env.zone,
		Before: &targetModel, Scope: string(scope), Notify: decision.Report(), DryRun: o.DryRun,
	}
	if note != "" {
		report.Notes = append(report.Notes, note)
	}
	if o.Force {
		// §4.4 on the write where it matters most: a forced cancel
		// deletes an event under If-Match: *, so a change somebody made
		// since the read is gone with it. update_event and move_event
		// said so and this did not.
		report.Notes = append(report.Notes, forcedNote)
	}
	report.Notes = append(report.Notes, guestsStillHaveIt(decision))

	switch scope {
	case recur.ScopeThisAndFollowing:
		return s.cancelFollowing(ctx, env, target, targetModel, before, etag,
			decision.SendUpdatesFor(), report)
	case recur.ScopeInstance:
		report.Changes = []plan.Change{{Field: "status", From: targetModel.Status, To: gcal.StatusCancelled}}
		report.Notes = append(report.Notes,
			"One occurrence, cancelled with a status patch rather than deleted: that is how a single date "+
				"leaves a series, and list_instances with show_cancelled still shows it.")
		status := gcal.StatusCancelled
		if o.DryRun {
			after, perr := project(target, gcal.EventPatch{Status: &status}, env)
			if perr != nil {
				return render.WriteReport{}, perr
			}
			report.After, report.Requests = &after, gapi.Requests(ctx)
			return report, nil
		}
		updated, perr := s.API.PatchEvent(ctx, env.cal.ID, target.ID,
			&gcal.EventPatch{Status: &status}, decision.SendUpdatesFor(), etag)
		if perr != nil {
			return render.WriteReport{}, perr
		}
		after, merr := model.FromEvent(env.cal.ID, *updated, &env.zone)
		if merr != nil {
			return render.WriteReport{}, merr
		}
		report.After, report.Requests = &after, gapi.Requests(ctx)
		return report, nil
	default:
		what := "the event"
		if targetModel.IsSeries() {
			what = "the whole series, every occurrence of it"
		}
		// Tense-neutral, because a dry run prints these notes too and
		// "Deleted the event" under "DRY RUN — nothing was written" is
		// the result contradicting itself in six lines.
		report.Notes = append(report.Notes, "This deletes "+what+
			" outright rather than marking it cancelled. Google keeps the record: it is still readable "+
			"with show_cancelled.")
		if o.DryRun {
			// A delete leaves nothing behind, so After stays nil and the
			// renderer says "(cancelled)" — the same line the real call
			// prints, which is the point of a dry run.
			report.Requests = gapi.Requests(ctx)
			return report, nil
		}
		derr := s.API.DeleteEvent(ctx, env.cal.ID, target.ID, decision.SendUpdatesFor(), etag)
		if derr != nil {
			return render.WriteReport{}, alreadyGone(derr)
		}
		report.Requests = gapi.Requests(ctx)
		return report, nil
	}
}

// cancelFollowing ends a series at the target occurrence.
//
// One call rather than the two of a this_and_following update: there is
// no new series to start, so the pattern collapses to truncating the
// original. The exceptions after the target go with the occurrences they
// belonged to, which is what the caller asked for here rather than a
// surprise.
func (s *Service) cancelFollowing(ctx context.Context, env *writeEnv, parentRaw gcal.Event,
	parent, target model.Event, etag, sendUpdates string, report render.WriteReport,
) (render.WriteReport, error) {
	if !target.IsInstance() {
		return render.WriteReport{}, gapi.Errf(gapi.ClassInvalid,
			"\"this and following\" needs the occurrence it starts at. Pass original_start alongside the "+
				"series id, or the occurrence's own id")
	}
	set, err := recur.Parse(parentRaw.Recurrence)
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	split := scheduledStart(target)
	var rule string
	if parent.Start.AllDay {
		rule, _, err = set.SplitDates(parent.Start.Date, split.Date)
	} else {
		rule, _, err = set.Split(parent.Start.At, split.At)
	}
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}

	report.Changes = []plan.Change{{
		Field: "recurrence",
		From:  strings.Join(parentRaw.Recurrence, " "),
		To:    rule,
	}}
	report.Notes = append(report.Notes,
		fmt.Sprintf("The series %s now ends before this occurrence. Earlier occurrences are untouched; "+
			"this one and every later one are gone. This is one call rather than the two a "+
			"this_and_following UPDATE takes, because nothing has to start again.", parent.ID))

	if report.DryRun {
		report.After, report.Requests = &parent, gapi.Requests(ctx)
		return report, nil
	}
	lines := []string{rule}
	updated, err := s.API.PatchEvent(ctx, env.cal.ID, parent.ID,
		&gcal.EventPatch{Recurrence: &lines}, sendUpdates, etag)
	if err != nil {
		return render.WriteReport{}, err
	}
	after, err := model.FromEvent(env.cal.ID, *updated, &env.zone)
	if err != nil {
		return render.WriteReport{}, err
	}
	report.After, report.Requests = &after, gapi.Requests(ctx)
	return report, nil
}

// guestsStillHaveIt is §4.3.3's sentence, and it is the one a caller
// most needs on a cancellation.
//
// Deleting an event with `none` removes it from the organiser's calendar
// and leaves it on the guests' (§18 row 43). That is not quiet, it is a
// meeting they will still turn up to.
func guestsStillHaveIt(d plan.Decision) string {
	// The count comes from the decision that was just made, not from a
	// second pass over the event. One result quoting two guest counts is
	// a defect nobody would think to look for.
	n := d.Reach.Guests
	if n == 0 {
		return "This event has no guests, so nobody else is holding it."
	}
	if d.Asked && d.Notify != plan.NotifyNone {
		return fmt.Sprintf("Google was asked to tell the %d %s. Whether the mail arrives is not something "+
			"the API reports, so check with them if it matters.", n, plan.People(n))
	}
	return fmt.Sprintf("Nobody was asked to be told, and %d %s still %s this meeting on their calendar. "+
		"Cancelling with no notification removes it from yours and leaves it on theirs.",
		n, plan.People(n), holdWord(n))
}

func holdWord(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

// alreadyGone turns a 404 or 410 on a delete into the sentence that
// describes it, because a retried delete lands here and is not a failure.
func alreadyGone(err error) error {
	// On the STATUS, not on the class. classify maps 410 and 412 to the
	// same class, and a 412 is the opposite situation: the event is very
	// much there and somebody edited it between the read and the write.
	// Reporting that as "already gone" tells a caller their meeting was
	// cancelled when it is still live, and sends them to show_cancelled
	// instead of to a re-read — §4.4's protection reported as its
	// opposite.
	var e *gapi.Error
	if !errors.As(err, &e) || (e.Status != http.StatusNotFound && e.Status != http.StatusGone) {
		return err
	}
	return gapi.Wrap(e.Class, err, "that event is already gone: either it was cancelled between this "+
		"server reading it and writing, or it never existed. Read it with get_event and show_cancelled "+
		"to tell the two apart")
}

// -------------------------------------------------------------- moving

// MoveOptions is what move_event takes.
//
// It carries an etag like every other write, and for a while it did not:
// events.move is a POST with no body and nothing Google publishes says
// If-Match applies to it, so this server sent none and its result told
// callers the protection was absent. Spike J asked the API instead of
// the documentation — a stale etag is refused with 412 (§18 row 49) — so
// §4.4 covers this too and the exception is gone.
type MoveOptions struct {
	Calendar      string
	EventID       string
	ToCalendar    string
	OriginalStart string
	Scope         string
	TimeZone      string
	Notify        string
	ETag          string
	Force         bool
	DryRun        bool
}

// MoveEvent changes which calendar an event belongs to, which is to say
// who organises it.
func (s *Service) MoveEvent(ctx context.Context, o MoveOptions) (render.WriteReport, error) {
	// There is nothing to split when an event simply changes calendars,
	// whether or not it repeats, so this is answered before anything is
	// read (§2.8).
	if err := plan.NoSplit(recur.Scope(strings.ToLower(strings.TrimSpace(o.Scope))),
		"moving an event to another calendar"); err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	ctx, env, err := s.prepare(ctx, o.Calendar, o.TimeZone)
	if err != nil {
		return render.WriteReport{}, err
	}
	if strings.TrimSpace(o.ToCalendar) == "" {
		return render.WriteReport{}, gapi.Errf(gapi.ClassInvalid,
			"to_calendar is required: move_event changes which calendar an event lives on")
	}
	dest, err := s.ResolveCalendar(ctx, o.ToCalendar)
	if err != nil {
		return render.WriteReport{}, err
	}
	if dest.ID == env.cal.ID {
		return render.WriteReport{}, gapi.Errf(gapi.ClassInvalid,
			"the event is already on %q. move_event changes the calendar; use update_event to change the time",
			dest.Title)
	}
	if err := needRole(dest, gcal.RoleWriterWithoutPrivateData, "moving an event there"); err != nil {
		return render.WriteReport{}, err
	}

	id, note, err := address(o.EventID, o.OriginalStart)
	if err != nil {
		return render.WriteReport{}, err
	}
	raw, before, err := s.readEvent(ctx, env, id)
	if err != nil {
		return render.WriteReport{}, notFoundHint(err, id, o.OriginalStart)
	}
	scope, err := plan.Scope(o.Scope, before, s.seriesReach(ctx, env, raw, before))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	target, targetModel, aimed, err := s.aim(ctx, env, scope, raw, before, o.EventID)
	if err != nil {
		return render.WriteReport{}, err
	}
	note = strings.TrimSpace(note + " " + aimed)
	decision, err := plan.Notification(o.Notify, plan.ReachOfEvent(organiserOf(targetModel, env), env.organiser, targetModel))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}

	etag, err := ifMatch(raw.ETag, target.ETag, o.ETag, o.Force)
	if err != nil {
		return render.WriteReport{}, err
	}

	report := render.WriteReport{
		Verb: render.VerbMove, Calendar: env.cal.Title, Zone: env.zone,
		Before: &targetModel, Scope: string(scope), Notify: decision.Report(), DryRun: o.DryRun,
		Changes: []plan.Change{{Field: "calendar", From: env.cal.ID, To: dest.ID}},
	}
	if note != "" {
		report.Notes = append(report.Notes, note)
	}
	if o.Force {
		report.Notes = append(report.Notes, forcedNote)
	}
	report.Notes = append(report.Notes,
		fmt.Sprintf("Moving an event changes its organiser to %q. Its id does not change, but the calendar "+
			"it is addressed on does — read it on %s from now on.", dest.Title, dest.ID),
		"The move is made under If-Match, like every other write here, so it is refused rather than "+
			"applied if somebody changed the event since it was read.")

	if o.DryRun {
		report.After, report.Requests = &targetModel, gapi.Requests(ctx)
		return report, nil
	}
	moved, err := s.API.MoveEvent(ctx, env.cal.ID, target.ID, dest.ID, decision.SendUpdatesFor(), etag)
	if err != nil {
		return render.WriteReport{}, err
	}
	// The event is READ BACK from the destination rather than taken from
	// the move's own response, and the live run is why: a successful
	// move answers with `status: cancelled`, so the result reported a
	// meeting that had just been moved as a meeting that had been
	// called off. Reading the destination afterwards showed it
	// confirmed and intact (§18 row 48).
	//
	// That costs one request, which §4.7 says a result must then own —
	// api_requests counts it, and the note below says the move is two
	// calls. It buys the difference between "moved" and "cancelled" in
	// the one sentence a caller acts on.
	landed := *moved
	if fresh, rerr := s.API.GetEvent(ctx, dest.ID, moved.ID); rerr == nil {
		landed = *fresh
	} else {
		report.Notes = append(report.Notes,
			"The move succeeded, but reading the event back on its new calendar did not, so the state "+
				"shown above is what the move call returned. Read it with get_event to be sure.")
	}
	after, err := model.FromEvent(dest.ID, landed, &env.zone)
	if err != nil {
		return render.WriteReport{}, err
	}
	report.After, report.Requests = &after, gapi.Requests(ctx)
	return report, nil
}

// ------------------------------------------------------------ responding

// RespondOptions is what respond_to_event takes.
type RespondOptions struct {
	Calendar      string
	EventID       string
	OriginalStart string
	Scope         string
	TimeZone      string
	Response      string
	Comment       string
	Notify        string
	ETag          string
	Force         bool
	DryRun        bool
}

// RespondToEvent sets this account's own RSVP.
//
// Separate from update_event because RSVPing is not editing: the
// permissions differ, and a model that conflates them will try to RSVP
// by patching the whole attendee array — which is how everybody else's
// response gets overwritten.
func (s *Service) RespondToEvent(ctx context.Context, o RespondOptions) (render.WriteReport, error) {
	// Both of these read only the caller's own arguments, so they are
	// answered before a request is spent. "this and following" is
	// refused whether or not the event repeats: there is no such
	// operation for an RSVP either way (§2.8).
	response, err := parseResponse(o.Response)
	if err != nil {
		return render.WriteReport{}, err
	}
	if err := plan.NoSplit(recur.Scope(strings.ToLower(strings.TrimSpace(o.Scope))),
		"answering an invitation"); err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	ctx, env, err := s.prepare(ctx, o.Calendar, o.TimeZone)
	if err != nil {
		return render.WriteReport{}, err
	}
	id, note, err := address(o.EventID, o.OriginalStart)
	if err != nil {
		return render.WriteReport{}, err
	}
	raw, before, err := s.readEvent(ctx, env, id)
	if err != nil {
		return render.WriteReport{}, notFoundHint(err, id, o.OriginalStart)
	}
	scope, err := plan.Scope(o.Scope, before, s.seriesReach(ctx, env, raw, before))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	target, targetModel, aimed, err := s.aim(ctx, env, scope, raw, before, o.EventID)
	if err != nil {
		return render.WriteReport{}, err
	}
	note = strings.TrimSpace(note + " " + aimed)

	attendees, was, err := setOwnResponse(target.Attendees, env.organiser, response, o.Comment)
	if err != nil {
		return render.WriteReport{}, err
	}
	decision, err := plan.Notification(o.Notify, plan.ReachOfEvent(organiserOf(targetModel, env), env.organiser, targetModel))
	if err != nil {
		return render.WriteReport{}, classifyPlan(err)
	}
	etag, err := ifMatch(raw.ETag, target.ETag, o.ETag, o.Force)
	if err != nil {
		return render.WriteReport{}, err
	}

	report := render.WriteReport{
		Verb: render.VerbAnswer, Calendar: env.cal.Title, Zone: env.zone,
		Before: &targetModel, Scope: string(scope), Notify: decision.Report(), DryRun: o.DryRun,
		Changes: []plan.Change{{Field: "your response", From: was, To: response}},
	}
	if note != "" {
		report.Notes = append(report.Notes, note)
	}
	report.Notes = append(report.Notes,
		"Only this account's own response changed. Every other guest's answer is exactly as it was read, "+
			"which is why RSVPing is its own tool rather than an update.")
	if o.Force {
		report.Notes = append(report.Notes, forcedNote)
	}

	if o.DryRun {
		report.After, report.Requests = &targetModel, gapi.Requests(ctx)
		return report, nil
	}
	updated, err := s.API.PatchEvent(ctx, env.cal.ID, target.ID,
		&gcal.EventPatch{Attendees: &attendees}, decision.SendUpdatesFor(), etag)
	if err != nil {
		return render.WriteReport{}, err
	}
	after, err := model.FromEvent(env.cal.ID, *updated, &env.zone)
	if err != nil {
		return render.WriteReport{}, err
	}
	report.After, report.Requests = &after, gapi.Requests(ctx)
	return report, nil
}

// parseResponse reads the three answers an invitation has.
//
// needsAction is not among them: it is the state an invitation starts
// in, not an answer somebody gives, and offering it as a choice would
// let a model "un-answer" an invitation it had already accepted.
func parseResponse(v string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "accepted", "yes", "accept":
		return gcal.ResponseAccepted, nil
	case "declined", "no", "decline":
		return gcal.ResponseDeclined, nil
	case "tentative", "maybe":
		return gcal.ResponseTentative, nil
	case "":
		return "", gapi.Errf(gapi.ClassInvalid,
			"response is required: accepted, declined or tentative")
	default:
		return "", gapi.Errf(gapi.ClassInvalid,
			"%q is not an answer to an invitation. Pass accepted, declined or tentative", v)
	}
}

// setOwnResponse rewrites this account's entry and nobody else's (§4.4).
//
// The list is the one that was read, carried through unchanged except
// for the caller's own row — comments, extra-guest counts and everybody
// else's responses included. A replacement built from the caller's
// intent alone is how an RSVP that arrived in between gets erased.
func setOwnResponse(attendees []gcal.EventAttendee, self, response, comment string) ([]gcal.EventAttendee, string, error) {
	out := make([]gcal.EventAttendee, len(attendees))
	copy(out, attendees)
	for i, a := range out {
		mine := a.Self || (self != "" && strings.EqualFold(a.Email, self))
		if !mine {
			continue
		}
		was := a.ResponseStatus
		if was == "" {
			was = gcal.ResponseNeedsAction
		}
		out[i].ResponseStatus = response
		if comment != "" {
			out[i].Comment = comment
		}
		return out, was, nil
	}
	return nil, "", gapi.Errf(gapi.ClassInvalid,
		"this account is not a guest on that event, so there is no invitation to answer. "+
			"get_event lists who is; use update_event if you organise it")
}
