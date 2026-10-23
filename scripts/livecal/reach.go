//go:build live

package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Who a run may reach (§9.1).
//
// A default run mails nobody. No event or share it writes names anybody
// but the signed-in account, or an address under .test, a top-level
// domain RFC 2606 reserves, where no mail can arrive. Spikes A and B and the real-guest
// write steps may name other people only when -spike-notify armed the
// run and -spike-invitees named every one of them. reachGuard holds
// that on both of the driver's paths to Google, as primaryGuard does,
// so no step has to remember it.

// reach guards every write the driver makes.
var reach = &reachGuard{}

// reachGuard refuses a write that names somebody the run may not reach.
type reachGuard struct {
	mu sync.Mutex
	// self is the signed-in account's address.
	self string
	// invitees are the addresses -spike-invitees named, lowercased. Empty
	// unless -spike-notify armed the run.
	invitees map[string]bool
	// sweep is -sweep-spikes: kept spike events are canceled to their
	// guests, which mails them.
	sweep bool
}

// addressArgs are the tool arguments that name somebody a write reaches.
var addressArgs = []string{
	"guests", "optional_guests", "rooms", "add_guests", "add_optional_guests", "add_rooms",
	"remove_guests", "who",
}

// setSelf records the account's address, once it has been read.
func (g *reachGuard) setSelf(self string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.self = strings.ToLower(strings.TrimSpace(self))
}

// arm lets the run reach the invitees -spike-invitees named, and with
// sweep, cancel kept spike events to their guests.
func (g *reachGuard) arm(invitees []string, sweep bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.invitees = map[string]bool{}
	for _, a := range invitees {
		g.invitees[strings.ToLower(strings.TrimSpace(a))] = true
	}
	g.sweep = sweep
}

// may reports whether a write may name addr.
func (g *reachGuard) may(addr string) bool {
	a := strings.ToLower(strings.TrimSpace(addr))
	g.mu.Lock()
	defer g.mu.Unlock()
	return (g.self != "" && a == g.self) || underTest(a) || g.invitees[a]
}

// armed reports whether the run may ask Google to mail anybody.
func (g *reachGuard) armed() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.invitees) > 0 || g.sweep
}

// tool decides whether a tool call may go out.
func (g *reachGuard) tool(name string, args map[string]any) error {
	if readTools[name] {
		return nil
	}
	for _, k := range addressArgs {
		addrs, ok := addresses(args[k])
		if !ok {
			return fmt.Errorf("%s was not called: the driver cannot read its %s, so it cannot tell "+
				"who it reaches (§9.1)", name, k)
		}
		for _, a := range addrs {
			if !g.may(a) {
				return fmt.Errorf("%s was not called: its %s names somebody this run may not reach. "+
					"Only -spike-notify with -spike-invitees naming them lets a run reach a person (§9.1)", name, k)
			}
		}
	}
	return nil
}

// rest decides whether one of the driver's own REST calls may go out.
func (g *reachGuard) rest(method, path string, body any) error {
	if method == http.MethodGet {
		return nil
	}
	if _, query, ok := strings.Cut(path, "?"); ok {
		q, err := url.ParseQuery(query)
		if err != nil {
			return fmt.Errorf("%s with a query the driver cannot read was not sent (§9.1)", method)
		}
		for _, v := range q["sendUpdates"] {
			if v != "none" && !g.armed() {
				return fmt.Errorf("%s with sendUpdates=%s was not sent: an unarmed run mails nobody (§9.1)",
					method, v)
			}
		}
		for _, v := range q["sendNotifications"] {
			if v != "false" && !g.armed() {
				return fmt.Errorf("%s with sendNotifications=%s was not sent: an unarmed run mails nobody (§9.1)",
					method, v)
			}
		}
	}
	addrs, ok := bodyAddresses(body)
	if !ok {
		return fmt.Errorf("%s was not sent: the driver cannot read who its body names (§9.1)", method)
	}
	for _, a := range addrs {
		if !g.may(a) {
			return fmt.Errorf("%s was not sent: its body names somebody this run may not reach. "+
				"Only -spike-notify with -spike-invitees naming them lets a run reach a person (§9.1)", method)
		}
	}
	return nil
}

