// Package plan turns a requested change into the API call that makes it,
// and refuses the calls that must not be made.
//
// It has no network, no clock and no client: a plan is a value, so every
// guard §4.2, §4.3 and §4.4 require is testable on its own rather than
// through a fake calendar. The service calls these, spends the requests
// and reports the result; what belongs here is the decision.
//
// Its refusals are sentinels — ErrInvalid, ErrBlocked, ErrUnsupported —
// which the service turns into the matching error class. The same shape
// as internal/recur, and for the same reason: a guard package that
// imports the REST client to name a class has the layering backwards.
package plan

import (
	"errors"
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
	"github.com/mmedum/google-calendar-mcp/internal/recur"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// The three refusals a plan can make, mapped to error classes by the
// service.
var (
	// ErrInvalid: the request is malformed or under-specified.
	ErrInvalid = errors.New("plan: invalid")
	// ErrBlocked: a guard refused what the API would have allowed.
	ErrBlocked = errors.New("plan: blocked")
	// ErrUnsupported: the API cannot do this (§2).
	ErrUnsupported = errors.New("plan: unsupported")
)

// Draft is a requested change, field by field.
//
// A nil pointer means the caller did not ask about that field, which is
// what makes this a patch rather than a replacement: §4.4 forbids a
// write that destroys what it cannot see, and Event's own `omitempty`
// strings cannot tell "leave it" from "clear it".
type Draft struct {
	Title       *string
	Description *string
	Location    *string

	// Start and End are the caller's own strings: a yyyy-mm-dd date for
	// an all-day event or an RFC3339 timestamp for a timed one. Empty
	// means unchanged. The two must agree — a date is not a time (§4.1).
	//
	// End on an all-day event is the LAST DAY, inclusive. Google's wire
	// end is exclusive, and the renderer already shows it inclusive, so
	// taking it inclusive here is what makes the value a caller reads
	// the value a caller writes.
	Start string
	End   string
	// Zone is the resolved zone and goes on every timed write, recurring
	// or not, so the same path is exercised every time (§4.1).
	Zone when.Zone

	// Recurrence replaces the RFC 5545 lines together. A non-nil pointer
	// to an empty slice ends the repetition.
	Recurrence *[]string

	// AddGuests and RemoveGuests are applied to the list that was read,
	// never to a list the caller supplied whole: an RSVP that arrived
	// between the read and the write would otherwise be overwritten
	// (§4.4).
	AddGuests    []string
	RemoveGuests []string

	// Transparent marks the event as not making the person busy.
	Transparent *bool

	// Conference asks Google to attach a Google Meet link. It is only
	// meaningful on an insert: Patch refuses it rather than dropping it
	// silently, because adding a conference to an event that exists is
	// a write this server does not make (§17.3).
	Conference bool
}

// Empty reports whether this draft asks for nothing.
func (d Draft) Empty() bool {
	return d.Title == nil && d.Description == nil && d.Location == nil &&
		d.Start == "" && d.End == "" && d.Recurrence == nil &&
		len(d.AddGuests) == 0 && len(d.RemoveGuests) == 0 && d.Transparent == nil
}

// Change is one field a write alters, in the exact values that went over
// the wire (§4.9). The readable before and after are rendered from the
// event itself; this is the machine half, so a caller can check what was
// sent rather than trust a sentence about it.
type Change struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// Patch builds the events.patch body for a draft against the event that
// was read, and lists what it changes.
//
// A field whose new value equals the old one is not sent and is not
// listed, so the change list describes the write rather than the
// request. Whether such a no-op would ALSO move the etag under everybody
// else holding one is unprobed for events: it was assumed here, and the
// live run refuted it for calendars (§18 row 58).
func Patch(before gcal.Event, d Draft) (gcal.EventPatch, []Change, error) {
	var p gcal.EventPatch
	var changes []Change

	if d.Conference {
		return gcal.EventPatch{}, nil, fmt.Errorf("%w: a Google Meet link can only be attached when the "+
			"event is created. This server does not add one to an event that already exists — create the "+
			"event with conference: true, or add the link in Google Calendar", ErrUnsupported)
	}

	set := func(field, from, to string, dst **string) {
		if from == to {
			return
		}
		v := to
		*dst = &v
		changes = append(changes, Change{Field: field, From: from, To: to})
	}
	if d.Title != nil {
		set("title", before.Summary, *d.Title, &p.Summary)
	}
	if d.Description != nil {
		set("description", before.Description, *d.Description, &p.Description)
	}
	if d.Location != nil {
		set("location", before.Location, *d.Location, &p.Location)
	}
	if d.Transparent != nil {
		to := gcal.TransparencyOpaque
		if *d.Transparent {
			to = gcal.TransparencyTransparent
		}
		from := before.Transparency
		if from == "" {
			from = gcal.TransparencyOpaque
		}
		set("free_not_busy", from, to, &p.Transparency)
	}

	start, end, err := d.times(&before)
	if err != nil {
		return gcal.EventPatch{}, nil, err
	}
	if start != nil && !sameMoment(before.Start, start) {
		p.Start = start
		changes = append(changes, Change{Field: "start", From: moment(before.Start), To: moment(start)})
	}
	if end != nil && !sameMoment(before.End, end) {
		p.End = end
		changes = append(changes, Change{Field: "end", From: moment(before.End), To: moment(end)})
	}

	if d.Recurrence != nil {
		lines, rerr := cleanRecurrence(*d.Recurrence)
		if rerr != nil {
			return gcal.EventPatch{}, nil, rerr
		}
		if strings.Join(lines, "\n") != strings.Join(before.Recurrence, "\n") {
			p.Recurrence = &lines
			changes = append(changes, Change{
				Field: "recurrence",
				From:  strings.Join(before.Recurrence, " "),
				To:    strings.Join(lines, " "),
			})
		}
	}

	if len(d.AddGuests) > 0 || len(d.RemoveGuests) > 0 {
		list, gc, gerr := guestList(before.Attendees, d.AddGuests, d.RemoveGuests, before.AttendeesOmitted)
		if gerr != nil {
			return gcal.EventPatch{}, nil, gerr
		}
		if gc != nil {
			p.Attendees = &list
			changes = append(changes, *gc)
		}
	}

	if len(changes) == 0 {
		return gcal.EventPatch{}, nil, fmt.Errorf(
			"%w: nothing to change — every field given already holds that value, so there is no write to "+
				"make and nothing a result could report having changed",
			ErrInvalid)
	}
	return p, changes, nil
}

// Insert builds the events.insert body for a new event.
//
// id is the client-generated one of §2.11: the server mints it so a
// retry after an answer nobody saw collides rather than double-books.
func Insert(id string, d Draft) (gcal.Event, error) {
	if err := gcal.ValidEventID(id); err != nil {
		return gcal.Event{}, fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	if d.Title == nil || strings.TrimSpace(*d.Title) == "" {
		return gcal.Event{}, fmt.Errorf("%w: title is required to create an event", ErrInvalid)
	}
	start, end, err := d.times(nil)
	if err != nil {
		return gcal.Event{}, err
	}
	e := gcal.Event{
		ID: id, Summary: *d.Title, Start: start, End: end,
		Status: gcal.StatusConfirmed, EventType: gcal.EventTypeDefault,
	}
	if d.Description != nil {
		e.Description = *d.Description
	}
	if d.Location != nil {
		e.Location = *d.Location
	}
	if d.Transparent != nil && *d.Transparent {
		e.Transparency = gcal.TransparencyTransparent
	}
	if d.Recurrence != nil {
		lines, rerr := cleanRecurrence(*d.Recurrence)
		if rerr != nil {
			return gcal.Event{}, rerr
		}
		e.Recurrence = lines
	}
	if d.Conference {
		// The request only asks: Google makes the conference
		// asynchronously, so the insert's answer usually says pending
		// and the caller is told to read the event for the link. The
		// request id is the event id, so a retry of an insert whose
		// answer was never seen (§2.11) cannot make a second
		// conference — a repeated request id is ignored.
		e.ConferenceData = gcal.NewConferenceRequest(id)
	}
	if len(d.RemoveGuests) > 0 {
		return gcal.Event{}, fmt.Errorf("%w: a new event has no guests to remove", ErrInvalid)
	}
	for _, g := range d.AddGuests {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if err := validAddress(g, "a guest"); err != nil {
			return gcal.Event{}, err
		}
		e.Attendees = append(e.Attendees, gcal.EventAttendee{Email: g})
	}
	return e, nil
}

// times turns the caller's start and end into the wire pair.
//
// before is nil on an insert, where both are required. On a patch,
// giving only one is allowed and keeps the other as it is — but the one
// given has to be the same KIND as the event it lands on, because an
// all-day event with a timed end is not a thing the API can hold.
func (d Draft) times(before *gcal.Event) (start, end *gcal.EventDateTime, err error) {
	hasStart, hasEnd := strings.TrimSpace(d.Start) != "", strings.TrimSpace(d.End) != ""
	if !hasStart && !hasEnd {
		if before == nil {
			return nil, nil, fmt.Errorf("%w: start and end are required to create an event", ErrInvalid)
		}
		return nil, nil, nil
	}
	if before == nil && (!hasStart || !hasEnd) {
		return nil, nil, fmt.Errorf("%w: a new event needs both start and end", ErrInvalid)
	}

	startAllDay, startErr := kindOf(d.Start)
	endAllDay, endErr := kindOf(d.End)
	switch {
	case hasStart && startErr != nil:
		return nil, nil, startErr
	case hasEnd && endErr != nil:
		return nil, nil, endErr
	case hasStart && hasEnd && startAllDay != endAllDay:
		return nil, nil, fmt.Errorf(
			"%w: start and end disagree about whether this is an all-day event. A date is not a time (§4.1): "+
				"pass two yyyy-mm-dd dates for an all-day event, or two RFC3339 timestamps for a timed one",
			ErrInvalid)
	}

	allDay := startAllDay
	if !hasStart {
		allDay = endAllDay
	}
	if before != nil {
		was := before.Start != nil && before.Start.IsAllDay()
		if hasStart != hasEnd && allDay != was {
			return nil, nil, fmt.Errorf(
				"%w: this event is %s and the %s given is %s. Pass both start and end to change which it is",
				ErrInvalid, allDayWord(was), sideGiven(hasStart), allDayWord(allDay))
		}
	}

	if allDay {
		var startD, endD when.Date
		if hasStart {
			dt, perr := when.ParseDate(d.Start)
			if perr != nil {
				return nil, nil, fmt.Errorf("%w: %s", ErrInvalid, perr.Error())
			}
			startD = dt
			start = &gcal.EventDateTime{Date: dt.String()}
		}
		if hasEnd {
			dt, perr := when.ParseDate(d.End)
			if perr != nil {
				return nil, nil, fmt.Errorf("%w: %s", ErrInvalid, perr.Error())
			}
			// The caller names the last day; Google wants the day after.
			endD = dt.AddDays(1)
			end = &gcal.EventDateTime{Date: endD.String()}
		}
		// The side that is NOT changing still has to be crossed. A patch
		// moving only the end was checked against nothing, so an end
		// before the start that stays was accepted, sent, and rendered
		// as an event ending before it began.
		if !hasStart && before != nil && before.Start != nil && before.Start.Date != "" {
			startD, _ = when.ParseDate(before.Start.Date)
		}
		if !hasEnd && before != nil && before.End != nil && before.End.Date != "" {
			endD, _ = when.ParseDate(before.End.Date)
		}
		if !startD.IsZero() && !endD.IsZero() && !endD.After(startD) {
			return nil, nil, fmt.Errorf("%w: the last day %s is not on or after the first day %s",
				ErrInvalid, endD.AddDays(-1), startD)
		}
		return start, end, nil
	}

	if d.Zone.Loc == nil {
		return nil, nil, fmt.Errorf("%w: a timed write needs a resolved time zone, and this call has none. "+
			"Pass time_zone, or use a calendar that has one", ErrInvalid)
	}
	var startZ, endZ when.Zoned
	if hasStart {
		z, perr := when.ParseZoned(d.Start, d.Zone.Loc)
		if perr != nil {
			return nil, nil, fmt.Errorf("%w: %s", ErrInvalid, perr.Error())
		}
		startZ = z
		start = &gcal.EventDateTime{DateTime: z.String(), TimeZone: d.Zone.Name()}
	}
	if hasEnd {
		z, perr := when.ParseZoned(d.End, d.Zone.Loc)
		if perr != nil {
			return nil, nil, fmt.Errorf("%w: %s", ErrInvalid, perr.Error())
		}
		endZ = z
		end = &gcal.EventDateTime{DateTime: z.String(), TimeZone: d.Zone.Name()}
	}
	if !hasStart && before != nil && before.Start != nil && before.Start.DateTime != "" {
		startZ, _ = when.ParseZoned(before.Start.DateTime, d.Zone.Loc)
	}
	if !hasEnd && before != nil && before.End != nil && before.End.DateTime != "" {
		endZ, _ = when.ParseZoned(before.End.DateTime, d.Zone.Loc)
	}
	// Compared as INSTANTS, never as the strings that carry them.
	//
	// An RFC3339 string sorts by its text, and either side of a
	// daylight-saving fold two strings carry different offsets — so
	// "02:45+02:00" to "02:30+01:00" is 45 real minutes forward and
	// sorts backwards, while the genuinely backwards pair sorts forwards.
	// The string comparison refused the valid event and accepted the
	// impossible one, which is §4.1's whole argument for carrying a zone
	// rather than an offset, failing inside the guard meant to enforce it.
	if !startZ.IsZero() && !endZ.IsZero() && !endZ.After(startZ) {
		return nil, nil, fmt.Errorf("%w: the end %s is not after the start %s",
			ErrInvalid, endZ, startZ)
	}
	return start, end, nil
}

// kindOf reports whether a caller's time string is a date.
func kindOf(v string) (allDay bool, err error) {
	if _, derr := when.ParseDate(v); derr == nil {
		return true, nil
	}
	if _, zerr := when.ParseZoned(v, nil); zerr == nil {
		return false, nil
	}
	return false, fmt.Errorf("%w: %q is neither a yyyy-mm-dd date nor an RFC3339 timestamp", ErrInvalid, v)
}

func allDayWord(allDay bool) string {
	if allDay {
		return "all-day"
	}
	return "timed"
}

func sideGiven(hasStart bool) string {
	if hasStart {
		return "start"
	}
	return "end"
}

// moment renders one end of an event for a change line.
func moment(e *gcal.EventDateTime) string {
	switch {
	case e == nil:
		return ""
	case e.Date != "":
		return e.Date
	default:
		return e.DateTime
	}
}

func sameMoment(a, b *gcal.EventDateTime) bool {
	return moment(a) == moment(b)
}

// cleanRecurrence validates the caller's RFC 5545 lines and returns them
// unchanged.
//
// Unchanged is the point (§6.4): the parse is there to refuse a rule the
// server cannot expand and to explain it back, not to rebuild it. A
// round trip through a struct is where a BYDAY goes missing.
func cleanRecurrence(lines []string) ([]string, error) {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		out = append(out, l)
	}
	if len(out) == 0 {
		// A non-nil, empty list is how a series stops repeating.
		return []string{}, nil
	}
	if _, err := recur.Parse(out); err != nil {
		return nil, fromRecur(err)
	}
	return out, nil
}

// guestList applies the additions and removals to the list that was
// read (§4.4).
//
// The attendees come from the raw event rather than from model.Attendee
// deliberately: the model drops a guest's comment, their extra-guest
// count and their id, and rebuilding the array from it would erase all
// three on every guest on the event, on any write that touched the list
// at all. Read-modify-write means the values that were read.
func guestList(before []gcal.EventAttendee, add, remove []string, truncated bool) ([]gcal.EventAttendee, *Change, error) {
	if truncated {
		return nil, nil, fmt.Errorf(
			"%w: Google truncated this event's guest list, so the server cannot change it without dropping the "+
				"guests it was not shown. Open the event in Calendar to change the guests", ErrBlocked)
	}
	out := make([]gcal.EventAttendee, 0, len(before)+len(add))
	drop := map[string]bool{}
	for _, r := range remove {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if err := validAddress(r, "a guest"); err != nil {
			return nil, nil, err
		}
		drop[strings.ToLower(r)] = true
	}
	have := map[string]bool{}
	removed := 0
	for _, a := range before {
		if drop[strings.ToLower(a.Email)] {
			removed++
			continue
		}
		have[strings.ToLower(a.Email)] = true
		out = append(out, a)
	}
	added := 0
	for _, g := range add {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		if err := validAddress(g, "a guest"); err != nil {
			return nil, nil, err
		}
		if have[strings.ToLower(g)] {
			continue
		}
		have[strings.ToLower(g)] = true
		out = append(out, gcal.EventAttendee{Email: g})
		added++
	}
	if added == 0 && removed == 0 {
		return nil, nil, nil
	}
	// Counts, never addresses (§9).
	return out, &Change{
		Field: "guests",
		From:  fmt.Sprintf("%d", len(before)),
		To:    fmt.Sprintf("%d (%d added, %d removed)", len(out), added, removed),
	}, nil
}

// validAddress refuses something that is not an address before it
// becomes a guest nobody can reach — or a sharing rule naming nobody.
//
// what names what the address would have been, because the same check
// serves two tools and "so it cannot be a guest" is a puzzling thing to
// read from share_calendar, which has none.
func validAddress(v, what string) error {
	at := strings.LastIndex(v, "@")
	if at <= 0 || at == len(v)-1 || strings.ContainsAny(v, " \t\r\n") || !strings.Contains(v[at:], ".") {
		return fmt.Errorf("%w: %q is not an email address, so it cannot be %s", ErrInvalid, v, what)
	}
	return nil
}

// ------------------------------------------------------------- the scope

// Scope applies §4.2 to one write.
//
// reach is called ONLY on the refusal path. Counting a series' occurrences
// means reading its parent, and a guard that spends a request on every
// successful write to improve a message nobody sees is §4.7 failing where
// the result cannot show it.
//
// An event that does not repeat needs no scope and is not refused for
// passing one: the caller believed it repeated, and the result says it
// does not, which is the useful answer rather than a refusal.
func Scope(v string, e model.Event, reach func() string) (recur.Scope, error) {
	// Parsed BEFORE the event is consulted, so a typo is a typo whatever
	// it was aimed at. The old order returned early on a non-repeating
	// event and never looked at the word, so `scope: "sereis"` was
	// accepted in silence and the caller was told nothing.
	if v = strings.TrimSpace(v); v != "" {
		s, err := recur.ParseScope(v)
		if err != nil {
			return "", fromRecur(err)
		}
		if !e.IsRecurring() {
			return "", nil
		}
		return s, nil
	}
	if !e.IsRecurring() {
		return "", nil
	}
	{
		note := ""
		if reach != nil {
			if r := reach(); r != "" {
				note = "\n  (" + r + ")"
			}
		}
		return "", fmt.Errorf("%w: this event repeats, so a write has to say which occurrences it means. "+
			"There is no default: the same words mean three different operations here. Pass scope:%s%s",
			ErrInvalid, recur.ChoiceList(), note)
	}
}

// fromRecur re-raises an internal/recur failure as this package's own.
//
// One place, because the prefixes stack otherwise: wrapping a recur
// error in ErrInvalid and letting the service strip only its own prefix
// left the caller reading "plan: invalid: recur: invalid: …" in a
// message that already says [invalid]. Two of the call sites stripped it
// by hand with a literal and two did not, so the same class of mistake
// read two different ways ten lines apart.
func fromRecur(err error) error {
	return fmt.Errorf("%w: %s", ErrInvalid,
		strings.TrimPrefix(err.Error(), recur.ErrInvalid.Error()+": "))
}

// NoSplit refuses `this_and_following` where this server cannot build it.
//
// The refusal is about this server's construction, not a guess about
// Google's: §2.8 says there is NO server-side "this and following", so
// the only way to have one is to truncate the original series and insert
// a new one from the target. That takes the right to rewrite the series.
// An attendee RSVPing has no such right, and a move between calendars
// has nothing to split — so for those two the operation does not exist,
// and saying "unsupported" is more honest than half-doing it.
func NoSplit(s recur.Scope, what string) error {
	if s != recur.ScopeThisAndFollowing {
		return nil
	}
	return fmt.Errorf("%w: %s cannot be done for \"this and following\". Google has no such operation — "+
		"the server builds it by ending the original series and starting a new one (§2.8), which rewrites "+
		"the series itself. Use scope:instance for one occurrence, or scope:series for all of them",
		ErrUnsupported, what)
}

// Aim is which event a scoped write actually touches.
type Aim int

// The two aims.
const (
	// AimHere writes the event that was addressed.
	AimHere Aim = iota
	// AimSeries writes the parent, which the caller reached through one
	// of its occurrences.
	AimSeries
)

// Target is §4.2's second half: having chosen a scope, WHICH event does
// the write land on?
//
// Choosing the scope and applying it are two decisions, and only the
// first had an owner. The second was written out at each call site, and
// two of the four were already wrong: move_event had no version of it at
// all, so `scope:series` on an occurrence id moved one occurrence while
// the result printed `scope: series`, and `scope:instance` on a series
// id moved the whole series. respond_to_event was missing the refusal
// and would have patched a whole series' attendee array — the exact
// thing its own description says it exists to prevent.
//
// this_and_following is not decided here. It is not a target but a
// construction (§2.8), so a caller either builds it or refuses it with
// NoSplit before asking.
func Target(s recur.Scope, e model.Event, eventID string) (Aim, error) {
	switch s {
	case recur.ScopeSeries:
		if e.IsInstance() {
			return AimSeries, nil
		}
		// Already the parent, or not a series at all.
		return AimHere, nil
	case recur.ScopeInstance:
		if e.IsSeries() {
			return AimHere, fmt.Errorf(
				"%w: %s is the series itself, so scope:instance does not say which occurrence to write. "+
					"Pass original_start alongside it — list_instances reports one per occurrence — or "+
					"pass the occurrence's own id. To reach every occurrence, pass scope:series",
				ErrInvalid, eventID)
		}
		return AimHere, nil
	default:
		return AimHere, nil
	}
}
