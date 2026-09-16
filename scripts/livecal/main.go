//go:build live

// Command livecal drives the built binary against a real Google account.
//
// It is maintainer tooling, run by hand, and it is the only thing in
// this repository that touches a real calendar. Two rules make that
// safe, and both are structural rather than a matter of care (§9.1):
//
//   - It reads ONLY a calendar it created and filled itself. A scratch
//     calendar is created at the start and deleted at the end, so the
//     only content that can reach a transcript, a log or a test failure
//     is content this file invented.
//   - Every print goes through one redactor. `gates transcript` parses
//     this source and fails on any other print, because redaction that
//     depends on remembering to route a call is redaction that ends the
//     next time somebody adds a debug line.
//
// Setup and teardown talk to the REST API directly rather than through
// the server's tools: phase 0 has no write tools, and giving the driver
// its own writes keeps the server's published surface honest.
//
//	make live
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/mmedum/google-calendar-mcp/internal/redact"
)

func main() {
	bin := flag.String("bin", "./google-calendar-mcp", "the built binary to drive")
	keep := flag.Bool("keep", false,
		"leave the scratch calendar behind; the next run adopts it and spends no calendar quota")
	// A green count is not a read transcript. -show prints the redacted
	// body of every step whose name contains the substring, because the
	// defects this project keeps finding are the ones a pass/fail line
	// cannot show.
	show := flag.String("show", "", "print the full result of steps whose name contains this")
	// Which login to drive. Spike G's negative half needs a profile
	// granted WITHOUT the ACL scopes, so the driver has to be able to
	// point at one — hardcoding the default profile made that half
	// unanswerable and would have wasted a login to find out.
	profile := flag.String("profile", "default", "the configuration profile to drive")
	// Spike I's readable half creates 51 real calendars and deletes
	// them. That is a lot to do to somebody's account for one question,
	// so it is asked for rather than assumed.
	ceiling := flag.Bool("spike-ceiling", false,
		"spike I: also probe the free/busy ceiling with 51 REAL calendars, created and deleted")
	flag.Parse()

	spikeCeiling = *ceiling

	showFilter = *show

	out := redact.New(os.Stderr)
	code := run(context.Background(), out, *bin, *profile, *keep)
	os.Exit(code)
}

