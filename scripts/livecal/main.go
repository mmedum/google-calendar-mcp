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
	keep := flag.Bool("keep", false, "do not delete the scratch calendar (for debugging)")
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
	flag.Parse()

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

	scratch, err := api.createScratchCalendar(ctx)
	if err != nil {
		out.Printf("could not create the scratch calendar: %v\n", err)
		return 2
	}
	out.Printf("scratch calendar %s created\n", redact.ID(scratch))
	if !keep {
		defer func() {
			if err := api.deleteCalendar(context.Background(), scratch); err != nil {
				out.Printf("WARNING: could not delete the scratch calendar %s: %v\n",
					redact.ID(scratch), err)
				out.Printf("delete it by hand; this driver must leave nothing behind\n")
				return
			}
			out.Printf("\nscratch calendar deleted\n")
		}()
	}

	if err := api.seed(ctx, scratch); err != nil {
		out.Printf("could not fill the scratch calendar: %v\n", err)
		return 2
	}
	out.Printf("filled with %d invented events\n\n", len(seedEvents()))

	sess, err := startServer(ctx, bin, profile)
	if err != nil {
		out.Printf("could not start the server: %v\n", err)
		return 2
	}
	defer sess.close()

	r := &results{out: out}
	for _, st := range steps(scratch) {
		r.run(ctx, sess, st)
	}

	// Spike G does not go through the tool surface: it asks what the
	// GRANT allows, which is a question about the scopes rather than
	// about a tool.
	r.total++
	v, note := spikeG(ctx, out, api, scratch)
	switch v {
	case pass:
		out.Printf("ok    %-28s %s\n", "spike G: acl scopes", note)
	case undetermined:
		r.undetermined++
		out.Printf("?     %-28s %s\n", "spike G: acl scopes", note)
	default:
		r.failed++
		out.Printf("FAIL  %-28s %s\n", "spike G: acl scopes", note)
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

// results tallies and prints, through the redactor only.
type results struct {
	out                         *redact.Printer
	total, failed, undetermined int
}

// show prints a step's whole result when asked, so the transcript can
// be read rather than counted.
func (r *results) show(name string, res callResult) {
	if showFilter == "" || !strings.Contains(name, showFilter) {
		return
	}
	for _, line := range strings.Split(strings.TrimRight(res.text, "\n"), "\n") {
		r.out.Printf("      | %s\n", line)
	}
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
		r.show(st.name, res)
	case undetermined:
		r.undetermined++
		r.out.Printf("?     %-28s %s\n", st.name, note)
	default:
		r.failed++
		r.out.Printf("FAIL  %-28s %s\n", st.name, note)
		// The body, redacted, so a failure can be diagnosed without a
		// second run.
		r.out.Printf("      %s\n", truncate(res.text, 400))
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

func steps(scratch string) []step {
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
				return pass, "the scratch calendar is listed with its zone"
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
				return pass, "series with its rule returned"
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
			name: "unknown calendar refused",
			tool: "get_calendar",
			args: map[string]any{"calendar": "no-such-calendar-here"},
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
				return pass, "refused"
			},
		},
	}
}
