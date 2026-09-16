package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/gapi"
	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/plan"
	"github.com/mmedum/google-calendar-mcp/internal/render"
)

// The calendar writes (§7.5): create_calendar, manage_calendar, and the
// two gated ones.
//
// **No etag parameter, and the reason is the API's shape rather than an
// omission.** An event is one resource with one etag, so §4.4's round
// trip — read it, decide, write under the version you read — has one
// thing to hold. A calendar is two: the calendar itself and this user's
// subscription to it, each with its own etag, each patched by a
// different method. One `etag` on the call could stand for only one of
// them while appearing to protect both, which is worse than not offering
// it. So each patch here carries the etag of the read that produced it,
// made immediately before, and the result shows before and after for
// every field so a caller can see a value they did not expect.

// openCalendar resolves a calendar for a write WITHOUT requiring write
// access to it.
//
// prepare refuses a calendar this account cannot write events to, which
// is right for an event write and wrong here: the per-user half of
// manage_calendar — the colour, the name you gave it, whether it is
// hidden — works perfectly well on a calendar you can only read. The
// access each axis actually needs is checked where that axis is applied.
func (s *Service) openCalendar(ctx context.Context, ref string) (context.Context, model.Calendar, error) {
	if err := s.ready(); err != nil {
		return ctx, model.Calendar{}, err
	}
	ctx = gapi.WithCounter(ctx)
	c, err := s.ResolveCalendar(ctx, ref)
	if err != nil {
		return ctx, model.Calendar{}, err
	}
	return ctx, c, nil
}

// needRole refuses when the access role is KNOWN and is not enough.
//
// Known is the operative word, as in prepare: a calendar reached by id
// alone carries no role, and refusing on a field this server could not
// read would deny a write Google would have allowed.
//
// The two thresholds are where the evidence puts them, not where caution
// would. **Sharing needs owner** because Google's own role description
// says so: owner "has all of the permissions of the writer role with the
// additional ability to modify access levels of other users". **Changing
// the calendar itself needs writer**, which is the strongest claim the
// documentation supports — a reader plainly cannot write anything, and
// whether a writer may rename a calendar is not documented either way,
// so that one is left to Google to answer rather than guessed at here.
func needRole(c model.Calendar, want, what string) error {
	if c.Role == "" || gcal.AtLeast(c.Role, want) {
		return nil
	}
	return gapi.Errf(gapi.ClassForbidden,
		"this account's access to %q is %s, which %s. %s needs %s access; ask whoever owns the calendar",
		c.Title, c.Role, gcal.RoleMeans(c.Role), what, want)
}

// ------------------------------------------------------------ creating

// CreateCalendarOptions is what create_calendar takes.
type CreateCalendarOptions struct {
	Title       string
	Description string
	Location    string
	TimeZone    string
	DryRun      bool
}

// CreateCalendar makes a new secondary calendar.
//
// The zone is resolved rather than left to Google (§4.1): a calendar
// created with no zone takes the account's, which is usually right and
// is never stated. This server sends one and says where it came from, so
// a calendar created from a machine in another zone cannot quietly
// disagree with the events that land on it.
func (s *Service) CreateCalendar(ctx context.Context, o CreateCalendarOptions) (render.CalendarReport, error) {
	if err := s.ready(); err != nil {
		return render.CalendarReport{}, err
	}
	ctx = gapi.WithCounter(ctx)

	title := strings.TrimSpace(o.Title)
	if title == "" {
		return render.CalendarReport{}, gapi.Errf(gapi.ClassInvalid,
			"title is required: a calendar with no name cannot be told apart in anybody's list")
	}
	// No calendar to read a zone from — it does not exist yet — so the
	// order is the caller's, then the account's setting.
	zone, err := s.Zone(ctx, o.TimeZone, "")
	if err != nil {
		return render.CalendarReport{}, err
	}

	body := &gcal.Calendar{
		Summary: title, Description: o.Description, Location: o.Location, TimeZone: zone.Name(),
	}
	report := render.CalendarReport{Verb: render.VerbCreate, DryRun: o.DryRun}
	report.Notes = append(report.Notes, zone.Explain()+
		"\nThe new calendar is created in that zone, and every time written to it is read against it.")

	if o.DryRun {
		report.Calendar = model.Calendar{
			Title: title, TimeZone: zone.Name(), Description: o.Description,
			Role: gcal.RoleOwner, Selected: true,
		}
		report.Notes = append(report.Notes, "The id is Google's to mint, so a dry run cannot show it.")
		report.Requests = gapi.Requests(ctx)
		return report, nil
	}

	created, err := s.API.InsertCalendar(ctx, body)
	if err != nil {
		return render.CalendarReport{}, createCalendarError(err, title)
	}
	report.Calendar = model.Calendar{
		ID: created.ID, Title: created.Summary, TimeZone: created.TimeZone,
		Description: created.Description, ETag: created.ETag,
		Role: gcal.RoleOwner, Selected: true,
	}
	// Google subscribes the creator, so the cached list is now missing a
	// calendar the account has. Without this, resolving it by the title
	// this call just gave it answered "no calendar called that" for the
	// life of the process.
	s.rememberCalendar(report.Calendar)
	report.Notes = append(report.Notes,
		"You are its owner, and it is in your calendar list. Nobody else can see it until you share it.")
	report.Requests = gapi.Requests(ctx)
	return report, nil
}

