package render

import (
	"fmt"
	"strings"

	"github.com/mmedum/google-calendar-mcp/v2/internal/model"
	"github.com/mmedum/google-calendar-mcp/v2/internal/plan"
	"github.com/mmedum/google-calendar-mcp/v2/internal/when"
)

// WriteReport is everything a write produced (§4.9).
//
// A write result that says only "ok" is a defect: the caller cannot tell
// a patch that changed one field from one that changed five, and cannot
// tell who was emailed. So every write carries what the event looked
// like before, what changed, what it looks like now, and what the server
// asked Google to send — never what anybody received (§4.3.3).
type WriteReport struct {
	// Verb is what this write does — create, update, cancel, move,
	// answer — and NOT the word printed. The past tense and the
	// conditional are derived from it below.
	//
	// They used to be two strings assigned at eight call sites, which is
	// how "Would update" came to appear twice and "Would cancel" three
	// times, and how five of the eight dry-run branches said "nothing
	// was sent" while three did not. A dry run being honest is a
	// property of a dry run, not of each operation remembering.
	Verb     string
	DryRun   bool
	Calendar string
	Zone     when.Zone

	// Before is absent on a create, where there was nothing.
	Before  *model.Event
	After   *model.Event
	Changes []plan.Change

	// Scope is the recurrence decision this write was made under, empty
	// when the event does not repeat (§4.2).
	Scope string
	// Notify is the sentence §4.3.3 requires: what was asked for.
	Notify string
	// Notes carry the consequences a caller does not expect — the reset
	// exceptions of a this_and_following write, the guests who still
	// hold a canceled meeting.
	Notes []string
	// Requests is how many API calls this cost. §4.7's exceptions are
	// the writes that are two calls, and this is where they say so.
	Requests int
}

// verbs are the past tense and the conditional for each write. One
// table, so the pair can never disagree — and one table for every write
// in the server, so a calendar write cannot grow a second vocabulary
// that says "Would delete" where an event write says "Deleted".
var verbs = map[string][2]string{
	VerbCreate:      {"Created", "Would create"},
	VerbUpdate:      {"Updated", "Would update"},
	VerbCancel:      {"Canceled", "Would cancel"},
	VerbMove:        {"Moved", "Would move"},
	VerbAnswer:      {"Answered", "Would answer"},
	VerbSubscribe:   {"Subscribed to", "Would subscribe to"},
	VerbUnsubscribe: {"Unsubscribed from", "Would unsubscribe from"},
	VerbDelete:      {"Deleted", "Would delete"},
	VerbClear:       {"Cleared", "Would clear"},
	VerbShare:       {"Shared", "Would share"},
	VerbUnshare:     {"Stopped sharing", "Would stop sharing"},
}

// The five event writes.
const (
	VerbCreate = "create"
	VerbUpdate = "update"
	VerbCancel = "cancel"
	VerbMove   = "move"
	VerbAnswer = "answer"
)

// The calendar and sharing writes (§7.5, §7.6).
const (
	VerbSubscribe   = "subscribe"
	VerbUnsubscribe = "unsubscribe"
	VerbDelete      = "delete"
	VerbClear       = "clear"
	VerbShare       = "share"
	VerbUnshare     = "unshare"
)

// said is the word a write is reported with: the past tense, or the
// conditional under a dry run.
func said(verb string, dryRun bool) string {
	pair := verbs[verb]
	if dryRun {
		return pair[1]
	}
	return pair[0]
}

// Said is the word this write is reported with: the past tense, or the
// conditional under a dry run. The structured half quotes it too, so a
// caller reading only that cannot be told something happened when it
// did not.
func (w WriteReport) Said() string { return said(w.Verb, w.DryRun) }

// Text renders a write.
func (w WriteReport) Text() string {
	var b strings.Builder

	if w.DryRun {
		b.WriteString("DRY RUN — nothing was written.\n\n")
	}
	said := w.Said()
	title := "this event"
	if w.After != nil && w.After.Title != "" {
		title = fmt.Sprintf("%q", w.After.Title)
	} else if w.Before != nil && w.Before.Title != "" {
		title = fmt.Sprintf("%q", w.Before.Title)
	}
	fmt.Fprintf(&b, "%s %s on %s.\n", said, title, w.Calendar)
	if w.Scope != "" {
		fmt.Fprintf(&b, "scope: %s\n", w.Scope)
	}
	b.WriteString("\n")

	if w.Before != nil {
		fmt.Fprintf(&b, "  before: %s\n", dated(*w.Before, w.Zone))
	} else {
		b.WriteString("  before: (did not exist)\n")
	}
	if w.After != nil {
		fmt.Fprintf(&b, "  after:  %s\n", dated(*w.After, w.Zone))
	} else {
		b.WriteString("  after:  (canceled)\n")
	}

	if len(w.Changes) > 0 {
		b.WriteString("\nchanged:\n")
		for _, c := range w.Changes {
			fmt.Fprintf(&b, "  %s: %s → %s\n", c.Field, shown(c.From), shown(c.To))
		}
	}

	if w.Notify != "" {
		fmt.Fprintf(&b, "\n%s\n", w.Notify)
	}
	for _, n := range w.Notes {
		fmt.Fprintf(&b, "\n%s\n", n)
	}
	if n := w.Crowded(); n != "" {
		// Derived from the event this write produced rather than added
		// by whichever write grew the guest list: every write that can
		// leave an event with too many guests says so, including the
		// ones that did not add them.
		fmt.Fprintf(&b, "\n%s\n", n)
	}
	if w.DryRun {
		b.WriteString("\nNothing was sent. Everything above is what a real call would do.\n")
	}

	b.WriteString("\n")
	if w.After != nil && w.After.ID != "" {
		fmt.Fprintf(&b, "id: %s on calendar %s\n", w.After.ID, w.After.CalendarID)
		if w.After.ETag != "" {
			fmt.Fprintf(&b, "etag: %s — pass it to the next write, which will be refused if somebody else "+
				"changed this first\n", w.After.ETag)
		}
	}
	fmt.Fprintf(&b, "%s\n", w.Zone.Explain())
	if w.Requests > 1 {
		fmt.Fprintf(&b, "(%d API requests)\n", w.Requests)
	}
	return b.String()
}

// Crowded is §17.5's warning for the event this write produced, or ""
// when there is nothing to warn about.
//
// It counts ATTENDEE ROWS, not guests, and the difference matters here
// in the other direction from everywhere else. Google's threshold is on
// its own attendees field — the account itself and the rooms are on it —
// so counting guests would stay silent on an event Google had already
// stopped tracking. It is also the number this result PRINTS beside the
// warning, so the two cannot disagree.
func (w WriteReport) Crowded() string {
	if w.After == nil {
		return ""
	}
	return plan.CrowdWarning(len(w.After.Attendees))
}

// dated renders one side of a write WITH its date.
//
// EventLine leaves the date out on purpose: a schedule groups by day and
// the heading carries it. A write result has no heading, so the same
// line reads "10:00-11:00" and does not say which Thursday — which is
// exactly the mistake §4.5 forbids on a read, on the result where it
// matters more. An all-day event already renders its own date, so it is
// not repeated.
func dated(e model.Event, z when.Zone) string {
	if e.Start.AllDay || e.Start.At.IsZero() {
		return EventLine(e, z)
	}
	return e.Start.At.Date().String() + "  " + EventLine(e, z)
}

// shown renders one side of a change, so an emptied field reads as
// emptied rather than as a blank line.
func shown(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(empty)"
	}
	return v
}
