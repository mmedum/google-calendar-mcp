// Package model is the server's view of a calendar and an event: the
// wire types of internal/gcal turned into values that carry resolved
// time (internal/when) and nothing ambiguous.
//
// The conversion happens here and nowhere else, so there is one place
// where a gcal.EventDateTime becomes either a Date or a Zoned, and one
// place to look when a time is wrong.
package model

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/when"
)

// Calendar is a calendar as this server presents it.
type Calendar struct {
	ID          string
	Title       string
	Original    string // the calendar's own title, when this user renamed it
	TimeZone    string
	Role        string
	Primary     bool
	Selected    bool
	Hidden      bool
	ColorID     string
	Description string
	// Conference is which conference types this calendar accepts, nil
	// when Google said nothing about it — which is not a refusal (§17.3).
	Conference *gcal.ConferenceProperties
	// DefaultReminders are the reminders this account gets for an event
	// on this calendar that uses the calendar's own. Nil when the
	// calendar was read as a resource rather than from this account's
	// list, which is the only place Google keeps them.
	DefaultReminders *Reminders
	// No etag, deliberately. A calendar is TWO resources — itself and
	// this user's subscription to it — with an etag each, and a single
	// field here carried whichever read had produced the value: the
	// subscription's etag reached calendars.delete and was refused on
	// every call, reported to the caller as somebody else's edit.
	// Splitting it in two left two fields nothing read, because a write
	// cannot use a cached etag anyway: these tools have no `force`, so a
	// version minutes old would be a [stale] refusal with no way past
	// it. Each write reads the etag it is held to, immediately before
	// making the write, and holds it as a local.
}

// FromCalendarList converts one subscription entry.
func FromCalendarList(e gcal.CalendarListEntry) Calendar {
	c := Calendar{
		ID: e.ID, Title: e.Summary, TimeZone: e.TimeZone, Role: e.AccessRole,
		Primary: e.Primary, Selected: e.Selected, Hidden: e.Hidden,
		ColorID: e.ColorID, Description: e.Description,
		Conference:       e.ConferenceProperties,
		DefaultReminders: remindersFrom(false, e.DefaultReminders),
	}
	// A rename is this user's alone: the same calendar has a different
	// name for a colleague, so both are carried and the renderer says so.
	if e.SummaryOverride != "" {
		c.Title = e.SummaryOverride
		c.Original = e.Summary
	}
	return c
}

// FromCalendar converts the calendar RESOURCE, which is what a read by
// id falls back to when the account is not subscribed to it.
//
// It exists because the hand-built literals it replaces were where a new
// wire field went missing: `conferenceProperties` reached
// FromCalendarList and not these, so a calendar resolved by id looked
// like one that allows every conference type and the guard in §17.3
// never fired for it. Two constructors, both here, is the shape that
// makes the next field a one-line change rather than a hunt.
func FromCalendar(c gcal.Calendar) Calendar {
	return Calendar{
		ID: c.ID, Title: c.Summary, TimeZone: c.TimeZone,
		Description: c.Description, Conference: c.ConferenceProperties,
	}
}

// CanWrite reports whether this user may change events here.
func (c Calendar) CanWrite() bool {
	return gcal.AtLeast(c.Role, gcal.RoleWriterWithoutPrivateData)
}

// When is an event's start or end: a Date or a Zoned, never both, never
// neither. It is the type that makes §4.1 structural rather than a
// convention.
type When struct {
	// AllDay says which half is set.
	AllDay bool
	Date   when.Date
	At     when.Zoned
}

// IsZero reports whether this end of an event was set at all.
//
// Which half to look at depends on AllDay, and that is exactly the check
// callers kept writing out longhand and getting subtly different each
// time.
func (w When) IsZero() bool {
	if w.AllDay {
		return w.Date.IsZero()
	}
	return w.At.IsZero()
}

