//go:build live

package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/gcal"
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
	// Two rules this spike got wrong once each, both of them the same
	// mistake: answering a question it did not ask.
	//
	// DISTINCT ids. The first run sent the scratch calendar's id 51
	// times. Google keys the response by calendar id, so 51 copies came
	// back as one entry, and the branch reading "fewer than asked for"
	// called that a silent truncation. It was deduplication.
	//
	// NO calendarExpansionMax. The second run sent 51 ids and asked
	// Google to expand at most 50 — so a response carrying 50 would
	// have been this driver's own cap reported as Google's ceiling.
	// The server sets the cap in production (§4.6); the spike must not,
	// because the cap is the thing being measured.
	//
	// The filler ids are invented and belong to nobody, so this still
	// reads only the one calendar the driver created (§9.1). An id that
	// cannot be resolved comes back as an entry carrying an error, which
	// is what makes the count meaningful: a calendar dropped for
	// exceeding a ceiling is ABSENT, where an unreadable one is present
	// and errored.
	ids := make([]string, 0, 51)
	ids = append(ids, scratch)
	for i := 1; i < 51; i++ {
		ids = append(ids, fmt.Sprintf("livecal-ceiling-%02d@example.test", i))
	}

	atFifty, err := api.freeBusy(ctx, ids[:50], 0)
	if err != nil {
		return undetermined, "50 calendars failed outright: " + redact.String(err.Error())
	}
	out.Printf("      50 distinct calendars, no expansion cap: %d answered, %d errored\n",
		len(atFifty), errored(atFifty))
	if len(atFifty) != 50 {
		return undetermined, fmt.Sprintf("50 distinct calendars answered for %d, so the response is "+
			"not one entry per requested id and the count at 51 would mean nothing", len(atFifty))
	}

	atFiftyOne, err := api.freeBusy(ctx, ids, 0)
	if err != nil {
		return pass, "CONFIRMS §2.10: 51 calendars in one query is refused (" +
			redact.String(firstLine(err.Error())) + "). The batching at 50 is correctness, not politeness"
	}
	out.Printf("      51 distinct calendars, no expansion cap: %d answered, %d errored\n",
		len(atFiftyOne), errored(atFiftyOne))

	// What this can and cannot settle, stated before the verdict.
	//
	// Fifty of the fifty-one ids are invented, so Google answers them
	// with an error rather than with free/busy. If the ceiling counts
	// only the calendars it actually EXPANDS, an errored entry costs
	// nothing against it and 51 readable calendars could still behave
	// differently. Settling that would mean creating 51 real calendars,
	// which this driver is not going to do to somebody's account.
	const caveat = " Fifty of the ids were unreadable, so this does not settle 51 READABLE calendars: " +
		"the ceiling may count only the calendars it expands"

	// The readable half, which is the only thing the unreadable run
	// cannot settle. Off by default: it creates 51 real calendars on a
	// real account, and 51 calendars nobody asked for is a worse outcome
	// than an unanswered question if this crashes halfway.
	if spikeCeiling {
		v, note := ceilingWithReadableCalendars(ctx, out, api)
		if v != undetermined {
			return v, note
		}
		out.Printf("      readable half: %s\n", note)
	}

	switch {
	case len(atFiftyOne) == 51:
		return pass, "51 distinct ids came back as 51 entries with no expansion cap set, so Google " +
			"neither refuses nor trims the request at the documented maximum of 50." + caveat
	case len(atFiftyOne) > 0:
		return pass, fmt.Sprintf("51 distinct ids came back as %d entries: Google DROPS the excess "+
			"silently rather than refusing, so a calendar can be missing from the response entirely — "+
			"which is why a missing calendar is reported unknown and never free (§4.6).%s",
			len(atFiftyOne), caveat)
	default:
		return undetermined, "51 calendars answered for none, which is neither a refusal nor a truncation"
	}
}

// errored counts the calendars Google answered with an error rather than
// with free/busy. The distinction is the whole point of the count: an
// unreadable calendar is PRESENT and errored, where one dropped for
// exceeding the ceiling is absent.
func errored(calendars map[string]gcal.FreeBusyCalendar) int {
	n := 0
	for _, c := range calendars {
		if len(c.Errors) > 0 {
			n++
		}
	}
	return n
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ceilingWithReadableCalendars answers what the unreadable ids cannot:
// whether free/busy trims a request of 51 calendars it can actually
// expand.
//
// It creates them, asks, and deletes them, reporting anything it could
// not delete so nothing is left behind silently (§9.1). Every calendar
// is one this driver made, so it still reads only what it wrote.
func ceilingWithReadableCalendars(ctx context.Context, out *redact.Printer, api *liveAPI) (verdict, string) {
	const want = 51
	ids, err := api.createFillerCalendars(ctx, want)
	defer func() {
		stuck := 0
		for _, id := range ids {
			if derr := api.deleteCalendar(context.Background(), id); derr != nil {
				stuck++
				out.Printf("      WARNING: could not delete filler calendar %s: %v\n",
					redact.ID(id), redact.String(derr.Error()))
			}
		}
		if stuck > 0 {
			out.Printf("      %d filler calendars are still on the account; delete them by hand\n", stuck)
			return
		}
		out.Printf("      %d filler calendars deleted\n", len(ids))
	}()
	if err != nil {
		return undetermined, fmt.Sprintf("could only create %d of %d calendars: %s",
			len(ids), want, redact.String(err.Error()))
	}

	answered, err := api.freeBusy(ctx, ids, 0)
	if err != nil {
		return pass, "CONFIRMS §2.10: 51 READABLE calendars in one free/busy query is refused (" +
			redact.String(firstLine(err.Error())) + "). The batching at 50 is correctness"
	}
	out.Printf("      51 readable calendars, no expansion cap: %d answered, %d errored\n",
		len(answered), errored(answered))
	switch {
	case len(answered) == want:
		return pass, "51 READABLE calendars all came back, so the documented maximum of 50 does not " +
			"cap a free/busy request at all. §4.6's batching at 50 is politeness, and the rule that " +
			"matters — a calendar missing from the response is unknown, never free — stands on spike H"
	case len(answered) > 0:
		return pass, fmt.Sprintf("51 READABLE calendars came back as %d: Google TRIMS the excess "+
			"silently, so a calendar can be absent from the response entirely. That is exactly what "+
			"§4.6 reports as unknown rather than free, and the batching at 50 is correctness", len(answered))
	default:
		return undetermined, "51 readable calendars answered for none, which is neither a refusal nor a trim"
	}
}
