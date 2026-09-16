// Package model is the server's view of a calendar and an event: the
// wire types of internal/gcal turned into values that carry resolved
// time (internal/when) and nothing ambiguous.
//
// The conversion happens here and nowhere else, so there is one place
// where a gcal.EventDateTime becomes either a Date or a Zoned, and one
// place to look when a time is wrong.
package model

import (
	"sort"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/when"
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
	ETag        string
}

// FromCalendarList converts one subscription entry.
func FromCalendarList(e gcal.CalendarListEntry) Calendar {
	c := Calendar{
		ID: e.ID, Title: e.Summary, TimeZone: e.TimeZone, Role: e.AccessRole,
		Primary: e.Primary, Selected: e.Selected, Hidden: e.Hidden,
		ColorID: e.ColorID, Description: e.Description, ETag: e.ETag,
	}
	// A rename is this user's alone: the same calendar has a different
	// name for a colleague, so both are carried and the renderer says so.
	if e.SummaryOverride != "" {
		c.Title = e.SummaryOverride
		c.Original = e.Summary
	}
	return c
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
	Attendees   []Attendee
	// AttendeesTruncated is Google's attendeesOmitted.
	AttendeesTruncated bool
	Organizer          string
	OrganizerSelf      bool
}

// IsSeries reports whether this is a recurring parent.
func (e Event) IsSeries() bool { return len(e.Recurrence) > 0 }

// IsInstance reports whether this is one occurrence of a series.
func (e Event) IsInstance() bool { return e.SeriesID != "" }

// IsRecurring reports whether a write here needs a scope (§4.2).
func (e Event) IsRecurring() bool { return e.IsSeries() || e.IsInstance() }

// Cancelled reports whether the event is cancelled (§2.13).
func (e Event) Cancelled() bool { return e.Status == gcal.StatusCancelled }

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
func (e Event) Guests(account string) []Attendee {
	out := make([]Attendee, 0, len(e.Attendees))
	for _, a := range e.Attendees {
		if a.Self || a.Resource {
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
		AttendeesTruncated: e.AttendeesOmitted,
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
	return out, nil
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
// min drops gaps shorter than a meeting worth having; a zero min keeps
// them all. What this does NOT do is decide what counts as working
// hours: the API has no such field, 09:00 is not 09:00 everywhere, and
// §17.2 leaves that choice to the caller, who can narrow the window.
func FreeGaps(w when.Window, busy []Busy, min time.Duration) []when.Window {
	var out []when.Window
	cursor := w.Start
	add := func(from, to when.Zoned) {
		if !to.T.After(from.T) {
			return
		}
		if to.T.Sub(from.T) < min {
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
	return out
}