// ParseWhen converts a wire EventDateTime, rendering any instant in loc.
//
// loc affects only how a timed event READS; it never touches an all-day
// date, because there is nothing there to convert.
func ParseWhen(e *gcal.EventDateTime, loc *when.Zone) (When, error) {
	if e == nil {
		return When{}, nil
	}
	if e.Date != "" {
		d, err := when.ParseDate(e.Date)
		if err != nil {
			return When{}, err
		}
		return When{AllDay: true, Date: d}, nil
	}
	if e.DateTime == "" {
		return When{}, nil
	}
	// A nil zone means "render in whatever offset the wire carried",
	// which is only correct when the caller has no zone to impose. Every
	// tool path resolves one first (§4.1); this branch exists for the
	// renderer's own round-trip tests.
	var target *time.Location
	if loc != nil {
		target = loc.Loc
	}
	z, err := when.ParseZoned(e.DateTime, target)
	if err != nil {
		return When{}, err
	}
	return When{At: z}, nil
}

// Event is an event as this server presents it.
type Event struct {
	ID          string
	CalendarID  string
	Title       string
	Description string
	Location    string
	Status      string
	Type        string
	Link        string
	ETag        string

	Start When
	End   When
	// EndInvented is Google's endTimeUnspecified: the end is not one
	// anybody set, and a duration computed from it is fiction.
	EndInvented bool

	// Recurrence is the series' RRULE lines, set on a parent.
	Recurrence []string
	// SeriesID is set on an instance and names its parent.
	SeriesID string
	// OriginalStart identifies an instance even after it is moved.
	OriginalStart When

	Transparent bool
	// Visibility is Google's value as it came, empty for the default.
	Visibility string
	// Reminders are this account's own for this event, nil when Google
	// sent none.
	Reminders *Reminders
	// What a guest may do, with Google's defaults filled in: a guest can
	// invite others and see the guest list, and cannot change the event.
	GuestsCanModify         bool
	GuestsCanInviteOthers   bool
	GuestsCanSeeOtherGuests bool
	// Conference is the event's video meeting, if it has one: the link
	// to join, or the fact that Google is still making it (§17.3).
	Conference gcal.Conference
	Attendees  []Attendee
	// AttendeesTruncated is Google's attendeesOmitted.
	AttendeesTruncated bool
	Organizer          string
	OrganizerSelf      bool
	// Attachments are the files on the event. Shown, never written.
	Attachments []Attachment
	// StatusDetails are a status event's settings: out of office, focus
	// time or a working location. Nil on any other event, and on one
	// whose details Google did not send or this server cannot read.
	StatusDetails *StatusDetails
}

// StatusDetails are a status event's settings (§7.4), in the words
// create_event takes them in. A field that does not belong to the
// event's type is empty.
type StatusDetails struct {
	// AutoDecline is none, new or all: which invitations that overlap
	// the event Google declines.
	AutoDecline string
	// DeclineMessage goes to each organizer whose invitation is
	// declined. It is the account's own text: shown, never logged (§9).
	DeclineMessage string
	// ChatStatus is available or do_not_disturb, on focus time only.
	ChatStatus string
	// WorkingLocation is home, office or custom, and
	// WorkingLocationLabel names the office or the place.
	WorkingLocation      string
	WorkingLocationLabel string
}

// Words is a closed set of values with two spellings each: the one this
// server's inputs and results use, and Google's.
type Words [][2]string

// The three vocabularies of a status event.
var (
	AutoDeclineWords = Words{
		{"none", gcal.AutoDeclineNone}, {"new", gcal.AutoDeclineNew}, {"all", gcal.AutoDeclineAll},
	}
	ChatStatusWords      = Words{{"available", gcal.ChatAvailable}, {"do_not_disturb", gcal.ChatDoNotDisturb}}
	WorkingLocationWords = Words{
		{"home", gcal.WorkingHome}, {"office", gcal.WorkingOffice}, {"custom", gcal.WorkingCustom},
	}
)

