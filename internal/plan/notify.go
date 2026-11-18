package plan

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v5/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v5/internal/model"
)

// Notify is the caller's notification choice. It has no default, in
// either direction.
//
// Google says `none` "can have significant adverse effects, including
// events not syncing to external calendars or events being lost
// altogether for some users", so the safe-looking default is the one
// documented to lose data. Google's own defaults are opposite: an event
// write emails nobody unless sendUpdates says so, and an ACL write's
// sendNotifications defaults to true. Each method states only its own,
// so "whatever Google does" gives one decision opposite outcomes in two
// tools. Checked by the live step "notify required with guests".
// https://developers.google.com/workspace/calendar/api/v3/reference/events/insert
type Notify string

// The three choices.
const (
	// NotifyNone asks Google to send nothing. It is never reported as
	// silence: every event write's reference says "some emails might
	// still be sent even if you set the value to false", though none has
	// been seen to. Checked by the live step "spike A: notification
	// truth".
	// https://developers.google.com/workspace/calendar/api/v3/reference/events/insert
	NotifyNone Notify = "none"
	// NotifyExternalOnly reaches the guests outside the organizer's own
	// Workspace domain, Google Calendar users included. Google's reference
	// says "non-Google Calendar guests only", which is wrong: of two guests
	// on Google Calendar, the one outside the domain is mailed and the one
	// inside is not. Checked by the live step "spike A: notification truth".
	// https://developers.google.com/workspace/calendar/api/v3/reference/events/insert
	NotifyExternalOnly Notify = "external_only"
	// NotifyAll reaches every guest.
	NotifyAll Notify = "all"
)

// Notifies is the vocabulary, in the order a refusal lists them.
var Notifies = []Notify{NotifyNone, NotifyExternalOnly, NotifyAll}

// SendUpdates is the wire spelling Google wants.
func (n Notify) SendUpdates() string {
	switch n {
	case NotifyAll:
		return gcal.SendUpdatesAll
	case NotifyExternalOnly:
		return gcal.SendUpdatesExternalOnly
	default:
		return gcal.SendUpdatesNone
	}
}

// Means says what this choice does, in the words a refusal uses.
func (n Notify) Means() string {
	switch n {
	case NotifyNone:
		return "asks Google to email nobody — which is not a promise of silence, and is refused when a guest is outside your organization"
	case NotifyExternalOnly:
		return "emails only the guests outside your own organization"
	case NotifyAll:
		return "emails every guest"
	default:
		return ""
	}
}

// NotifyChoices is the three choices and what each does, for a refusal.
func NotifyChoices() string {
	var b strings.Builder
	for _, n := range Notifies {
		fmt.Fprintf(&b, "\n  %s — %s", n, n.Means())
	}
	return b.String()
}

// Reach is how far a write can carry: how many people, and how many of
// them are outside the organizer's own domain.
//
// Counts, never addresses. A refusal says "this event has 4 guests", and
// the four addresses stay out of the message, the log and the transcript.
type Reach struct {
	// Guests is people other than the caller. A room is not a person.
	Guests int
	// External is how many of those are outside the organizer's domain,
	// which is the axis `external_only` actually splits on.
	External int
	// domain is the organizer's, kept for the split and never printed.
	domain string
}

// Any reports whether this write can reach a person at all, which is
// what makes notify required.
func (r Reach) Any() bool { return r.Guests > 0 }

// ReachOfEvent counts an event's guests against the organizer's domain,
// with the guests the write adds: they are invited by it, so a write
// that adds an outside guest to an event with none reaches somebody.
// Checked by the live step "notify required when an update adds a
// guest".
//
// Who counts as a guest is model.Event.Guests' to say, not this
// package's: it is the rule a notify refusal is decided on, and a second
// copy here would let one result quote two different counts.
//
// account is the signed-in account's own address, which is never a
// guest. Google's `self` flag alone does not establish that (see
// model.Event.Guests).
func ReachOfEvent(organizer, account string, e model.Event, adding ...string) Reach {
	guests := e.Guests(account)
	r := Reach{domain: domainOf(organizer), Guests: len(guests)}
	seen := map[string]bool{strings.ToLower(account): true, strings.ToLower(organizer): true}
	for _, a := range guests {
		seen[strings.ToLower(a.Email)] = true
		if r.isExternal(a.Email) {
			r.External++
		}
	}
	for _, a := range adding {
		a = strings.TrimSpace(a)
		if a == "" || seen[strings.ToLower(a)] || model.IsRoom(a) {
			continue
		}
		seen[strings.ToLower(a)] = true
		r.Guests++
		if r.isExternal(a) {
			r.External++
		}
	}
	return r
}