func run(ctx context.Context, out *redact.Printer, bin, profile string, keep bool) int {
	out.Printf("livecal: driving %s as profile %q\n\n", bin, profile)

	api, err := newAPI(ctx, profile)
	if err != nil {
		out.Printf("could not authenticate: %v\n", err)
		out.Printf("run `%s login --profile=%s` first\n", bin, profile)
		return 2
	}

	scratch, created, err := api.ensureScratchCalendar(ctx)
	if err != nil {
		out.Printf("could not create the scratch calendar: %v\n", redact.String(err.Error()))
		// The one failure here that is not a bug and not a setup
		// mistake, so it gets its own sentence rather than a raw 403.
		if strings.Contains(err.Error(), "quotaExceeded") ||
			strings.Contains(err.Error(), "usage limits") {
			out.Printf("\nThis account's calendar-creation quota is spent (§18 row 36). The limit " +
				"counts\ncalendars created, and deleting them does not refund it, so the driver " +
				"cannot make\nthe scratch calendar it reads. Wait for Google to reset it and run " +
				"again.\n")
			out.Printf("`-spike-ceiling` spends this quota 51 at a time; that is why it is off by " +
				"default.\n")
		}
		return 2
	}
	if created {
		out.Printf("scratch calendar %s created\n", redact.ID(scratch))
	} else {
		out.Printf("scratch calendar %s adopted and emptied; no calendar quota spent\n", redact.ID(scratch))
	}
	// Only a calendar this run created is deleted. One left by an earlier
	// `-keep` is somebody's deliberate choice, and removing it would
	// spend the quota again on the next run — which is the cost this
	// adoption exists to avoid (§18 row 36).
	if created && !keep {
		defer func() {
			if err := api.deleteCalendar(context.Background(), scratch); err != nil {
				out.Printf("WARNING: could not delete the scratch calendar %s: %v\n",
					redact.ID(scratch), redact.String(err.Error()))
				out.Printf("delete it by hand; this driver must leave nothing behind\n")
				return
			}
			out.Printf("\nscratch calendar deleted\n")
		}()
	}
	if !created || keep {
		defer func() {
			out.Printf("\nscratch calendar %s kept; the next run adopts and empties it, "+
				"spending no calendar quota.\nIt stays until you delete it by hand: a run only "+
				"removes a calendar it created itself.\n", redact.ID(scratch))
		}()
	}

	if err := api.seed(ctx, scratch); err != nil {
		out.Printf("could not fill the scratch calendar: %v\n", err)
		return 2
	}
	// One occurrence of the weekly series is removed, because a
	// cancelled instance is how a single date leaves a series and
	// list_instances exists to show which dates are gone.
	state := seedState{}
	cancelled, err := api.removeOneOccurrence(ctx, scratch)
	if err != nil {
		out.Printf("could not cancel one occurrence: %v\n", redact.String(err.Error()))
		return 2
	}
	state.cancelledOccurrence = cancelled
	out.Printf("filled with %d invented events; the occurrence on %s was cancelled\n\n",
		len(seedEvents()), cancelled)

	sess, err := startServer(ctx, bin, profile)
	if err != nil {
		out.Printf("could not start the server: %v\n", err)
		return 2
	}
	defer sess.close()

	// Spikes A and B are the only things here that can mail a person, and
	// they only do it when addresses are configured. Saying so before
	// they run means nobody discovers it in a colleague's inbox.
	if g := guestsFromEnv(); g.any() {
		out.Printf("guests are configured, so spikes A and B WILL send real invitations: %s\n",
			g.describe())
		out.Printf("run with -keep if you want the events left in place to inspect\n\n")
	}

	r := &results{out: out, invented: map[string]bool{
		scratch:            true,
		noSuchCalendar:     true,
		unknownCalendarRef: true,
	}}
	for _, st := range steps(scratch, state) {
		r.run(ctx, sess, st)
	}

	// The spikes that do not go through the tool surface. Each asks
	// something about the API itself — what a grant allows, what Google
	// does with a recurrence carrying no zone, where the free/busy
	// ceiling really is — which no tool call can answer.
	for _, sp := range []struct {
		name string
		run  func(context.Context, *redact.Printer, *liveAPI, string) (verdict, string)
	}{
		{"spike A: notification truth", spikeA},
		{"spike B: none on insert", spikeB},
		{"spike G: acl scopes", spikeG},
		{"spike C: unzoned series", spikeC},
		{"spike E: this and following", spikeE},
		{"spike F: duplicate insert", spikeF},
		{"spike I: 50 vs 51 calendars", spikeI},
	} {
		r.total++
		v, note := sp.run(ctx, out, api, scratch)
		switch v {
		case pass:
			out.Printf("ok    %-28s %s\n", sp.name, note)
		case undetermined:
			r.undetermined++
			out.Printf("?     %-28s %s\n", sp.name, note)
		default:
			r.failed++
			out.Printf("FAIL  %-28s %s\n", sp.name, note)
		}
	}

	out.Printf("\n%d steps, %d failed, %d undetermined\n", r.total, r.failed, r.undetermined)
	if r.failed > 0 {
		out.Printf("\nRead the transcript above rather than this count. A sibling's driver twice\n")
		out.Printf("reported success while its results were wrong.\n")
		return 1
	}
	out.Printf("\nGreen is not done: read every line above before believing it.\n")
	return 0
}

// showFilter names the steps whose full body should be printed.
var showFilter string

// spikeCeiling arms the half of spike I that creates real calendars.
var spikeCeiling bool

// results tallies and prints, through the redactor only.
type results struct {
	out *redact.Printer
	// invented is every calendar id this driver made up, the scratch
	// calendar it created included. It is what §9.1's promise means in
	// practice, and what decides whether a body may be printed.
	invented                    map[string]bool
	total, failed, undetermined int
}

