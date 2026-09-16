// Package redact is the single point through which maintainer tooling
// prints.
//
// The live driver and the spikes talk to a real account, so everything
// they see is somebody's real calendar — titles, guest lists, locations,
// calendar ids. A transcript is pasted into an issue, read in CI output
// and left in a terminal's scrollback, so it has to be safe by
// construction rather than by whoever wrote the print.
//
// `scripts/gates transcript` parses the drivers' source and fails if any
// print reaches the terminal other than through a Printer. Without that
// gate, redaction is a list of call sites somebody remembered to route,
// and the next print added while debugging looks exactly like the safe
// ones beside it.
package redact

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Rules are anchored on shapes the tooling's own output cannot take, so
// a rule cannot collide with a timestamp or a step number and then get
// dismissed as flaky.
var (
	// An address needs an @ and a dot-suffixed domain.
	emailRe = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
	// A secondary calendar's id carries Google's literal suffix.
	calendarRe = regexp.MustCompile(`\b[A-Za-z0-9]{20,}@group\.calendar\.google\.com\b`)
	// An event link carries the event id in its eid.
	eventLinkRe = regexp.MustCompile(`https://[A-Za-z0-9.\-]*google\.com/calendar/[^\s"]*`)
	// A Meet link is a credential in URL form: anybody holding it can
	// walk into the meeting.
	meetRe = regexp.MustCompile(`https://meet\.google\.com/[A-Za-z0-9\-]+`)
	// A refresh token's literal prefix.
	tokenRe = regexp.MustCompile(`\b1//[0-9A-Za-z_\-]{10,}\b`)
	// An OAuth client id.
	clientRe = regexp.MustCompile(`\b\d{6,}-[a-z0-9]{10,}\.apps\.googleusercontent\.com\b`)
	// A sync or page token, anchored on the LABEL rather than a shape.
	//
	// The tokens are opaque base64 and have no shape of their own, so a
	// shape rule would either miss them or match half the transcript.
	// The label is a better anchor than a shape anyway: it is printed by
	// this server's own renderer, so it cannot drift without the
	// renderer changing.
	//
	// They are cursors rather than credentials — neither grants access —
	// but they are account state with the entropy of a secret, and the
	// leak gate already refuses a string of that shape in the tree. A
	// transcript that is pasted into an issue should not be the
	// exception.
	cursorRe = regexp.MustCompile(`(?i)((?:next sync token|sync_token|page_token|next page token)[:=] ?)\S+`)
)

// String redacts one value.
func String(s string) string {
	s = tokenRe.ReplaceAllString(s, "[token]")
	s = cursorRe.ReplaceAllString(s, "${1}[cursor]")
	s = clientRe.ReplaceAllString(s, "[client-id]")
	s = calendarRe.ReplaceAllString(s, "[calendar-id]")
	s = eventLinkRe.ReplaceAllString(s, "[calendar-url]")
	s = meetRe.ReplaceAllString(s, "[meet-url]")
	s = emailRe.ReplaceAllStringFunc(s, maskEmail)
	return s
}

// maskEmail keeps enough to tell two addresses apart and not enough to
// reach anybody: the first character and the top-level domain.
func maskEmail(addr string) string {
	at := strings.LastIndex(addr, "@")
	local, domain := addr[:at], addr[at+1:]
	dot := strings.LastIndex(domain, ".")
	tld := domain[dot+1:]
	return string(local[0]) + "***@***." + tld
}

// ID truncates an identifier to a correlation key that cannot be looked
// up or pasted into a URL. The standard's §4 rule: a log must not
// identify or reconstruct the subject, not that identifiers are banned.
func ID(id string) string {
	if len(id) <= 6 {
		return "[id]"
	}
	return id[:6] + "…"
}

// Printer is the only way maintainer tooling writes to a terminal.
type Printer struct{ w io.Writer }

// New returns a Printer writing to w.
func New(w io.Writer) *Printer { return &Printer{w: w} }

// Printf writes a redacted, formatted line.
func (p *Printer) Printf(format string, args ...any) {
	_, _ = io.WriteString(p.w, String(fmt.Sprintf(format, args...)))
}

// Println writes a redacted line.
func (p *Printer) Println(args ...any) {
	_, _ = io.WriteString(p.w, String(fmt.Sprintln(args...)))
}
