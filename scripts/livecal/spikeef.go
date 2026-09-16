//go:build live

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/recur"
	"github.com/mmedum/google-calendar-mcp/internal/redact"
	"github.com/mmedum/google-calendar-mcp/internal/when"
)

// spikeE answers §15's "this and following" question, and it is the one
// §4.2 makes a promise about: Google's guide says the two-call pattern
// "resets any exceptions happening after the target instance", and the
// server's result says so in plain words. If the guide is wrong, the
// server is telling every caller something untrue.
//
// The split itself comes from internal/recur, not from arithmetic
// invented here, so this also holds phase 1's Split against Google:
// a spike that computes the answer its own way tests nothing the write
// path will actually do.
func spikeE(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	zone, err := time.LoadLocation(scratchZone)
	if err != nil {
		return undetermined, "could not load " + scratchZone
	}
	if err := api.insertEvent(ctx, scratch, map[string]any{
		"id": spikeESeriesID, "summary": spikeETitle,
		"start":      map[string]any{"dateTime": spikeEStart, "timeZone": scratchZone},
		"end":        map[string]any{"dateTime": spikeEEnd, "timeZone": scratchZone},
		"recurrence": []string{spikeERule},
	}); err != nil {
		return undetermined, "could not create the series: " + redact.String(err.Error())
	}

	rows, err := api.listInstances(ctx, scratch, spikeESeriesID)
	if err != nil {
		return undetermined, "could not read the series' occurrences: " + redact.String(err.Error())
	}
	if len(rows) < 7 {
		return undetermined, fmt.Sprintf("the series expanded to %d occurrences; this spike needs 7", len(rows))
	}

	// The target is the fourth occurrence, and the exception is the
	// sixth — AFTER the target, which is the only case §2.8 is about.
	const targetIdx, exceptionIdx = 3, 5
	target, exception := rows[targetIdx], rows[exceptionIdx]

	// Move the sixth occurrence half an hour later. That is the
	// exception whose survival is the question.
	movedStart, err := shiftRFC3339(exception.Start.DateTime, 30*time.Minute)
	if err != nil {
		return undetermined, "could not compute the exception's new start: " + err.Error()
	}
	movedEnd, err := shiftRFC3339(exception.Start.DateTime, 90*time.Minute)
	if err != nil {
		return undetermined, "could not compute the exception's new end: " + err.Error()
	}
	if err := api.patchEvent(ctx, scratch, exception.ID, map[string]any{
		"start": map[string]any{"dateTime": movedStart, "timeZone": scratchZone},
		"end":   map[string]any{"dateTime": movedEnd, "timeZone": scratchZone},
	}); err != nil {
		return undetermined, "could not move the sixth occurrence: " + redact.String(err.Error())
	}
	out.Printf("      occurrence %d moved to %s; splitting at occurrence %d (%s)\n",
		exceptionIdx+1, wallClock(movedStart), targetIdx+1, wallClock(target.Start.DateTime))

	// The split, computed by the package the write path will use.
	set, err := recur.Parse([]string{spikeERule})
	if err != nil {
		return undetermined, "internal/recur could not parse its own rule: " + err.Error()
	}
	startZ, err := parseZoned(rows[0].Start.DateTime, zone)
	if err != nil {
		return undetermined, "could not read the series start: " + err.Error()
	}
	targetZ, err := parseZoned(target.Start.DateTime, zone)
	if err != nil {
		return undetermined, "could not read the target start: " + err.Error()
	}
	before, after, err := set.Split(startZ, targetZ)
	if err != nil {
		return undetermined, "internal/recur could not split the series: " + err.Error()
	}
	out.Printf("      split: original keeps %q, the new series takes %q\n", before, after)

	// Call one: end the original before the target.
	if err := api.patchEvent(ctx, scratch, spikeESeriesID, map[string]any{
		"recurrence": []string{before},
	}); err != nil {
		return undetermined, "could not truncate the original series: " + redact.String(err.Error())
	}
	// Call two: a new series starting at the target.
	if err := api.insertEvent(ctx, scratch, map[string]any{
		"id": spikeENewID, "summary": spikeETitle,
		"start":      map[string]any{"dateTime": target.Start.DateTime, "timeZone": scratchZone},
		"end":        map[string]any{"dateTime": addHour(target.Start.DateTime), "timeZone": scratchZone},
		"recurrence": []string{after},
	}); err != nil {
		return undetermined, "could not create the following series: " + redact.String(err.Error())
	}

	// The question: on the new series, is the old exception's date back
	// at its scheduled time, or still moved?
	newRows, err := api.listInstances(ctx, scratch, spikeENewID)
	if err != nil {
		return undetermined, "could not read the new series: " + redact.String(err.Error())
	}
	wantDate := exception.Start.DateTime[:10]
	scheduled, moved := wallClock(target.Start.DateTime), wallClock(movedStart)
	for _, r := range newRows {
		if !strings.HasPrefix(r.Start.DateTime, wantDate) {
			continue
		}
		switch got := wallClock(r.Start.DateTime); got {
		case scheduled:
			return pass, "CONFIRMS §2.8: the exception after the target was RESET — the moved " +
				"occurrence came back at " + got + ", its scheduled time. §4.2's warning is accurate " +
				"and the write path must say so in its result"
		case moved:
			return fail, "REFUTES §2.8: the exception survived the split at " + got +
				". §4.2 warns callers about a reset that does not happen, and the wording must change"
		default:
			return undetermined, "the occurrence on that date came back at " + got +
				", which is neither its scheduled time nor where it was moved"
		}
	}
	return undetermined, "the new series has no occurrence on " + wantDate +
		", so there is nothing to compare"
}