// Wire is Google's spelling of a word, and whether the word is one.
func (w Words) Wire(word string) (string, bool) {
	for _, p := range w {
		if p[0] == word {
			return p[1], true
		}
	}
	return "", false
}

// Word is this server's spelling of Google's value. A value Google adds
// later comes back as Google spells it, so a result never hides it.
func (w Words) Word(wire string) string {
	for _, p := range w {
		if p[1] == wire {
			return p[0]
		}
	}
	return wire
}

// List is the words, for a refusal: "none, new or all". Every set has
// at least two.
func (w Words) List() string {
	words := make([]string, 0, len(w))
	for _, p := range w {
		words = append(words, p[0])
	}
	return strings.Join(words[:len(words)-1], ", ") + " or " + words[len(words)-1]
}

// statusDetailsOf reads a status event's details block. A block this
// server cannot read is left out rather than failing the read: the
// caller asked about the event, and the block is the least of it.
func statusDetailsOf(e gcal.Event) *StatusDetails {
	raw := e.StatusDetails()
	if len(raw) == 0 {
		return nil
	}
	var d StatusDetails
	switch e.EventType {
	case gcal.EventTypeOutOfOffice:
		var p gcal.EventOutOfOfficeProperties
		if json.Unmarshal(raw, &p) != nil {
			return nil
		}
		d.AutoDecline, d.DeclineMessage = AutoDeclineWords.Word(p.AutoDeclineMode), p.DeclineMessage
	case gcal.EventTypeFocusTime:
		var p gcal.EventFocusTimeProperties
		if json.Unmarshal(raw, &p) != nil {
			return nil
		}
		d.AutoDecline, d.DeclineMessage = AutoDeclineWords.Word(p.AutoDeclineMode), p.DeclineMessage
		d.ChatStatus = ChatStatusWords.Word(p.ChatStatus)
	case gcal.EventTypeWorkingLocation:
		var p gcal.EventWorkingLocationProperties
		if json.Unmarshal(raw, &p) != nil {
			return nil
		}
		// "Any details are specified in a sub-field of the specified
		// name", so a block without its type still says which it is.
		kind := p.Type
		switch {
		case kind != "":
		case p.OfficeLocation != nil:
			kind = gcal.WorkingOffice
		case p.CustomLocation != nil:
			kind = gcal.WorkingCustom
		case len(p.HomeOffice) > 0:
			kind = gcal.WorkingHome
		}
		d.WorkingLocation = WorkingLocationWords.Word(kind)
		switch {
		case kind == gcal.WorkingOffice && p.OfficeLocation != nil:
			d.WorkingLocationLabel = p.OfficeLocation.Label
		case kind == gcal.WorkingCustom && p.CustomLocation != nil:
			d.WorkingLocationLabel = p.CustomLocation.Label
		}
	}
	return &d
}

// Attachment is one file on an event. The file belongs to the Drive
// server, and FileID is what a caller hands it.
type Attachment struct {
	// Title is text somebody else wrote: content, shown and never logged
	// (§9).
	Title    string
	FileID   string
	URL      string
	MimeType string
}

// Reminders are when this account is reminded of an event. Google keeps
// them per person, so a guest has their own.
type Reminders struct {
	// Default says the calendar's own reminders apply, and Popup and
	// Email are then empty.
	Default bool
	// Popup and Email are minutes before the start, ascending. Both
	// empty, and not Default, means no reminders at all.
	Popup []int
	Email []int
}

// remindersFrom reads Google's reminder fields. A method Google no
// longer publishes, such as the retired sms, is left out.
func remindersFrom(useDefault bool, overrides []gcal.EventReminder) *Reminders {
	r := &Reminders{Default: useDefault}
	for _, o := range overrides {
		switch o.Method {
		case gcal.ReminderPopup:
			r.Popup = append(r.Popup, o.Minutes)
		case gcal.ReminderEmail:
			r.Email = append(r.Email, o.Minutes)
		}
	}
	sort.Ints(r.Popup)
	sort.Ints(r.Email)
	return r
}

