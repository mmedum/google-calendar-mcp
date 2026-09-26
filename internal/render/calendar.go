package render

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/mmedum/google-calendar-mcp/v2/internal/model"
	"github.com/mmedum/google-calendar-mcp/v2/internal/plan"
)

// The calendar and sharing results (§7.5, §7.6).
//
// Both follow §4.9's rule for a write: what it looked like before, what
// changed, what it looks like now, and who was told. The sharing one
// adds the rule §7.6 exists for — exposure is shown BEFORE and AFTER,
// because "shared with someone" is not an answer to "who can see this
// now".

// CalendarReport is what a write to a calendar produced.
type CalendarReport struct {
	// Verb is what this write does; the printed word comes from the one
	// table in write.go, so a calendar write and an event write cannot
	// disagree about the past tense.
	Verb   string
	DryRun bool

	// Calendar is the state to report: after the write, or — for a
	// delete, where there is no after — what was removed.
	Calendar model.Calendar
	// Before is absent on a create.
	Before  *model.Calendar
	Changes []plan.Change
	// Notes carry the consequences a caller does not expect: that
	// unsubscribing deletes nothing, that a renamed calendar keeps its
	// name for everybody else, that a cleared calendar cannot be undone.
	Notes    []string
	Requests int
}

// Said is the word this write is reported with.
func (c CalendarReport) Said() string { return said(c.Verb, c.DryRun) }

// Text renders a calendar write.
func (c CalendarReport) Text() string {
	var b strings.Builder
	if c.DryRun {
		b.WriteString("DRY RUN — nothing was written.\n\n")
	}
	title := "this calendar"
	if c.Calendar.Title != "" {
		title = fmt.Sprintf("%q", c.Calendar.Title)
	}
	fmt.Fprintf(&b, "%s %s.\n\n", c.Said(), title)

	b.WriteString(CalendarLine(c.Calendar))

	if len(c.Changes) > 0 {
		b.WriteString("\nchanged:\n")
		for _, ch := range c.Changes {
			fmt.Fprintf(&b, "  %s: %s → %s\n", ch.Field, shown(ch.From), shown(ch.To))
		}
	}
	for _, n := range c.Notes {
		fmt.Fprintf(&b, "\n%s\n", n)
	}
	if c.DryRun {
		b.WriteString("\nNothing was sent. Everything above is what a real call would do.\n")
	}
	// No etag line, and it is not an omission. An event result prints one
	// because update_event takes it back; no calendar or sharing tool
	// has an `etag` parameter, deliberately (§7.5), so "pass it to the
	// next write" would name something nothing accepts. What is true is
	// said in the tool description instead: each patch is made under the
	// etag of the read this call just did.
	if c.Requests > 1 {
		fmt.Fprintf(&b, "\n(%d API requests)\n", c.Requests)
	}
	return b.String()
}

// SharingReport is what a read or a write of the sharing rules produced.
type SharingReport struct {
	// Verb is empty for list_sharing, which changes nothing.
	Verb   string
	DryRun bool

	CalendarID string
	Title      string
	// Before and After are the whole exposure, not just the rule that
	// changed: the question behind a share is "who can see this now".
	Before []model.Sharing
	After  []model.Sharing
	// Changed is the rule this write was about, absent on a read.
	Changed *model.Sharing
	// Notify is the sentence §4.3.3 requires: what was asked for.
	Notify   string
	Notes    []string
	Requests int
}

// Said is the word this write is reported with.
func (s SharingReport) Said() string { return said(s.Verb, s.DryRun) }

// Text renders a sharing result.
func (s SharingReport) Text() string {
	var b strings.Builder
	if s.DryRun {
		b.WriteString("DRY RUN — nothing was written.\n\n")
	}
	title := s.Title
	if title == "" {
		title = s.CalendarID
	}

	switch {
	case s.Verb == "":
		fmt.Fprintf(&b, "Who can see %q:\n\n", title)
		b.WriteString(Exposure(s.After))
	case s.Changed != nil:
		fmt.Fprintf(&b, "%s %q with %s.\n\n", s.Said(), title, s.Changed.Who())
		b.WriteString("who could see it before:\n")
		b.WriteString(Exposure(s.Before))
		b.WriteString("\nwho can see it now:\n")
		b.WriteString(Exposure(s.After))
	default:
		fmt.Fprintf(&b, "%s %q.\n\n", s.Said(), title)
		b.WriteString(Exposure(s.After))
	}

	if s.Notify != "" {
		fmt.Fprintf(&b, "\n%s\n", s.Notify)
	}
	for _, n := range s.Notes {
		fmt.Fprintf(&b, "\n%s\n", n)
	}
	if s.DryRun {
		b.WriteString("\nNothing was sent. Everything above is what a real call would do.\n")
	}
	if s.Requests > 1 {
		fmt.Fprintf(&b, "\n(%d API requests)\n", s.Requests)
	}
	return b.String()
}

// Sentence turns a refusal into a line that reads as one.
//
// A failure message is written to follow "[class] " and so starts lower
// case and carries no full stop; the same words shown as a NOTE on an
// otherwise successful result are a sentence in their own right. This is
// what lets one wording serve both without a second copy of it.
func Sentence(msg string) string {
	if msg == "" {
		return ""
	}
	r := []rune(msg)
	out := string(unicode.ToUpper(r[0])) + string(r[1:])
	if !strings.HasSuffix(out, ".") {
		out += "."
	}
	return out
}

// Exposure lists who can see a calendar.
//
// One renderer, used by get_calendar, list_sharing and both halves of a
// share result. The public rule is called out in capitals and listed
// first whatever order Google returned the rules in: it is the one that
// changes the answer to "is this private", and a reader scanning a list
// of addresses will not notice a word in the middle of it.
func Exposure(rules []model.Sharing) string {
	if len(rules) == 0 {
		return "  nobody else.\n"
	}
	var b strings.Builder
	for _, r := range rules {
		if r.Public() {
			fmt.Fprintf(&b, "  %s — %s (%s)\n", r.Who(), r.Role, r.RoleMeans())
		}
	}
	for _, r := range rules {
		if !r.Public() {
			fmt.Fprintf(&b, "  %s — %s (%s)\n", r.Who(), r.Role, r.RoleMeans())
		}
	}
	return b.String()
}
