//go:build live

package main

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/redact"
)

// spikeC answers §15's daylight-saving question, and it is the half
// phase 0 could not answer: what happens to a recurring event written
// WITHOUT a time zone.
//
// The zoned half already runs as a step — the seeded weekly series holds
// 14:00 across the 29 March transition. This writes the same series with
// a dateTime and no timeZone and reads its occurrences back. §2.2 says
// the zone is required on a recurring event, so there are three possible
// answers and each is worth having:
//
//   - Google REFUSES it. §4.1's refusal has the API behind it.
//   - Google accepts it and the series DRIFTS. That is §3's second row,
//     reproduced, and the reason this server always sends a zone.
//   - Google accepts it and the series holds. Then the zone this server
//     insists on is belt and braces, which is worth knowing and does not
//     change the rule.
func spikeC(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	if err := api.createUnzonedSeries(ctx, scratch); err != nil {
		// A refusal IS the answer, not a failure of the spike. Google's
		// message is quoted through the redactor.
		if strings.Contains(err.Error(), "returned 4") {
			out.Printf("      Google refused the unzoned series: %s\n", redact.String(err.Error()))
			return pass, "CONFIRMS §2.2: a recurring event without a timeZone is refused by the API"
		}
		return undetermined, "could not create the unzoned series: " + redact.String(err.Error())
	}

	rows, err := api.listInstances(ctx, scratch, unzonedID)
	if err != nil {
		return undetermined, "the unzoned series was created but its instances could not be read: " +
			redact.String(err.Error())
	}
	if len(rows) < 3 {
		return undetermined, fmt.Sprintf("only %d occurrences came back; the 29 March transition is at the third", len(rows))
	}

	// The wall clock of each occurrence, as Google expanded it. The
	// series starts at 14:00 on 17 March; the third occurrence is 31
	// March, after the transition.
	var walls []string
	for _, r := range rows {
		walls = append(walls, wallClock(r.Start.DateTime))
	}
	out.Printf("      unzoned series expanded to: %s\n", strings.Join(walls, ", "))

	before, after := walls[0], walls[len(walls)-1]
	if before == after {
		return pass, fmt.Sprintf("Google accepted a recurrence with no timeZone and held %s across the "+
			"transition — this server still sends the zone (§2.2), and now knows the API does not need it", before)
	}
	return pass, fmt.Sprintf("REPRODUCES §3: an unzoned recurrence drifted from %s to %s across the "+
		"29 March transition. This is the defect every surveyed server ships", before, after)
}

// wallClock takes the local time out of an RFC3339 timestamp without
// parsing it into an instant: the point is what a person would read on
// the clock, which is the string Google returned.
func wallClock(dateTime string) string {
	if len(dateTime) < 16 {
		return dateTime
	}
	return dateTime[11:16]
}

// spikeI answers §15's ceiling question: calendarExpansionMax is
// documented with a maximum of 50 (§2.10), and this server batches at
// that number. What happens at 51 decides whether the batching is
// correctness or politeness — and, more importantly, whether a query
// that exceeds it FAILS or silently answers for fewer calendars.
//
// A silent truncation is the dangerous one: the missing calendars come
// back as no answer at all, which §4.6 reports as unknown. That is only
// true because this server treats a missing calendar as unknown rather
// than as free, and this spike is what tells us the case is real.
func spikeI(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	// The scratch calendar repeated: the driver reads only what it
	// created (§9.1), and the question is about the count rather than
	// about which calendars they are.
	ids := func(n int) []string { return slices.Repeat([]string{scratch}, n) }

	atFifty, err := api.freeBusy(ctx, ids(50), 50)
	if err != nil {
		return undetermined, "50 calendars failed outright: " + redact.String(err.Error())
	}
	out.Printf("      50 calendars: %d answered\n", len(atFifty))

	atFiftyOne, err := api.freeBusy(ctx, ids(51), 50)
	switch {
	case err != nil:
		return pass, "CONFIRMS §2.10: 51 calendars in one query is refused (" +
			redact.String(firstLine(err.Error())) + "). The batching at 50 is correctness, not politeness"
	case len(atFiftyOne) < 51 && len(atFiftyOne) > 0:
		return pass, fmt.Sprintf("51 calendars answered for %d: Google TRUNCATES silently rather than "+
			"refusing, so a calendar can be missing from the response entirely — which is why a missing "+
			"calendar is reported unknown and never free (§4.6)", len(atFiftyOne))
	default:
		// Note the request deduplicates: the same id 51 times may be one
		// calendar as far as expansion is concerned, which would make
		// the count meaningless rather than reassuring.
		return undetermined, fmt.Sprintf("51 calendars answered for %d; the repeated id may have been "+
			"deduplicated before expansion, so this run does not settle the ceiling", len(atFiftyOne))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