// Private reports whether only the event's guests see its details.
func (e Event) Private() bool {
	return e.Visibility == gcal.VisibilityPrivate || e.Visibility == gcal.VisibilityConfidential
}

// IsSeries reports whether this is a recurring parent.
func (e Event) IsSeries() bool { return len(e.Recurrence) > 0 }

// IsInstance reports whether this is one occurrence of a series.
func (e Event) IsInstance() bool { return e.SeriesID != "" }

// IsRecurring reports whether a write here needs a scope (§4.2).
func (e Event) IsRecurring() bool { return e.IsSeries() || e.IsInstance() }

// Canceled reports whether the event is canceled (§2.13).
func (e Event) Canceled() bool { return e.Status == gcal.StatusCanceled }

// Moved reports whether this occurrence sits somewhere other than where
// the series put it.
//
// The rule lives here rather than in the renderer and the result
// builder, which each had their own copy: originalStartTime is the
// instance's scheduled start and the difference between it and the
// start is exactly an exception somebody made (§6.2).
func (e Event) Moved() bool {
	switch {
	case e.OriginalStart.AllDay && e.Start.AllDay:
		return !e.OriginalStart.Date.IsZero() && e.OriginalStart.Date != e.Start.Date
	case !e.OriginalStart.At.IsZero() && !e.Start.At.IsZero():
		return !e.OriginalStart.At.T.Equal(e.Start.At.T)
	default:
		return false
	}
}

// Guests are the attendees who are other people: not this account, and
// not a room.
//
// This is the one definition of "who a write can reach", which is what
// §4.3.2 hangs on — a write that reaches nobody does not have to ask
// about notification.
//
// account is the signed-in account's own address, and it is needed
// because Google's `self` flag is NOT sufficient. The live driver put
// the account on its own event, on a calendar the account owns, and the
// attendee came back without `self` — so the caller counted as their own
// guest and a write that reached nobody demanded a notification
// decision. The flag appears to be relative to the calendar in the
// request rather than to the authenticated user, and a secondary
// calendar is not the user; that mechanism is a reading of one live
// observation, so the fix does not depend on it being right.
//
// An empty account falls back to the flag alone, which is what a
// renderer has: it is describing an event, not deciding a refusal.
//
// A room is decided by its address (IsRoom), not by Google's `resource`
// flag: the flag holds whatever the write that added the attendee said,
// so a person added as a room would carry it and never be counted.
func (e Event) Guests(account string) []Attendee {
	out := make([]Attendee, 0, len(e.Attendees))
	for _, a := range e.Attendees {
		if a.Self || IsRoom(a.Email) {
			continue
		}
		if account != "" && strings.EqualFold(a.Email, account) {
			continue
		}
		out = append(out, a)
	}
	return out
}

// GuestCount is how many people would be reached. The count goes in a
// refusal; the addresses never do (§9).
func (e Event) GuestCount(account string) int { return len(e.Guests(account)) }

// roomDomain is where Google puts the address of every room and other
// resource it makes. Google publishes no shape for that address
// (`resourceEmail` is "generated"), so this is observed rather than
// documented (§18), and both ways it could be wrong are safe: a room it
// misses counts as a guest, as rooms did before; and only Google issues
// addresses under google.com, so no person is taken for a room.
const roomDomain = "resource.calendar.google.com"

// IsRoom reports whether an address is a room's or another resource's.
// It is the one rule for that, whichever list the address came in.
func IsRoom(address string) bool {
	i := strings.LastIndex(address, "@")
	return i >= 0 && strings.EqualFold(strings.TrimSpace(address[i+1:]), roomDomain)
}

// Attendee is one guest.
type Attendee struct {
	Email     string
	Name      string
	Response  string
	Optional  bool
	Resource  bool
	Self      bool
	Organizer bool
}

