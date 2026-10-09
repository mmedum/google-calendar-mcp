//go:build live

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
)

// The write steps (§16, phase 2). This is the phase §16 says needs the
// most live work and where the transcript matters most, for one reason:
// a write that reports success and did something else leaves a meeting
// on somebody's calendar, and no gate against a fake can see it.
//
// Two rules hold here and are structural rather than careful:
//
//   - Nothing these steps write has a guest but this account itself, so
//     no step below can mail another person. The notification guards are
//     driven through their REFUSALS, which never reach Google at all;
//     spikes A and B are the only things in this driver that send, and
//     they are behind -spike-notify.
//   - Everything is on the scratch calendar this run created and emptied,
//     or on the second one it created for move_event, so §9.1's promise
//     about the transcript holds for the write path too.

const (
	writeTitle    = "Livecal write probe"
	writtenTitle  = "Livecal written probe"
	allDayWrite   = "Livecal all-day write probe"
	dryRunTitle   = "Livecal dry run that must not exist"
	rsvpTitle     = "Livecal rsvp probe"
	meetTitle     = "Livecal conference probe"
	cancelTitle   = "Livecal cancel probe"
	optionalTitle = "Livecal optional guest probe"
	// The two that reach a real person. Armed by -spike-notify, like
	// spikes A and B, because every other step in this file is written
	// so that it CANNOT mail anybody and these two are written so that
	// they do.
	guestTitle = "Livecal guest write probe"
	quietTitle = "Livecal quiet cancel probe"
)

// rsvpID is the event this account is a guest on, so respond_to_event
// has an invitation to answer. Its only attendee is the account itself:
// an RSVP probe that invited anybody else would mail them on every run.
var rsvpID = "livecalrsvpprobe" + runSuffix

// outsideGuest is an address that cannot exist, in a domain that cannot
// resolve (RFC 2606). It is only ever used on calls the server refuses
// BEFORE it builds a request, so nothing is ever sent to it.
const outsideGuest = "livecal-nobody@example.test"

// writeState carries what one write step made to the next.
//
// The ids do not exist when the step list is built — the server mints
// them (§2.11) — so the steps that use them read this through argsFn
// instead of through a literal.
type writeState struct {
	created   string
	etag      string
	dest      string
	self      string
	splitFrom string
	// meeting is the event created with conference: true, so the step
	// that reads the link back knows which event to ask for.
	meeting string
	// optional is the event whose only guest is this account, invited
	// as optional, so the step after it can read the role back.
	optional string
	// guest is a REAL person's address, from GCAL_LIVE_GUEST_INTERNAL,
	// and it is empty unless -spike-notify armed the run. The steps that
	// use it are the only ones here that put an event in somebody else's
	// calendar, so they check both before doing anything.
	guest string
	// api reads what no tool shows, for the steps that must check a
	// field the server does not model.
	api *liveAPI
}

// seedRSVP puts an event on the scratch calendar with this account as a
// guest, which is what respond_to_event needs to have anything to answer.
//
// sendUpdates=none and one attendee, the account itself: there is nobody
// else for Google to notify, so this cannot reach a person however the
// parameter is read.
func (a *liveAPI) seedRSVP(ctx context.Context, cal, self string) error {
	// Through insertEvent, which owns the sendUpdates=none rule and the
	// id check. A second copy of "this driver never mails anybody" is
	// one copy too many for the invariant §9.1 rests on.
	return a.insertEvent(ctx, cal, map[string]any{
		"id": rsvpID, "summary": rsvpTitle,
		// Deliberately OUTSIDE the read steps' window. It sat on
		// 19 March at 13:00 and quietly broke two of them: one grepped
		// the whole page for a date, the other for a clock time, and
		// this probe satisfied both. Those assertions are tightened now,
		// but a probe that is not in the window cannot be mistaken for
		// one that is.
		"start":     map[string]any{"dateTime": "2026-04-08T11:00:00+02:00", "timeZone": scratchZone},
		"end":       map[string]any{"dateTime": "2026-04-08T12:00:00+02:00", "timeZone": scratchZone},
		"attendees": []map[string]any{{"email": self}},
	})
}