// createCalendarError is §2.11's problem without §2.11's remedy.
//
// An event insert can supply its own id, so a retry after an answer
// nobody saw either lands or collides. A calendar insert cannot: Google
// mints the id, so a blind retry makes a second calendar with the same
// title — and calendar creations are quota-counted and not refunded by
// deleting them (§18 row 36). So a failure that may have landed says
// "go and look", by title, and does not retry.
func createCalendarError(err error, title string) error {
	e, maybe := maybeLanded(err)
	if !maybe {
		return err
	}
	return gapi.Wrap(gapi.ClassAmbiguousOutcome, err,
		"this calendar may or may not have been created: the request failed without an answer this server "+
			"can trust (%s). Run list_calendars and look for %q before trying again — Google mints the id, "+
			"so a retry cannot collide and would simply make a second one",
		e.Message, title)
}

// ------------------------------------------------------------- managing

// ManageOptions is what manage_calendar takes.
//
// Three axes, and the struct keeps them apart because the API does and
// because the difference matters to other people: the first group
// changes the calendar for everybody, the second changes this account's
// list, the third changes only how this account sees it.
type ManageOptions struct {
	Calendar string

	// The calendar itself (calendars.patch).
	Title       *string
	Description *string
	Location    *string
	TimeZone    *string

	// The subscription (calendarList.insert / delete).
	Subscribe   bool
	Unsubscribe bool

	// This user's own overrides (calendarList.patch).
	MyName        *string
	ColorID       *string
	Hidden        *bool
	Selected      *bool
	Notifications *[]string

	DryRun bool
}

// ManageCalendar changes a calendar, a subscription to it, or one
// user's view of it (§7.5).
func (s *Service) ManageCalendar(ctx context.Context, o ManageOptions) (render.CalendarReport, error) {
	ctx, cal, err := s.openCalendar(ctx, o.Calendar)
	if err != nil {
		return render.CalendarReport{}, err
	}

	shared := plan.CalendarDraft{
		Title: o.Title, Description: o.Description, Location: o.Location, TimeZone: o.TimeZone,
	}
	mine := plan.ListDraft{
		MyName: o.MyName, ColorID: o.ColorID, Hidden: o.Hidden,
		Selected: o.Selected, Notifications: o.Notifications,
	}
	if err := manageGuards(o, cal, shared, mine); err != nil {
		return render.CalendarReport{}, err
	}

	// Before the report is built, because this path builds its own and
	// the one here would be discarded.
	if o.Unsubscribe {
		return s.unsubscribe(ctx, cal, o.DryRun)
	}

	report := render.CalendarReport{Verb: render.VerbUpdate, DryRun: o.DryRun, Calendar: cal}
	before := cal
	report.Before = &before

	// entry is the subscription a subscribe in this same call just
	// created, so the per-user patch below does not read back what
	// Google returned a moment ago. nil everywhere else.
	var entry *gcal.CalendarListEntry
	if o.Subscribe {
		made, err := s.subscribe(ctx, cal, &report)
		if err != nil {
			return render.CalendarReport{}, err
		}
		entry = made
	}

	if !shared.Empty() {
		if err := s.patchCalendarItself(ctx, cal, shared, &report); err != nil {
			return render.CalendarReport{}, partly(err, report)
		}
	}
	if !mine.Empty() {
		if err := s.patchSubscription(ctx, cal, mine, &report, entry); err != nil {
			return render.CalendarReport{}, partly(err, report)
		}
	}
	report.Requests = gapi.Requests(ctx)
	return report, nil
}

