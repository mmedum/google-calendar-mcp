package recur

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmedum/google-calendar-mcp/v2/internal/when"
)

// Scope is which occurrences a write to a recurring event touches.
//
// There is no default and there is no inference (§4.2). "Move the 10:00
// standup to 10:30" is three different operations, the API makes them
// look like one, and the surveyed servers guess. A guess here rewrites
// somebody's next six months.
type Scope string

// The three scopes. Every event write takes one.
const (
	// ScopeInstance changes one occurrence and creates an exception.
	ScopeInstance Scope = "instance"
	// ScopeSeries changes the parent, and so every occurrence that is
	// not already an exception.
	ScopeSeries Scope = "series"
	// ScopeThisAndFollowing is the two-call pattern of §2.8: the
	// original series is truncated and a new one starts at the target.
	// It resets exceptions after the target, which no caller expects.
	ScopeThisAndFollowing Scope = "this_and_following"
)

// Scopes is the vocabulary, in the order a refusal lists them.
var Scopes = []Scope{ScopeInstance, ScopeSeries, ScopeThisAndFollowing}

// Valid reports whether v is one of the three.
func (s Scope) Valid() bool {
	for _, k := range Scopes {
		if k == s {
			return true
		}
	}
	return false
}

// Means says what this scope would do, in the words a refusal uses.
func (s Scope) Means() string {
	switch s {
	case ScopeInstance:
		return "changes only that one occurrence and leaves the rest of the series alone"
	case ScopeSeries:
		return "changes the whole series, including every occurrence that is not already an exception"
	case ScopeThisAndFollowing:
		return "changes that occurrence and every later one, and RESETS any exceptions after it"
	default:
		return ""
	}
}

// ParseScope reads a caller's scope.
//
// An empty scope is refused with the same message as an unknown one,
// because "the caller did not choose" and "the caller chose something
// that is not a choice" need the same answer: the three options and what
// each would do.
func ParseScope(v string) (Scope, error) {
	s := Scope(strings.ToLower(strings.TrimSpace(v)))
	if s.Valid() {
		return s, nil
	}
	if v == "" {
		return "", fmt.Errorf("%w: this event repeats, so a write has to say which occurrences it means. "+
			"Pass scope:%s", ErrInvalid, ChoiceList())
	}
	return "", fmt.Errorf("%w: %q is not a scope. Pass scope:%s", ErrInvalid, v, ChoiceList())
}

// ChoiceList is the three scopes and what each does, for a refusal.
func ChoiceList() string {
	var b strings.Builder
	for _, s := range Scopes {
		fmt.Fprintf(&b, "\n  %s — %s", s, s.Means())
	}
	return b.String()
}

// Reach describes how far a series scope would carry, for the refusal
// §4.2 requires: a caller choosing "series" should be told how many
// occurrences that is before they choose it.
func (s Set) Reach(start when.Zoned, limit int) string {
	if s.Rule == nil {
		return "this event does not repeat"
	}
	occ, err := s.ExpandTimes(start, when.Zoned{}, when.Zoned{}, limit)
	return s.reachOf(occ, err)
}

// ReachDates is Reach for an all-day series, which has dates and no
// instants (§4.1).
func (s Set) ReachDates(start when.Date, limit int) string {
	if s.Rule == nil {
		return "this event does not repeat"
	}
	occ, err := s.ExpandDates(start, when.Date{}, when.Date{}, limit)
	return s.reachOf(occ, err)
}

func (s Set) reachOf(occ Occurrences, err error) string {
	if err != nil {
		return "this server cannot count the occurrences of this rule; list_instances asks Google for them"
	}
	switch {
	case occ.Truncated || s.Rule.Open():
		return fmt.Sprintf("this series has no end date — at least %d occurrences", occ.Count())
	case occ.Count() == 0:
		return "this series has no occurrences left"
	case occ.Count() == 1:
		return "this series has one occurrence"
	default:
		return fmt.Sprintf("this series has %d occurrences, the last on %s", occ.Count(), lastOf(occ))
	}
}

// lastOf names the final occurrence, from whichever half of Occurrences
// the expansion filled.
func lastOf(occ Occurrences) string {
	if n := len(occ.Dates); n > 0 {
		return occ.Dates[n-1].String()
	}
	if n := len(occ.Times); n > 0 {
		return occ.Times[n-1].Date().String()
	}
	return ""
}