// ReachOfAddresses counts a guest list the caller supplied, which is
// what create_event has before the event exists.
//
// The organizer's own address does not count: inviting yourself is not
// reaching somebody, and a notify requirement over it would be friction
// with no safety in it. Nor does a room.
func ReachOfAddresses(organizer string, addresses []string) Reach {
	r := Reach{domain: domainOf(organizer)}
	for _, a := range addresses {
		a = strings.TrimSpace(a)
		if a == "" || strings.EqualFold(a, organizer) || model.IsRoom(a) {
			continue
		}
		r.Guests++
		if r.isExternal(a) {
			r.External++
		}
	}
	return r
}

// isExternal reports whether an address sits outside the organizer's
// domain.
//
// An unknown organizer domain makes every guest external, and that is
// deliberate: the consequence of guessing wrong is the refusal of `none`
// not firing for somebody who cannot discover the event by any other means.
// Failing toward the refusal costs a caller one explicit choice.
func (r Reach) isExternal(email string) bool {
	d := domainOf(email)
	return d == "" || r.domain == "" || !strings.EqualFold(d, r.domain)
}

func domainOf(address string) string {
	i := strings.LastIndex(address, "@")
	if i < 0 {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(address[i+1:]))
}

// Decision is what the server will ask Google for.
type Decision struct {
	Notify Notify
	// Asked is false when the write reaches nobody and the caller named
	// no choice: sendUpdates is then left out of the request entirely,
	// because there is no decision to make.
	Asked bool
	Reach Reach
}

// Notification decides what one write asks Google to send.
//
// Three rules, in this order:
//
//  1. a write that reaches nobody does not ask (D-22);
//  2. a write that reaches somebody and names no choice is refused, with
//     the count and the three options (D-21);
//  3. `none` with a guest outside the organizer's domain is refused
//     rather than warned about (D-24). That guest may have no Google
//     Calendar, and then mail is the only channel there is: an address
//     outside Google invited with `none` receives nothing and has no
//     calendar for the event to land in. Google's reference warns that
//     `none` can lose an event and does not say how. Checked by the live
//     step "spike B: none on insert", and through the tool by the live
//     step "none refused outside domain".
//     https://developers.google.com/workspace/calendar/api/v3/reference/events/insert
func Notification(v string, r Reach) (Decision, error) {
	choice, err := ParseNotify(v)
	switch {
	case err != nil && v != "":
		return Decision{}, err
	case v == "" && !r.Any():
		return Decision{Asked: false, Reach: r}, nil
	case v == "":
		return Decision{}, fmt.Errorf("%w: this write reaches %d %s; pass notify to say whether they are emailed."+
			" There is no default, in either direction, and the reason is in the choices:%s",
			ErrInvalid, r.Guests, People(r.Guests), NotifyChoices())
	}
	if choice == NotifyNone && r.External > 0 {
		return Decision{}, fmt.Errorf(
			"%w: %d of the %d guests %s outside your organization, and notify:none is refused there rather than"+
				" warned about. Such a guest may have no Google Calendar for the event to appear in, so email is"+
				" the only way they can learn of it — the event would exist with them attached and they could not"+
				" find it. Pass notify:external_only to reach exactly them, or notify:all",
			ErrBlocked, r.External, r.Guests, isAre(r.External))
	}
	return Decision{Notify: choice, Asked: true, Reach: r}, nil
}

// PersonalNotification is the decision for a write that changes only
// what Google keeps per person, which is an event's reminders. It reaches
// nobody whatever the guest list, so sendUpdates is left out of the
// request, as for any write that reaches nobody. A notify the
// caller passed is still read, so a typo is refused rather than ignored.
func PersonalNotification(v string) (Decision, error) {
	if strings.TrimSpace(v) != "" {
		if _, err := ParseNotify(v); err != nil {
			return Decision{}, err
		}
	}
	return Decision{}, nil
}