// partly names what a failed call already did.
//
// manage_calendar can be two patches, and the second failing does not
// undo the first: a call that renamed the calendar and then failed to
// set the colour returned a bare error, so the caller could not tell
// that half the write had landed and a retry would redo it. There is no
// rollback to offer — §2 has no transaction — so the honest thing is to
// say which change is already made.
func partly(err error, report render.CalendarReport) error {
	var done []string
	// A subscribe is not a field change and would otherwise vanish from
	// this sentence, leaving a caller to unsubscribe by hand after a
	// failure they were told nothing about.
	if report.Verb == render.VerbSubscribe {
		done = append(done, "the subscription (this calendar is in your list now)")
	}
	for _, c := range report.Changes {
		done = append(done, c.Field)
	}
	if len(done) == 0 {
		return err
	}
	return gapi.Wrap(classOf(err), err,
		"%s\n\nThis call had already changed %s before it failed, and that change stands — there is no "+
			"rollback. Re-read the calendar before trying the rest again",
		message(err), strings.Join(done, ", "))
}

// classOf and message take a failure apart so partly can rebuild it
// without changing what it says about itself.
func classOf(err error) gapi.Class {
	if cls, ok := gapi.ClassOf(err); ok {
		return cls
	}
	return gapi.ClassUnavailable
}