// setReminders changes only this account's reminders on the probe
// event. It needs no notify, which is part of what it checks.
func setReminders(on func(map[string]any) map[string]any, w *writeState, name string,
	args map[string]any,
) step {
	return step{
		name: name,
		tool: "update_event",
		argsFn: func() map[string]any {
			args["event_id"] = w.created
			return on(args)
		},
		skip: func() string {
			if w.created == "" {
				return "no probe event was created"
			}
			return ""
		},
		check: func(r callResult) (verdict, string) {
			if r.isError {
				return fail, "returned an error: " + truncate(r.text, 300)
			}
			if !strings.Contains(r.text, "Reminders are yours alone") {
				return fail, "a reminders-only update did not say it reaches nobody"
			}
			return pass, "patched without notify"
		},
	}
}

// readReminders reads the probe event's reminders back.
func readReminders(scratch string, w *writeState, name, want string) step {
	return step{
		name: name,
		tool: "get_event",
		argsFn: func() map[string]any {
			return map[string]any{"calendar": scratch, "event_id": w.created}
		},
		skip: func() string {
			if w.created == "" {
				return "no probe event was created"
			}
			return ""
		},
		check: func(r callResult) (verdict, string) {
			if r.isError {
				return fail, "returned an error: " + truncate(r.text, 200)
			}
			if !strings.Contains(r.text, want+"\n") {
				return fail, "Google kept something else: " + truncate(r.text, 400)
			}
			return pass, "read back as " + strings.TrimPrefix(want, "your reminders: ")
		},
	}
}

// field reads one value out of a write result, which is how a step hands
// an id or an etag to the next one.
func field(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), prefix)
		if !ok {
			continue
		}
		if f := strings.Fields(rest); len(f) > 0 {
			return f[0]
		}
	}
	return ""
}

