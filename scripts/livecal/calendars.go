//go:build live

package main

import (
	"strings"
)

// The calendar and sharing steps (§16, phase 3).
//
// Everything here happens on a calendar this run created through
// create_calendar and deletes through delete_calendar, so §9.1 holds for
// this half too: the only content that can reach the transcript is
// content the driver invented.
//
// **Nothing here can reach a person, and that is structural rather than
// careful.** The one address these steps share a calendar with is in
// `example.test` — RFC 2606, a domain that can never resolve — and every
// share is made with notify:none. A sharing step that named a real
// address would put a calendar in somebody's list on every run.

const (
	probeTitle   = "Livecal phase 3 calendar probe"
	probeRenamed = "Livecal phase 3 calendar renamed"
	probeMyName  = "Livecal phase 3 as I see it"
)

// probeGuest is an address in a domain that cannot exist. The sharing
// steps below DO reach Google, unlike the notify refusals in writes.go,
// so the address has to be one that no mail can arrive at.
const probeGuest = "livecal-nobody@example.test"

// calendarState carries the probe calendar between the steps that make
// it, use it and remove it.
type calendarState struct {
	// probe is the calendar create_calendar made this run. Empty when
	// that step failed — most often because the account's calendar
	// creation quota is spent (§18 row 36), which is not a defect in the
	// server, so the steps that need it report undetermined rather than
	// failing.
	probe string
	// remember tells the run this id is the driver's own, so §9.1 lets a
	// step's body be printed. It fails closed: an id nobody registered
	// makes the step account-wide by construction, and withheld.
	remember func(string)
}

// needProbe is every phase 3 step's skip: it reports why the step cannot
// run, or an empty string.
//
// It exists so a step whose calendar was never created is never CALLED.
// A tool call naming no calendar resolves to the account's primary one,
// so the alternative — calling and letting the result be judged
// afterward — would point `manage_calendar`, `unshare_calendar` and
// `clear_calendar` at the operator's own calendar on any run where
// `create_calendar` failed. The quota that makes that failure ordinary
// is §18 row 36.
func (c *calendarState) needProbe() string {
	if c.probe == "" {
		return "no probe calendar this run, so this step could not run"
	}
	return ""
}