// FromEvent converts a wire event, reading times in zone.
func FromEvent(calendarID string, e gcal.Event, zone *when.Zone) (Event, error) {
	out := Event{
		ID: e.ID, CalendarID: calendarID, Title: e.Summary,
		Description: e.Description, Location: e.Location,
		Status: e.Status, Type: e.EventType, Link: e.HTMLLink, ETag: e.ETag,
		Recurrence: e.Recurrence, SeriesID: e.RecurringEventID,
		EndInvented:        e.EndTimeUnspecified,
		Transparent:        e.Transparency == gcal.TransparencyTransparent,
		Visibility:         e.Visibility,
		Conference:         gcal.ReadConference(e.ConferenceData),
		AttendeesTruncated: e.AttendeesOmitted,
		// Google's published defaults: false, true and true.
		GuestsCanModify:         e.GuestsCanModify,
		GuestsCanInviteOthers:   e.GuestsCanInviteOthers == nil || *e.GuestsCanInviteOthers,
		GuestsCanSeeOtherGuests: e.GuestsCanSeeOtherGuests == nil || *e.GuestsCanSeeOtherGuests,
		StatusDetails:           statusDetailsOf(e),
	}
	if e.Reminders != nil {
		out.Reminders = remindersFrom(e.Reminders.UseDefault, e.Reminders.Overrides)
	}
	var err error
	if out.Start, err = ParseWhen(e.Start, zone); err != nil {
		return Event{}, err
	}
	if out.End, err = ParseWhen(e.End, zone); err != nil {
		return Event{}, err
	}
	if out.OriginalStart, err = ParseWhen(e.OriginalStartTime, zone); err != nil {
		return Event{}, err
	}
	if e.Organizer != nil {
		out.Organizer = e.Organizer.Email
		out.OrganizerSelf = e.Organizer.Self
	}
	for _, a := range e.Attendees {
		out.Attendees = append(out.Attendees, Attendee{
			Email: a.Email, Name: a.DisplayName, Response: a.ResponseStatus,
			Optional: a.Optional, Resource: a.Resource, Self: a.Self, Organizer: a.Organizer,
		})
	}
	for _, a := range e.Attachments {
		out.Attachments = append(out.Attachments, Attachment{
			Title: a.Title, FileID: a.FileID, URL: a.FileURL, MimeType: a.MimeType,
		})
	}
	return out, nil
}

// Sharing is one ACL rule as this server presents it: who can see a
// calendar, and what they can see (§7.6).
//
// One type, so that get_calendar's exposure block, list_sharing and the
// before/after on a share are the same lines computed once. Two
// renderings of "who can see this calendar" is how one result comes to
// contradict another.
type Sharing struct {
	// RuleID is the rule's own address, which unshare_calendar deletes
	// by. It is Google's, never built here.
	RuleID string
	// ScopeType is user, group, domain or default.
	ScopeType string
	// Value is the address or domain; empty for the public scope.
	Value string
	Role  string
	ETag  string
}

// FromACL converts one wire rule.
func FromACL(r gcal.AclRule) Sharing {
	return Sharing{
		RuleID: r.ID, ScopeType: r.Scope.Type, Value: r.Scope.Value,
		Role: r.Role, ETag: r.ETag,
	}
}

// Public reports whether this rule exposes the calendar to anybody at
// all.
func (s Sharing) Public() bool { return s.ScopeType == gcal.ScopeTypeDefault }

// Who names the audience in the words a result uses.
func (s Sharing) Who() string {
	if s.Public() {
		return "ANYONE, signed in or not"
	}
	if s.ScopeType == gcal.ScopeTypeDomain {
		return "everybody in " + s.Value
	}
	return s.Value
}

// RoleMeans explains what this rule actually grants.
func (s Sharing) RoleMeans() string { return gcal.RoleMeans(s.Role) }