// ParseNotify reads a caller's choice.
//
// Google's own spelling, `externalOnly`, is accepted alongside this
// server's `external_only`: a model that has read the API reference will
// type the one it saw there, and refusing it teaches nothing.
func ParseNotify(v string) (Notify, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "none":
		return NotifyNone, nil
	case "external_only", "externalonly":
		return NotifyExternalOnly, nil
	case "all":
		return NotifyAll, nil
	case "":
		return "", fmt.Errorf("%w: notify is required on a write that can reach another person. Pass:%s",
			ErrInvalid, NotifyChoices())
	default:
		return "", fmt.Errorf("%w: %q is not a notify choice. Pass:%s", ErrInvalid, v, NotifyChoices())
	}
}

// SendUpdatesFor is the wire value this decision sends, or empty when
// there was no decision to make.
func (d Decision) SendUpdatesFor() string {
	if !d.Asked {
		return ""
	}
	return d.Notify.SendUpdates()
}

// Report is what a result says about notification.
//
// It says what the server ASKED FOR, never what arrived (D-23). `none` is
// not silence, because Google says some mail may still be sent. `all` is
// not delivery: Google sends to every guest, and the receiving provider
// may still not show it, which nothing in the API reports. Checked by the
// live step "spike A: notification truth".
// https://developers.google.com/workspace/calendar/api/v3/reference/events/insert
func (d Decision) Report() string {
	// On the REACH, not on whether a choice was passed. A caller who
	// passes notify:none on an event with no guests got "Asked Google to
	// notify nobody, of 0 guests … this is not a promise of silence",
	// which reads as a warning about nothing and invites a reader to
	// wonder who the zero guests are.
	if d.Reach.Guests == 0 {
		return "Nobody to notify: this write reaches no guests, so no notification was requested."
	}
	switch d.Notify {
	case NotifyNone:
		return fmt.Sprintf("Asked Google to notify nobody, of %d %s. Google says some mail may still be sent,"+
			" so this is not a promise of silence.", d.Reach.Guests, People(d.Reach.Guests))
	case NotifyExternalOnly:
		return fmt.Sprintf("Asked Google to notify the %d of %d %s outside your organization."+
			" That is what was asked for, not what arrived: the API reports nothing about delivery.",
			d.Reach.External, d.Reach.Guests, People(d.Reach.Guests))
	default:
		return fmt.Sprintf("Asked Google to notify all %d %s, %d of them outside your organization."+
			" That is what was asked for, not what arrived: the API reports nothing about delivery.",
			d.Reach.Guests, People(d.Reach.Guests), d.Reach.External)
	}
}

// People is "guest" or "guests". Exported because the service's own
// notification sentences need the same word and had a second copy of it.
func People(n int) string {
	if n == 1 {
		return "guest"
	}
	return "guests"
}

// isAre is "is" or "are".
func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

// AttendeeLimit is where Google stops propagating each guest's response
// status on an event. It is not the hard limit on guests, which Google
// does not publish.
const AttendeeLimit = 200

// CrowdWarning is what a result says about an event with more attendees
// than Google will track individually, and "" for an ordinary one.
//
// The count is ATTENDEE ROWS, because the threshold is Google's and it
// is on Google's own field: the organizer's row and the rooms are on
// that list. Counting "guests" — the narrower set a write can reach —
// would leave an event Google had already stopped tracking unqualified,
// and would disagree with the number the same result prints.
//
// It reports the COUNT and never the addresses. The sentence is
// about what the server can no longer tell the caller, which is the
// honest half: above this many the RSVPs in a read are not the RSVPs on
// the event, and a caller counting acceptances would be wrong without
// anything looking wrong.
func CrowdWarning(attendees int) string {
	if attendees <= AttendeeLimit {
		return ""
	}
	return fmt.Sprintf("This event has %d attendees, above Google's limit of %d for tracking replies. "+
		"Above that, individual responses are not propagated, so the RSVPs any read reports here are "+
		"incomplete — do not count acceptances off them. Google does not publish where it stops "+
		"accepting guests altogether, so this server cannot tell you how much further there is to go.",
		attendees, AttendeeLimit)
}