// addresses is every address a tool argument names. An entry may be a
// bare address, a mailbox with a display name, or several of either;
// each part with an @ counts. Anything but a string or a list of them is
// a shape the guard cannot read, so it fails closed.
func addresses(v any) ([]string, bool) {
	var entries []string
	switch v := v.(type) {
	case nil:
		return nil, true
	case string:
		entries = []string{v}
	case []string:
		entries = v
	case []any:
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			entries = append(entries, s)
		}
	default:
		return nil, false
	}
	var out []string
	for _, e := range entries {
		for _, part := range strings.FieldsFunc(e, func(r rune) bool {
			return strings.ContainsRune(",;<>\" \t\r\n", r)
		}) {
			if strings.Contains(part, "@") {
				out = append(out, part)
			}
		}
	}
	return out, true
}

// bodyAddresses is every address a REST body names: an event's
// attendees and an ACL rule's scope. A body of a shape the guard does
// not know fails closed.
func bodyAddresses(body any) ([]string, bool) {
	if body == nil {
		return nil, true
	}
	m, ok := body.(map[string]any)
	if !ok {
		return nil, false
	}
	var out []string
	add := func(v any) bool {
		got, ok := addresses(v)
		out = append(out, got...)
		return ok
	}
	switch at := m["attendees"].(type) {
	case nil:
	case []map[string]any:
		for _, a := range at {
			if !add(a["email"]) {
				return nil, false
			}
		}
	case []any:
		for _, e := range at {
			a, ok := e.(map[string]any)
			if !ok || !add(a["email"]) {
				return nil, false
			}
		}
	default:
		return nil, false
	}
	switch sc := m["scope"].(type) {
	case nil:
	case map[string]any:
		if !add(sc["value"]) {
			return nil, false
		}
	default:
		return nil, false
	}
	return out, true
}

// underTest reports whether addr is under .test, which RFC 2606
// reserves and which can never receive mail. Every invented address the
// driver writes is there. The other reserved names, example.com and its
// siblings, are not let through: this driver's tests use them to stand
// for a real person, so the guard treats them as one.
func underTest(addr string) bool {
	at := strings.LastIndex(addr, "@")
	return at >= 0 && strings.HasSuffix(strings.ToLower(addr[at+1:]), ".test")
}

// armNotify reads -spike-notify and -spike-invitees, and returns the
// invitees the run may reach. -spike-notify is refused unless
// -spike-invitees names exactly the guests the environment configures:
// the person running it types who will be mailed.
func armNotify(notify bool, list string, g guests) ([]string, error) {
	var named []string
	seen := map[string]bool{}
	for _, a := range strings.Split(list, ",") {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" && !seen[a] {
			seen[a] = true
			named = append(named, a)
		}
	}
	if !notify {
		if len(named) > 0 {
			return nil, fmt.Errorf("-spike-invitees is only read with -spike-notify")
		}
		return nil, nil
	}
	if len(named) == 0 {
		return nil, fmt.Errorf("-spike-notify mails real people, so it needs -spike-invitees naming " +
			"every one of them, comma-separated")
	}
	configured := map[string]bool{}
	unnamed := 0
	for _, a := range []string{g.internal, g.external, g.nonGoogle} {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || configured[a] {
			continue
		}
		configured[a] = true
		if !seen[a] {
			unnamed++
		}
	}
	unconfigured := 0
	for _, a := range named {
		if !configured[a] {
			unconfigured++
		}
	}
	if unnamed > 0 || unconfigured > 0 {
		return nil, fmt.Errorf("-spike-invitees must name exactly the guests %s, %s and %s set: "+
			"%d set are not named, and %d named are not set",
			envGuestInternal, envGuestExternal, envGuestNonGoogle, unnamed, unconfigured)
	}
	return named, nil
}