func message(err error) string {
	var e *gapi.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

// manageGuards refuses the combinations that cannot mean anything.
func manageGuards(o ManageOptions, cal model.Calendar, shared plan.CalendarDraft, mine plan.ListDraft) error {
	if o.Subscribe && o.Unsubscribe {
		return gapi.Errf(gapi.ClassInvalid,
			"subscribe and unsubscribe were both passed, which cannot both be meant. Pass one")
	}
	if o.Unsubscribe {
		if cal.Primary {
			return gapi.Errf(gapi.ClassBlocked,
				"this is your own primary calendar, and it cannot be removed from your list. Hiding it is "+
					"the nearest thing: pass hidden:true")
		}
		if !shared.Empty() || !mine.Empty() {
			return gapi.Errf(gapi.ClassInvalid,
				"unsubscribe removes this calendar from your list, so the other changes in this call would "+
					"have nowhere to land. Make them first, or unsubscribe on its own")
		}
		return nil
	}
	if shared.Empty() && mine.Empty() && !o.Subscribe {
		return gapi.Errf(gapi.ClassInvalid,
			"this call asks for no change. Pass a title, description, location or time_zone to change the "+
				"calendar itself; my_name, color_id, hidden, selected or notifications to change only how "+
				"you see it; or subscribe/unsubscribe to add or remove it from your list")
	}
	if !shared.Empty() {
		if err := needRole(cal, gcal.RoleWriter, "changing the calendar itself"); err != nil {
			return err
		}
	}
	return nil
}

// unsubscribe removes the calendar from this user's list and nothing
// else. Its own function because the result has to say what it did NOT
// do: the calendar and every event on it are untouched, and nobody else
// notices.
func (s *Service) unsubscribe(ctx context.Context, cal model.Calendar, dryRun bool) (render.CalendarReport, error) {
	report := render.CalendarReport{Verb: render.VerbUnsubscribe, DryRun: dryRun, Calendar: cal}
	report.Notes = append(report.Notes,
		"This removes the calendar from YOUR list. The calendar itself is untouched, every event on it stays, "+
			"and nobody else sees any difference. Subscribe again with subscribe:true and the id above.")
	if !dryRun {
		etag, err := s.entryETag(ctx, cal)
		if err != nil {
			return render.CalendarReport{}, notInYourList(err, cal)
		}
		if err := s.API.DeleteCalendarListEntry(ctx, cal.ID, etag); err != nil {
			return render.CalendarReport{}, err
		}
		s.forgetCalendar(cal.ID)
	}
	report.Requests = gapi.Requests(ctx)
	return report, nil
}

// entryETag and calendarETag are the etag of the resource a write is
// about to touch — from the read that already produced it where there
// was one, and from a fresh read otherwise.
//
// Read fresh, not taken from the resolved calendar, and the reason is
// that these tools have no `force`. The calendar list is cached for the
// process (§7.1), so its etag can be minutes old; sending it would turn
// a colour somebody changed in the web UI into a `[stale]` refusal with
// no way past it. A read immediately before the write is what the tool
// descriptions promise and the only version worth holding a write to.
//
// model.Calendar keeps the two etags in two fields — one for the
// calendar, one for the subscription — so neither can stand in for the
// other. A single field carrying "whichever read produced the value"
// sent the subscription's etag to calendars.delete on every
// list-resolved calendar, which is a 412 that reads to the caller as
// somebody else's edit.
func (s *Service) entryETag(ctx context.Context, cal model.Calendar) (string, error) {
	entry, err := s.API.GetCalendarListEntry(ctx, cal.ID)
	if err != nil {
		return "", err
	}
	return entry.ETag, nil
}

func (s *Service) calendarETag(ctx context.Context, cal model.Calendar) (string, error) {
	current, err := s.API.GetCalendar(ctx, cal.ID)
	if err != nil {
		return "", err
	}
	return current.ETag, nil
}

// subscribe adds an existing calendar to this user's list.
func (s *Service) subscribe(ctx context.Context, cal model.Calendar,
	report *render.CalendarReport,
) (*gcal.CalendarListEntry, error) {
	report.Verb = render.VerbSubscribe
	// Being able to resolve it does not mean it is in the list: a
	// calendar reached by id alone is not, and that is exactly the case
	// subscribing is for.
	if s.subscribed(ctx, cal.ID) {
		report.Verb = render.VerbUpdate
		report.Notes = append(report.Notes,
			"Already in your calendar list, so nothing was added. Everything else in this call still applied.")
		return nil, nil
	}
	if report.DryRun {
		return nil, nil
	}
	entry, err := s.API.InsertCalendarListEntry(ctx, &gcal.CalendarListEntry{ID: cal.ID})
	if err != nil {
		return nil, err
	}
	report.Calendar = model.FromCalendarList(*entry)
	s.rememberCalendar(report.Calendar)
	report.Notes = append(report.Notes,
		"Subscribing adds it to your list. It grants no access: what you can see is still what the calendar "+
			"is shared with you as.")
	return entry, nil
}

// subscribed reports whether this account's list holds a calendar.
func (s *Service) subscribed(ctx context.Context, id string) bool {
	cals, err := s.Calendars(ctx, true)
	if err != nil {
		return false
	}
	for _, c := range cals {
		if c.ID == id {
			return true
		}
	}
	return false
}

// patchCalendarItself changes what everybody subscribed to the calendar
// sees.
func (s *Service) patchCalendarItself(ctx context.Context, cal model.Calendar,
	draft plan.CalendarDraft, report *render.CalendarReport,
) error {
	// Read the calendar resource, not the list entry: its etag is what
	// calendars.patch is held to, and the entry carries a different one.
	current, err := s.API.GetCalendar(ctx, cal.ID)
	if err != nil {
		return err
	}
	patch, changes, err := draft.Patch(*current)
	if err != nil {
		return classifyPlan(err)
	}
	report.Changes = append(report.Changes, changes...)
	if len(changes) == 0 {
		report.Notes = append(report.Notes,
			"The calendar itself already reads that way, so nothing was sent for it.")
		return nil
	}
	report.Notes = append(report.Notes,
		"That changes the calendar for everybody it is shared with, not only for you. To change only your "+
			"own view of it, pass my_name rather than title.")
	if report.DryRun {
		next := *current
		patch.ApplyTo(&next)
		report.Calendar = mergeCalendar(report.Calendar, next)
		return nil
	}
	updated, err := s.API.PatchCalendar(ctx, cal.ID, &patch, current.ETag)
	if err != nil {
		return err
	}
	report.Calendar = mergeCalendar(report.Calendar, *updated)
	s.rememberCalendar(report.Calendar)
	return nil
}

// patchSubscription changes only how this account sees the calendar.
func (s *Service) patchSubscription(ctx context.Context, cal model.Calendar,
	draft plan.ListDraft, report *render.CalendarReport, current *gcal.CalendarListEntry,
) error {
	if current == nil {
		read, err := s.API.GetCalendarListEntry(ctx, cal.ID)
		if err != nil {
			return notSubscribed(err, cal)
		}
		current = read
	}
	patch, changes, err := draft.Patch(*current)
	if err != nil {
		return classifyPlan(err)
	}
	report.Changes = append(report.Changes, changes...)
	if len(changes) == 0 {
		report.Notes = append(report.Notes,
			"Your own view of it already reads that way, so nothing was sent for it.")
		return nil
	}
	report.Notes = append(report.Notes,
		"Those are your own settings for this calendar. Nobody else sees any of them change.")
	if report.DryRun {
		next := *current
		patch.ApplyTo(&next)
		report.Calendar = model.FromCalendarList(next)
		return nil
	}
	updated, err := s.API.PatchCalendarListEntry(ctx, cal.ID, &patch, current.ETag)
	if err != nil {
		return err
	}
	report.Calendar = model.FromCalendarList(*updated)
	s.rememberCalendar(report.Calendar)
	return nil
}

// notSubscribed turns "no such list entry" into the sentence that says
// what to do about it, because the per-user settings only exist for a
// calendar in the list.
func notSubscribed(err error, cal model.Calendar) error {
	if cls, ok := gapi.ClassOf(err); !ok || cls != gapi.ClassNotFound {
		return err
	}
	return gapi.Errf(gapi.ClassNotFound,
		"%q is not in your calendar list, and the colour, the name you give it and the notifications are "+
			"settings ON that list entry. Pass subscribe:true in the same call to add it first", cal.Title)
}

// notInYourList is the same failure from the other direction: asked to
// remove a subscription that is not there. "Add it first" would be
// absurd advice, and unsubscribe refuses the combination that would let
// a caller follow it.
func notInYourList(err error, cal model.Calendar) error {
	if cls, ok := gapi.ClassOf(err); !ok || cls != gapi.ClassNotFound {
		return err
	}
	return gapi.Errf(gapi.ClassNotFound,
		"%q is not in your calendar list, so there is nothing to remove. list_calendars shows what is in it",
		cal.Title)
}

// mergeCalendar folds a patched calendar resource into what the report
// already holds, keeping the fields only the list entry carries.
//
// The calendar resource has no access role, no colour and no hidden
// flag: they belong to the subscription. Replacing the report's calendar
// with the resource wholesale would print "your access: " with nothing
// after it on every rename.
//
// The rename rule is model.FromCalendarList's and is not restated here.
// The first version of this function wrote its own copy and inverted it:
// it set Title from the calendar's new summary and then, for a user who
// had renamed the calendar, set Title = Original — so renaming a shared
// calendar erased that user's own name for it and printed "Team planning
// (you renamed this; others see \"Team planning\")", a line that
// contradicts itself. A rename of the calendar itself does not touch
// summaryOverride, so the override wins and only the original moves.
func mergeCalendar(have model.Calendar, c gcal.Calendar) model.Calendar {
	have.ID = c.ID
	have.Description = c.Description
	have.TimeZone = c.TimeZone
	have.ETag = c.ETag
	if have.Original == "" {
		// No override: the calendar's own title is what this user sees.
		have.Title = c.Summary
		return have
	}
	have.Original = c.Summary
	return have
}

// rememberCalendar and forgetCalendar keep the cached list right after a
// write, instead of throwing it away.
//
// The list is cached for the process (§7.1), which is what keeps a
// resolution from costing a request; dropping it made the NEXT tool call
// pay for a full calendarList.list, so setting a colour on ten calendars
// cost thirty requests instead of twenty-one. Every write here has the
// row Google just returned in hand, so the cache can be corrected rather
// than emptied.
//
// A calendar this process CREATED has to be added for a second reason,
// and it is a defect rather than a cost: create_calendar left the cache
// untouched, so the new calendar was not in it, and resolving the
// calendar by the title it had just been given answered "no calendar
// called that" — for the life of the process, with no way for the caller
// to recover.
func (s *Service) rememberCalendar(c model.Calendar) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.calendarsAt {
		return
	}
	for i, have := range s.calendars {
		if have.ID == c.ID {
			s.calendars[i] = c
			return
		}
	}
	s.calendars = append(s.calendars, c)
	sortCalendars(s.calendars)
}

