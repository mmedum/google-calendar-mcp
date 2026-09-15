# Changelog

All notable changes to this project are documented here.

The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- `check_availability` spent an HTTP request per calendar reference
  before it asked anything. The calendar list is cached once, hidden
  calendars included, and an address is used as the id it already is —
  free/busy is the one read that works on a calendar this account cannot
  open. Asking about 55 calendars went from 168 requests to 4, and the
  count the result reports is now the count it spends.
- `check_availability` reported free time it could not know about. With
  every calendar unknown, the text said no free time could be computed
  while the structured half offered the whole window as free. The
  service decides now, so both halves say the same thing.
- `list_instances` showed fewer marks than `list_events` on the same
  event. The two line renderers had separate tag lists and the
  occurrence view had stopped showing four of them: out-of-office, an
  invented end time, a guest list Google truncated, and an event that
  does not make anybody busy.
- An `UNTIL` that is an instant is honoured on an all-day series. The
  timed path applied it and the all-day path ignored it.
- Recurrence expansion: six defects, all found by review after the
  tests were green.
  - An `RDATE` is filtered by the window, the exclusions and the limit
    like any other occurrence. They were appended after the walk, so a
    date decades outside the requested window came back inside it.
  - `BYDAY` is a set, not an order. The weekly walker yielded its days
    in the order they were written, so `BYDAY=WE,MO` and `BYDAY=MO,WE`
    — the same rule — gave different answers.
  - `BYDAY` and `BYMONTHDAY` on a monthly rule intersect, as RFC 5545
    says: "the 13th, when it is a Friday", not whichever was read first.
  - A yearly rule honours `BYDAY`. It was never read, so "the fourth
    Thursday in November" silently used the start's day of the month.
  - A series that ends exactly at the read limit is complete, not
    truncated. It was reported as endless, in the refusal text a scope
    decision is made from.
  - "This and following" counts what the rule generates, not what is
    visible. An excluded date made the truncated series lose an
    occurrence; an added one made it overlap the new series.
- A read that stops at its budget with more to come says so and hands
  back a page token. `list_events` decided truncation from the overflow
  alone, so a read that stopped exactly at its budget reported itself
  complete; `list_instances` asked for a whole page, kept `max_events`
  of it and returned that page's token, skipping everything in between
  while claiming to be resumable.
- A free gap that crosses midnight shows both dates. `17:00-09:00` read
  as ending before it began.
- `login` reports which account signed in. `tokeninfo` returns an email
  only when an email scope was granted and this server asks for none, so
  the account was blank and `status` had a field that could never
  populate. It now comes from the primary calendar's id.
- `status` lists the scopes Google granted, not the ones login asked
  for. The two differ exactly when a scope was refused, which is the
  case worth seeing.
- Tests can no longer reach the real OS keyring. A test redirected the
  config directory and the environment, could not redirect the keyring,
  and revoked the maintainer's own Google grant. `TestMain` substitutes
  it for the whole `cmd` package, with a decoy test holding that.

### Added

- Phase 1: recurrence and availability.
- `list_instances` — the occurrences of one repeating event, with the
  dates that were moved and, on request, the ones that were cancelled. A
  cancelled occurrence is how a single date leaves a series, so the
  result says when it is hiding them.
- `check_availability` — busy intervals and free gaps from
  `freebusy.query`, not from a list of events: a list misses everything
  whose details the caller cannot read and ignores events marked free. A
  calendar that could not be read is reported unknown and never folded
  into free, and the free gaps say how many calendars they were computed
  from. `min_minutes` drops gaps too short to use.
- `internal/recur`, the recurrence model: RFC 5545 rules parsed once for
  the whole server, EXDATE and RDATE, prose explanations, and the three
  scopes of §4.2 with the "this and following" arithmetic phase 2 needs.
  Expansion walks dates and carries the wall clock, so a weekly 09:00
  stays 09:00 across a daylight-saving transition; the table tests cover
  both hemispheres and Lord Howe's 30-minute shift.
- Free-gap arithmetic in `internal/model`, beside the busy intervals it
  works on. Overlapping and touching busy blocks merge, so no gap is
  reported between two meetings that run into each other.
- Two gates this repository claimed to have and did not: `api-fields`,
  one verdict per published field of `Event`, `Calendar`,
  `CalendarListEntry` and `AclRule` (81 fields, 56 modelled, 25 written
  off), and `live-cover`, which fails on a tool with no step in the live
  driver. `make check` is nineteen targets.
- Golden files for the renderers, in `testdata/golden/`, which §13 asked
  for and phase 0 left as an empty directory. `go test ./internal/render
  -update` rewrites them.
- Live driver: steps for both new tools, and spikes C (a recurrence
  written with no time zone), H (free/busy on a calendar nobody can
  read) and I (the 50-calendar ceiling at 51). None of the three has run
  yet.

### Changed

- `check_availability` has its own ceiling of 100 calendars per call
  rather than `GCAL_MAX_CALENDARS`: free/busy answers for 50 calendars
  in one request where a schedule read spends one per calendar. The
  refusal says which limit it is.
- Recurrence rules are explained by `internal/recur` rather than by a
  second parser in the renderer, so a rule reads the same way in a
  result and in a guard. "every 2 weeks on Monday and Wednesday" instead
  of "on Monday, Wednesday", and ordinals ("2nd Tuesday", "last day").

- Phase 0: the read surface, the repository's own gates, and the design.
- Six tools: `list_calendars`, `get_calendar`, `list_events`,
  `search_events`, `get_event`, `get_settings`.
- `login`, `logout`, `status` and `doctor` subcommands. Loopback OAuth on
  the 127.0.0.1 literal with PKCE S256, refresh token to the OS keyring
  with a warned 0600 file fallback, and `--no-browser` for signing in
  over SSH.
- `internal/when`, the time model: an all-day event is a date and never
  becomes an instant, and every timed event carries an IANA zone rather
  than just an offset. Table tests cover daylight-saving transitions in
  both hemispheres, a 30-minute transition, a non-hour offset and a zone
  with no transitions at all.
- Zone resolution in a fixed order — the call, the calendar, the
  account's settings — which refuses rather than falling back to the
  machine's own zone. Every read names which source it used.
- A closed error vocabulary of twelve classes, held from both sides by
  `make classes`.
- A verdict for all 38 published Calendar v3 methods in
  `testdata/api-coverage.tsv`, held by `make api-coverage` against the
  committed discovery snapshot.
- The repository's own gates as Go: coverage floor per package, error
  classes, API coverage, leak scan, transcript redaction, workflow pins,
  make/CI parity, stdio smoke, schema diff and staleness.
- `internal/redact` and `scripts/livecal`, the live driver. It creates a
  scratch calendar, reads only what it wrote, and deletes it; every
  print goes through one redactor, which the transcript gate holds.

### Notes

- Writing events, recurrence, availability and sharing are phases 1 to 3.
  `docs/architecture.md` §16 has the plan.
- **Verified live** against a real Workspace account: 19 steps, none
  failed, transcript read. Spike D confirmed all-day stability from
  UTC-10 and UTC+13; a weekly series held its wall clock across the
  29 March transition.
- Spike G confirmed its positive half. The negative half is not probed
  on purpose: Google's grant is per OAuth client and user, so proving a
  refusal would mean revoking this server's access and logging in again
  to re-confirm what the discovery document states. §18 row 24 has the
  reasoning and why the consequence is contained.
- CI has not yet run on macOS or Windows.