// show prints a step's whole result when asked, so the transcript can
// be read rather than counted.
func (r *results) show(st step, res callResult) {
	if showFilter == "" || !strings.Contains(st.name, showFilter) {
		return
	}
	if r.withheld(st) {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(res.text, "\n"), "\n") {
		r.out.Printf("      | %s\n", line)
	}
}

// withheld reports whether §9.1 forbids printing this step's body, and
// prints the notice when it does.
//
// The first live run printed a dozen of the account's real calendar
// titles, because `list_calendars` is account-wide and the redactor is
// anchored on shapes — an address, an id, a URL — while a display name
// has none. No pattern could have caught them.
//
// So the rule is derived from the step's own arguments rather than set
// by hand on each step. A flag would be a matter of care, and §9.1's two
// protections are required to be structural: the first draft of this
// carried a flag, and missed `get_settings` on the same day it was
// written. A step names the calendars it reads, and if every one of them
// is a calendar this driver invented then the answer can only hold
// content this driver invented. A step that names none is account-wide
// by construction. It fails closed, so an argument shape this does not
// understand is withheld rather than printed.
func (r *results) withheld(st step) bool {
	if st.readsOnlyInvented(r.invented) {
		return false
	}
	r.out.Printf("      (body withheld: this step reads past the calendar the driver created, §9.1)\n")
	return true
}

// readsOnlyInvented is the allow-list §9.1 asks for, anchored on ids
// this driver generated rather than on a shape a real id cannot take.
func (st step) readsOnlyInvented(invented map[string]bool) bool {
	named := 0
	for _, key := range []string{"calendar", "calendars"} {
		switch v := st.args[key].(type) {
		case nil:
		case string:
			named++
			if !invented[v] {
				return false
			}
		case []string:
			for _, id := range v {
				named++
				if !invented[id] {
					return false
				}
			}
		default:
			return false
		}
	}
	return named > 0
}

func (r *results) run(ctx context.Context, s *session, st step) {
	r.total++
	res, err := s.call(ctx, st.tool, st.args)
	if err != nil {
		r.failed++
		r.out.Printf("FAIL  %-28s transport: %v\n", st.name, err)
		return
	}
	verdict, note := st.check(res)
	switch verdict {
	case pass:
		r.out.Printf("ok    %-28s %s\n", st.name, note)
		r.show(st, res)
	case undetermined:
		r.undetermined++
		r.out.Printf("?     %-28s %s\n", st.name, note)
	default:
		r.failed++
		r.out.Printf("FAIL  %-28s %s\n", st.name, note)
		// The body, redacted, so a failure can be diagnosed without a
		// second run — unless the step reads past the scratch calendar.
		if !r.withheld(st) {
			r.out.Printf("      %s\n", truncate(res.text, 400))
		}
	}
}

type verdict int

const (
	pass verdict = iota
	fail
	undetermined
)

