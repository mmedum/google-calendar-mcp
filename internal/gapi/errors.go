package gapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// Class is the closed error vocabulary (§6.5).
//
// Closed means both directions, and `scripts/gates classes` asserts
// both: every class emitted by the code appears in Classes below, and
// every class in Classes is emitted somewhere. A vocabulary that is only
// checked one way drifts — a sibling documented ten classes while the
// code emitted fifteen, four of them named nowhere.
type Class string

// The twelve classes. The standard names six; the other six are forced
// by this API and each is argued in §6.5.
const (
	// ClassInvalid: the request is malformed or under-specified.
	ClassInvalid Class = "invalid"
	// ClassNotFound: no such calendar or event.
	ClassNotFound Class = "not_found"
	// ClassAuth: not signed in, or a scope is missing.
	ClassAuth Class = "auth"
	// ClassForbidden: signed in, but the access role is insufficient.
	ClassForbidden Class = "forbidden"
	// ClassConflict: the resource state refuses this operation.
	ClassConflict Class = "conflict"
	// ClassStale: the etag moved under you (412). Re-read and retry.
	// Kept apart from conflict because it asks for a different action.
	ClassStale Class = "stale"
	// ClassAmbiguous: a title matched several calendars. Pass an id.
	ClassAmbiguous Class = "ambiguous"
	// ClassBlocked: a guard refused what the API would have allowed.
	ClassBlocked Class = "blocked"
	// ClassRateLimited: 403 or 429 usageLimits.
	ClassRateLimited Class = "rate_limited"
	// ClassUnavailable: a transient upstream failure.
	ClassUnavailable Class = "unavailable"
	// ClassUnsupported: the API cannot do this (§2).
	ClassUnsupported Class = "unsupported"
	// ClassAmbiguousOutcome: a write may or may not have landed. Read
	// before retrying — §2.11 makes this reachable on event creation.
	ClassAmbiguousOutcome Class = "ambiguous_outcome"
)

// Classes is the vocabulary, in the order §6.5 tabulates it. The gate
// derives the emitted set from the code and compares against this.
var Classes = []Class{
	ClassInvalid, ClassNotFound, ClassAuth, ClassForbidden,
	ClassConflict, ClassStale, ClassAmbiguous, ClassBlocked,
	ClassRateLimited, ClassUnavailable, ClassUnsupported,
	ClassAmbiguousOutcome,
}

// Valid reports whether c is in the vocabulary.
func (c Class) Valid() bool {
	for _, k := range Classes {
		if k == c {
			return true
		}
	}
	return false
}

// Retryable reports whether a failure of this class may be retried
// without the caller doing anything. Note what is absent: stale needs a
// re-read, and ambiguous_outcome needs somebody to look.
func (c Class) Retryable() bool {
	return c == ClassUnavailable || c == ClassRateLimited
}

// Error is a classified API failure. It is a tool result, never a
// protocol error (the standard's §2).
type Error struct {
	Class Class
	// Message is what the caller should read. It says what to do, not
	// only what happened.
	Message string
	// Status is the HTTP status, 0 for a failure that never reached
	// Google.
	Status int
	// Reason is Google's own machine reason, kept for the evidence log
	// and for classification. Never a payload.
	Reason string
	err    error
}

// Error renders "[class] message", which is the format the standard
// fixes for a tool result.
func (e *Error) Error() string { return fmt.Sprintf("[%s] %s", e.Class, e.Message) }

// Unwrap exposes the cause.
func (e *Error) Unwrap() error { return e.err }

// Errf builds a classified error.
func Errf(c Class, format string, args ...any) *Error {
	return &Error{Class: c, Message: fmt.Sprintf(format, args...)}
}

// Wrap builds a classified error carrying a cause.
func Wrap(c Class, err error, format string, args ...any) *Error {
	return &Error{Class: c, Message: fmt.Sprintf(format, args...), err: err}
}

// ClassOf returns the class of err, and whether it carried one.
func ClassOf(err error) (Class, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Class, true
	}
	return "", false
}

