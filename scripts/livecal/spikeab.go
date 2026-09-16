//go:build live

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/redact"
)

// Guest addresses come from the environment and never from this
// repository (hard rule 1, §9.1). The leak gate's allow-list is
// example/test/invalid/localhost, so a real address committed anywhere
// in the tree fails `make check` on the spot — which is the intended
// way to find out, rather than a reviewer noticing.
const (
	envGuestInternal  = "GCAL_LIVE_GUEST_INTERNAL"
	envGuestExternal  = "GCAL_LIVE_GUEST_EXTERNAL"
	envGuestNonGoogle = "GCAL_LIVE_GUEST_NONGOOGLE"
)

// guests is who spikes A and B invite, and which axis each one tests.
type guests struct {
	// internal is inside the signed-in account's own domain.
	internal string
	// external is outside the domain but still on Google Calendar, and is
	// the guest `externalOnly` reaches: the axis is the organiser's
	// Workspace domain, not the guest's calendar system, which took a
	// live probe to establish against the documentation (§18 row 40).
	external string
	// nonGoogle is not on Google Calendar at all. It is out-of-domain too,
	// so `externalOnly` should reach it as well; what it adds is a guest
	// whose calendar is independent of Google's, which is what §2.7's
	// "not syncing to external calendars" warning is about.
	nonGoogle string
}

func guestsFromEnv() guests {
	return guests{
		internal:  strings.TrimSpace(os.Getenv(envGuestInternal)),
		external:  strings.TrimSpace(os.Getenv(envGuestExternal)),
		nonGoogle: strings.TrimSpace(os.Getenv(envGuestNonGoogle)),
	}
}

func (g guests) attendees() []map[string]any {
	var out []map[string]any
	for _, a := range []string{g.internal, g.external, g.nonGoogle} {
		if a != "" {
			out = append(out, map[string]any{"email": a})
		}
	}
	return out
}

func (g guests) any() bool { return len(g.attendees()) > 0 }

// describe says who is configured without printing anybody's address in
// full; the redactor would mask them anyway, and a count reads better.
func (g guests) describe() string {
	parts := []string{}
	for _, p := range []struct{ label, addr string }{
		{"internal", g.internal},
		{"external-on-Google", g.external},
		{"non-Google", g.nonGoogle},
	} {
		if p.addr != "" {
			parts = append(parts, p.label)
		} else {
			parts = append(parts, p.label+": none")
		}
	}
	return strings.Join(parts, ", ")
}

// spikeA sets up §15's notification question. It cannot answer it.
//
// Who received mail is visible in an inbox and nowhere in any API
// response, so this creates one event per `sendUpdates` value, says
// exactly what to look for, and stops. The verdict is written into §18
// by hand after the inboxes are read. A spike that returned `pass` here
// would be scoring the insert, not the notification — which is the
// failure §15 opens by naming.
func spikeA(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	if !spikeNotify {
		return undetermined, "not armed: this spike mails real people, so it needs -spike-notify. " +
			"Its verdict is already recorded in §18 rows 40 and 41"
	}
	g := guestsFromEnv()
	if !g.any() {
		return undetermined, fmt.Sprintf("no guests configured; set %s, %s and %s to run it "+
			"(addresses never enter this repository, §9.1)",
			envGuestInternal, envGuestExternal, envGuestNonGoogle)
	}
	out.Printf("      guests: %s\n", g.describe())

	// One event per value, each named so an inbox can be read against
	// this list without guessing.
	for _, arm := range []struct{ value, id, when string }{
		{"none", spikeAIDBase + "0", "2026-06-02T09:00:00+02:00"},
		{"externalOnly", spikeAIDBase + "1", "2026-06-02T11:00:00+02:00"},
		{"all", spikeAIDBase + "2", "2026-06-02T13:00:00+02:00"},
	} {
		body := map[string]any{
			"id":        arm.id,
			"summary":   spikeATitle + " — sendUpdates=" + arm.value,
			"start":     map[string]any{"dateTime": arm.when, "timeZone": scratchZone},
			"end":       map[string]any{"dateTime": addHour(arm.when), "timeZone": scratchZone},
			"attendees": g.attendees(),
		}
		if err := api.insertWithUpdates(ctx, scratch, body, arm.value); err != nil {
			return fail, "could not create the " + arm.value + " event: " + redact.String(err.Error())
		}
		out.Printf("      created %q with sendUpdates=%s\n", spikeATitle+" — "+arm.value, arm.value)
	}

	note := "SET UP, not answered: three events exist, one per sendUpdates value. Read each guest's " +
		"inbox AND calendar and record what arrived in §18. The two together are what distinguishes " +
		"a notification Google withheld from one that was filtered on the way"
	if g.external == "" {
		note += ". NOTE: no out-of-domain guest is configured, and that is the only one externalOnly " +
			"reaches (§18 row 40), so that arm is untested"
	}
	return undetermined, note
}

// spikeB sets up §15's "none loses events" question, which decides
// whether §4.3 must refuse `none` on insert outright.
//
// Also not answerable from a response: the failure is the event never
// arriving in the guest's calendar, which only the guest can see. The
// insert succeeds either way.
func spikeB(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	if !spikeNotify {
		return undetermined, "not armed: this spike mails real people, so it needs -spike-notify"
	}
	g := guestsFromEnv()
	if !g.any() {
		return undetermined, "no guests configured; see spike A"
	}
	body := map[string]any{
		"id":        spikeBID,
		"summary":   spikeBTitle,
		"start":     map[string]any{"dateTime": "2026-06-03T09:00:00+02:00", "timeZone": scratchZone},
		"end":       map[string]any{"dateTime": "2026-06-03T10:00:00+02:00", "timeZone": scratchZone},
		"attendees": g.attendees(),
	}
	if err := api.insertWithUpdates(ctx, scratch, body, "none"); err != nil {
		return fail, "could not create the event: " + redact.String(err.Error())
	}
	// It is on the organiser's calendar whatever happened — that is not
	// the question, and checking it here would be the spike answering
	// something easy in place of something hard.
	if _, err := api.getEvent(ctx, scratch, spikeBID); err != nil {
		return fail, "the insert reported success and the event is not there: " + redact.String(err.Error())
	}
	out.Printf("      created %q with sendUpdates=none and %d guest(s)\n", spikeBTitle, len(g.attendees()))

	note := "SET UP, not answered: the insert succeeded, which it does whether or not the guests ever " +
		"see it. Check each guest's CALENDAR, not their mail: §2.7 warns of events not syncing or " +
		"being lost altogether. If it reproduces, §4.3 refuses none on insert. Read it against the " +
		"same guest's `all` event from spike A — an absence means nothing unless the control arrived"
	if g.nonGoogle == "" {
		note += ". NOTE: no non-Google guest is configured. A Google Calendar guest may only see the " +
			"event once its invitation reaches them, which makes mail delivery and event delivery " +
			"hard to tell apart on that side"
	}
	return undetermined, note
}
