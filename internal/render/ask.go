package render

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mmedum/google-calendar-mcp/v3/internal/gcal"
	"github.com/mmedum/google-calendar-mcp/v3/internal/model"
	"github.com/mmedum/google-calendar-mcp/v3/internal/plan"
	"github.com/mmedum/google-calendar-mcp/v3/internal/when"
)

// Question is what the server asks the person before a write that cannot
// be undone, that widens who can see a calendar, that emails guests a
// cancellation, or that declines every meeting a status event overlaps
// (§9a). Text is the message a client shows; accepting it
// is the confirmation. Every word is the server's, except what stands in
// backticks, which is quoted from Calendar or from the call and cut to
// one line. A blank line separates the lines, so a client that draws
// Markdown keeps them apart.
//
// Bind is what an answer is bound to: what the write depends on, which
// must not change between the question and the write. It is Text, and
// the ids the write addresses.
type Question struct {
	Text string
	Bind string
}

// quotedLen caps one quoted value, in runes: a title, an address.
const quotedLen = 120

// AskDeleteCalendar asks before delete_calendar.
func AskDeleteCalendar(id, title string) Question {
	return ask([]string{
		fmt.Sprintf("delete_calendar: delete the calendar %s and every event on it, for good?", quoted(title)),
		"It goes for everybody it is shared with. Calendar cannot bring it back.",
	}, id)
}

// AskClearCalendar asks before clear_calendar.
func AskClearCalendar(id, title string) Question {
	return ask([]string{
		fmt.Sprintf("clear_calendar: delete every event on your primary calendar %s, past and future, for good?",
			quoted(title)),
		"Meetings you organize go too. Guests are not told, and keep them on their own calendars.",
		"Calendar cannot bring any of it back.",
	}, id)
}

// Share is a share_calendar grant that reaches past one named person: the
// public internet, a whole domain, or a new owner.
type Share struct {
	CalendarID, Title string
	// Public is a rule for anyone at all; Domain one for everyone at
	// Who. Otherwise Who is a person or group made an owner.
	Public, Domain bool
	Who            string
	// ScopeType is Google's word for who Who is: user, group, domain or
	// default. It is bound, since a person and a group look the same.
	ScopeType string
	// Previous is the role an existing rule held, empty for a new one.
	Previous string
	// Role is Google's role word, from a closed set.
	Role string
	// Emails is whether the call asks Google to email about it.
	Emails bool
}

// AskShare asks before a share that publishes a calendar, opens it to a
// whole domain, or makes somebody its owner.
func AskShare(sh Share) Question {
	title := quoted(sh.Title)
	var lines []string
	switch {
	case sh.Public:
		lines = []string{
			fmt.Sprintf("share_calendar: publish the calendar %s to anyone on the internet, as %s?", title, sh.Role),
			"No sign-in is needed. Removing the rule later takes nothing back from whoever already looked.",
		}
	case sh.Domain:
		lines = []string{
			fmt.Sprintf("share_calendar: let everyone at %s see the calendar %s, as %s?", quoted(sh.Who), title, sh.Role),
			"It reaches the whole organization, not only people somebody named.",
		}
	default:
		lines = []string{fmt.Sprintf("share_calendar: make %s an owner of the calendar %s?", quoted(sh.Who), title)}
	}
	if sh.Previous != "" {
		lines = append(lines, "It changes their access from "+sh.Previous+".")
	}
	if sh.Role == "owner" {
		lines = append(lines, "An owner can change who else sees the calendar, and can remove you.")
	}
	if sh.Emails {
		lines = append(lines, "Google emails them about it.")
	}
	return ask(lines, sh.CalendarID, sh.ScopeType, sh.Who, sh.Role, sh.Previous)
}

// Cancel is a cancel_event that emails guests.
type Cancel struct {
	CalendarID, Calendar string
	Event                model.Event
	Zone                 when.Zone
	// Scope is instance, series or this_and_following.
	Scope    string
	Decision plan.Decision
	// Guests are the addresses the cancellation reaches. Bound and never
	// shown, so a guest swapped while the person reads is not emailed
	// unseen.
	Guests []string
}