// writeSteps is phase 2's half of the run.
func writeSteps(scratch string, w *writeState) []step {
	on := func(extra map[string]any) map[string]any {
		out := map[string]any{"calendar": scratch}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	return []step{
		{
			name: "create_event",
			tool: "create_event",
			args: on(map[string]any{
				"title": writeTitle,
				"start": "2026-04-01T09:00:00+02:00", "end": "2026-04-01T10:00:00+02:00",
				"location": "Room one",
				// Honored on an event with no guests, so the parameter
				// is exercised end to end while nothing can be sent.
				"notify": "none",
			}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				w.created, w.etag = field(r.text, "id: "), field(r.text, "etag: ")
				if w.created == "" {
					return fail, "the result does not report the id it created"
				}
				if err := gcal.ValidEventID(w.created); err != nil {
					return fail, "the server minted an id Google should have refused: " + err.Error()
				}
				if w.etag == "" {
					return fail, "the result does not report an etag for the next write (§4.9)"
				}
				if !strings.Contains(r.text, "09:00-10:00") {
					return fail, "the created event does not read back at 09:00-10:00"
				}
				return pass, "created with a client-generated id and an etag"
			},
		},
		{
			// §4.1 on the write path, which is the mirror of spike D:
			// the caller names the LAST day and it must come back as one
			// day, all day, on that date — not two, and never a time.
			name: "create_event all-day",
			tool: "create_event",
			args: on(map[string]any{
				"title": allDayWrite, "start": "2026-04-03", "end": "2026-04-03",
			}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "all day") {
					return fail, "an all-day write did not come back all-day"
				}
				if !strings.Contains(r.text, "2026-04-03") {
					return fail, "the all-day event is not on the date it was written for"
				}
				if strings.Contains(r.text, "2026-04-04") {
					return fail, "the inclusive end became an extra day"
				}
				if strings.Contains(r.text, "00:00") {
					return fail, "an all-day event rendered a time"
				}
				return pass, "one day, all day, on 2026-04-03"
			},
		},
		{
			// §17.3. The link is made asynchronously, so this step
			// asserts what the answer SAYS rather than that a link
			// arrived — a step that demanded a URI here would be
			// asserting Google's timing.
			name: "create_event with a Meet link",
			tool: "create_event",
			args: on(map[string]any{
				"title": meetTitle,
				"start": "2026-04-09T09:00:00+02:00", "end": "2026-04-09T10:00:00+02:00",
				"conference": true, "notify": "none",
			}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				w.meeting = field(r.text, "id: ")
				if w.meeting == "" {
					return fail, "the result does not report the id it created"
				}
				switch {
				case strings.Contains(r.text, "Google Meet: https://meet.google.com/"):
					return pass, "the link came back with the insert"
				case strings.Contains(r.text, "still making it"):
					return pass, "reported pending rather than promising a link"
				case strings.Contains(r.text, "could NOT be created"):
					return fail, "Google refused the conference on a calendar that allows it: " +
						truncate(r.text, 300)
				default:
					return fail, "a conference was asked for and the result says nothing about one: " +
						truncate(r.text, 300)
				}
			},
		},
		{
			// The half the first step cannot assert: the link Google
			// made after answering. This is also the read the result
			// tells a caller to do, so a failure here means that advice
			// is wrong.
			name: "the Meet link arrives",
			tool: "get_event",
			skip: func() string {
				if w.meeting == "" {
					return "no conference event was created"
				}
				return ""
			},
			argsFn: func() map[string]any {
				return map[string]any{"calendar": scratch, "event_id": w.meeting}
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				switch {
				case strings.Contains(r.text, "join: https://meet.google.com/"):
					return pass, "the event carries a link to join"
				case strings.Contains(r.text, "still creating"):
					return undetermined, "Google has not finished making the link; it is not this " +
						"server's to hurry, and the event says so rather than claiming a link"
				default:
					return fail, "the event neither carries a link nor says one is coming: " +
						truncate(r.text, 300)
				}
			},
		},
		{
			// §4.3.1. Refused before a request is built, so the address
			// below is never sent anywhere.
			name: "notify required with guests",
			tool: "create_event",
			args: on(map[string]any{
				"title": "Livecal must not be created",
				"start": "2026-04-02T09:00:00+02:00", "end": "2026-04-02T10:00:00+02:00",
				"guests": []string{outsideGuest},
			}),
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "an event with guests was created without a notify decision"
				}
				if !strings.Contains(r.text, "[invalid]") {
					return fail, "the refusal is not classified invalid: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "1 guest") {
					return fail, "the refusal does not say how many people it would reach"
				}
				return pass, "refused with [invalid], naming the count and the three choices"
			},
		},
		{
			// §4.3.4 and spike B, as a guard: `none` is refused rather
			// than warned about when a guest is outside the domain.
			name: "none refused outside domain",
			tool: "create_event",
			args: on(map[string]any{
				"title": "Livecal must not be created either",
				"start": "2026-04-02T09:00:00+02:00", "end": "2026-04-02T10:00:00+02:00",
				"guests": []string{outsideGuest}, "notify": "none",
			}),
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "notify:none was accepted for a guest outside the organizer's domain"
				}
				if !strings.Contains(r.text, "[blocked]") {
					return fail, "the refusal is not classified blocked: " + truncate(r.text, 200)
				}
				return pass, "refused with [blocked]"
			},
		},
		{
			// §18 row 92: an optional guest is mailed like any guest, so
			// notify is required. Refused before a request is built, so
			// the address below is never sent anywhere.
			name: "notify required with an optional guest",
			tool: "create_event",
			args: on(map[string]any{
				"title": "Livecal must not be created, optional",
				"start": "2026-04-02T09:00:00+02:00", "end": "2026-04-02T10:00:00+02:00",
				"optional_guests": []string{outsideGuest},
			}),
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "an event with an optional guest was created without a notify decision"
				}
				if !strings.Contains(r.text, "[invalid]") || !strings.Contains(r.text, "1 guest") {
					return fail, "the refusal is not [invalid] counting one guest: " + truncate(r.text, 200)
				}
				return pass, "refused with [invalid], counting the optional guest"
			},
		},
		{
			// §18 row 92: does Google keep optional: true? The only guest
			// is this account, which reaches nobody, so none is allowed
			// and nobody is mailed.
			name: "create_event with an optional guest",
			tool: "create_event",
			argsFn: func() map[string]any {
				return on(map[string]any{
					"title": optionalTitle,
					"start": "2026-04-02T11:00:00+02:00", "end": "2026-04-02T12:00:00+02:00",
					"optional_guests": []string{w.self}, "notify": "none",
				})
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				w.optional = field(r.text, "id: ")
				if w.optional == "" {
					return fail, "the result does not report the id it created"
				}
				return pass, "created with this account as its optional guest"
			},
		},
		{
			name: "get_event shows the optional guest",
			tool: "get_event",
			argsFn: func() map[string]any {
				return map[string]any{"calendar": scratch, "event_id": w.optional}
			},
			skip: func() string {
				if w.optional == "" {
					return "the step before created nothing"
				}
				return ""
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, ", optional") {
					return fail, "the guest does not read back as optional, so Google dropped the role"
				}
				return pass, "Google kept the guest optional"
			},
		},
		{
			// §4.3.5: the blast radius, without the blast.
			name: "dry_run writes nothing",
			tool: "create_event",
			args: on(map[string]any{
				"title": dryRunTitle,
				"start": "2026-04-04T09:00:00+02:00", "end": "2026-04-04T10:00:00+02:00",
				"dry_run": true,
			}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "DRY RUN") {
					return fail, "a dry run does not say so in the text a model sees"
				}
				return pass, "reported without writing"
			},
		},
		{
			// And the proof, which is the half a dry run cannot assert
			// about itself.
			name: "the dry run did not land",
			tool: "list_events",
			args: map[string]any{
				"calendars": []string{scratch}, "from": "2026-04-04", "to": "2026-04-04",
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if strings.Contains(r.text, dryRunTitle) {
					return fail, "the dry run created an event"
				}
				return pass, "nothing on the day the dry run named"
			},
		},
		{
			name: "update_event",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{
					"event_id": w.created, "title": writtenTitle, "location": "Room two",
				})
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "Room one") || !strings.Contains(r.text, "Room two") {
					return fail, "the result does not show what it changed from and to (§4.9)"
				}
				next := field(r.text, "etag: ")
				if next == "" {
					return fail, "the result reports no new etag"
				}
				if next == w.etag {
					return fail, "the etag did not move after a write"
				}
				return pass, "patched, with a before and after and a new etag"
			},
		},
		{
			// §18 row 95: q reads the location, among the fields Google
			// documents. The update above set this one.
			name: "search_events matches a location",
			tool: "search_events",
			args: map[string]any{
				"calendars": []string{scratch}, "from": "2026-04-01", "to": "2026-04-01", "query": "Room two",
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, writtenTitle) {
					// The index is eventually consistent, as on the
					// read-side search step.
					return undetermined, "the event was not found by its location; Google's index may not " +
						"have caught up with a change made seconds ago"
				}
				return pass, "found the event by its location, not its title"
			},
		},
		{
			// §4.3.2 and §18 row 86: a guest the write adds is reached by
			// it, so adding one to an event with none asks. Refused before
			// a request is built, so the address is never sent.
			name: "notify required when an update adds a guest",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.created, "add_guests": []string{outsideGuest}})
			},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "a guest was added without a notify decision"
				}
				if !strings.Contains(r.text, "[invalid]") || !strings.Contains(r.text, "1 guest") {
					return fail, "the refusal is not [invalid] naming one guest: " + truncate(r.text, 200)
				}
				return pass, "refused with [invalid], counting the guest the update adds"
			},
		},
		{
			// §4.3.4 for the same write: none is refused for an outside
			// guest the update adds.
			name: "none refused when an update adds an outside guest",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.created, "add_guests": []string{outsideGuest}, "notify": "none"})
			},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "notify:none was accepted for an outside guest the update adds"
				}
				if !strings.Contains(r.text, "[blocked]") {
					return fail, "the refusal is not classified blocked: " + truncate(r.text, 200)
				}
				return pass, "refused with [blocked]"
			},
		},
		{
			// §18 row 93: a guest given as a mailbox, as a Gmail server
			// hands one over, is its bare address. A dry run, so the
			// address is never sent anywhere.
			name: "a guest given as a mailbox",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{
					"event_id": w.created, "add_guests": []string{`"Livecal Nobody" <` + outsideGuest + `>`},
					"notify": "external_only", "dry_run": true,
				})
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "a mailbox was refused: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "1 added") {
					return fail, "the dry run does not add the one guest"
				}
				if strings.Contains(r.text, "Livecal Nobody") {
					return fail, "the display name reached the result"
				}
				return pass, "taken as its bare address, the display name dropped"
			},
		},
		{
			name: "two addresses in one entry refused",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{
					"event_id": w.created, "add_guests": []string{outsideGuest + ", " + outsideGuest},
					"notify": "external_only", "dry_run": true,
				})
			},
			check: func(r callResult) (verdict, string) {
				if !r.isError || !strings.Contains(r.text, "[invalid]") {
					return fail, "a list in one entry was not refused as invalid: " + truncate(r.text, 200)
				}
				return pass, "refused with [invalid]"
			},
		},
		// §18 row 97: a reminders patch replaces the whole set, by an
		// overrides list sent in full or as null, and each is read back.
		setReminders(on, w, "reminders set", map[string]any{
			"popup_reminders": []int{30, 10}, "email_reminders": []int{1440},
		}),
		readReminders(scratch, w, "reminders read back",
			"your reminders: popup 10 minutes before, popup 30 minutes before, email 1 day before"),
		setReminders(on, w, "one list replaces both", map[string]any{"email_reminders": []int{60}}),
		readReminders(scratch, w, "the popups are gone", "your reminders: email 1 hour before"),
		setReminders(on, w, "an empty list removes them", map[string]any{"popup_reminders": []int{}}),
		readReminders(scratch, w, "no reminders read back", "your reminders: none"),
		setReminders(on, w, "back to the calendar's", map[string]any{"default_reminders": true}),
		readReminders(scratch, w, "the calendar's read back", "your reminders: the calendar's default ones"),
		{
			// §18 row 98: a patch can create a conference, under a
			// request id that is not the event id. The probe event has
			// no guest but this account, so nobody is reached.
			name: "add_conference",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.created, "add_conference": true, "notify": "none"})
			},
			skip: func() string {
				if w.created == "" {
					return "no probe event was created"
				}
				return ""
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				switch {
				case strings.Contains(r.text, "Google Meet: https://meet.google.com/"):
					return pass, "the link came back with the patch"
				case strings.Contains(r.text, "still making it"):
					return pass, "reported pending rather than promising a link"
				default:
					return fail, "a conference was asked for and the result does not report one: " +
						truncate(r.text, 300)
				}
			},
		},
		{
			name: "the added link arrives",
			tool: "get_event",
			argsFn: func() map[string]any {
				return map[string]any{"calendar": scratch, "event_id": w.created}
			},
			skip: func() string {
				if w.created == "" {
					return "no probe event was created"
				}
				return ""
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				switch {
				case strings.Contains(r.text, "join: https://meet.google.com/"):
					return pass, "the event carries the link the patch asked for"
				case strings.Contains(r.text, "still creating"):
					return undetermined, "Google has not finished making the link"
				default:
					return fail, "the event has no link after add_conference: " + truncate(r.text, 300)
				}
			},
		},
		{
			// Refused before a request: Google would replace the link.
			name: "add_conference refused when there is one",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.created, "add_conference": true, "notify": "none"})
			},
			skip: func() string {
				if w.created == "" {
					return "no probe event was created"
				}
				return ""
			},
			check: func(r callResult) (verdict, string) {
				if !r.isError || !strings.Contains(r.text, "[blocked]") {
					return fail, "a second conference request was not refused: " + truncate(r.text, 200)
				}
				return pass, "refused with [blocked]"
			},
		},
		{
			// §4.4: the etag the caller decided on is held to. The one
			// captured above is now the previous version.
			name: "stale etag refused",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{
					"event_id": w.created, "location": "Room three", "etag": w.etag,
				})
			},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "a write against a superseded etag overwrote somebody"
				}
				if !strings.Contains(r.text, "[stale]") {
					return fail, "the refusal is not classified stale: " + truncate(r.text, 200)
				}
				return pass, "refused with [stale], not retried"
			},
		},
		{
			// §4.2: no scope, no write — and the refusal says how far
			// each choice would reach.
			name: "scope required on a series",
			tool: "update_event",
			args: on(map[string]any{"event_id": weeklyID, "location": "Room four"}),
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "a repeating event was written without a scope"
				}
				if !strings.Contains(r.text, "[invalid]") {
					return fail, "the refusal is not classified invalid: " + truncate(r.text, 200)
				}
				for _, want := range []string{"instance", "series", "this_and_following"} {
					if !strings.Contains(r.text, want) {
						return fail, "the refusal does not offer " + want
					}
				}
				if !strings.Contains(r.text, "occurrences") {
					return fail, "the refusal does not say how far a series write would reach"
				}
				return pass, "refused with the three choices and the reach"
			},
		},
		{
			// §6.2: an occurrence addressed by the series id and the
			// start it was SCHEDULED for.
			// 17 March, the FIRST occurrence, deliberately: the seed
			// cancels the second one, and a step that changed a
			// canceled occurrence proved nothing and then made the
			// next two steps unreadable.
			name: "scope instance by start",
			tool: "update_event",
			args: on(map[string]any{
				"event_id": weeklyID, "original_start": "2026-03-17T14:00:00+01:00",
				"scope": "instance", "location": "Room five",
			}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "Addressed the occurrence") {
					return fail, "the result does not say which address it used (§6.2)"
				}
				if !strings.Contains(r.text, "Room five") {
					return fail, "the occurrence was not changed"
				}
				return pass, "one occurrence changed, addressed by its scheduled start"
			},
		},
		{
			// §2.8 and spike E, through the tool this time. The series
			// has four occurrences from 17 March; splitting at the third
			// leaves two behind and starts two anew.
			name: "this_and_following splits",
			tool: "update_event",
			args: on(map[string]any{
				"event_id": weeklyID, "original_start": "2026-03-31T14:00:00+02:00",
				"scope": "this_and_following", "title": "Livecal split probe",
			}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				w.splitFrom = field(r.text, "id: ")
				if !strings.Contains(r.text, "RESET") {
					return fail, "the result does not warn that later exceptions were reset (§4.2)"
				}
				if !strings.Contains(r.text, "COUNT=2") {
					return fail, "the original series was not truncated to its first two occurrences: " +
						truncate(r.text, 300)
				}
				// The SENTENCE, not the request total. api_requests
				// counts everything the call spent now — the setup reads
				// included — so it is six here, and asserting on "2 API
				// requests" was asserting on the old dishonest count.
				if !strings.Contains(r.text, "is two calls") {
					return fail, "the result does not say it was two calls (§4.7)"
				}
				return pass, "original truncated, new series started, reset stated"
			},
		},
		{
			// §4.2: the new series is the old one copied, fields the
			// server does not model included. The seed put a private
			// extended property on the series, and only a direct read
			// can see it.
			name: "the split keeps what the series carried",
			tool: "get_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.splitFrom})
			},
			skip: func() string {
				if w.splitFrom == "" {
					return "the split did not report the new series"
				}
				return ""
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				got, err := w.api.privateProperty(context.Background(), scratch, w.splitFrom, splitKey)
				if err != nil {
					return fail, "could not read the new series: " + truncate(err.Error(), 200)
				}
				if got != splitValue {
					return fail, "the new series lost the private extended property the series carried"
				}
				if attachmentURL() == "" {
					return pass, "the new series kept the series' private extended property; " +
						"the attachment half is owed (" + envAttachment + " is unset)"
				}
				if !strings.Contains(r.text, "Attachments (1):") {
					return fail, "the new series lost the file attached to the series"
				}
				return pass, "the new series kept the series' private extended property and its file"
			},
		},
		{
			// The proof that the split landed the way the result said.
			name: "the original series is short",
			tool: "list_instances",
			args: map[string]any{"calendar": scratch, "event_id": weeklyID},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if strings.Contains(r.text, "2026-03-31") || strings.Contains(r.text, "2026-04-07") {
					return fail, "an occurrence at or after the split is still in the original series"
				}
				if !strings.Contains(r.text, "2026-03-17") {
					return fail, "the first occurrence went missing from the original series"
				}
				// The exception made BEFORE the target must survive: the
				// reset is of what comes after. It is on 17 March, which
				// is still visible; the seed's canceled occurrence is
				// the second one and is hidden here by design.
				if !strings.Contains(r.text, "Room five") {
					return fail, "the 17 March exception did not survive a split made after it"
				}
				return pass, "the earlier exception survived the split, and nothing after it remains"
			},
		},
		{
			// §18 row 97: Google applies a more restrictive visibility on
			// one occurrence to the whole series. The result says so;
			// the step after reads the series to see it is true.
			name: "a private occurrence",
			tool: "update_event",
			args: on(map[string]any{
				"event_id": weeklyID, "original_start": "2026-03-17T14:00:00+01:00",
				"scope": "instance", "visibility": "private",
			}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "makes the whole series private") {
					return fail, "the result does not say the series changed: " + truncate(r.text, 300)
				}
				return pass, "made, with a note that the series changed"
			},
		},
		{
			name: "the series is private",
			tool: "get_event",
			args: map[string]any{"calendar": scratch, "event_id": weeklyID},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "[private") {
					return fail, "the series is not private, so the note the step before printed is false"
				}
				return pass, "one private occurrence made the series private, as the reference says"
			},
		},
		{
			// Refused before a request: Google ignores it.
			name: "a less restrictive occurrence refused",
			tool: "update_event",
			args: on(map[string]any{
				"event_id": weeklyID, "original_start": "2026-03-17T14:00:00+01:00",
				"scope": "instance", "visibility": "public",
			}),
			check: func(r callResult) (verdict, string) {
				if !r.isError || !strings.Contains(r.text, "[unsupported]") {
					return fail, "a public occurrence of a private series was not refused: " + truncate(r.text, 200)
				}
				return pass, "refused with [unsupported]"
			},
		},
		{
			// §7.4: an occurrence is canceled with a status patch, not
			// deleted, because that is how one date leaves a series.
			name: "cancel one occurrence",
			tool: "cancel_event",
			args: on(map[string]any{
				"event_id": weeklyID, "original_start": "2026-03-17T14:00:00+01:00",
				"scope": "instance",
			}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "rather than deleted") {
					return fail, "the result does not say which of the two shapes it used (§7.4)"
				}
				return pass, "one date removed with a status patch"
			},
		},
		{
			name: "the occurrence is gone",
			tool: "list_instances",
			args: map[string]any{"calendar": scratch, "event_id": weeklyID, "show_canceled": true},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "CANCELED") {
					return fail, "the canceled occurrence is not marked as one"
				}
				return pass, "the removed date shows as canceled, which is how it is told from missing"
			},
		},
		{
			name: "respond_to_event",
			tool: "respond_to_event",
			args: map[string]any{
				"calendar": scratch, "event_id": rsvpID, "response": "declined",
				"comment": "Livecal probe answer",
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "declined") {
					return fail, "the answer did not come back as declined"
				}
				if !strings.Contains(r.text, "your response") {
					return fail, "the result does not report which field it changed"
				}
				return pass, "answered, and only this account's own row changed"
			},
		},
		{
			name: "move_event",
			tool: "move_event",
			argsFn: func() map[string]any {
				// Same rule: an empty destination would resolve to the
				// operator's own primary calendar (§9.1).
				to := w.dest
				if to == "" {
					to = scratch
				}
				return on(map[string]any{"event_id": w.created, "to_calendar": to})
			},
			check: func(r callResult) (verdict, string) {
				if w.dest == "" {
					return undetermined, "no destination calendar; the creation quota is spent (§18 row 36)"
				}
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "organizer") {
					return fail, "the result does not say a move changes the organizer"
				}
				return pass, "moved to the second scratch calendar"
			},
		},
		{
			// And back, so the scratch calendar ends the run as it began.
			name: "move_event back",
			tool: "move_event",
			argsFn: func() map[string]any {
				// Never an empty calendar: ResolveCalendar reads "" as
				// "primary", which is the operator's OWN calendar, and
				// §9.1 says this driver touches only what it created.
				// The check below reports undetermined either way; what
				// matters is that the request is not made at all.
				from := w.dest
				if from == "" {
					from = scratch
				}
				return map[string]any{
					"calendar": from, "event_id": w.created, "to_calendar": scratch,
				}
			},
			check: func(r callResult) (verdict, string) {
				if w.dest == "" {
					return undetermined, "no destination calendar to move back from"
				}
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				return pass, "moved back, leaving the scratch calendar as it was"
			},
		},
		{
			name: "cancel_event dry_run",
			tool: "cancel_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.created, "dry_run": true})
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "DRY RUN") {
					return fail, "a dry run does not say so"
				}
				return pass, "reported without canceling"
			},
		},
		{
			name: "the event survived the dry run",
			tool: "get_event",
			argsFn: func() map[string]any {
				return map[string]any{"calendar": scratch, "event_id": w.created}
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "a dry run canceled the event: " + truncate(r.text, 200)
				}
				return pass, "still there"
			},
		},
		{
			name: "cancel_event",
			tool: "cancel_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.created})
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "deletes the event") {
					return fail, "the result does not say a whole event was deleted (§7.4)"
				}
				if !strings.Contains(r.text, "no guests") {
					return fail, "the result does not say whether anybody else is holding it (§4.3.3)"
				}
				return pass, "deleted, and the result says who else has it"
			},
		},
		// ---------------------------------------------------------------
		// The steps that reach a real person. Everything above this line
		// is written so that it cannot.
		//
		// §4.3 is the largest bet in this design and until now no live
		// step had ever exercised it through the TOOLS with somebody on
		// the other end: every write step above has no guests, and
		// spikes A and B go through the driver's own REST calls rather
		// than through the server. So the notification path a caller
		// actually uses — notify required, the result reporting what was
		// ASKED FOR rather than what arrived — was green offline and
		// unproven live.
		{
			name: "create_event with a real guest",
			tool: "create_event",
			argsFn: func() map[string]any {
				return on(map[string]any{
					"title": guestTitle,
					"start": "2026-04-20T10:00:00+02:00", "end": "2026-04-20T10:30:00+02:00",
					"guests": []string{w.guest}, "notify": "all",
				})
			},
			check: func(r callResult) (verdict, string) {
				if w.guest == "" {
					return undetermined, "no guest configured; this step mails a real person and needs " +
						envGuestInternal + " with -spike-notify"
				}
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				w.created = field(r.text, "id: ")
				if !strings.Contains(r.text, "1 guest") {
					return fail, "the result does not report who it reaches"
				}
				// §4.3.3: what was asked for, never what arrived.
				if !strings.Contains(r.text, "not what arrived") {
					return fail, "the result claims a delivery the API never reports: " + truncate(r.text, 200)
				}
				return pass, "invitation sent, and the result says asked-for rather than delivered"
			},
		},
		{
			name: "update_event with a real guest",
			tool: "update_event",
			argsFn: func() map[string]any {
				return on(map[string]any{
					"event_id": w.created, "start": "2026-04-20T11:00:00+02:00",
					"end": "2026-04-20T11:30:00+02:00", "notify": "all",
				})
			},
			check: func(r callResult) (verdict, string) {
				if w.guest == "" || w.created == "" {
					return undetermined, "no guest event to update"
				}
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "10:00") || !strings.Contains(r.text, "11:00") {
					return fail, "the result does not show the time it moved from and to"
				}
				return pass, "rescheduled, and the guest was asked to be told"
			},
		},
		{
			name: "cancel_event notifying",
			tool: "cancel_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.created, "notify": "all"})
			},
			check: func(r callResult) (verdict, string) {
				if w.guest == "" || w.created == "" {
					return undetermined, "no guest event to cancel"
				}
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "Google was asked to tell") {
					return fail, "the result does not say the guest was told: " + truncate(r.text, 200)
				}
				return pass, "withdrawn properly: the guest was asked to be told"
			},
		},
		{
			// §18 row 43, through the tool this time. This one LEAVES a
			// meeting on a real person's calendar that this account can
			// no longer withdraw — that is the finding, not a side
			// effect — so it is armed with everything else that mails,
			// and the run says so at the end.
			name: "create for the quiet cancel",
			tool: "create_event",
			argsFn: func() map[string]any {
				return on(map[string]any{
					"title": quietTitle,
					"start": "2026-04-21T10:00:00+02:00", "end": "2026-04-21T10:30:00+02:00",
					"guests": []string{w.guest}, "notify": "all",
				})
			},
			check: func(r callResult) (verdict, string) {
				if w.guest == "" {
					return undetermined, "no guest configured"
				}
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				w.splitFrom = field(r.text, "id: ")
				return pass, "created, and the guest was invited so they have it to keep"
			},
		},
		{
			name: "cancel_event quietly (§18 row 43)",
			tool: "cancel_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.splitFrom, "notify": "none"})
			},
			check: func(r callResult) (verdict, string) {
				if w.guest == "" || w.splitFrom == "" {
					return undetermined, "no guest event to cancel quietly"
				}
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				// The sentence §4.3.3 exists for, and the one a caller
				// most needs: it is gone from yours and still on theirs.
				if !strings.Contains(r.text, "leaves it on theirs") {
					return fail, "a quiet cancellation did not warn that the guest keeps the meeting: " +
						truncate(r.text, 250)
				}
				if !strings.Contains(r.text, "still") {
					return fail, "the warning does not say the guest still has it"
				}
				return pass, "canceled with no notification, and the result says the guest still has it — " +
					"CHECK THEIR CALENDAR: that meeting is still there and this account cannot remove it"
			},
		},
		{
			name: "canceling it twice conflicts",
			tool: "cancel_event",
			argsFn: func() map[string]any {
				return on(map[string]any{"event_id": w.created})
			},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "canceling an event that is already gone reported success"
				}
				if !strings.Contains(r.text, "[not_found]") && !strings.Contains(r.text, "[conflict]") {
					return fail, "the refusal is classified neither not_found nor conflict: " +
						truncate(r.text, 200)
				}
				return pass, "refused: " + firstLine(truncate(r.text, 120))
			},
		},
	}
}

// destinationCalendar prepares the calendar move_event needs, and says
// what it cost.
//
// A failure here is not a failed run: the two move steps report
// undetermined and everything else goes on. The quota this spends is the
// one §18 row 36 is about, and a run that cannot have a second calendar
// is still worth reading.
func destinationCalendar(ctx context.Context, api *liveAPI) (id string, created bool, note string) {
	id, created, err := api.ensureScratchCalendar(ctx, destTitle)
	if err != nil {
		return "", false, fmt.Sprintf("move_event has no destination calendar: %s", err.Error())
	}
	if created {
		return id, true, "destination calendar created; it costs one of the creation quota, once"
	}
	return id, false, "destination calendar adopted; no calendar quota spent"
}