type step struct {
	name  string
	tool  string
	args  map[string]any
	check func(callResult) (verdict, string)
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------------------------------------------------------------- steps

const (
	allDayDate  = "2026-03-20"
	timedStart  = "2026-03-16T09:00:00+01:00"
	scratchZone = "Europe/Copenhagen"
)

// liveUncovered names tools that have no step here, and why.
//
// `gates live-cover` reads it: a tool with neither a step nor an entry
// fails the gate, and an entry with no reason fails it too. The map can
// only shrink — a tool listed here and driven anyway also fails, so an
// exemption cannot outlive the reason for it.
var liveUncovered = map[string]string{}

func steps(scratch string, state seedState) []step {
	window := map[string]any{"from": "2026-03-15", "to": "2026-03-31"}
	cal := func(extra map[string]any) map[string]any {
		out := map[string]any{"calendars": []string{scratch}}
		for k, v := range window {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	return []step{
		{
			name: "list_calendars",
			tool: "list_calendars",
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, scratchTitle) {
					return fail, "the scratch calendar is missing from the list"
				}
				if !strings.Contains(r.text, scratchZone) {
					return fail, "the scratch calendar is listed without its time zone"
				}
				return pass, "the scratch calendar is listed with its zone, " + scratchZone
			},
		},
		{
			name: "get_calendar",
			tool: "get_calendar",
			args: map[string]any{"calendar": scratch},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, scratchZone) {
					return fail, "the calendar card does not report its time zone"
				}
				return pass, "card and sharing read"
			},
		},
		{
			name: "get_settings",
			tool: "get_settings",
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(strings.ToLower(r.text), "time zone") {
					return fail, "no time zone reported"
				}
				return pass, "account time zone read"
			},
		},
		{
			name: "list_events expanded",
			tool: "list_events",
			args: cal(nil),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, "expanded") {
					return fail, "the result does not say it expanded recurrences"
				}
				if !strings.Contains(r.text, weeklyTitle) {
					return fail, "the recurring event produced no occurrence"
				}
				return pass, "occurrences returned"
			},
		},
		{
			name: "list_events as series",
			tool: "list_events",
			args: cal(map[string]any{"no_expand": true}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, "series") {
					return fail, "the result does not say it returned series"
				}
				// showDeleted=false does not filter a cancelled INSTANCE
				// when singleEvents is false — the discovery document
				// says so and the first live run proved it. Google sends
				// it with no start and no summary, so it rendered as a
				// row with neither, and was counted.
				if strings.Contains(r.text, "(no title)") || strings.Contains(r.text, "(no start)") {
					return fail, "a cancelled instance leaked into the series view as a contentless row"
				}
				if strings.Contains(r.text, state.cancelledOccurrence) {
					return fail, "the cancelled occurrence on " + state.cancelledOccurrence +
						" appeared without show_cancelled"
				}
				return pass, "series with its rule returned, no cancelled instance among them"
			},
		},
		{
			// SPIKE D, as a step. This is the defect every surveyed
			// server has: an all-day event read from a negative-offset
			// zone renders a day early.
			name: "spike D: all-day west of UTC",
			tool: "list_events",
			args: cal(map[string]any{"time_zone": "Pacific/Honolulu"}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, allDayDate) {
					return fail, fmt.Sprintf("the all-day event is not on %s when read from Pacific/Honolulu", allDayDate)
				}
				if strings.Contains(r.text, "2026-03-19") {
					return fail, "the all-day event moved to the previous day"
				}
				if !strings.Contains(r.text, "all day") {
					return fail, "the all-day event did not render as all-day"
				}
				return pass, "stayed on " + allDayDate + " from a UTC-10 zone"
			},
		},
		{
			name: "all-day from a UTC+13 zone",
			tool: "list_events",
			args: cal(map[string]any{"time_zone": "Pacific/Auckland"}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, allDayDate) {
					return fail, "the all-day event moved when read from Pacific/Auckland"
				}
				return pass, "stayed on " + allDayDate + " from a UTC+13 zone"
			},
		},
		{
			// The §3 defect, live. The seeded series runs weekly from
			// 17 March, so its third and fourth occurrences fall AFTER
			// the 29 March European transition. A series written with a
			// zone holds its wall clock across that boundary; one
			// written as a bare UTC instant drifts an hour, and that
			// drift is what every surveyed server ships.
			name: "DST: wall clock holds",
			tool: "list_events",
			args: cal(map[string]any{"time_zone": scratchZone}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				before := strings.Count(r.text, "14:00-15:00")
				if before < 2 {
					return undetermined, "fewer than two occurrences of the series came back; nothing to compare"
				}
				// A drifted series renders 13:00 or 15:00 after the
				// transition. Neither may appear.
				for _, drifted := range []string{"13:00-14:00", "15:00-16:00"} {
					if strings.Contains(r.text, drifted) {
						return fail, "the series drifted to " + drifted + " across the 29 March transition"
					}
				}
				return pass, fmt.Sprintf("%d occurrences all at 14:00 local, across the 29 March transition", before)
			},
		},
		{
			name: "window echoes its zone",
			tool: "list_events",
			args: cal(map[string]any{"time_zone": "America/Chicago"}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, "America/Chicago") {
					return fail, "the result does not name the zone it used"
				}
				return pass, "absolute window and zone stated"
			},
		},
		{
			name: "search_events",
			tool: "search_events",
			args: cal(map[string]any{"query": searchTerm}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, timedTitle) {
					// Google's content index is eventually consistent, so
					// a fresh event may genuinely not be searchable yet.
					// That is not the same as a broken search, and the
					// difference is worth recording rather than guessing.
					return undetermined, "Google's index has not caught up with an event created seconds ago"
				}
				return pass, "found the event by text"
			},
		},
		{
			name: "cancelled hidden by default",
			tool: "list_events",
			args: cal(nil),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if strings.Contains(r.text, cancelledTitle) {
					return fail, "a cancelled event appeared without show_cancelled"
				}
				return pass, "cancelled event hidden"
			},
		},
		{
			name: "show_cancelled reveals it",
			tool: "list_events",
			args: cal(map[string]any{"show_cancelled": true}),
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, cancelledTitle) {
					return fail, "show_cancelled did not reveal the cancelled event"
				}
				return pass, "cancelled event shown"
			},
		},
		{
			name: "get_event all-day",
			tool: "get_event",
			args: map[string]any{"calendar": scratch, "event_id": allDayID, "time_zone": "Pacific/Honolulu"},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if strings.Contains(r.text, "00:00") {
					return fail, "an all-day event rendered a time"
				}
				if !strings.Contains(r.text, allDayDate) {
					return fail, "the date moved"
				}
				return pass, "no time of day, correct date"
			},
		},
		{
			name: "get_event timed",
			tool: "get_event",
			args: map[string]any{"calendar": scratch, "event_id": timedID},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error"
				}
				if !strings.Contains(r.text, "09:00") {
					return fail, "the timed event does not render its start in the calendar's zone"
				}
				return pass, "rendered 09:00 in " + scratchZone
			},
		},
		{
			name: "list_instances whole series",
			tool: "list_instances",
			args: map[string]any{"calendar": scratch, "event_id": weeklyID},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "occurrence") {
					return fail, "the result does not report occurrences"
				}
				// The cancelled one must NOT be here, and the result has
				// to say it is hiding it.
				if strings.Contains(r.text, state.cancelledOccurrence) {
					return fail, "a cancelled occurrence appeared without show_cancelled"
				}
				if !strings.Contains(r.text, "Cancelled occurrences are hidden") {
					return fail, "the result hides cancelled occurrences without saying so"
				}
				return pass, "occurrences listed, cancelled one hidden and declared"
			},
		},
		{
			name: "list_instances show_cancelled",
			tool: "list_instances",
			args: map[string]any{"calendar": scratch, "event_id": weeklyID, "show_cancelled": true},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, state.cancelledOccurrence) {
					return fail, "the cancelled occurrence on " + state.cancelledOccurrence + " is still missing"
				}
				if !strings.Contains(r.text, "CANCELLED") {
					return fail, "the cancelled occurrence is not marked as one"
				}
				return pass, "the removed date is shown and marked"
			},
		},
		{
			name: "list_instances window",
			tool: "list_instances",
			args: map[string]any{
				"calendar": scratch, "event_id": weeklyID,
				"from": "2026-03-17", "to": "2026-03-18",
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "2026-03-17") {
					return fail, "the first occurrence is missing from its own window"
				}
				if strings.Contains(r.text, "2026-03-31") {
					return fail, "an occurrence outside the window came back"
				}
				return pass, "one occurrence, inside the window"
			},
		},
		{
			// DST through list_instances: every occurrence of the zoned
			// series must read 14:00, including the ones after 29 March.
			name: "instances hold their clock",
			tool: "list_instances",
			args: map[string]any{"calendar": scratch, "event_id": weeklyID, "time_zone": scratchZone},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				for _, drifted := range []string{"13:00-14:00", "15:00-16:00"} {
					if strings.Contains(r.text, drifted) {
						return fail, "an occurrence drifted to " + drifted
					}
				}
				if n := strings.Count(r.text, "14:00-15:00"); n < 2 {
					return undetermined, "fewer than two occurrences came back; nothing to compare"
				}
				return pass, "every occurrence at 14:00 local, across the transition"
			},
		},
		{
			// The id names the CANCELLED occurrence deliberately. Google
			// does not refuse an occurrence id — it expands whatever
			// that occurrence is, and a cancelled one expands to
			// nothing, so the call succeeds with an empty list. The
			// first live run got "No occurrences" for a series with
			// three, which is why the server now reads the id's shape
			// instead of waiting to be told.
			name: "list_instances rejects an occurrence id",
			tool: "list_instances",
			args: map[string]any{"calendar": scratch, "event_id": weeklyID + "_20260324t130000z"},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "an occurrence id was accepted as a series id"
				}
				// The class is Google's choice and is what this step
				// records; what this server owes is the explanation.
				if !strings.Contains(r.text, "series_id") {
					return fail, "the refusal does not explain the mistake: " + truncate(r.text, 200)
				}
				return pass, "refused, and said which id to use: " + firstLine(truncate(r.text, 120))
			},
		},
		{
			name: "check_availability",
			tool: "check_availability",
			args: map[string]any{
				"calendars": []string{scratch},
				"from":      "2026-03-16", "to": "2026-03-16",
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "busy") && !strings.Contains(r.text, "free") {
					return fail, "the result says neither busy nor free"
				}
				// The seeded timed probe is 09:00-10:00, so the day is
				// not free all through.
				if strings.Contains(r.text, "free for the whole window") {
					return fail, "a calendar with an event on it came back free for the whole window"
				}
				return pass, "busy blocks and free gaps reported"
			},
		},
		{
			name: "free gaps filtered",
			tool: "check_availability",
			args: map[string]any{
				"calendars": []string{scratch},
				"from":      "2026-03-16", "to": "2026-03-16", "min_minutes": 30,
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "30m or longer") {
					return fail, "the result does not echo the filter it applied"
				}
				return pass, "gaps of 30 minutes or more"
			},
		},
		{
			// SPIKE H. A calendar this account cannot read must come back
			// UNKNOWN, never folded into free (§4.6). The id is invented
			// and belongs to nobody.
			name: "spike H: unreadable calendar",
			tool: "check_availability",
			args: map[string]any{
				"calendars": []string{scratch, noSuchCalendar},
				"from":      "2026-03-16", "to": "2026-03-16",
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "the whole query failed instead of reporting one calendar as unknown: " +
						truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "UNKNOWN") {
					return fail, "an unreadable calendar was not reported unknown"
				}
				if !strings.Contains(r.text, "Do not treat this as free") {
					return fail, "the result does not warn against reading unknown as free"
				}
				if !strings.Contains(r.text, "could not be read") {
					return fail, "the free gaps do not say a calendar was missing from them"
				}
				return pass, "CONFIRMS §4.6: per-calendar error, reported unknown, gaps qualified"
			},
		},
		{
			name: "unknown calendar refused",
			tool: "get_calendar",
			args: map[string]any{"calendar": unknownCalendarRef},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "an unknown calendar resolved to something"
				}
				if !strings.Contains(r.text, "[not_found]") {
					return fail, "the refusal is not classified not_found"
				}
				return pass, "refused with [not_found]"
			},
		},
		{
			name: "missing window refused",
			tool: "list_events",
			args: map[string]any{"calendars": []string{scratch}, "from": "2026-03-15"},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "a read without a full window succeeded"
				}
				return pass, "refused"
			},
		},
		{
			name: "relative date refused",
			tool: "list_events",
			args: map[string]any{"calendars": []string{scratch}, "from": "next tuesday", "to": "2026-03-31"},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "a relative expression was accepted; this server does not guess"
				}
				if !strings.Contains(r.text, "[invalid]") {
					return fail, "the refusal is not classified invalid"
				}
				return pass, "refused with [invalid]"
			},
		},
		{
			name: "invented zone refused",
			tool: "list_events",
			args: cal(map[string]any{"time_zone": "Mars/Olympus_Mons"}),
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "an invented zone was accepted"
				}
				// [invalid], not [unavailable]: a zone that does not
				// exist will not start existing, and [unavailable] is
				// retryable, so the caller was told to retry a request
				// that can never succeed.
				if !strings.Contains(r.text, "[invalid]") {
					return fail, "an impossible zone is not classified invalid: " + truncate(r.text, 160)
				}
				return pass, "refused with [invalid]"
			},
		},
	}
}
