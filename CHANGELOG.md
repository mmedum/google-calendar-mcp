# Changelog

All notable changes to this project are documented here.

The format is [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Fixed

- `externalOnly` splits on the organiser's Workspace domain, not on the
  guest's calendar system, so `dry_run` reports how many guests are
  outside that domain. Google documents the parameter as "notifications
  are sent to non-Google Calendar guests only"; spike A gave one guest
  inside the domain and one outside it, both on Google Calendar, and
  `externalOnly` mailed the outside one and skipped the inside one. The
  documentation is wrong about its own parameter, and the design said so
  first (§18 row 40).

- `list_instances` refuses an occurrence id instead of answering with an
  empty series. It used to explain the mistake only when Google returned
  an error; Google returns 200 and expands whatever occurrence the id
  names, and a cancelled one expands to nothing — so the tool reported
  "No occurrences" for a series that has three. An event id is base32hex
  and cannot contain an underscore, so the occurrence shape is
  recognised before the call and refused with the series id to use.
- A cancelled occurrence no longer appears in a series listing. Google
  documents that `showDeleted=false` does not filter cancelled instances
  when recurrences are not expanded, and it sends them with no start and
  no summary — so `list_events` with `no_expand` showed a row with no
  date and no title, and counted it. Cancelled events are filtered by
  the server now rather than by the parameter.
- Continuing a truncated multi-calendar read lost events. A Google page
  token is scoped to one calendar, and `list_events` kept whichever
  calendar produced one last and handed that single token back for all
  of them — so a continuation resumed the wrong calendar from an
  unrelated offset and dropped the others' pages, while the result said
  it was resumable. `next_page_token` now carries one token per
  calendar, and names only the calendars with more to read. It is still
  one opaque string, so nothing about the tool surface changed.
- The event budget was applied twice on a multi-calendar read, once per
  calendar and again to the combined list. The second cut discarded
  events the page token had already moved past, so they were reachable
  from nowhere. The budget is shared out across the calendars before
  they are read, and nothing fetched is thrown away.
- `search_events` accepts `page_token`. It was rendering "Pass
  page_token to continue" for a parameter it did not have.
- An impossible time zone is refused as `[invalid]`, not
  `[unavailable]`. The latter is retryable, so a caller was told to
  retry a request that could never succeed.
- An all-day event sorts to the front of its day east of UTC. Timed
  events were ordered by their UTC instant and all-day events by their
  local date, while the renderer groups by local date — so in
  Asia/Tokyo a day rendered `08:00`, `all day`, `10:00`.
- The event-id rule has one owner. `gcal.ValidEventID` and
  `gcal.SplitOccurrenceID` replace a copy in the live driver and a
  second, separate copy in the service, each of which depended on the
  other's premise.
- `doctor` could print an access token. The tokeninfo URL carries the
  token as a query parameter, and a transport failure stringifies the
  whole URL into the error `doctor` reports — which is the output a user
  pastes into a bug report. The URL no longer survives the error.
- The live driver never prints the body of a result that reaches past
  the calendar it created. `list_calendars` is account-wide, and the
  redactor matches shapes — an address, an id, a URL — while a calendar's
  display name has none, so a dozen real ones reached the transcript.
  Which steps are safe is derived from each step's own arguments against
  the ids the driver invented, not set by hand: the first draft carried
  a per-step flag and missed `get_settings` the same day it was written.
- Spike I's readable half is built, behind `-spike-ceiling`, and answers
  with the reason it cannot answer: Google refuses to create more than 38
  calendars on an account (403 `quotaExceeded`), so a free/busy query of
  51 readable calendars cannot be assembled. The flag stays off by
  default — the limit counts creations and deleting them does not refund
  it, so running the probe spends the quota the driver's own scratch
  calendar needs.
- Spike I stated a verdict it had not established, twice. It sent one
  calendar id 51 times, and Google deduplicated the response to one
  entry, which the spike called a silent truncation at the ceiling. Its
  second draft sent 51 distinct ids but also set `calendarExpansionMax`
  to 50 — so a trimmed response would have been the driver's own cap
  reported as Google's. It now sends distinct ids with no cap, and says
  what it cannot settle.
- The in-memory Calendar reproduces Google where it had been guessing:
  `events.instances` on an occurrence id answers 200 with that
  occurrence rather than 400, and a cancelled instance survives a
  non-expanded list. Both are why the two defects above had no test.

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

- The live driver cancels events that have guests instead of deleting
  them silently. Its cleanup used `sendUpdates=none`, which removes an
  event from the organiser's calendar and leaves it on everyone else's —
  so a day of probe runs left meetings on two real calendars that nobody
  could get rid of. `-sweep-spikes` removes the kept spike events the
  same way, cancelling them to their guests.
- The live driver no longer mails anyone unless asked. Spikes A and B
  sent four real invitations on every run with guest addresses
  configured, which a phase that runs the driver dozens of times would
  have turned into dozens of invitations to a colleague. They need
  `-spike-notify` per run, like `-spike-ceiling`, so a variable left in
  a shell profile cannot do it by accident.
- `schema-diff` has a baseline before the first tag. It compared the
  tool surface against the last git tag and there is no tag, so it
  printed "no previous tag" and passed on every run since the project
  started — inert through exactly the phases that add the most tools. It
  now falls back to `testdata/schema-baseline.json`, written by
  `make schema-baseline`.
- Spike A found a second thing, and it changes a rule rather than a
  parameter: `all` did not reach an out-of-domain guest in three runs,
  while `externalOnly` reached them every time. The event carries both
  guests, so the invitation was accepted and not delivered. §4.3 rule 3
  now refuses to promise delivery for `all` exactly as it already
  refused to promise silence for `none` (§18 row 42).
- Spike A is answered: `externalOnly` follows the organiser's domain,
  and `sendUpdates=none` mailed nobody on insert. The second does not
  soften §4.3 rule 3 — Google warns mail "might still be sent", so one
  silent run is not a promise of silence — but it does retire the fear
  that `none` is routinely noisy (§18 rows 40 and 41).
- Spikes A and B, which set up §15's notification questions and
  deliberately do not answer them: who received mail is visible in an
  inbox and nowhere in any API response, so the driver creates the
  events, says what to look for, and the verdict is written down by
  hand. Guest addresses come from `GCAL_LIVE_GUEST_INTERNAL`,
  `GCAL_LIVE_GUEST_EXTERNAL` and `GCAL_LIVE_GUEST_NONGOOGLE` and never
  enter the repository.
- Spikes E and F, and both are answered. **E**: "this and following"
  resets exceptions after the target, confirmed on a real series — so
  §4.2's warning is accurate and phase 2 must print it. The split came
  from `Set.Split` in `internal/recur`, which makes this the first live check of
  phase 1's scope arithmetic. **F**: two concurrent inserts of one
  client-generated id gave one 200 and one 409, so the collision is
  caught; `ambiguous_outcome` stays, because the API declines to
  guarantee that and the class also covers a retry after a transport
  failure.
- The live driver reuses its scratch calendar. `-keep` leaves it and the
  next run adopts and empties it, spending no calendar-creation quota —
  which matters because that quota counts creations and is not refunded
  by deleting. Event ids are generated per run, since a deleted event
  does not release its id.

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