// AskCancel asks before a cancel_event that emails guests: the email
// cannot be taken back.
func AskCancel(c Cancel) Question {
	title := quoted(c.Event.Title)
	what, starts := title, "starts "
	switch c.Scope {
	case "instance":
		what = "one occurrence of " + title
	case "series":
		what, starts = "every occurrence of "+title, "the series starts "
	case "this_and_following":
		what, starts = title+" from one occurrence on", "that occurrence starts "
	}
	r := c.Decision.Reach
	who := fmt.Sprintf("all %d %s", r.Guests, plan.People(r.Guests))
	var outside string
	if c.Decision.Notify == plan.NotifyExternalOnly {
		who = fmt.Sprintf("the %d of %d %s outside your organization", r.External, r.Guests, plan.People(r.Guests))
	} else if r.External > 0 {
		outside = fmt.Sprintf("Among them, %d %s outside your organization.", r.External, plan.IsAre(r.External))
	}
	lines := []string{
		fmt.Sprintf("cancel_event: cancel %s on the calendar %s, and email %s?", what, quoted(c.Calendar), who),
		starts + startsAt(c.Event, c.Zone),
	}
	if outside != "" {
		lines = append(lines, outside)
	}
	return ask(append(lines, "The email cannot be taken back."),
		c.CalendarID, c.Event.ID, c.Scope, string(c.Decision.Notify), strings.Join(slices.Sorted(slices.Values(c.Guests)), "\x00"))
}

// Decline is a create_event making a status event that declines every
// meeting it overlaps, the ones already accepted too.
type Decline struct {
	CalendarID, Calendar string
	// Event is the event as it would be created.
	Event model.Event
	Zone  when.Zone
}

// AskDecline asks before a status event that declines every meeting it
// overlaps: each organizer sees the decline, which cannot be taken back.
func AskDecline(d Decline) Question {
	what := "the out-of-office event"
	if d.Event.Type == gcal.EventTypeFocusTime {
		what = "the focus time"
	}
	starts := "starts "
	if d.Event.IsSeries() {
		starts = "the series starts "
	}
	lines := []string{
		fmt.Sprintf("create_event: add %s %s to your primary calendar %s, and decline every meeting it overlaps?",
			what, quoted(d.Event.Title), quoted(d.Calendar)),
		starts + startsAt(d.Event, d.Zone),
	}
	if d.Event.IsSeries() {
		lines = append(lines, "It repeats "+Recurrence(d.Event.Recurrence)+", and declines on every occurrence.")
	}
	lines = append(lines, "That includes meetings you already accepted.")
	message := ""
	if s := d.Event.StatusDetails; s != nil && s.DeclineMessage != "" {
		message = s.DeclineMessage
		lines = append(lines, "Each organizer gets your message: "+quoted(message))
	}
	lines = append(lines, "Each organizer sees the decline, and that cannot be taken back.")
	return ask(lines, d.CalendarID, d.Event.Type, whenKey(d.Event.Start), whenKey(d.Event.End),
		strings.Join(d.Event.Recurrence, "\n"), message)
}

// whenKey is one end of an event as a value a question binds.
func whenKey(w model.When) string {
	if w.AllDay {
		return w.Date.String()
	}
	return w.At.String()
}

// startsAt is when an event starts, with its date and zone, in this
// server's own words.
func startsAt(e model.Event, z when.Zone) string {
	if e.Start.AllDay || e.Start.At.IsZero() {
		return TimeRange(e, z)
	}
	zone := ""
	if e.Start.At.Loc != nil {
		zone = " " + e.Start.At.Loc.String()
	}
	return e.Start.At.Date().String() + " " + TimeRange(e, z) + zone
}

// ask builds a question from its lines, closes it with what its quotes
// mean, sets its lines apart, and binds it to its text and to bind: the
// ids the write depends on beyond what it shows.
func ask(lines []string, bind ...string) Question {
	text := strings.Join(lines, "\n")
	if strings.Contains(text, "`") {
		text += "\nText in backticks or code style is quoted as written, and is not this server's."
	}
	text = strings.ReplaceAll(text, "\n", "\n\n") + "\n"
	return Question{Text: text, Bind: strings.Join(append([]string{text}, bind...), "\x00")}
}