// Scope is the wire shape of this rule's audience.
func (s Sharing) Scope() gcal.AclScope {
	return gcal.AclScope{Type: s.ScopeType, Value: s.Value}
}

// PublicRule returns the rule that exposes a calendar to anybody at all,
// if it has one.
//
// Here rather than in the renderer: "is this calendar public" is a
// question about the calendar, and three callers ask it — one of them to
// decide whether to warn, which is policy rather than presentation.
func PublicRule(rules []Sharing) (Sharing, bool) {
	for _, r := range rules {
		if r.Public() {
			return r, true
		}
	}
	return Sharing{}, false
}

// Busy is one interval somebody is not free.
type Busy struct {
	Start when.Zoned
	End   when.Zoned
}

// Availability is one calendar's answer to a free/busy query.
//
// Unknown is the field that matters: §4.6 refuses to fold a calendar
// that could not be read into "free", because a model that cannot tell
// those apart will book over somebody.
type Availability struct {
	CalendarID string
	Busy       []Busy
	Unknown    bool
	Reason     string
	// Group is set for a group's address. Members is how many
	// calendars Google expanded it to, which is zero when it could not.
	Group   bool
	Members int
}

// Merge returns the busy intervals of every calendar as one ordered,
// non-overlapping set.
//
// Two people busy at the same time is one busy block, not two, and
// subtracting overlapping intervals one at a time from a window is how a
// gap gets counted twice. Merging first makes the arithmetic below
// trivial and is the only place the overlap rule lives.
func Merge(busy []Busy) []Busy {
	if len(busy) == 0 {
		return nil
	}
	ordered := make([]Busy, len(busy))
	copy(ordered, busy)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Start.T.Before(ordered[j].Start.T) })

	out := []Busy{ordered[0]}
	for _, b := range ordered[1:] {
		last := &out[len(out)-1]
		// Touching counts as overlapping: an event ending at 10:00 and
		// one starting at 10:00 leave no gap between them, and reporting
		// a zero-length gap there is noise a caller has to filter.
		if !b.Start.T.After(last.End.T) {
			if b.End.T.After(last.End.T) {
				last.End = b.End
			}
			continue
		}
		out = append(out, b)
	}
	return out
}

// FreeGaps is the window with the busy intervals taken out of it.
//
// It is here rather than left to the caller because the question behind
// "is this person free" is almost always "when can we meet", and a model
// doing interval arithmetic over a list of busy blocks is how a meeting
// gets booked at 02:00 (§7.3).
//
// hours is the caller's working-hours mask, empty when they asked for
// none: the API has no working-hours field, so this is the server's
// (§17.2). min drops gaps shorter than a meeting worth having; a zero
// min keeps them all.
//
// The order of the three steps is the whole reason they are one
// function. Cut the busy time out, then apply the mask, then drop what
// is too short: a 20-minute sliver left at the edge of the working day
// is exactly what min_minutes exists to remove, and filtering before
// the mask would report it.
func FreeGaps(w when.Window, busy []Busy, min time.Duration, hours when.Hours) []when.Window {
	var out []when.Window
	cursor := w.Start
	add := func(from, to when.Zoned) {
		if !to.T.After(from.T) {
			return
		}
		out = append(out, when.Window{Start: from, End: to, Loc: w.Loc})
	}

	for _, b := range Merge(busy) {
		if !b.End.T.After(w.Start.T) || !b.Start.T.Before(w.End.T) {
			continue // entirely outside the window
		}
		if b.Start.T.After(cursor.T) {
			add(cursor, b.Start.In(w.Loc))
		}
		if b.End.T.After(cursor.T) {
			cursor = b.End.In(w.Loc)
		}
	}
	add(cursor, w.End)

	if hours.Set() {
		out = when.Intersect(out, hours.Windows(w))
	}
	if min <= 0 {
		return out
	}
	kept := out[:0]
	for _, g := range out {
		if g.Duration() >= min {
			kept = append(kept, g)
		}
	}
	return kept
}