// googleError is the error envelope every Calendar endpoint returns.
type googleError struct {
	Error struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
		Errors  []struct {
			Domain  string `json:"domain"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"error"`
}

// classify turns an HTTP response into one of the twelve classes.
//
// The mapping is deliberately explicit rather than a range check on the
// status: 403 alone is three different situations here — a missing
// scope, an insufficient access role and a rate limit — and they ask the
// caller to do three different things.
func classify(status int, reason, message string) (Class, string) {
	switch status {
	case http.StatusBadRequest:
		return ClassInvalid, message
	case http.StatusUnauthorized:
		return ClassAuth, "not signed in, or the access token expired: run `google-calendar-mcp login`"
	case http.StatusForbidden:
		switch reason {
		case "rateLimitExceeded", "userRateLimitExceeded", "quotaExceeded", "dailyLimitExceeded":
			return ClassRateLimited, "Google is rate limiting this account: " + message
		case "insufficientPermissions", "forbiddenForServiceAccounts", "ACCESS_TOKEN_SCOPE_INSUFFICIENT":
			return ClassAuth, "the signed-in account has not granted a scope this call needs: run `google-calendar-mcp login` again. " + message
		default:
			return ClassForbidden, message
		}
	case http.StatusNotFound:
		return ClassNotFound, message
	case http.StatusConflict:
		return ClassConflict, message
	case http.StatusGone:
		// 410 on a sync token means the token is no longer usable and
		// the caller must start over. §17.1 keeps sync out of phase 0;
		// the class exists so the day it arrives the failure is not a
		// bare "conflict".
		return ClassStale, "the server discarded the state this request referred to; read again and retry. " + message
	case http.StatusPreconditionFailed:
		return ClassStale, "this changed since you read it, so the write was refused rather than overwriting somebody. Read it again and reapply your change"
	case http.StatusTooManyRequests:
		return ClassRateLimited, "Google is rate limiting this account: " + message
	case http.StatusRequestTimeout, http.StatusInternalServerError,
		http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return ClassUnavailable, message
	}
	if status >= 500 {
		return ClassUnavailable, message
	}
	return ClassInvalid, message
}

// classifyTransport maps a failure that never produced a response.
//
// The URL is stripped before anything here is used in a message,
// because a search term travels in a query string and a transport error
// quotes the URL (§9). stripURL is what does it; this function must
// never be handed a raw *url.Error message.
func classifyTransport(err error) *Error {
	switch {
	case errors.Is(err, context.Canceled):
		return Wrap(ClassUnavailable, err, "the request was cancelled")
	case errors.Is(err, context.DeadlineExceeded):
		return Wrap(ClassUnavailable, err, "the request timed out")
	default:
		return Wrap(ClassUnavailable, err, "could not reach Google: %s", stripURL(err))
	}
}

// stripURL removes the URL from a transport error.
//
// A *url.Error stringifies as `Get "https://...?q=secret": dial tcp`,
// so the obvious fmt of a transport failure puts the caller's search
// term in the log. §9's rule is that a search term hides in a query
// string; this is the function that keeps it out.
func stripURL(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		if ue.Err != nil {
			return ue.Err.Error()
		}
		return ue.Op + " failed"
	}
	s := err.Error()
	// Belt and braces: if anything URL-shaped survived, cut it.
	if i := strings.Index(s, "http"); i >= 0 {
		if j := strings.IndexAny(s[i:], " \""); j > 0 {
			return strings.TrimSpace(s[:i] + s[i+j:])
		}
		return strings.TrimSpace(s[:i])
	}
	return s
}

// Planned names the classes this server declares now and emits in a
// later phase, with the phase that owns each.
//
// It exists so the class gate's second direction — every declared class
// is emitted somewhere — stays on during a phased build instead of being
// switched off. An entry is a dated promise: the gate fails if a class
// here starts being emitted and the entry is not removed, so this map
// can only shrink.
//
// Phase 0 is the read surface. Everything below belongs to the write
// path, which is where a guard can refuse, an etag can move under you,
// and an insert can land twice.
var Planned = map[Class]string{
	ClassBlocked:          "phase 2: the write guards, which refuse what the API would allow",
	ClassAmbiguousOutcome: "phase 2: an insert that may have landed twice (§2.11)",
	ClassUnsupported:      "phase 2: refusing an edit the API cannot make, such as changing a fromGmail event or an eventType after creation (§2.12)",
}

// Note which two are NOT here, because the first draft of this map
// assumed they would be: conflict and stale are already emitted, by
// classify, on 409, 410 and 412. The write path is where a caller most
// often MEETS them, which is not the same as where they are produced —
// and the gate caught the difference.