func calendarSteps(c *calendarState) []step {
	// args builds a call against the probe calendar. It is only ever
	// reached for a step that has passed needProbe, because a call with
	// no calendar named would address the account's PRIMARY one.
	args := func(extra map[string]any) func() map[string]any {
		return func() map[string]any {
			out := map[string]any{"calendar": c.probe}
			for k, v := range extra {
				out[k] = v
			}
			return out
		}
	}

	return []step{
		{
			name: "create_calendar",
			tool: "create_calendar",
			args: map[string]any{
				"title": probeTitle, "time_zone": scratchZone,
				"description": "Created by the google-calendar-mcp live driver. Safe to delete.",
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					// The quota is spent rather than the server being
					// wrong, and §18 row 36 says that is expected on an
					// account this driver has run against all day.
					if strings.Contains(r.text, "quota") || strings.Contains(r.text, "usage limits") {
						return undetermined, "the calendar-creation quota is spent (§18 row 36); " +
							"phase 3's steps cannot run"
					}
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				c.probe = field(r.text, "id: ")
				if c.probe == "" {
					return fail, "the result does not report the id of the calendar it created"
				}
				c.remember(c.probe)
				if !strings.Contains(r.text, scratchZone) {
					return fail, "the new calendar does not report the zone it was created in"
				}
				return pass, "created, with its id and its zone"
			},
		},
		{
			// §7.5's first axis: the calendar itself, which everybody
			// subscribed to it sees.
			name:   "manage_calendar title",
			tool:   "manage_calendar",
			argsFn: args(map[string]any{"title": probeRenamed}),
			skip:   c.needProbe,
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, probeRenamed) {
					return fail, "the result does not show the new title"
				}
				if !strings.Contains(r.text, "everybody") {
					return fail, "the result does not say a rename is visible to everybody it is shared with"
				}
				return pass, "the calendar itself was renamed, and the result says who sees that"
			},
		},
		{
			// §7.5's third axis: this account's own view, which nobody
			// else sees. The color id is 1, which every account has.
			name: "manage_calendar my view",
			tool: "manage_calendar",
			argsFn: args(map[string]any{
				"my_name": probeMyName, "color_id": "1",
				"notifications": []string{"cancellation"},
			}),
			skip: c.needProbe,
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				for _, want := range []string{"my_name", "color_id", "notifications"} {
					if !strings.Contains(r.text, want) {
						return fail, "the change list does not mention " + want
					}
				}
				if !strings.Contains(r.text, "Nobody else sees") {
					return fail, "the result does not say these settings are this account's alone"
				}
				return pass, "the per-user overrides were set, and the result says nobody else sees them"
			},
		},
		{
			// **Unsubscribing from a calendar you own is refused by
			// Google**, and the live run is how that was found: 403,
			// "The data owner of a calendar cannot remove such a
			// calendar from their calendar list". Nothing published says
			// so, and this driver had been written expecting it to work.
			//
			// So the step holds the translation rather than the
			// operation. Every calendar this driver has is one it made,
			// which means the working path — unsubscribing from somebody
			// else's calendar — cannot be driven here at all without
			// reading past what the driver wrote (§9.1). It is covered
			// offline against the fake instead, and §16 says so.
			name:   "manage_calendar unsubscribe is refused for an owner",
			tool:   "manage_calendar",
			argsFn: args(map[string]any{"unsubscribe": true}),
			skip:   c.needProbe,
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "Google let the data owner unsubscribe from their own calendar, which it " +
						"refused on 2026-09-16 — §18 row 55 needs revisiting"
				}
				if !strings.Contains(r.text, "[unsupported]") {
					return fail, "the refusal is not classified unsupported: " + truncate(r.text, 200)
				}
				for _, want := range []string{"hidden:true", "delete_calendar"} {
					if !strings.Contains(r.text, want) {
						return fail, "the refusal does not offer " + want + ", which is what does work"
					}
				}
				return pass, "refused with [unsupported], naming the two things that do work"
			},
		},
		{
			// The calendar is still subscribed, because the step above
			// could not remove it. What this holds is the other half of
			// subscribe: asked for something already in the list, it
			// writes nothing and says so.
			name:   "manage_calendar subscribe is a no-op when already there",
			tool:   "manage_calendar",
			argsFn: args(map[string]any{"subscribe": true}),
			skip:   c.needProbe,
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "Already in your calendar list") {
					return fail, "the result does not say it was already subscribed"
				}
				return pass, "nothing was added, and the result says why"
			},
		},
		{
			name:   "list_sharing",
			tool:   "list_sharing",
			argsFn: args(nil),
			skip:   c.needProbe,
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				// A calendar this account just created is shared with
				// exactly one rule: its own owner.
				if !strings.Contains(r.text, "owner") {
					return fail, "a calendar this account owns does not list an owner rule"
				}
				if !strings.Contains(r.text, "can see and change everything") {
					return fail, "the roles are echoed rather than explained (§7.6)"
				}
				return pass, "the owner rule is listed and explained"
			},
		},
		{
			// §4.3 at the ACL: no notify, no write. Refused before a
			// request is built, so nothing reaches Google.
			name: "share_calendar needs notify",
			tool: "share_calendar",
			argsFn: args(map[string]any{
				"who": probeGuest, "role": "reader",
			}),
			skip: c.needProbe,
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "a calendar was shared with no notification decision"
				}
				if !strings.Contains(r.text, "[invalid]") {
					return fail, "the refusal is not classified invalid: " + truncate(r.text, 200)
				}
				return pass, "refused with [invalid], naming the two choices"
			},
		},
		{
			// external_only has nothing to mean on a sharing change, and
			// rounding it either way would email the wrong set of people.
			name: "share_calendar refuses external_only",
			tool: "share_calendar",
			argsFn: args(map[string]any{
				"who": probeGuest, "role": "reader", "notify": "external_only",
			}),
			skip: c.needProbe,
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "external_only was accepted on a sharing change"
				}
				if !strings.Contains(r.text, "[unsupported]") {
					return fail, "the refusal is not classified unsupported: " + truncate(r.text, 200)
				}
				return pass, "refused with [unsupported]"
			},
		},
		{
			// §7.6: the public scope needs the flag. This refusal is the
			// only thing standing between a model and publishing
			// somebody's calendar to the internet, so it is driven live.
			name: "share_calendar refuses public",
			tool: "share_calendar",
			argsFn: args(map[string]any{
				"who": "anyone", "role": "reader", "notify": "none",
			}),
			skip: c.needProbe,
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "THE CALENDAR WAS PUBLISHED: the public scope was accepted without allow_public"
				}
				if !strings.Contains(r.text, "[blocked]") {
					return fail, "the refusal is not classified blocked: " + truncate(r.text, 200)
				}
				return pass, "refused with [blocked], naming allow_public"
			},
		},
		{
			// The one sharing write that reaches Google. notify:none and
			// a domain that cannot resolve, so no mail can be sent and
			// none can arrive.
			name: "share_calendar",
			tool: "share_calendar",
			argsFn: args(map[string]any{
				"who": probeGuest, "role": "reader", "notify": "none",
			}),
			skip: c.needProbe,
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "who could see it before") ||
					!strings.Contains(r.text, "who can see it now") {
					return fail, "the result does not show exposure on both sides (§7.6)"
				}
				if !strings.Contains(r.text, "reader") {
					return fail, "the new rule is not in the exposure afterward"
				}
				if !strings.Contains(r.text, "not a promise of silence") {
					return fail, "notify:none was reported as silence (§4.3.3)"
				}
				return pass, "shared as reader, with exposure before and after"
			},
		},
		{
			// A second share for the same audience is a role change, and
			// it must be a patch rather than a second rule.
			name: "share_calendar changes a role",
			tool: "share_calendar",
			argsFn: args(map[string]any{
				"who": probeGuest, "role": "writer", "notify": "none",
			}),
			skip: c.needProbe,
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "from reader to writer") {
					return fail, "the result does not say what the access changed from"
				}
				if strings.Count(r.text, probeGuest) < 2 {
					return fail, "the changed rule is not in the exposure both before and after"
				}
				return pass, "the existing rule was patched, not duplicated"
			},
		},
		{
			name:   "unshare_calendar",
			tool:   "unshare_calendar",
			argsFn: args(map[string]any{"who": probeGuest}),
			skip:   c.needProbe,
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "no notification") {
					return fail, "the result does not say Google tells nobody about a removal"
				}
				// The exposure afterward must not still carry the rule
				// that was just removed. Read from the "now" heading
				// rather than from the whole page: the "before" half
				// names it too, and an assertion satisfied by a line it
				// is not about is the failure §15 opens by naming.
				at := strings.Index(r.text, "who can see it now")
				if at < 0 {
					return fail, "the result does not show the exposure afterward (§7.6)"
				}
				if strings.Contains(r.text[at:], probeGuest) {
					return fail, "the rule is still in the exposure after being removed"
				}
				return pass, "the rule is gone, and the result says nobody was told"
			},
		},
		{
			// Google documents clear as emptying a PRIMARY calendar, so
			// this server refuses a secondary one rather than sending a
			// request whose effect it cannot state. Spike K asks the API
			// directly; this holds the refusal a caller actually meets.
			name:   "clear_calendar refuses a secondary",
			tool:   "clear_calendar",
			argsFn: args(map[string]any{"confirm": true}),
			skip:   c.needProbe,
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "clear was accepted on a calendar that is not the primary"
				}
				if !strings.Contains(r.text, "[unsupported]") {
					return fail, "the refusal is not classified unsupported: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "delete_calendar") {
					return fail, "the refusal does not say what to use instead"
				}
				return pass, "refused with [unsupported], naming delete_calendar"
			},
		},
		{
			// §9's second half: the flag registers the tool and the call
			// still has to confirm.
			name:   "delete_calendar needs confirm",
			tool:   "delete_calendar",
			argsFn: args(nil),
			skip:   c.needProbe,
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "A CALENDAR WAS DELETED WITHOUT CONFIRMATION"
				}
				if !strings.Contains(r.text, "[blocked]") {
					return fail, "the refusal is not classified blocked: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "every event on it") {
					return fail, "the refusal does not say what would be destroyed"
				}
				return pass, "refused with [blocked], naming what would go"
			},
		},
		{
			// Last, because it removes the calendar every step above
			// used. It is also the only way this driver can leave
			// nothing behind: a run that fails before here leaves the
			// probe calendar, which the next run does not adopt.
			name:   "delete_calendar",
			tool:   "delete_calendar",
			argsFn: args(map[string]any{"confirm": true}),
			skip:   c.needProbe,
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 300)
				}
				if !strings.Contains(r.text, "cannot bring it back") {
					return fail, "the result does not say the deletion is irreversible"
				}
				return pass, "the probe calendar is gone"
			},
		},
	}
}
