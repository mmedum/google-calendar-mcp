package recur

import (
	"fmt"
	"strings"
	"time"
)

// Explain says what a recurrence means, in the words a person uses.
//
// It explains rather than reformats: the caller's own rule is what gets
// sent (§6.4), and this is what a result shows beside it. A rule this
// package cannot describe comes back as itself, which is honest and
// still readable — a caller who wrote "FREQ=WEEKLY;BYDAY=TU" recognises
// their own line.
func (s Set) Explain() string {
	if s.Rule == nil {
		if len(s.Lines) > 0 {
			return s.Lines[0]
		}
		return "repeats"
	}
	out := s.Rule.Explain()
	if n := len(s.Excluded); n > 0 {
		out += fmt.Sprintf(", except on %d date%s", n, plural(n))
	}
	if n := len(s.Added); n > 0 {
		out += fmt.Sprintf(", plus %d extra date%s", n, plural(n))
	}
	return out
}

// Explain describes one rule.
func (r Rule) Explain() string {
	base := r.frequency()
	if base == "" {
		return r.Raw
	}
	var b strings.Builder
	b.WriteString(base)

	if len(r.ByMonth) > 0 && r.Freq != Yearly {
		b.WriteString(" in " + monthNames(r.ByMonth))
	}
	if len(r.ByMonthDay) > 0 && r.Freq != Daily {
		b.WriteString(" on the " + monthDayNames(r.ByMonthDay))
	}
	switch {
	case r.Count == 1:
		b.WriteString(", once")
	case r.Count > 1:
		fmt.Fprintf(&b, ", %d times", r.Count)
	case !r.UntilDate.IsZero():
		b.WriteString(", until " + r.UntilDate.String())
	case !r.Until.IsZero():
		// The UNTIL is UTC on the wire and is shown as the date it falls
		// on there. Saying which zone that date is read in would need
		// the series' zone, which a rule on its own does not carry.
		b.WriteString(", until " + r.Until.UTC().Format("2006-01-02") + " (UTC)")
	}
	if len(r.Unknown) > 0 {
		fmt.Fprintf(&b, " (plus %s, which this server does not describe)", strings.Join(r.Unknown, ", "))
	}
	return b.String()
}

func (r Rule) frequency() string {
	unit := map[Freq]string{Daily: "day", Weekly: "week", Monthly: "month", Yearly: "year"}[r.Freq]
	if unit == "" {
		return ""
	}
	out := "every " + unit
	if r.Interval > 1 {
		out = fmt.Sprintf("every %d %ss", r.Interval, unit)
	}
	if len(r.ByDay) > 0 {
		out += " on " + byDayNames(r.ByDay)
	}
	return out
}

func byDayNames(days []ByDay) string {
	out := make([]string, 0, len(days))
	for _, d := range days {
		name := d.Day.String()
		switch {
		case d.Ordinal > 0:
			name = ordinal(d.Ordinal) + " " + name
		case d.Ordinal == -1:
			name = "last " + name
		case d.Ordinal < -1:
			name = ordinal(-d.Ordinal) + "-to-last " + name
		}
		out = append(out, name)
	}
	return join(out)
}

func monthNames(months []time.Month) string {
	out := make([]string, 0, len(months))
	for _, m := range months {
		out = append(out, m.String())
	}
	return join(out)
}

func monthDayNames(days []int) string {
	out := make([]string, 0, len(days))
	for _, n := range days {
		switch {
		case n == -1:
			out = append(out, "last day")
		case n < 0:
			out = append(out, ordinal(-n)+"-to-last day")
		default:
			out = append(out, ordinal(n))
		}
	}
	return join(out)
}

// ordinal is 1st, 2nd, 3rd, 4th. The teens are the exception every
// implementation of this forgets: 11th, 12th and 13th, not 11st.
func ordinal(n int) string {
	suffix := "th"
	if n%100 < 11 || n%100 > 13 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return fmt.Sprintf("%d%s", n, suffix)
}

func join(ss []string) string {
	switch len(ss) {
	case 0:
		return ""
	case 1:
		return ss[0]
	case 2:
		return ss[0] + " and " + ss[1]
	default:
		return strings.Join(ss[:len(ss)-1], ", ") + " and " + ss[len(ss)-1]
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