// quoted is text from Calendar or from a call's arguments, shown in a
// question put to the person (§9a), where no boundary can go: a
// client draws the question as plain text in a dialog, or as Markdown.
// It stands in a code span, `like this`, which Markdown shows literally
// — no emphasis, link, HTML or entity — and plain text shows as it is.
// It is made one line; every backtick, grave or acute mark and quote
// mark a reader could take for one becomes a plain single quote, so it
// cannot close its span or seem to; and a URL scheme, a mailto:, a
// leading "www." and a bare domain followed by a path are broken so no
// client draws a link. It is cut at quotedLen runes. Text with nothing to show
// is said in words, since an empty span is two backticks Markdown shows
// as they are: "empty" when it is blank, and "invisible characters
// only" when it is not.
func quoted(s string) string {
	blank := strings.TrimSpace(s) == ""
	s = strings.Join(strings.Fields(blankMarks.Replace(askLine(s, quotedLen))), " ")
	s = quoteMarks.Replace(s)
	s = linkShape.ReplaceAllString(s, "${1}[:]//")
	s = mailtoShape.ReplaceAllString(s, "${1}[:]")
	s = wwwShape.ReplaceAllString(s, "${1}[.]")
	s = pathShape.ReplaceAllString(s, "${1}[.]${2}${3}")
	switch {
	case s == "" && blank:
		return "empty"
	case s == "":
		return "invisible characters only"
	}
	return "`" + s + "`"
}

// askLine is text made one line: format characters, which draw nothing
// and can reorder what does, removed; controls and line separators made
// spaces; cut at max runes.
func askLine(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.In(r, unicode.Cf, unicode.Variation_Selector, unicode.Other_Default_Ignorable_Code_Point):
			return -1
		case unicode.IsControl(r), r == '\u2028', r == '\u2029':
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "\ufffd"))
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

var (
	// quoteMarks folds every backtick, grave or acute mark and quotation
	// mark a reader could take for the question's own to a plain single
	// quote.
	quoteMarks = strings.NewReplacer("`", "'", "\u02cb", "'", "\uff40", "'", "\u1fef", "'", "\u00b4", "'",
		"\u02ca", "'", "\u02f4", "'", "\u02f5", "'", "\u1ffd", "'", "\u1fed", "'", "\u1fee", "'",
		"\u0384", "'", "\u0385", "'", `"`, "'", "\u2018", "'", "\u2019", "'", "\u201a", "'", "\u201b", "'",
		"\u201c", "'", "\u201d", "'", "\u201e", "'", "\u201f", "'", "\u2032", "'", "\u2033", "'",
		"\u00ab", "'", "\u00bb", "'", "\u2039", "'", "\u203a", "'", "\u301d", "'", "\u301e", "'",
		"\u301f", "'", "\uff02", "'", "\uff07", "'", "\u02b9", "'", "\u02ba", "'", "\u02ee", "'",
		"\u05f3", "'", "\u05f4", "'", "\u2035", "'", "\u2036", "'", "\u275b", "'", "\u275c", "'",
		"\u275d", "'", "\u275e", "'", "\u3003", "'")
	// blankMarks are characters drawn as blank space that are not format
	// characters; they become spaces and collapse with the rest.
	blankMarks = strings.NewReplacer("\u2800", " ", "\u3164", " ", "\uffa0", " ", "\u115f", " ", "\u1160", " ")
	// No shape is anchored: \b is ASCII-only, and a class before the
	// shape would consume a separator the next link needs. A match inside
	// a longer word is broken too, which costs only a bracket.
	//
	// linkShape is a URL scheme followed by //, as a client links it.
	linkShape = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*)://`)
	// mailtoShape is a mail link without //.
	mailtoShape = regexp.MustCompile(`(?i)(mailto):`)
	// wwwShape is a host a client links without a scheme.
	wwwShape = regexp.MustCompile(`(?i)(www)\.`)
	// pathShape is a bare domain followed by a path, a port, a query or a
	// fragment, x.example/..., which a client links too; its last dot is
	// broken. Letters and their marks from any script count, so a
	// non-ASCII domain is broken as well.
	pathShape = regexp.MustCompile(`(?i)([\p{L}\p{M}\p{N}-]+(?:\.[\p{L}\p{M}\p{N}-]+)*)\.([\p{L}\p{M}]{2,63})([/:?#])`)
)