func (s *Service) forgetCalendar(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.calendarsAt {
		return
	}
	out := s.calendars[:0]
	for _, have := range s.calendars {
		if have.ID != id {
			out = append(out, have)
		}
	}
	s.calendars = out
}

// --------------------------------------------------------- destructive

// DestructiveOptions is what delete_calendar and clear_calendar take.
type DestructiveOptions struct {
	Calendar string
	Confirm  bool
	DryRun   bool
}

// DeleteCalendar removes a secondary calendar and everything on it.
//
// Registered only under GCAL_ENABLE_DESTRUCTIVE, and still refused
// without confirm on the call: the flag is the deployment's decision and
// the confirmation is this call's, and §9 wants both.
func (s *Service) DeleteCalendar(ctx context.Context, o DestructiveOptions) (render.CalendarReport, error) {
	ctx, cal, err := s.openCalendar(ctx, o.Calendar)
	if err != nil {
		return render.CalendarReport{}, err
	}
	if cal.Primary {
		return render.CalendarReport{}, gapi.Errf(gapi.ClassUnsupported,
			"this is your own primary calendar, and Google's delete removes only secondary ones. There is "+
				"no way to delete it: clear_calendar empties it, which cannot be undone either")
	}
	if err := needRole(cal, gcal.RoleOwner, "deleting a calendar"); err != nil {
		return render.CalendarReport{}, err
	}
	if err := plan.Confirm(o.Confirm,
		fmt.Sprintf("this deletes the calendar %q and every event on it, for everybody it is shared with",
			cal.Title)); err != nil {
		return render.CalendarReport{}, classifyPlan(err)
	}

	report := render.CalendarReport{Verb: render.VerbDelete, DryRun: o.DryRun, Calendar: cal}
	report.Notes = append(report.Notes,
		"Everything on it goes with it, for everybody it was shared with, and Calendar cannot bring it back. "+
			"To keep the calendar and remove only its events, use clear_calendar.")
	if !o.DryRun {
		etag, err := s.calendarETag(ctx, cal)
		if err != nil {
			return render.CalendarReport{}, err
		}
		if err := s.API.DeleteCalendar(ctx, cal.ID, etag); err != nil {
			return render.CalendarReport{}, err
		}
		s.forgetCalendar(cal.ID)
	}
	report.Requests = gapi.Requests(ctx)
	return report, nil
}

