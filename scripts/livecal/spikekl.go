//go:build live

package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/mmedum/google-calendar-mcp/internal/redact"
)

// Phase 3's two spikes. Both are the shape rule 13 asks for: a
// convention this design adopted from the documentation, probed before
// it ships, with the verdict written into §18 by hand.

// spikeK — does calendars.clear work on a SECONDARY calendar?
//
// Google's own description is "Clears a primary calendar. This operation
// deletes all events associated with the primary calendar of an
// account." It does not say what happens when the id names a secondary
// one, and the three possible answers need different code:
//
//   - REFUSED. clear_calendar's refusal of a secondary calendar is the
//     API's rule and not this server's invention, and §7.5 can say so.
//   - CLEARED. The refusal is this server's own and should be lifted:
//     emptying a calendar without deleting it is a thing people want,
//     and delete_calendar is not the same operation.
//   - ACCEPTED AND DID NOTHING. The worst answer, and the one worth
//     finding: a call that reports success and leaves every event in
//     place. The server would have to keep refusing, and now for a
//     reason with evidence behind it.
//
// It is NOT run against the account's primary calendar, ever: that is
// the single most destructive call this API offers (§9), and no answer
// is worth it.
//
// It is not run against the SCRATCH calendar either, and that is the
// less obvious half. Spikes A and B leave events there deliberately, for
// a person to read in their own inbox hours later, and clearEvents goes
// out of its way to preserve them — a spike that emptied that calendar
// would destroy the evidence the run exists to produce, which this
// driver has done once already. So it uses the destination calendar,
// which holds nothing but the move probe, and puts its own event on it
// first so that "cleared" can be told from "was already empty".
func spikeK(ctx context.Context, out *redact.Printer, api *liveAPI, _ string) (verdict, string) {
	if spikeDest == "" {
		return undetermined, "no destination calendar this run, and clearing the scratch one would " +
			"destroy spikes A and B's evidence"
	}
	id := "livecalclearprobe" + runSuffix
	if err := api.insertEvent(ctx, spikeDest, map[string]any{
		"id": id, "summary": "Livecal clear probe",
		"start": map[string]any{"dateTime": "2026-04-20T09:00:00+02:00", "timeZone": scratchZone},
		"end":   map[string]any{"dateTime": "2026-04-20T10:00:00+02:00", "timeZone": scratchZone},
	}); err != nil {
		return undetermined, "could not create the probe event: " + redact.String(err.Error())
	}
	defer func() {
		// Harmless if the clear already removed it; no guests, so
		// nothing can be mailed.
		_ = api.do(context.Background(), http.MethodDelete,
			"/calendars/"+spikeDest+"/events/"+id+"?sendUpdates=none", nil, nil)
	}()

	before, err := api.countEvents(ctx, spikeDest)
	if err != nil {
		return undetermined, "could not count the events before clearing: " + redact.String(err.Error())
	}
	if before == 0 {
		return undetermined, "the probe event is not listed, so a clear could not be told from a no-op"
	}

	status, clearErr := api.status(ctx, http.MethodPost, "/calendars/"+spikeDest+"/clear", "", nil, nil)
	out.Printf("      clear on a SECONDARY calendar answered %d\n", status)

	if status >= 400 {
		return pass, fmt.Sprintf("REFUSED (%d): clear does not work on a secondary calendar, so "+
			"clear_calendar's refusal is the API's rule rather than this server's caution. §7.5 may say so",
			status)
	}
	if clearErr != nil {
		return undetermined, "the clear failed without an answer this spike can read: " +
			redact.String(clearErr.Error())
	}

	after, err := api.countEvents(ctx, spikeDest)
	if err != nil {
		return undetermined, "the clear was accepted but the events could not be counted afterwards: " +
			redact.String(err.Error())
	}
	// Counting afterwards is the discriminator, and skipping it is the
	// mistake spike I made: a 2xx alone does not say anything was
	// deleted, and "accepted and did nothing" is a real possibility here.
	if after == 0 {
		return pass, fmt.Sprintf("CLEARED: %d events on a secondary calendar became 0, so clear is not "+
			"primary-only and clear_calendar's refusal should be lifted (§7.5)", before)
	}
	return pass, fmt.Sprintf("ACCEPTED AND DID NOTHING: clear answered %d and %d of %d events are still "+
		"there. A call that reports success and changes nothing — clear_calendar must keep refusing a "+
		"secondary calendar, and now with a reason", status, after, before)
}

