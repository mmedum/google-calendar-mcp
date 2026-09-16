package plan

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/internal/model"
)

// Notify is the caller's notification choice (§4.3).
//
// There is no default, in either direction. Google's own documentation
// says `none` "can have significant adverse effects, including events
// not syncing to external calendars or events being lost altogether for
// some users", so the safe-looking default is the one documented to lose
// data; and Google's own defaults are opposite between events and ACL
// rules (§2.5), so "whatever Google does" means the same decision has
// opposite outcomes in two tools for a reason nobody can see from the
// call. The server asks.
type Notify string

// The three choices.
const (
	// NotifyNone asks Google to send nothing. It is never reported as
	// silence: §2.6 says some mail may go out anyway.
	NotifyNone Notify = "none"
	// NotifyExternalOnly reaches the guests outside the organiser's own
	// Workspace domain. Google documents this as "non-Google Calendar
	// guests only" and is wrong about its own parameter: spike A gave it
	// one guest inside the domain and one outside, both on Google
	// Calendar, and it mailed the outside one (§18 row 40).
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
		return "asks Google to email nobody — which is not a promise of silence, and is refused when a guest is outside your organisation"
	case NotifyExternalOnly:
		return "emails only the guests outside your own organisation"
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
// them are outside the organiser's own domain.
//
// Counts, never addresses. A refusal says "this event has 4 guests", and
// the four addresses stay out of the message, the log and the transcript
// (§9).
type Reach struct {
	// Guests is people other than the caller. A room is not a person.
	Guests int
	// External is how many of those are outside the organiser's domain,
	// which is the axis `external_only` actually splits on (§18 row 40).
	External int
	// domain is the organiser's, kept for the split and never printed.
	domain string
}

// Any reports whether this write can reach a person at all, which is
// what makes notify required (§4.3.2).
func (r Reach) Any() bool { return r.Guests > 0 }

// ReachOfEvent counts an event's guests against the organiser's domain.
//
// Who counts as a guest is model.Event.Guests' to say, not this
// package's: it is the rule §4.3.2 decides a refusal on, and a second
// copy here would let one result quote two different counts.
//
// account is the signed-in account's own address, which is never a
// guest — Google's `self` flag alone was not enough to establish that,
// and the live run proved it (see model.Event.Guests).
func ReachOfEvent(organiser, account string, e model.Event) Reach {
	guests := e.Guests(account)
	r := Reach{domain: domainOf(organiser), Guests: len(guests)}
	for _, a := range guests {
		if r.isExternal(a.Email) {
			r.External++
		}
	}
	return r
}

// ReachOfAddresses counts a guest list the caller supplied, which is
// what create_event has before the event exists.
//
// The organiser's own address does not count: inviting yourself is not
// reaching somebody, and a notify requirement over it would be friction
// with no safety in it (§4.3.2).
func ReachOfAddresses(organiser string, addresses []string) Reach {
	r := Reach{domain: domainOf(organiser)}
	for _, a := range addresses {
		a = strings.TrimSpace(a)
		if a == "" || strings.EqualFold(a, organiser) {
			continue
		}
		r.Guests++
		if r.isExternal(a) {
			r.External++
		}
	}
	return r
}

// isExternal reports whether an address sits outside the organiser's
// domain.
//
// An unknown organiser domain makes every guest external, and that is
// deliberate: the consequence of guessing wrong is §4.3.4's refusal not
// firing for somebody who cannot discover the event by any other means.
// Failing towards the refusal costs a caller one explicit choice.
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
	// because there is no decision to make (§4.3.2).
	Asked bool
	Reach Reach
}

// Notification applies §4.3 to one write.
//
// Three rules, in this order:
//
//  1. a write that reaches nobody does not ask (§4.3.2);
//  2. a write that reaches somebody and names no choice is refused, with
//     the count and the three options (§4.3.1);
//  3. `none` with a guest outside the organiser's domain is refused
//     rather than warned about — that guest may have no Google Calendar
//     at all, and then mail is the only channel there is. Spike B
//     invited one with `none`, it received nothing, and there was no
//     calendar for the event to land in (§4.3.4, §18 row 44).
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
			"%w: %d of the %d guests %s outside your organisation, and notify:none is refused there rather than"+
				" warned about. Such a guest may have no Google Calendar for the event to appear in, so email is"+
				" the only way they can learn of it — the event would exist with them attached and they could not"+
				" find it. Pass notify:external_only to reach exactly them, or notify:all",
			ErrBlocked, r.External, r.Guests, isare(r.External))
	}
	return Decision{Notify: choice, Asked: true, Reach: r}, nil
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
// there was no decision to make (§4.3.2).
func (d Decision) SendUpdatesFor() string {
	if !d.Asked {
		return ""
	}
	return d.Notify.SendUpdates()
}

// Report is what a result says about notification (§4.9).
//
// It says what the server ASKED FOR. It never says what arrived: `none`
// is not silence (§2.6), and `all` is not delivery — spike A watched one
// invitation reach one of two guests and not the other, the difference
// being the receiving provider and nothing in the request (§18 row 42).
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
		return fmt.Sprintf("Asked Google to notify the %d of %d %s outside your organisation."+
			" That is what was asked for, not what arrived: the API reports nothing about delivery.",
			d.Reach.External, d.Reach.Guests, People(d.Reach.Guests))
	default:
		return fmt.Sprintf("Asked Google to notify all %d %s, %d of them outside your organisation."+
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

func isare(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}
