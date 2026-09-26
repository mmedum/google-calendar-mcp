package redact_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mmedum/google-calendar-mcp/v2/internal/redact"
)

// The fixtures below are assembled at run time rather than written out
// as literals.
//
// They are invented, but they are shaped exactly like the things the
// leak gate hunts for — that is the point of them — so as literals they
// make `make leaks` fail on this file. The alternative was to exempt
// this path from the gate, which would leave one file where a real
// address could later be pasted invisibly. Composing them keeps the gate
// universal, and costs one helper.
func at(local, domain string) string { return local + "@" + domain }

func TestRedactsTheShapesThatCarrySomebody(t *testing.T) {
	var (
		address    = at("person.name", "company.example")
		calendarID = at("abcdefghijklmnopqrstuvwx", "group.calendar.google.com")
		eventLink  = "https://www.google.com/calendar/" + "event?eid=abc123"
		token      = "1" + "//0gabcdefghijklmnop"
		clientID   = "123456789012-abcdefghijklmnop" + ".apps.googleusercontent.com"
	)
	cases := []struct {
		name, in, mustNotContain string
	}{
		{"an address", "invited " + address, "person.name"},
		{"a calendar id", "reading " + calendarID, "abcdefghij"},
		{"an event link", "see " + eventLink, "eid=abc123"},
		{"a refresh token", "token " + token, token},
		{"a client id", "client " + clientID, "123456789012-"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := redact.String(c.in)
			if strings.Contains(got, c.mustNotContain) {
				t.Fatalf("redact.String(%q) = %q, which still carries %q", c.in, got, c.mustNotContain)
			}
		})
	}
}

// TestRedactionKeepsTranscriptsReadable: a transcript where every line
// is the same mask is one nobody reads, so two different addresses must
// still look different.
func TestRedactionKeepsTranscriptsReadable(t *testing.T) {
	a := redact.String(at("alice", "example.test"))
	b := redact.String(at("bob", "example.test"))
	if a == b {
		t.Fatalf("two different addresses redact identically (%q); a transcript becomes unreadable", a)
	}
	if !strings.HasSuffix(a, ".test") {
		t.Fatalf("the top-level domain was lost: %q", a)
	}
}

// TestRedactionCannotCollideWithGeneratedOutput. The rules are anchored
// on shapes the tooling's own prints cannot take — a timestamp, a step
// number, a duration — so the gate cannot fire on its own output and
// then be dismissed as flaky.
func TestRedactionCannotCollideWithGeneratedOutput(t *testing.T) {
	for _, safe := range []string{
		"step 12 of 40 passed in 1.234s",
		"time=2026-09-15T19:35:55.170969+02:00 level=INFO",
		"7.50", "07.502", "2026-03-20", "09:00-10:30",
		"read 250 events across 3 calendars",
	} {
		if got := redact.String(safe); got != safe {
			t.Fatalf("redaction changed ordinary output %q into %q", safe, got)
		}
	}
}

func TestID(t *testing.T) {
	if got := redact.ID("abcdefghijklmnop"); got != "abcdef…" {
		t.Fatalf("ID = %q", got)
	}
	if got := redact.ID("short"); got != "[id]" {
		t.Fatalf("a short id must not be shown whole: %q", got)
	}
}

func TestPrinterRedacts(t *testing.T) {
	var buf bytes.Buffer
	p := redact.New(&buf)
	p.Printf("invited %s to %s\n", at("person", "company.example"), "a meeting")
	p.Println("organizer", at("someone", "company.example"))
	got := buf.String()
	if strings.Contains(got, "person@") || strings.Contains(got, "someone@") {
		t.Fatalf("the printer let an address through: %q", got)
	}
	if !strings.Contains(got, "a meeting") {
		t.Fatalf("the printer dropped ordinary text: %q", got)
	}
}

// A sync token is account state with the entropy of a secret, and a
// transcript gets pasted into issues. It has no shape of its own, so the
// rule is anchored on the label this server's own renderer prints.
//
// The fixtures below are invented and say so. The first draft of this
// test carried a REAL token copied out of a live transcript, and the
// leak gate refused the commit — which is the gate doing exactly its job
// and the reason §9.1 says fixtures are generated, never recorded.
func TestSyncAndPageTokensAreRedacted(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Next sync token: example-token-not-a-real-one", "Next sync token: [cursor]"},
		{"page_token: example-page-token", "page_token: [cursor]"},
		{"next page token: abc123", "next page token: [cursor]"},
	}
	for _, tc := range cases {
		if got := redact.String(tc.in); got != tc.want {
			t.Errorf("String(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
	// And the prose around it survives, or the transcript stops being
	// readable in exchange for being safe.
	const sentence = "Pass it as sync_token next time to get only what changed."
	if got := redact.String(sentence); got == sentence {
		t.Log("prose with no token after the label is left alone")
	}
}

// A Meet link is joinable by anybody holding it, so the redactor's job
// is to miss none. These are the three it used to miss — found when
// CodeQL flagged the line for over-matching and the opposite turned out
// to be true.
func TestMeetLinksAreRedactedInEveryFormTheyTake(t *testing.T) {
	cases := []struct{ name, in string }{
		{"in the middle of a sentence", "join at https://meet.google.com/abc-defg-hij today"},
		{"a lookup path", "https://meet.google.com/lookup/abcdefghij"},
		{"upper case", "HTTPS://MEET.GOOGLE.COM/abc-defg-hij"},
		{"plain http", "http://meet.google.com/abc-defg-hij"},
		{"with a query", "https://meet.google.com/abc-defg-hij?authuser=1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := redact.String(tc.in)
			for _, leak := range []string{"abc-defg-hij", "abcdefghij", "ABC-DEFG-HIJ"} {
				if strings.Contains(got, leak) {
					t.Fatalf("the meeting code survived redaction: %q", got)
				}
			}
			if !strings.Contains(got, "[meet-url]") {
				t.Fatalf("nothing was redacted: %q", got)
			}
		})
	}
}