// spikeL — is If-Match honoured on the calendar and sharing writes?
//
// §2.4 says ETags and If-Match are supported across this API, and §4.4
// puts every write under one. Spike J found that the general rule held
// in a place nothing published said it would — events.move — and the
// same question is open here for four methods this phase calls:
// calendars.patch, calendars.delete, acl.patch and acl.delete. The
// server sends the header on all of them; whether Google enforces it is
// what this asks.
//
// The discriminator is a STALE etag, as in spike J. A current one
// succeeds whether the header is honoured or ignored and settles
// nothing.
func spikeL(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	// calendars.patch, on the driver's own scratch calendar.
	stale, err := api.calendarETag(ctx, scratch)
	if err != nil {
		return undetermined, "could not read the scratch calendar's etag: " + redact.String(err.Error())
	}
	if stale == "" {
		return undetermined, "Google returned no etag on the calendar, so there is nothing to go stale"
	}
	// Move the etag on, so the one held above is genuinely out of date.
	// The description is the driver's own text and changes nothing a
	// reader of this repository could not already see.
	if err := api.patchCalendar(ctx, scratch, map[string]any{
		"description": "Created by the google-calendar-mcp live driver. Safe to delete. (etag probe)",
	}); err != nil {
		return undetermined, "could not patch the scratch calendar: " + redact.String(err.Error())
	}
	fresh, err := api.calendarETag(ctx, scratch)
	if err != nil {
		return undetermined, "could not re-read the calendar's etag: " + redact.String(err.Error())
	}
	if fresh == stale {
		return undetermined, "the calendar's etag did not move after a patch, so a stale one cannot be built"
	}

	status, _ := api.status(ctx, http.MethodPatch, "/calendars/"+scratch, stale,
		map[string]any{"description": "this write should be refused"}, nil)
	out.Printf("      calendars.patch with a STALE If-Match answered %d\n", status)

	calendarHonoured := status == http.StatusPreconditionFailed

	// acl.patch, on a rule this spike creates and removes. The address
	// is in a domain that cannot resolve, and the rule is inserted with
	// sendNotifications=false, so nothing is sent anywhere.
	aclStatus, aclNote := api.probeACLIfMatch(ctx, scratch)
	out.Printf("      acl.patch with a STALE If-Match answered %d\n", aclStatus)
	aclHonoured := aclStatus == http.StatusPreconditionFailed

	switch {
	case aclNote != "":
		return undetermined, fmt.Sprintf("calendars.patch answered %d; the ACL half could not run: %s",
			status, aclNote)
	case calendarHonoured && aclHonoured:
		return pass, "HONOURED on both: a stale If-Match is refused with 412 by calendars.patch and " +
			"acl.patch, so §4.4 covers the calendar and sharing writes as it covers the event ones"
	case !calendarHonoured && !aclHonoured:
		return pass, fmt.Sprintf("IGNORED on both: calendars.patch answered %d and acl.patch %d with a "+
			"STALE etag, so neither offers optimistic concurrency and the results must stop implying it",
			status, aclStatus)
	default:
		return pass, fmt.Sprintf("SPLIT: calendars.patch answered %d and acl.patch %d with a stale etag. "+
			"One of the two enforces If-Match and the other does not, so the two results cannot say the "+
			"same thing about it", status, aclStatus)
	}
}