// ClearCalendar deletes every event on the account's primary calendar.
//
// The most destructive call this API offers (§9), and the narrowest:
// Google's own description is "Clears a primary calendar", so a
// secondary one is refused here rather than sent and hoped for. Which
// way Google actually answers for a secondary calendar is spike K; until
// that has run, this server does not send a request whose effect it
// cannot state.
func (s *Service) ClearCalendar(ctx context.Context, o DestructiveOptions) (render.CalendarReport, error) {
	ctx, cal, err := s.openCalendar(ctx, o.Calendar)
	if err != nil {
		return render.CalendarReport{}, err
	}
	if !cal.Primary {
		return render.CalendarReport{}, gapi.Errf(gapi.ClassUnsupported,
			"clear empties the account's OWN primary calendar, which %q is not — Google documents the method "+
				"as clearing a primary calendar. To empty this one, delete_calendar removes it whole, or "+
				"cancel_event removes events one at a time", cal.Title)
	}
	if err := plan.Confirm(o.Confirm,
		"this deletes EVERY event on your primary calendar, past and future, invitations included"); err != nil {
		return render.CalendarReport{}, classifyPlan(err)
	}

	report := render.CalendarReport{Verb: render.VerbClear, DryRun: o.DryRun, Calendar: cal}
	report.Notes = append(report.Notes,
		"Every event on this calendar is deleted: past, future, and every meeting you organise. Guests are "+
			"not notified by this call. Calendar cannot bring any of it back.")
	if !o.DryRun {
		etag, err := s.calendarETag(ctx, cal)
		if err != nil {
			return render.CalendarReport{}, err
		}
		if err := s.API.ClearCalendar(ctx, cal.ID, etag); err != nil {
			return render.CalendarReport{}, err
		}
	}
	report.Requests = gapi.Requests(ctx)
	return report, nil
}