// Split computes the two rules a this_and_following write needs (§2.8),
// for a timed series.
//
// Google has no server-side "this and following": the pattern is to end
// the original series before the target and insert a new one starting at
// it. This returns the RRULE line for each half and nothing else — the
// two API calls, the exception reset and the wording belong to the write
// path, which is where a caller can be told what happened.
func (s Set) Split(start, target when.Zoned) (before, after string, err error) {
	if start.Loc == nil || target.IsZero() {
		return "", "", fmt.Errorf("%w: splitting a series needs its start and the target occurrence", ErrInvalid)
	}
	if !target.T.After(start.T) {
		return "", "", fmt.Errorf("%w: the target occurrence %s is not after the series start %s; "+
			"a this_and_following write at the first occurrence is a series write", ErrInvalid, target, start)
	}
	return s.splitAt(target.String(), func(rule Set) (Occurrences, error) {
		return rule.ExpandTimes(start, when.Zoned{}, target, 0)
	})
}

// SplitDates is Split for an all-day series, where the occurrences are
// dates and there is no instant to compare (§4.1).
//
// The two front-ends differ only in which expansion counts the head, so
// the arithmetic — and the COUNT rule that was wrong twice — lives once
// in splitAt.
func (s Set) SplitDates(start, target when.Date) (before, after string, err error) {
	if start.IsZero() || target.IsZero() {
		return "", "", fmt.Errorf("%w: splitting a series needs its start and the target occurrence", ErrInvalid)
	}
	if !target.After(start) {
		return "", "", fmt.Errorf("%w: the target occurrence %s is not after the series start %s; "+
			"a this_and_following write at the first occurrence is a series write", ErrInvalid, target, start)
	}
	return s.splitAt(target.String(), func(rule Set) (Occurrences, error) {
		return rule.ExpandDates(start, when.Date{}, target, 0)
	})
}

// splitAt truncates the original rule to the occurrences before the
// target and returns the rule the new series carries.
//
// head counts what the RULE generates before the target — the rule
// alone, not the series as a caller sees it. A COUNT counts what the
// rule produces, and Google applies EXDATE and RDATE on top of it, so
// counting visible occurrences wrote a COUNT that was too small when a
// date had been excluded, losing an occurrence from the original series,
// and too large when an RDATE had been added, running the truncated
// original past the split target and overlapping the new series.
//
// The truncation keeps COUNT as a COUNT rather than converting it to an
// UNTIL. Both are legal, and a count that stays a count cannot be moved
// by an hour when somebody's zone rules change.
func (s Set) splitAt(target string, head func(Set) (Occurrences, error)) (before, after string, err error) {
	if err := s.expandable(); err != nil {
		return "", "", err
	}
	occ, err := head(Set{Lines: s.Lines, Rule: s.Rule})
	if err != nil {
		return "", "", err
	}
	if occ.Truncated {
		return "", "", fmt.Errorf("%w: this series has too many occurrences before %s to split reliably",
			ErrInvalid, target)
	}
	if occ.Count() == 0 {
		return "", "", fmt.Errorf("%w: no occurrence falls before %s, so there is nothing to truncate",
			ErrInvalid, target)
	}

	r := *s.Rule
	before = r.with("COUNT", fmt.Sprintf("%d", occ.Count()), "UNTIL")
	switch {
	case r.Count > 0:
		remaining := r.Count - occ.Count()
		if remaining < 1 {
			return "", "", fmt.Errorf("%w: every occurrence of this series falls before %s", ErrInvalid, target)
		}
		after = r.with("COUNT", fmt.Sprintf("%d", remaining), "UNTIL")
	default:
		// An open-ended rule, or one with an UNTIL, carries over
		// unchanged: the new series starts at the target and the end it
		// already had still applies.
		after = r.Raw
	}
	return before, after, nil
}

// with returns the rule's line with one part set and another removed,
// preserving the order and the spelling of everything else.
//
// It edits the caller's string rather than composing a new rule from the
// struct, for §6.4's reason: a round trip through a struct is where a
// part this package does not model goes missing.
func (r Rule) with(key, value, drop string) string {
	prefix, body := "", r.Raw
	if i := strings.Index(r.Raw, ":"); i >= 0 {
		prefix, body = r.Raw[:i+1], r.Raw[i+1:]
	}
	var parts []string
	replaced := false
	for _, part := range strings.Split(body, ";") {
		name, _, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(strings.TrimSpace(name)) {
		case strings.ToUpper(key):
			parts = append(parts, key+"="+value)
			replaced = true
		case strings.ToUpper(drop):
			// Dropped: COUNT and UNTIL cannot both be present.
		default:
			parts = append(parts, part)
		}
	}
	if !replaced {
		parts = append(parts, key+"="+value)
	}
	return prefix + strings.Join(parts, ";")
}

// UntilLine returns the rule ending at or before t, as the UTC UNTIL
// RFC 5545 requires on a zoned series.
//
// Kept apart from Split because a caller that wants a series to stop on
// a date is not splitting anything, and because the Z is the detail that
// gets this wrong: a local UNTIL on a zoned series is not valid, and
// reading one back as UTC moves the end of the series by the offset.
func (r Rule) UntilLine(t time.Time) string {
	return r.with("UNTIL", t.UTC().Format("20060102T150405Z"), "COUNT")
}
