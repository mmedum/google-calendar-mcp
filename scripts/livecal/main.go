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
	"regexp"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/internal/redact"
	"github.com/mmedum/google-calendar-mcp/internal/when"
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
	// Spikes A and B mail real people. Arming them per run rather than
	// per environment means a phase that runs this driver dozens of
	// times does not send dozens of invitations to a colleague, and
	// setting the guest variables in a shell profile cannot do it by
	// accident.
	notify := flag.Bool("spike-notify", false,
		"spikes A and B: send REAL invitations to the configured guests")
	// Spike A and B events are kept for a person to read. Removing them
	// cancels them properly, which mails the guests — so it is asked for.
	sweep := flag.Bool("sweep-spikes", false,
		"delete the kept spike A and B events, cancelling them to their guests")
	flag.Parse()

	clearSpikeEvents = *sweep

	spikeCeiling = *ceiling
	spikeNotify = *notify

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

	scratch, created, err := api.ensureScratchCalendar(ctx, scratchTitle)
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

	// The write path needs two things the read path did not: somewhere
	// to move an event to, and an invitation this account can answer.
	dest, destCreated, destNote := destinationCalendar(ctx, api)
	out.Printf("%s\n", destNote)
	if destCreated && !keep {
		defer func() {
			if err := api.deleteCalendar(context.Background(), dest); err != nil {
				out.Printf("WARNING: could not delete the destination calendar %s: %v\n",
					redact.ID(dest), redact.String(err.Error()))
				out.Printf("delete it by hand; this driver must leave nothing behind\n")
			}
		}()
	}

	if err := api.seed(ctx, scratch); err != nil {
		out.Printf("could not fill the scratch calendar: %v\n", err)
		return 2
	}
	self, err := api.primaryAddress(ctx)
	if err != nil {
		out.Printf("could not read this account's own address: %v\n", redact.String(err.Error()))
		return 2
	}
	if err := api.seedRSVP(ctx, scratch, self); err != nil {
		out.Printf("could not seed the invitation to answer: %v\n", redact.String(err.Error()))
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

	// Spikes A and B are the only things here that can reach another
	// person. Saying so before they run means nobody discovers it in a
	// colleague's inbox.
	if spikeNotify {
		if g := guestsFromEnv(); g.any() {
			out.Printf("-spike-notify is set: spikes A and B WILL send real invitations to %s\n",
				g.describe())
			out.Printf("run with -keep if you want the events left in place to inspect\n\n")
		}
	}

	r := &results{out: out, invented: map[string]bool{
		scratch:            true,
		noSuchCalendar:     true,
		unknownCalendarRef: true,
	}}
	// The destination calendar is this driver's too, so a step naming it
	// may have its body printed. An empty id is not added: §9.1's
	// allow-list must never gain a blank key that matches an absent
	// argument.
	if dest != "" {
		r.invented[dest] = true
	}

	spikeDest = dest
	writes := &writeState{dest: dest, self: self}
	// The only address in this run that belongs to another person. It is
	// read from the environment and never written anywhere: not to a
	// file, not to the transcript (the redactor masks it by shape), and
	// above all not into this repository (§9.1).
	if spikeNotify {
		writes.guest = guestsFromEnv().internal
	}
	for _, st := range steps(scratch, state) {
		r.run(ctx, sess, st)
	}
	// The writes run after the reads, so a read step never sees a
	// calendar half-way through being rewritten — and so the read steps'
	// expectations stay about what the seed put there.
	for _, st := range writeSteps(scratch, writes) {
		r.run(ctx, sess, st)
	}

	// Phase 3, on a calendar of its own that create_calendar makes and
	// delete_calendar removes. It is registered as the driver's own as
	// soon as it exists, so §9.1 lets its bodies be printed; a run that
	// fails before the delete step leaves it behind, so the deferred
	// sweep below removes it.
	cals := &calendarState{remember: func(id string) { r.invented[id] = true }}
	defer func() {
		if cals.probe == "" {
			return
		}
		// Best effort, and silent when it is already gone: the delete
		// step removes it in an ordinary run and this is the net for the
		// runs that stop early. A calendar left behind costs the next
		// run a creation from a quota that is not refunded (§18 row 36).
		_ = api.deleteCalendar(context.Background(), cals.probe)
	}()
	for _, st := range calendarSteps(cals) {
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
		{"spike J: move under If-Match", spikeJ},
		{"spike K: clear on a secondary", spikeK},
		{"spike L: calendar and acl If-Match", spikeL},
		{"spike M: conference creation", spikeM},
		{"spike N: what suppresses nextSyncToken", spikeN},
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
	if writes.guest != "" {
		// Said at the END, because this is the one thing in the run that
		// somebody else has to act on, and a driver whose discipline is
		// "read the transcript" should not bury it forty lines up.
		out.Printf("\nThis run mailed a real person, and left one meeting behind on purpose.\n")
		out.Printf("  \u2022 %q was cancelled with notify:none. It is gone from this account and\n", quietTitle)
		out.Printf("    STILL ON THEIRS, and this account can no longer withdraw it (\u00a718 row 43).\n")
		out.Printf("    Ask them to delete it; nothing here can.\n")
		out.Printf("  \u2022 Spikes A and B set up events and cannot score themselves: who received\n")
		out.Printf("    what is visible in an inbox and nowhere in the API. Read the inbox AND\n")
		out.Printf("    the calendar, then write the verdict into \u00a718 by hand.\n")
	}
	return 0
}

// showFilter names the steps whose full body should be printed.
var showFilter string

// spikeCeiling arms the half of spike I that creates real calendars.
var spikeCeiling bool

// spikeNotify arms spikes A and B, which are the only things here that
// can reach another person.
var spikeNotify bool

// clearSpikeEvents lets the cleanup remove the spike events it normally
// preserves, cancelling them to their guests on the way out.
var clearSpikeEvents bool

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
func (r *results) show(st step, args map[string]any, res callResult) {
	if showFilter == "" || !strings.Contains(st.name, showFilter) {
		return
	}
	if r.withheld(args) {
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
func (r *results) withheld(args map[string]any) bool {
	if readsOnlyInvented(args, r.invented) {
		return false
	}
	r.out.Printf("      (body withheld: this step reads past the calendar the driver created, §9.1)\n")
	return true
}

// readsOnlyInvented is the allow-list §9.1 asks for, anchored on ids
// this driver generated rather than on a shape a real id cannot take.
// The keys are every argument that names a calendar. A tool that takes
// one this list does not know about reads as "names none", which is
// account-wide by construction and withheld — which is the direction
// this has to fail in.
func readsOnlyInvented(args map[string]any, invented map[string]bool) bool {
	named := 0
	for _, key := range []string{"calendar", "calendars", "to_calendar"} {
		switch v := args[key].(type) {
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
	if st.skip != nil {
		if why := st.skip(); why != "" {
			r.undetermined++
			r.out.Printf("?     %-28s %s\n", st.name, why)
			return
		}
	}
	args := st.reads()
	res, err := s.callFor(ctx, st, st.arguments())
	if err != nil {
		r.failed++
		r.out.Printf("FAIL  %-28s transport: %v\n", st.name, err)
		return
	}
	verdict, note := st.check(res)
	switch verdict {
	case pass:
		r.out.Printf("ok    %-28s %s\n", st.name, note)
		r.show(st, args, res)
	case undetermined:
		r.undetermined++
		r.out.Printf("?     %-28s %s\n", st.name, note)
	default:
		r.failed++
		r.out.Printf("FAIL  %-28s %s\n", st.name, note)
		// The body, redacted, so a failure can be diagnosed without a
		// second run — unless the step reads past the scratch calendar.
		if !r.withheld(args) {
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
	name string
	tool string
	// resource names the published resource this step reads instead of
	// calling a tool: the URI or template exactly as the surface
	// publishes it, because that is the key `live-cover` matches. uriFn
	// builds the concrete URI, which usually names something an earlier
	// step created.
	resource string
	uriFn    func() string
	args     map[string]any
	// argsFn defers the arguments to run time, and wins over args when
	// set. A write step's target is usually something an earlier step
	// created, so its id does not exist when the list is built.
	argsFn func() map[string]any
	// skip returns a reason this step cannot run, or an empty string.
	// Checked BEFORE the call, and that is the point rather than a
	// nicety: a step whose target an earlier step failed to create would
	// otherwise be called with no calendar named, and a calendar
	// reference this server cannot resolve is the PRIMARY one. A driver
	// that may not read past what it wrote (§9.1) must not be able to
	// write past it either, and "the tool refuses without confirm" is
	// care rather than structure — it is also the last guard in the
	// chain rather than the first.
	skip  func() string
	check func(callResult) (verdict, string)
}

// reads is what the §9.1 print decision is made from: the calendars
// this step actually touches.
//
// For a tool call that is its arguments. For a resource read it is
// derived from the URI the step READS, not from anything the step
// declares — a declaration is a flag in an argument map's clothes, and
// a uriFn edited to point somewhere else while the declaration still
// said "scratch" would print a real calendar's body. The rule §9.1
// requires is that the two cannot disagree, so there is only one of
// them.
func (st step) reads() map[string]any {
	if st.resource == "" {
		return st.arguments()
	}
	calendar, _ := splitResourceURI(st.uriFn())
	if calendar == "" {
		// Account-wide by construction: the calendar list resource names
		// no calendar, so its body is withheld.
		return nil
	}
	return map[string]any{"calendar": calendar}
}

// splitResourceURI takes the calendar and event ids out of a gcal://
// URI, as internal/tools does for the server side.
func splitResourceURI(uri string) (calendar, event string) {
	rest, ok := strings.CutPrefix(uri, "gcal://calendars/")
	if !ok {
		return "", ""
	}
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 1:
		return parts[0], ""
	case len(parts) == 3 && parts[1] == "events":
		return parts[0], parts[2]
	default:
		return "", ""
	}
}

// callFor runs the step: a tool call, or a resource read.
func (s *session) callFor(ctx context.Context, st step, args map[string]any) (callResult, error) {
	if st.resource != "" {
		return s.readResource(ctx, st.uriFn())
	}
	return s.call(ctx, st.tool, args)
}

// arguments resolves the step's arguments, late if it has to.
func (st step) arguments() map[string]any {
	if st.argsFn != nil {
		return st.argsFn()
	}
	return st.args
}

// gapRow matches one free gap as the renderer prints it, in both of its
// forms: "2026-03-17 12:00-17:00 (5h)" and the cross-midnight
// "2026-03-17 12:00 to 2026-03-18 09:00 (21h)".
//
// Both, because the second is what a mask that did NOT apply produces —
// an overnight gap — so a pattern matching only the first would skip the
// one row it exists to catch and pass on the heading alone.
var gapRow = regexp.MustCompile(
	`^(\d{4}-\d{2}-\d{2}) (\d{2}:\d{2})(?:-(\d{2}:\d{2})| to (\d{4}-\d{2}-\d{2}) (\d{2}:\d{2})) \(`)

// gapsOutside returns the free gaps that fall outside a working-hours
// mask of from..to on weekdays — which is the assertion §17.2's step
// needs, and it is about the ROWS rather than about the sentence over
// them. A mask that said it applied and did not would otherwise pass on
// its own heading.
func gapsOutside(text, from, to string) []string {
	var bad []string
	for _, line := range strings.Split(text, "\n") {
		m := gapRow.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		day, err := when.ParseDate(m[1])
		if err != nil {
			continue
		}
		// A gap that ends on another DAY is outside any working-hours
		// mask by construction, whatever its clock times say.
		if m[4] != "" {
			bad = append(bad, strings.TrimSpace(line))
			continue
		}
		weekend := day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
		if weekend || m[2] < from || m[3] > to {
			bad = append(bad, strings.TrimSpace(line))
		}
	}
	return bad
}

// linesWith returns the rendered rows that mention needle.
//
// Every assertion about one event's time or date goes through this, and
// the live run is why: two of them grepped the whole result for a date
// or a clock time, so an unrelated probe seeded on 19 March at 13:00
// made "the all-day event moved to the previous day" and "the series
// drifted to 13:00-14:00" both fire. Neither had. An assertion that can
// be satisfied by a line it is not about is the failure §15 opens by
// naming, pointed the other way.
func linesWith(text, needle string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

// clockTime is HH:MM, which is what tells an occurrence row from a
// heading that merely names the same event.
var clockTime = regexp.MustCompile(`[0-9]{2}:[0-9]{2}`)

// timedRows returns the rows for one event that actually carry a clock.
//
// list_instances prints the series title on a header line of its own and
// then again on every occurrence, so a filter on the title alone picks
// up a line with no time in it — and an assertion about drift fires on
// the heading. That is the same mistake as the one linesWith fixed, one
// level in.
func timedRows(text, needle string) []string {
	var out []string
	for _, line := range linesWith(text, needle) {
		if clockTime.MatchString(line) {
			out = append(out, line)
		}
	}
	return out
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
	// The sync token the baseline step issues, read by the step after
	// it. seedState arrives by value, so it cannot carry something one
	// step learns and the next one needs.
	var syncToken string

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
			// The three resources of §8, read the way a client that
			// attaches rather than calls would. This one is account-wide
			// by construction, so its body is withheld and the check
			// says what it verified instead (§9.1).
			name:     "resource: calendar list",
			resource: "gcal://calendars",
			uriFn:    func() string { return "gcal://calendars" },
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, scratchTitle) {
					return fail, "the scratch calendar is missing from the resource"
				}
				return pass, "the calendar list read as a resource"
			},
		},
		{
			// A secondary calendar id is an address, so this URI carries
			// an at sign written as itself — the form a model will
			// write, and the one a template without reserved expansion
			// silently fails to match.
			name:     "resource: one calendar",
			resource: "gcal://calendars/{+calendar_id}",
			uriFn:    func() string { return "gcal://calendars/" + scratch },
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, scratchTitle) {
					return fail, "the resource is not the scratch calendar"
				}
				return pass, "the calendar card read as a resource"
			},
		},
		{
			name:     "resource: one event",
			resource: "gcal://calendars/{+calendar_id}/events/{+event_id}",
			uriFn:    func() string { return "gcal://calendars/" + scratch + "/events/" + timedID },
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "id: "+timedID) {
					return fail, "the resource is not the event that was asked for"
				}
				if !strings.Contains(r.text, timedTitle) {
					return fail, "the event resource does not carry the event"
				}
				return pass, "one event read as a resource"
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
				rows := linesWith(r.text, allDayTitle)
				if len(rows) == 0 {
					return fail, "the all-day event is missing from a window that contains it"
				}
				for _, row := range rows {
					if !strings.Contains(row, allDayDate) {
						return fail, "the all-day event moved when read from Pacific/Honolulu: " + row
					}
					if !strings.Contains(row, "all day") {
						return fail, "the all-day event did not render as all-day: " + row
					}
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
				rows := linesWith(r.text, allDayTitle)
				if len(rows) == 0 {
					return fail, "the all-day event is missing from a window that contains it"
				}
				for _, row := range rows {
					if !strings.Contains(row, allDayDate) {
						return fail, "the all-day event moved when read from Pacific/Auckland: " + row
					}
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
				rows := timedRows(r.text, weeklyTitle)
				if len(rows) < 2 {
					return undetermined, "fewer than two occurrences of the series came back; nothing to compare"
				}
				// A drifted series renders 13:00 or 15:00 after the
				// transition. Asserted on the series' OWN rows, so no
				// other event on the page can satisfy or break it.
				for _, row := range rows {
					if !strings.Contains(row, "14:00-15:00") {
						return fail, "an occurrence drifted across the 29 March transition: " + row
					}
				}
				return pass, fmt.Sprintf("%d occurrences all at 14:00 local, across the 29 March transition", len(rows))
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
			// §17.1. Two calls, because the claim worth driving live is
			// not "it lists" but "the token round-trips": the baseline
			// hands one back, and passing it returns a quiet answer
			// rather than the whole calendar again.
			name: "list_changes baseline and round-trip",
			tool: "list_changes",
			args: map[string]any{"calendar": scratch},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "Baseline for") {
					return fail, "a call with no token did not report itself as a baseline"
				}
				token := afterLabel(r.text, "Next sync token: ")
				if token == "" {
					// The first live run died here, and the cause was
					// not the tool: the scratch calendar carries ~500
					// tombstones, the token arrives on the last page
					// only, and the event budget stopped the read
					// before it. A baseline pages past the budget now.
					return fail, "the baseline handed back no sync token, so nothing can follow it — " +
						"is it stopping before the last page again?"
				}
				syncToken = token
				return pass, "baseline read, sync token issued"
			},
		},
		{
			name: "list_changes with the token",
			tool: "list_changes",
			argsFn: func() map[string]any {
				return map[string]any{"calendar": scratch, "sync_token": syncToken}
			},
			skip: func() string {
				if syncToken == "" {
					return "the baseline step issued no token"
				}
				return ""
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if strings.Contains(r.text, "Baseline for") {
					return fail, "a call WITH a token still reported a baseline"
				}
				if !strings.Contains(r.text, "Changes on") {
					return fail, "the result does not report itself as a change list"
				}
				if afterLabel(r.text, "Next sync token: ") == "" {
					return fail, "the incremental read handed back no new token, so the chain stops here"
				}
				return pass, "token accepted, a new one issued"
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
				rows := timedRows(r.text, weeklyTitle)
				if len(rows) < 2 {
					return undetermined, "fewer than two occurrences came back; nothing to compare"
				}
				for _, row := range rows {
					if !strings.Contains(row, "14:00-15:00") {
						return fail, "an occurrence drifted: " + row
					}
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
			// §17.2. A week asked about in one call: without the mask
			// the answer's longest gap is an overnight one, which passes
			// any min_minutes and is useless.
			name: "availability, working hours",
			tool: "check_availability",
			args: map[string]any{
				"calendars": []string{scratch},
				"from":      "2026-03-16", "to": "2026-03-20",
				"working_from": "09:00", "working_to": "17:00",
				"working_days": []string{"mon", "tue", "wed", "thu", "fri"},
			},
			check: func(r callResult) (verdict, string) {
				if r.isError {
					return fail, "returned an error: " + truncate(r.text, 200)
				}
				if !strings.Contains(r.text, "Only working hours are shown") {
					return fail, "the result does not say the gaps were masked"
				}
				if !strings.Contains(r.text, "Free within 09:00-17:00") {
					return fail, "the heading does not name the mask it applied"
				}
				// The assertion that matters, and it is about the rows
				// rather than the sentence: no gap may begin before
				// 09:00, end after 17:00, or fall at a weekend.
				if bad := gapsOutside(r.text, "09:00", "17:00"); len(bad) > 0 {
					return fail, "a gap outside the mask was reported: " + bad[0]
				}
				return pass, "gaps masked to the working week"
			},
		},
		{
			// The refusal, which has to be classified rather than
			// falling through to [unavailable] — a caller told to retry
			// an overnight mask would retry forever.
			name: "overnight mask refused",
			tool: "check_availability",
			args: map[string]any{
				"calendars": []string{scratch},
				"from":      "2026-03-16", "to": "2026-03-20",
				"working_from": "22:00", "working_to": "06:00",
			},
			check: func(r callResult) (verdict, string) {
				if !r.isError {
					return fail, "a mask crossing midnight was accepted"
				}
				if !strings.Contains(r.text, "[invalid]") {
					return fail, "the refusal is not classified invalid: " + truncate(r.text, 200)
				}
				return pass, "refused with [invalid]"
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

// afterLabel returns the rest of the line following a label, trimmed.
// The driver reads values out of rendered text, so a label that moves is
// a step that reports nothing rather than one that passes wrongly.
func afterLabel(text, label string) string {
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), label); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}