// spikeF answers §15's duplicate-insert question, which decides whether
// `ambiguous_outcome` (§6.5) is the right class or an over-cautious one.
//
// §2.11: "we cannot guarantee that ID collisions will be detected at
// event creation time". Two inserts of the same client-generated id, at
// the same time, is the case that sentence is about.
func spikeF(ctx context.Context, out *redact.Printer, api *liveAPI, scratch string) (verdict, string) {
	body := func() map[string]any {
		return map[string]any{
			"id": spikeFID, "summary": spikeFTitle,
			"start": map[string]any{"dateTime": "2026-05-12T09:00:00+02:00", "timeZone": scratchZone},
			"end":   map[string]any{"dateTime": "2026-05-12T10:00:00+02:00", "timeZone": scratchZone},
		}
	}

	// Both in flight before either can land. A sequential pair would
	// answer a different, easier question.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = api.insertEvent(ctx, scratch, body())
		}(i)
	}
	close(start)
	wg.Wait()

	ok, conflicts, other := 0, 0, []string{}
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case strings.Contains(err.Error(), "returned 409"):
			conflicts++
		default:
			other = append(other, redact.String(firstLine(err.Error())))
		}
	}
	out.Printf("      two concurrent inserts of one id: %d accepted, %d conflicted, %d other\n",
		ok, conflicts, len(other))
	for _, o := range other {
		out.Printf("      other: %s\n", o)
	}

	// Whatever Google answered, exactly one event may exist afterwards:
	// the id is the key, so a second event under it is impossible. What
	// the spike is really asking is whether the CALLER was told.
	if _, err := api.getEvent(ctx, scratch, spikeFID); err != nil {
		return undetermined, "neither insert left an event behind: " + redact.String(err.Error())
	}

	switch {
	case ok == 2:
		return pass, "REPRODUCES §2.11: BOTH concurrent inserts of the same id were accepted, so a " +
			"collision is not detected at creation time. A caller that retries cannot tell whether it " +
			"created one event or two, which is exactly what `ambiguous_outcome` exists to say"
	case ok == 1 && conflicts == 1:
		return pass, "The collision WAS caught: one insert succeeded and the other got 409. That is " +
			"the good case, and it does not retire `ambiguous_outcome` — §2.11 declines to guarantee " +
			"this, and the class is also for a retry after a transport failure, where the caller never " +
			"saw the first answer at all"
	case ok == 0:
		return undetermined, "neither insert succeeded, so the collision question was never reached"
	default:
		return undetermined, fmt.Sprintf("%d accepted, %d conflicted, %d other: not a shape this "+
			"spike knows how to read", ok, conflicts, len(other))
	}
}

// shiftRFC3339 moves a timestamp by d, keeping its offset spelling.
func shiftRFC3339(ts string, d time.Duration) (string, error) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "", fmt.Errorf("could not parse %q", ts)
	}
	return t.Add(d).Format(time.RFC3339), nil
}

func addHour(ts string) string {
	out, err := shiftRFC3339(ts, time.Hour)
	if err != nil {
		return ts
	}
	return out
}

// parseZoned turns Google's timestamp into the value internal/recur
// works in, in the calendar's own zone rather than the offset's.
func parseZoned(ts string, loc *time.Location) (when.Zoned, error) {
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return when.Zoned{}, fmt.Errorf("could not parse %q", ts)
	}
	return when.Zoned{T: t.In(loc), Loc: loc}, nil
}
