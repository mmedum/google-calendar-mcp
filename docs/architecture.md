# Architecture — google-calendar-mcp

**Status: phase 2 is built, run live and green (2026-09-16).** Phase 0 —
the scaffolding, the gates, the time model and the six read tools —
phase 1 — `internal/recur`, `list_instances` and `check_availability` —
and phase 2 — `internal/plan`, the five event writes, `If-Match`, the
client-generated id and `dry_run` — are built, verified live and
committed. The surface is thirteen tools; `make check` is green across
nineteen targets; the live driver runs **60 steps against a real account
with none failing** — 7 undetermined by default, being the five that
reach a real person and spikes A and B, all of which need
`-spike-notify` and a configured guest.

**What phase 2's live runs cost and taught.** Six runs. The first failed
five steps: three were the driver's own assertions, which grepped a whole
rendered page for a date or a clock time and so were satisfied by a probe
event that had nothing to do with them. One was a step that cancelled an
occurrence the seed had already cancelled. The fifth was real — Google's
`self` flag is not set on the account's own attendee row on a secondary
calendar, so a write that reached nobody demanded a notification choice
(§18 row 50). Then the transcript, read rather than counted, showed two
more that every gate had passed: **a successful move reported itself as
`[cancelled]`**, because `events.move` answers with that status while the
event sits confirmed on the destination (§18 row 48); and a cancellation
dry run said "Deleted the event" under the words "nothing was written".
And spike J settled the one question phase 2 had left open with a
disclaimer: **`events.move` does honour `If-Match`** (§18 row 49), so
§4.4 has no exception and `move_event` takes an etag like everything
else.

**Every spike except one is answered (§15).** A, B, C, D, E, F, H and I.
That matters because §16's phase 2 says its refusal wording waits on A,
B, E and F, and it no longer does. Spike G's negative half is still
owed, and phase 3 builds the `GCAL_SHARING=off` path it needs, so it
stops being a separate errand there.

**What the live work cost and taught, in one line each.** The first run
failed one step and was quietly wrong about two that passed. Spike I
reported a verdict about a ceiling it never reached. A field's published
description misled twice, on `showDeleted` and on `sendUpdates`. Three
consistent runs of spike A were consistent because the instrument was,
and only a second receiver on the same event separated sending from
delivery. The driver deleted guest-carrying events with `none` for a
day and left meetings on two real calendars that the organiser could no
longer withdraw. Nine of §18's forty-four rows were written or rewritten
on 2026-09-16, four of them correcting something this document had
asserted earlier the same day.

**Still owed:** spike A's `externalOnly` arm and spike B, which need an
out-of-domain guest and a non-Google one and cannot be scored by any
driver (§15); spike G's negative half; and **CI has never run on macOS
or Windows** — the workflow covers all three platforms and the branch
has never been pushed. Both platforms cross-compile clean, including the
tagged tests, so a first run is unlikely to fail on compilation; the
keyring, the file fallback's permissions and path handling are untested.

**What phase 1 found in phase 0's own record.** Three gates this
document listed as part of `make check` did not exist: `api-fields`,
`live-cover` and `mcpb`. `parity` did not notice, because it compares
`make check` with CI and both were equally short — a list of gates is
not a gate. `internal/gcal`'s doc comment said a gate held its field
list; it did not. `testdata/golden/` was an empty directory while §13
described golden files as part of the test surface. Phase 1 built the
first two gates and the golden files; `mcpb` belongs with the bundle in
phase 4. The first thing `live-cover` did was fail on both tools phase 1
had just added, which is the argument for it.

**What the live run found that the gates could not.** Two defects, both
in code that reads as obviously correct. Event ids are base32hex —
lowercase `a`–`v` and the digits, so `w`, `x`, `y` and `z` are refused —
and the driver's own ids used `y`; Google answered "Invalid resource id
value" naming neither the field nor the rule. And `login` never reported
which account had signed in, because `tokeninfo` returns an email only
when an email scope was granted and this server asks for none, leaving
`status` with a field that could never populate. Neither is the kind of
thing a unit test against a fake would have produced, which is why §13
says green gates are not done.

**A test signed the maintainer out of their own Google account, and the
repair is the interesting part.** `TestLogoutWithNothingStored`
redirected the config directory and the environment, and could not
redirect the OS keyring — so it found the real refresh token under the
`default` profile, revoked the grant at Google and deleted it. The
sibling servers document this exact failure, and the warning is quoted
in this repository's own `internal/credentials` source: it was written
here *before* it happened. A rule stated is not a rule kept. The fix is
structural rather than a reminder — `TestMain` substitutes the keyring
for the whole `cmd` package before any test runs, so reaching the real
one is impossible rather than discouraged, and a decoy test fails if a
refactor drops it.

**What the gates caught while being built, which is the argument for
having them.** The class gate found six classes it called dead; the gate
was wrong about four of them (it excluded the file where `classify`
emits most of the vocabulary) and right about two. The parity gate
reported a divergence that did not exist, by matching CI for a literal
string instead of mapping targets to the steps that run them. The
coverage gate double-counted every block, because `-coverpkg` makes each
test binary emit a profile for every package. The staleness gate found
the README naming two tools that do not exist and this document naming a
package path that does not. The smoke gate found the server refusing to
start without credentials — so a client could not list its tools before
login, and every host would have logged that as a crash. The race
detector found a data race in the test fake. Each of those was a rule
that read as true and was false in the code, which is what the standard's
preamble says to expect.

**Everything here was checked against the Calendar API v3 discovery
document** (`www.googleapis.com/discovery/v1/apis/calendar/v3/rest`,
revision 20260826, fetched 2026-09-15), the Calendar guides, and the
public MCP calendar servers named in §1. §18 is the evidence log. Eleven
of its thirty-two rows refute an assumption this design started out
holding, and one of them reversed the single most consequential
decision in it — §4.3, where the maintainer's own proposed default
turned out to be the thing Google warns in writing can lose a user's
events.

**The three things worth reading first**, because they are where this
server differs from every calendar integration surveyed:

- **§4.1 — a date is not a time.** All-day events are carried as dates
  and never as instants. Every other calendar tool surveyed normalises
  them to UTC midnight, which puts them on the wrong day for every user
  west of UTC.
- **§4.2 — the recurrence scope is required, never inferred.** "Change
  the event" is three different operations on a recurring series and
  Google's API makes them look like one. The server refuses to guess
  which was meant.
- **§4.3 — notification is a required choice with no default.** Not
  "never notify", which Google warns can lose events; not Google's own
  default, which is unspecified, inconsistent between methods and free
  to change.

§2 is what the platform forces, §8a the verdict on all 38 published
methods, §16 the phase plan, §17 the decisions still open.

This document is written so that whoever picks the work up can start
from the repository alone: read the status line, §16 for the phase, §17
for anything open, and begin.

## 1. Mission and scope

A production-grade, Go, stdio MCP server that lets Claude work with
**time**: read a schedule and know exactly which hours in which zone it
is looking at, find when people are free, create and change events
without silently dropping the guests already on them, and say afterwards
what changed and who was told. Single binary, per-user OAuth against the
user's own Google account, Workspace or consumer.

**The repository is self-contained and meant to be distributed.** Every
deployer creates their own Google Cloud project and OAuth client;
nothing deployer-specific is baked into the code, the repository or the
release artifacts (§9, §12).

**Scope is the calendar, every capability.** In: calendars and the
subscribed list, events and their recurrences, availability, sharing,
colours and the user's own settings. Out (**decided**): everything a
meeting *produces* rather than *is* — the Meet recording, the notes
document, the file attached to an event — which belongs to the servers
built on the Drive and Docs APIs. Google Tasks is a separate API and a
separate server. A conference attached to an event is a Calendar field
and is in scope; the Meet API's recordings and transcripts are not
(§17.3).

**The boundary sentence, for tool descriptions:** this server answers
*when*, and hands off *what*.

Tool names are chosen so a client can run this server beside the Drive,
Docs and Sheets ones without a collision: `search_events` and
`list_calendars` here, where those say `search_files`,
`search_documents`, `search_spreadsheets`.

### Why build it (research summary, verified 2026-09-15)

Eight public Google Calendar MCP servers were surveyed. Every one is
Node or Python. They converge on the same shape — a `createEvent`, an
`updateEvent`, a `listEvents` — and they converge on the same three
defects, which §3 states as requirements. Those defects are not
incidental: each one is the natural result of modelling a calendar as a
list of rows, which is what an API client library hands you.

The gap worth building into is not more tools. It is a server that is
correct about **time**, **recurrence** and **who gets emailed** — the
three things a calendar has that a spreadsheet does not, and the three
things the surveyed servers get wrong.

### Non-goals

- **Push notifications.** `events.watch`, `acl.watch`, `settings.watch`
  and `channels.stop` need a public HTTPS endpoint Google can POST to. A
  stdio server has nowhere to put one. Out, permanently (§8a).
- **Calendar migration.** `events.import` exists to carry a private copy
  of an event between systems, preserving `iCalUID` and organiser. It is
  a migration tool, not a scheduling one, and the failure modes are
  entirely different. Out (§8a).
- **Admin operations.** `calendars.transferOwnership` requires a
  Workspace administrator privilege and `useAdminAccess: true`. This
  server authenticates as one ordinary user and never requests admin
  access. Out (§8a).
- **A local event cache or sync loop.** `syncToken` is designed for a
  client that keeps a replica. This server is stateless between calls;
  a replica it cannot invalidate is a replica that lies. §17.1 keeps the
  question open for a later phase, and §4.5 is what is done instead.
- **Natural-language event parsing.** `events.quickAdd` parses a string
  like "Dinner Thursday 7pm". The model calling this server is a better
  parser than Google's, and quickAdd hides which fields it set and in
  which zone it read the time. Out (§8a) — this is a refusal on quality
  grounds and §17.4 records the argument against it.

## 2. Hard constraints from the platform

These are not preferences. They are what the API does, and the design
either accommodates them or lies.

1. **An `EventDateTime` is `date` XOR `dateTime`.** `date` is
   `yyyy-mm-dd` and means an all-day event. `dateTime` is RFC3339 and
   requires an offset unless `timeZone` is set alongside it. The two are
   not convertible: an all-day event has no instant, and forcing one
   picks a day.
2. **`timeZone` is required on a recurring event's start and end.** The
   discovery document states it outright. A recurrence expanded from a
   UTC instant drifts an hour at every daylight-saving boundary; one
   expanded from a zoned wall-clock time does not.
3. **`events.update` is a PUT and replaces the whole resource.** Omit
   `attendees` and every guest and every RSVP is gone. Omit `recurrence`
   and the series is flattened. `events.patch` is the partial one.
4. **ETags and `If-Match` are fully supported, and return 412.** Calendar
   is the one Google Workspace API that documents optimistic concurrency
   properly. `If-Match: *` forces the write through without a read.
5. **`sendUpdates` has no consistent default.** On events it is
   effectively `none`/false. On `acl.insert`, `acl.patch` and
   `acl.update` the equivalent `sendNotifications` defaults to **true**.
   Sharing a calendar emails somebody unless you say otherwise; creating
   an event does not unless you say otherwise. Same API, opposite
   defaults.
6. **`none` is not a guarantee of silence.** Every method's
   documentation carries the sentence "some emails might still be sent
   even if you set the value to false".
7. **Google warns against `none` on `events.insert` specifically.** Its
   own enum description: "Using the value `none` can have significant
   adverse effects, including events not syncing to external calendars
   or events being lost altogether for some users." No other method
   carries this warning.
8. **"This and following" is two API calls, and it resets exceptions.**
   Google's recurring-events guide: set `UNTIL` on the original, insert
   a new series from the target. "Changing all following instances
   resets any exceptions happening after the target instance."
9. **`singleEvents` changes what `events.list` returns.** False (the
   default) returns recurring *parents* with their `recurrence` and no
   instances. True returns the expanded *instances* and no parents.
   `orderBy: startTime` requires `singleEvents: true`.
10. **`freebusy.query` covers at most 50 calendars** per request
    (`calendarExpansionMax`, maximum value 50), and returns per-calendar
    errors rather than failing the whole query.
11. **A client may supply an event `id` on insert**, base32hex, 5–1024
    characters, unique per calendar. But: "Due to the globally
    distributed nature of the system, we cannot guarantee that ID
    collisions will be detected at event creation time." Nearly
    idempotent, not idempotent (§11).
12. **`eventType` cannot be modified after creation**, and `fromGmail`
    events cannot be created at all. Birthdays, focus time, out-of-office
    and working-location events are not ordinary meetings and carry their
    own property blocks.
13. **Cancelled events are hidden by default.** `events.list` returns
    them only with `showDeleted: true` or on an incremental sync;
    `events.get` always returns them.
14. **Quotas: 10 000 requests/minute per project, 600/minute per user**,
    on a sliding window, returning 403 or 429 `usageLimits`.
15. **`acl.list` is not covered by `calendar.readonly`.** It needs
    `calendar.acls` or `calendar.acls.readonly`. `acl.get` *is* covered
    by `calendar.readonly`. Reading one rule and reading the list of them
    take different scopes (§18).

### What the API cannot do (so we don't promise it)

- **No server-side "this and following".** It is a client-side pattern
  built from two calls, and it loses exceptions (§2.8).
- **No transactional multi-event write.** There is no batch endpoint in
  the union sense; creating three events is three requests, and the
  second can fail after the first succeeded.
- **No search across calendars.** `events.list` takes one `calendarId`.
  Searching several is several requests, fanned out by this server.
- **`q` is a free-text match, not a field query.** It has no documented
  syntax, no field scoping and no guarantee about which fields it reads.
  A result set from `q` is a suggestion (§7.2).
- **No way to know whether mail was actually sent.** The API reports
  nothing about notifications. `sendUpdates` is a request, and §2.6 says
  it is not even an honoured one.

## 3. Requirements distilled from other servers' failures

Each row is a defect found in a surveyed server or its issue tracker,
and the requirement it forces here.

| Failure seen | Requirement |
|---|---|
| All-day events normalised to UTC midnight, rendering on the wrong day for every user west of UTC | A `date` is carried as a date end to end and never becomes an instant (§4.1) |
| Recurring events written with a UTC instant, so a weekly 09:00 drifts to 08:00 after a daylight-saving change | Every recurring write carries an IANA `timeZone`; the server refuses a recurrence without one (§4.1) |
| `sendUpdates` never set, so invitations that reported success reached nobody | `notify` is required on every write that can reach a person; there is no default (§4.3) |
| "Update the event" applied to a whole series when one instance was meant, or the reverse | `scope` is required on every event write: `instance`, `series` or `this_and_following` (§4.2) |
| `events.update` used for a partial change, silently dropping attendees and RSVPs | The server never calls `events.update`. Patch, with `If-Match` (§4.4) |
| Availability answered by listing events, missing events whose details the caller cannot see | Availability comes from `freebusy.query`, which sees busy blocks without details (§4.6) |
| The server's own timezone (or the container's UTC) used as the user's | The zone is read from the user's settings or the calendar, never from the process, and every read names the zone it used (§4.1) |
| A truncated event list presented as the whole schedule | Every read states its window, its zone and whether it was truncated (§4.5) |

## 4. Core design bets

### 4.1 Time is always zoned, and a date is not a time

The single largest source of defects in this category of software. Three
rules, held by tests and by the type system.

**An all-day event is a date.** `internal/when` has two distinct types:
a `Date` (`yyyy-mm-dd`, no zone, no instant) and a `Zoned` (an instant
plus the IANA zone it was expressed in). There is no conversion from
`Date` to `Zoned` in the package, because there is no correct one — the
caller must supply a zone and say so, and only the renderer does that,
for display, labelled.

**Every `dateTime` carries its IANA zone, not just an offset.** An
offset is a fact about one moment; a zone is a rule. `+02:00` is not
enough to expand a recurrence correctly across a daylight-saving
boundary, which is why §2.2 makes `timeZone` mandatory there. The server
sends `timeZone` on every write, recurring or not, so the same code path
is exercised every time rather than only on the rare path.

**The zone is read, never assumed.** In order: the zone the caller named
on the call; else the target calendar's `timeZone`; else the user's
`timezone` setting from `settings.list`. Never the process's local zone
and never UTC-as-a-fallback. Every read names which of the three it used
and what it resolved to. This is the direct analogue of the sibling
Sheets server's "sheet names are read, never assumed", and it exists for
the same reason: the default that looks harmless on the maintainer's
machine is wrong on everyone else's.

A consequence worth stating: **the server has no opinion about "today"**
until it has resolved a zone. A tool that takes a relative window
resolves the zone first and prints the absolute window it derived.

### 4.2 The recurrence scope is required, never inferred

"Change the 10:00 standup to 10:30" is three different operations and
the API makes them look like one. A tool that takes an event id and a
patch cannot tell which was meant, and the surveyed servers guess.

Every event write takes a required `scope`:

- **`instance`** — one occurrence. Addressed by the instance's own id, or
  by the series id plus an `original_start`. Creates an exception.
- **`series`** — the parent event, and therefore every occurrence that is
  not already an exception.
- **`this_and_following`** — the two-call pattern of §2.8. The server
  performs it, and its result says in plain words that **exceptions
  after the target instance were reset**, because Google's guide says
  they are and no caller expects it.

There is no default. A write against an event that has a
`recurringEventId` or a `recurrence` field and no `scope` is refused with
`[invalid]` and a message naming the three choices and what each would
do here — including, for `series`, how many occurrences it would reach.

Google's own guidance is enforced as a warning, not a refusal: "Do not
modify instances individually when you want to modify the entire
recurring event." When a caller writes the same field to the same value
across several instances of one series, the result says a `series` write
would have been one call.

### 4.3 Notification is a required choice, and the server has no default

**This bet reversed during design, and the reversal is the point.** The
maintainer's proposal — and the obvious reading of the sibling Drive
server's "no mail unless `notify` is set" — was to default `sendUpdates`
to `none` everywhere. Checking it against the discovery document refuted
it: Google's own enum description for `none` on `events.insert` says
"Using the value `none` can have significant adverse effects, including
events not syncing to external calendars or events being lost altogether
for some users." A safe-looking default is the one documented to lose
data.

The opposite default is no better. Google's own defaults are
**inconsistent between methods** (§2.5): events default to not
notifying, ACL rules default to notifying. Adopting "whatever Google
does" means the same `notify`-shaped decision has opposite outcomes in
two tools, for a reason nobody can see from the call.

So: **`notify` is a required parameter on every write that can reach
another person, and the server refuses the call if it is absent.** No
default, in either direction. The refusal names who would be reached.

Five supporting rules:

1. **The refusal is specific.** `[invalid] this event has 4 guests; pass
   notify to say whether they are emailed` — with the count, never the
   addresses (§9).
2. **A write that reaches nobody does not ask.** An event with no
   attendees on a calendar shared with nobody has no `notify` decision to
   make, and demanding one is friction with no safety in it. The
   parameter is required only when the write can actually reach a person.
3. **A result says what the server asked for, never what a guest
   received.** `none` is never reported as silence, because Google says
   some mail may go out anyway (§2.6). `all` is never reported as
   delivery, because a notification Google sends can still fail to
   arrive: spike A watched one invitation reach one of its two guests
   and not the other, the difference being the receiving provider and
   nothing in the request (§18 row 42). The server knows what it asked
   for; it does not know what landed, and will not say it does.

   **And `none` on a cancellation is worse than quiet — it is a lie.**
   Deleting an event with `none` leaves it on the guests' calendars
   while removing it from the organiser's (§18 row 43). `cancel_event`
   therefore says, in its result, that the guests still have the meeting
   unless they were notified.
4. **`none` is refused when a guest is outside the organiser's domain**,
   not merely warned about. Such a guest may have no Google Calendar at
   all, and then mail is the only channel that exists: spike B invited a
   non-Google address with `none`, it received nothing, and there was no
   calendar for the event to land in. The guest cannot discover the
   event by any means, while the organiser's copy shows them invited.
   That is §2.7's "lost altogether" with the mechanism visible, and it
   is structural rather than a defect — so it earns a refusal rather
   than a warning a caller can skim (§18 row 44).
5. **`dry_run` shows the blast radius before anything is sent**: how
   many guests would be notified, how many of them are outside the
   organiser's own domain and so are the ones `externalOnly` reaches, and
   what the event would look like afterwards.

   **The axis is the organiser's Workspace domain, and it took a live
   probe to establish that.** This document first said "internal versus
   external"; the discovery document appeared to refute it, describing
   `externalOnly` as "notifications are sent to non-Google Calendar
   guests only", so the wording was changed to split by calendar system;
   then spike A mailed the out-of-domain guest and skipped the
   same-domain one, both of them on Google Calendar (§18 row 40). The
   original wording was right and the documentation was wrong.

   The count is computed from the organiser's primary calendar id, which
   is the address, against each attendee's domain — never printed, only
   counted (§9).

The ACL tools carry the same parameter with the same requirement, which
is what makes the two consistent where Google's defaults are not.

### 4.4 A write never destroys what it cannot see

The sibling Sheets server had to record a deviation here: a rectangle of
cells cannot be minimally diffed, so it refuses instead. Calendar does
not need the deviation. The API supports partial updates and optimistic
concurrency properly, so the standard's rule is implementable as
written.

- **`events.update`, `calendars.update`, `calendarList.update` and
  `acl.update` are never called.** They are PUTs. §8a writes each of them
  off by name, and a gate holds the client to it: the four methods have
  no client function, and adding one fails the API-coverage check.
- **Every write is a patch**, carrying only the fields the caller asked
  to change.
- **Every patch carries `If-Match` with the etag from the read that
  produced the plan.** A 412 becomes `[stale]` with "re-read and try
  again", never a silent retry — a retry here would apply the caller's
  intent to a resource somebody else has since changed.
- **`If-Match: *` is available and is not the default.** It exists for
  the caller who genuinely means "whatever it says now", and saying so is
  an explicit flag.
- **Guest lists are read before they are written.** Adding an attendee is
  a read-modify-write on the array, never a replacement of it, so an RSVP
  that arrived between the read and the write is reported as `[stale]`
  rather than overwritten.
- **`events.move` carries `If-Match` too, and finding that out is the
  argument for §15.** It is not a patch — a POST with the destination in
  the query string and no body — and nothing Google publishes says the
  header applies. So this server sent none and its result told callers
  the protection was absent, which was honest about the uncertainty and
  wrong about the API. Spike J sent a stale etag and got **412**, so the
  rule has no exception: every write here is made under `If-Match`
  (§18 row 49).

### 4.5 Every read states its window, its zone and its completeness

The analogue of the Sheets server's "every read shows addresses". A
model asked "am I free Thursday afternoon" must not be able to answer
from a different Thursday, a different zone, or the first page of three.

Every read result carries: the absolute window queried, the IANA zone
the times are rendered in and where that zone came from (§4.1), which
calendars were read, how many events matched, how many are shown, and a
continuation token when there are more. A truncated read says so in the
text a model sees, not only in a structured field it may not be shown
(§10 and the standard's note on `content` versus `structuredContent`).

Reads are budgeted in events, not requests, with the budget stated in
the result.

### 4.6 Availability is computed, never inferred from a list

`freebusy.query` returns busy intervals for calendars whose *details* the
caller cannot read. Answering "is this person free" by listing their
events misses everything they have marked private, and ignores
`transparency` — an event marked "free" is not busy, and a list-based
answer counts it.

So availability is `freebusy.query`, fanned out in batches of 50 (§2.10),
with two rules: a calendar that returned an error is named in the result
as **unknown**, never folded into "free"; and the result distinguishes
"no busy blocks" from "could not read this calendar", because a model
that cannot tell those apart will book over somebody.

### 4.7 One tool call is one API request

Held everywhere except where it cannot be, and the exceptions are named
in the tool description rather than hidden: `this_and_following` is two
writes (§2.8), an availability query across more than 50 calendars is
one request per batch (§2.10), and a cross-calendar search is one request
per calendar (§2, *no search across calendars*). Each of those says in
its result how many requests it made.

### 4.8 Own wire types, raw REST

No `google.golang.org/api`. The wire types live in `internal/gcal`,
hand-written for the fields actually used, and `internal/gapi` calls the
REST endpoints directly. §8b is the field-level coverage gate that keeps
"the fields actually used" an argued list rather than an accident.

### 4.9 Results say what changed, and who was told

Every write returns: what the resource looked like before, what changed,
what it looks like now, how many people were notified and by which
`sendUpdates` value, the new etag, and — where §2.6 applies — that
silence is not guaranteed. A write result that says only "ok" is a
defect.

## 5. Module layout

Derived from `go list ./...` and checked by the staleness gate, per the
standard's "derive the package map, or delete it".

- `cmd/google-calendar-mcp/` — subcommands (`login`, `logout`, `status`,
  `doctor`) and server wiring.
- `internal/config/` env plus bound flags; `internal/credentials/`
  keyring → file → env; `internal/userconfig/` non-secret profile state;
  `internal/fileperm/` restricting a file to the account that wrote it;
  `internal/auth/` loopback OAuth and the token source.
- `internal/gcal/` wire types; `internal/gapi/` the raw REST client, with
  `caltest/` the in-memory Calendar used by tests.
- `internal/when/` dates, zoned times, windows and the zone-resolution
  order of §4.1. No network, no clock of its own — the clock is injected.
- `internal/recur/` RRULE parsing and formatting, instance expansion for
  what the server must reason about locally, and the three scopes of
  §4.2.
- `internal/model/` the server's view of a calendar, an event and a busy
  interval; `internal/render/` text output; `internal/plan/` the typed
  write ops and the guards of §4.2, §4.3 and §4.4 — no network, no
  client, so every guard is testable on its own;
  `internal/service/` orchestration and policy;
  `internal/tools/` the MCP tools; `internal/server/` SDK wiring and the
  schema dump.
- `internal/redact/` the log and transcript redactor.
- `scripts/gates/` the repository's own checks, as Go; `scripts/livecal/`
  the live driver, which also carries the live probes of §15;
  `scripts/evals/` (phase 4) the model-facing scoring harness.
- `testdata/` synthetic fixtures, renderer goldens, the API surface
  snapshot and coverage records of §8a and §8b, and the recorded
  tool-schema baseline.

## 6. Addressing

### 6.1 Referring to a calendar

Three forms, in this order:

1. **`primary`** — the authenticated user's own calendar. The API's own
   alias; passed through.
2. **A calendar id.** For a user's primary calendar this is their email
   address; for a secondary calendar an opaque id. Ids are the contract.
3. **A title**, resolved against `calendarList.list`. A title matching
   more than one calendar is `[ambiguous]` with the candidates and their
   ids, never the first match — the sibling Drive server's rule, and the
   failure mode is the same here.

A calendar the user is not subscribed to does not appear in
`calendarList.list` and cannot be resolved by title. Its id still works.
The refusal says so, because "not found" for a calendar the caller can
read is a lie.

### 6.2 Referring to an event

By **event id plus calendar id**. An event id is unique per calendar, not
globally, so an id without a calendar is not an address.

An instance of a recurring series is addressed either by its own id or by
the series id plus `original_start` (§4.2). The server accepts both and
says which it used, because `originalStartTime` is the stable one:
§2 confirms it identifies the instance even after the instance is moved.

`iCalUID` is accepted on lookup (`events.list` takes it) and is never
used as the primary address: one `iCalUID` is shared by every occurrence
of a series, which is exactly the ambiguity §4.2 exists to remove.

### 6.3 Referring to a time

Callers pass one of:

- an **all-day date**, `yyyy-mm-dd`;
- a **zoned time**, RFC3339 with an offset, plus an optional IANA zone
  name that takes precedence for recurrence expansion;
- a **window**, two of the above, resolved and echoed absolutely.

Relative expressions ("next Tuesday") are the model's job, not the
server's — the model knows the user's intent and the server does not.
What the server does is make the resolution checkable: it echoes the
absolute window and the zone it used, so a model that resolved "Tuesday"
wrongly can see it did (§4.5).

### 6.4 Referring to a recurrence

An RRULE string, RFC 5545. The server parses it to validate and to
explain it back in prose ("every two weeks on Tuesday, 10 times"), and
sends the caller's string. It does not compose RRULEs from structured
fields: the round trip through a struct is where a `BYDAY` gets lost.

### 6.5 Error classes

Closed vocabulary, derived from the code by `scripts/gates classes` and
asserted in both directions — every class the code emits is listed here,
every class listed here is emitted somewhere. The standard's six, plus
five this API forces:

| Class | Means | Caller should |
|---|---|---|
| `invalid` | the request is malformed or under-specified | fix the arguments |
| `not_found` | no such calendar or event | check the address |
| `auth` | not signed in, or the scope is missing | run `login` |
| `forbidden` | signed in, but the ACL role is insufficient | ask for access |
| `conflict` | the resource state refuses this operation | read and reconsider |
| `stale` | the etag moved under you (412) | re-read and retry |
| `ambiguous` | a title matched several calendars | pass an id |
| `blocked` | a guard refused what the API would have allowed | pass the override, or don't |
| `rate_limited` | 403/429 `usageLimits` | back off (§11) |
| `unavailable` | a transient upstream failure | retry |
| `unsupported` | the API cannot do this | see §2 |
| `ambiguous_outcome` | a write may or may not have landed | read before retrying |

`ambiguous_outcome` earns its place from §2.11: a client-supplied event id
makes an insert *nearly* idempotent, and Google declines to guarantee the
collision is caught. A retry that might double-book is not a retry the
server performs silently.

`stale` and `conflict` are kept apart deliberately. They ask the caller to
do different things, and collapsing them into `conflict` — which is what
the standard's six-class list would do — loses the one piece of
information that makes 412 actionable.

## 7. Reading and writing

### 7.1 Locating and describing

`list_calendars` is the entry point and is cheap. It returns, per
calendar: id, title, the IANA zone, the caller's `accessRole`, whether it
is primary, whether it is selected and hidden in the UI, and its colour.
The zone is on this result specifically so the zone-resolution order of
§4.1 can be followed without a second call.

`get_calendar` adds the description, the sharing exposure (§7.6) and the
default reminders.

### 7.2 Reading events

`list_events` takes a calendar (or several), a window, and a required
decision about recurrence: `expand` (`singleEvents: true`, instances,
orderable by start time) or `series` (`singleEvents: false`, parents with
their RRULEs). §2.9 makes these return different things, and the surveyed
servers pick one silently; the caller picks here, and the result says
which it got.

Defaults that are decided: cancelled events are excluded unless asked
for (§2.13); `maxResults` is the API's 250 per page and the server pages
to its own event budget (§4.5); `eventTypes` is unfiltered, but the
renderer marks birthdays, focus-time, out-of-office and working-location
events as what they are rather than as meetings (§2.12).

`search_events` is `events.list` with `q`, fanned out across the named
calendars. Its description states plainly that `q` is undocumented
free text with no field scoping (§2), so a model treats an empty result
as "found nothing" rather than "there is nothing".

`get_event` returns one event whole. `list_instances` expands one series.

### 7.3 Availability

`check_availability` is `freebusy.query` (§4.6), batched at 50, returning
busy intervals per calendar plus an explicit **unknown** for any calendar
that errored. It also reports the free gaps in the window, computed from
the busy set, in the resolved zone — because the question behind the
question is almost always "when can we meet", and making the model do
interval arithmetic over a list is how a meeting gets booked at 02:00.

Three details are decided, all in phase 1:

- **A calendar missing from the response is unknown**, exactly like one
  that errored. That is what a query truncated at `calendarExpansionMax`
  looks like from here: no busy list, no error, no row.
- **An address is not resolved before it is asked about.** Free/busy is
  the one read that works on a calendar this account cannot open, so
  resolving the reference first would refuse the query the tool exists
  for. A title is still resolved, because a typo that silently became an
  id would come back "unknown" and read as a real answer.
- **Its own ceiling.** One call asks about at most 100 calendars, not
  `GCAL_MAX_CALENDARS`, because the cost is different: a schedule read
  spends a request per calendar and this spends one per fifty. The
  refusal says which limit it is, so nobody changes the wrong setting.

`min_minutes` drops gaps too short to use. Working hours are NOT applied
(§17.2).

### 7.4 Writing events

`create_event`, `update_event`, `cancel_event`, `move_event`,
`respond_to_event`.

Every one of them: takes `notify` when the write can reach a person
(§4.3); takes `scope` when the target is recurring (§4.2); patches rather
than replaces, under `If-Match` (§4.4); and returns a before/after with
the notification report (§4.9).

`create_event` generates the event id client-side (§2.11) so a retry
after an ambiguous failure is nearly idempotent, and reports
`ambiguous_outcome` rather than retrying when it is not sure.

`cancel_event` is `events.delete` for a single event and a
`status: cancelled` patch for one instance of a series — two different
API shapes behind one honest verb, with the result naming which happened.
It is **not** behind the destructive flag: cancelling a meeting is an
ordinary calendar action, it notifies by the same `notify` rule, and
Google keeps the record. §9 has the line.

`respond_to_event` sets the caller's own `responseStatus` and comment. It
is separate from `update_event` because RSVPing is not editing, the
permissions differ, and a model that conflates them will try to RSVP by
patching the whole attendee array.

### 7.5 Calendars and the subscribed list

`create_calendar`, `manage_calendar`. The second covers three things the
API splits across two resources and which users do not distinguish:
renaming or re-zoning the calendar itself (`calendars.patch`),
subscribing and unsubscribing (`calendarList.insert` / `delete`), and the
per-user overrides — colour, hidden, selected, notification settings
(`calendarList.patch`).

The tool description names the distinction that matters:
**unsubscribing removes it from your list; it does not delete it or
affect anybody else.** Deleting is `delete_calendar`, gated (§9).

### 7.6 Sharing

`list_sharing`, `share_calendar`, `unshare_calendar` over the ACL
resource, following the sibling Drive server's sharing rules because the
hazard is identical.

- **Exposure is shown before and after.** A share result says who could
  see this calendar before and who can now.
- **`notify` is required** (§4.3) — and here Google's default is *true*
  (§2.5), so the requirement is what stops a quiet reshare from becoming
  a surprise email, and equally what stops a deliberate one from being
  silently suppressed.
- **A public calendar needs an explicit flag.** `scope.type: default`
  means "anyone", and it is the one ACL value that cannot be undone from
  the other side. It requires `allow_public: true` on the call.
- **Roles are explained, not echoed.** `writerWithoutPrivateAccess` and
  `freeBusyReader` are not self-explanatory; the renderer says what each
  can actually see.
- **`GCAL_SHARING=off` removes the three tools**, as `GDRIVE_SHARING=off`
  does in the sibling.

## 8. Tool surface

Twenty tools. Read-only mode registers the first eight and requests only
the read scopes (§10).

| Tool | API methods | Notes |
|---|---|---|
| `list_calendars` | `calendarList.list` | entry point; carries the zone (§7.1) |
| `get_calendar` | `calendars.get`, `calendarList.get`, `acl.list` | exposure needs the ACL scope (§2.15) |
| `list_events` | `events.list` | `expand` vs `series` required (§7.2) |
| `get_event` | `events.get` | |
| `list_instances` | `events.instances` | |
| `search_events` | `events.list` (`q`) | fanned out; `q` is unscoped (§2) |
| `check_availability` | `freebusy.query` | batched at 50; free gaps (§7.3) |
| `get_settings` | `settings.list`, `colors.get` | the user's zone and week start |
| `create_event` | `events.insert` | client-side id (§2.11) |
| `update_event` | `events.patch` | `scope` + `notify` + `If-Match` |
| `cancel_event` | `events.delete`, `events.patch` | not gated (§7.4) |
| `move_event` | `events.move` | between calendars |
| `respond_to_event` | `events.patch` | RSVP only (§7.4) |
| `create_calendar` | `calendars.insert` | |
| `manage_calendar` | `calendars.patch`, `calendarList.insert/patch/delete` | three axes (§7.5) |
| `list_sharing` | `acl.list` | |
| `share_calendar` | `acl.insert`, `acl.patch` | `notify` required; public needs a flag |
| `unshare_calendar` | `acl.delete` | |
| `delete_calendar` | `calendars.delete` | **gated**, needs `confirm` |
| `clear_calendar` | `calendars.clear` | **gated**, needs `confirm` |

Resources, for clients that attach rather than call: `gcal://calendars`
(the list), `gcal://calendars/{id}` (the card) and
`gcal://calendars/{id}/events/{id}` (one event). They carry the same
content as the matching tools and no handles.

### 8a. Every published method, with a verdict

The API-coverage gate of the standard, instantiated. Two files:
`testdata/api-surface.json`, written by `gates api-diff` from the
discovery document and edited by nobody; and
`testdata/api-coverage.tsv`, one hand-written verdict per method. The
offline gate holds three things: a published method with no verdict
fails, a client call with no row fails, and a verdict on a method that no
longer exists fails.

All 38 methods of revision 20260826 are accounted for. 24 used, 14
written off.

| Method | Verdict | Reason |
|---|---|---|
| `acl.delete` | used | `unshare_calendar` |
| `acl.get` | out | `acl.list` returns every rule; a single-rule read has no caller |
| `acl.insert` | used | `share_calendar`, new rule |
| `acl.list` | used | `list_sharing` and `get_calendar`'s exposure |
| `acl.patch` | used | `share_calendar`, role change |
| `acl.update` | out | PUT, replaces the rule whole — §4.4 |
| `acl.watch` | out | needs a public webhook — §1 non-goals |
| `calendarList.delete` | used | `manage_calendar` unsubscribe |
| `calendarList.get` | used | `get_calendar`, per-user overrides |
| `calendarList.insert` | used | `manage_calendar` subscribe |
| `calendarList.list` | used | `list_calendars` |
| `calendarList.patch` | used | `manage_calendar`, colour and visibility |
| `calendarList.update` | out | PUT — §4.4 |
| `calendarList.watch` | out | webhook |
| `calendars.clear` | used | `clear_calendar`, gated |
| `calendars.delete` | used | `delete_calendar`, gated |
| `calendars.get` | used | `get_calendar` |
| `calendars.insert` | used | `create_calendar` |
| `calendars.patch` | used | `manage_calendar`, title and zone |
| `calendars.transferOwnership` | out | needs Workspace admin privilege and `useAdminAccess` — §1 |
| `calendars.update` | out | PUT — §4.4 |
| `channels.stop` | out | webhook |
| `colors.get` | used | `get_settings`; names the colour ids |
| `events.delete` | used | `cancel_event`, whole event |
| `events.get` | used | `get_event` |
| `events.import` | out | migration, not scheduling — §1 |
| `events.insert` | used | `create_event` |
| `events.instances` | used | `list_instances` |
| `events.list` | used | `list_events`, `search_events` |
| `events.move` | used | `move_event` |
| `events.patch` | used | every event write — §4.4 |
| `events.quickAdd` | out | the model parses better and quickAdd hides what it set — §17.4 |
| `events.update` | out | PUT, drops attendees and recurrence — §4.4 |
| `events.watch` | out | webhook |
| `freebusy.query` | used | `check_availability` |
| `settings.get` | out | `settings.list` returns all of them in one request |
| `settings.list` | used | `get_settings`, the user's zone |
| `settings.watch` | out | webhook |

### 8b. Field coverage

Methods are the coarse axis; this API keeps its capability in the `Event`
resource, which has 44 properties. A second gate, `gates api-fields`,
holds one verdict per published field of `Event`, `Calendar`,
`CalendarListEntry` and `AclRule`: modelled in `internal/gcal`, or
written off with a reason. Without it, "we support events" hides the fact
that `attachments`, `extendedProperties`, `gadget` and the four
event-type property blocks were never considered.

Phase 1 writes the record: 81 fields, 56 modelled and 25 written off,
each with a reason. `api-diff` records the field list from the discovery
document alongside the methods, so a field Google adds arrives as a gate
failure naming it. The write path fills verdicts in as the phases reach
them, and a reason of the form `planned=N` names the phase that will.

Phase 0 was supposed to write this record and did not, while
`internal/gcal`'s own doc comment said a gate held the list. Nothing
noticed, because `parity` compares `make check` with CI and both were
equally short.

## 9. Confidentiality, security, safety

Nothing deployer-specific ever enters the repository: no calendar ids or
URLs, account or attendee email addresses, organisation names, Cloud
project ids, OAuth client ids or secrets, and no title, description,
location or guest list from a real calendar. This holds for code, docs,
fixtures, goldens, transcripts, commit and tag messages, pull requests
and logs.

A calendar is worse than a spreadsheet here, and the difference shapes
the gate. **Every event carries other people's email addresses**, and an
attendee list is personal data about people who never consented to this
repository existing. A leak here is not an embarrassment, it is a
disclosure.

### 9.1 What may never enter the repository, and what stops it

Two structural rules, not matters of care:

1. **Fixtures are generated, never recorded.** A fixture copied from a
   live response is itself the leak, whatever a scanner says about it.
2. **The live driver reads only what it wrote**, on a scratch calendar it
   created and **empties at the start of every run** — so the only
   content that can reach a log, a golden or a test failure is content
   the driver invented.

   The emptying is what carries the guarantee, not the deleting. A run
   deletes the calendar afterwards only if it created it; `-keep` leaves
   it, and the next run adopts and empties it instead of making another.
   That is not tidiness, it is §18 row 36: the calendar-creation quota
   counts creations and is not refunded by deletion, so a phase with many
   live runs cannot afford one calendar per run. Event ids are generated
   per run for the same reason — a deleted event does not release its id,
   and Google answers a re-insert with 409.

The leak gate is an allow-list, and it is anchored on shapes the server's
own generated fields cannot take — an `@` with a dot-suffixed domain, a
known URL prefix, a literal keyword before an id — per the standard's
warning about a four-character fixture value colliding with a timestamp.
The gate's exemption list is asserted, so a third entry is an argued
decision rather than a quiet widening.

**Logging.** Method, tool, outcome, duration, a truncated calendar id.
Never: an email address, an event title, description or location, a
search term, or an attendee count in a context that identifies the
event. A search term reaches a log through a request URL, so transport
errors are stripped of their query string before they are logged. A test
fails if any forbidden value appears in a log at any level.

**Destructive tools are unregistered** unless
`GCAL_ENABLE_DESTRUCTIVE=true`, and each still needs `confirm: true` on
the call. The two behind it are `delete_calendar` and `clear_calendar`.
`clear_calendar` deserves a note: it deletes every event on the user's
**primary** calendar and cannot be undone. It is the single most
destructive call this API offers.

**Where the destructive line falls, and why it is not where it first
looks.** `cancel_event` is not gated. Cancelling a meeting is what a
calendar is for; it is reversible in practice (the event is retained and
visible with `showDeleted`); and gating it would put a flag between the
model and the most ordinary write there is, training people to set
`GCAL_ENABLE_DESTRUCTIVE=true` permanently — which would then also arm
`clear_calendar`. A gate everybody turns on protects nobody. The
protection `cancel_event` gets instead is §4.2's required `scope`, so
cancelling a series can never be a slip of the wrist, and §4.3's required
`notify`.

**Read-only mode** (`GCAL_READONLY=true`) registers only the eight read
tools and requests only the read scopes.

## 10. Auth, config, process model

Settled by the standard's §3b and not re-derived: loopback IP literal on
a random port, `http://127.0.0.1:{port}/callback`, PKCE with S256, no
out-of-band flow, refresh token to the OS keyring with a 0600 file
fallback and a warning. `--no-browser` prints the URL, and the README
carries the SSH port-forwarding section, verified against the binary's
own output.

**Scopes**, generated from the code and gated (the standard's rule that a
scope list a person must paste into a consent screen is an input, not a
description):

- Read-only: `calendar.readonly`, `calendar.settings.readonly`,
  `calendar.acls.readonly`. The third is separate because §2.15 —
  `acl.list` is not covered by `calendar.readonly`, which is the kind of
  thing that surfaces as a 403 from one tool weeks after setup.
- Read-write: the above plus `calendar.events`, `calendar.calendars`,
  `calendar.calendarlist`, `calendar.acls`. Deliberately **not** the
  broad `calendar` scope, which grants everything including the two
  gated tools regardless of the flag.

Config is env plus bound flags, prefix `GCAL_`. The client JSON path is
`--client-secret` / `GCAL_CLIENT_SECRET`, the same name as in every
sibling.

Both halves of every tool result are sent whenever a tool declares an
output schema — `content` as the readable presentation and
`structuredContent` as the machine one, never the same bytes — because a
client shows one or the other and the failure to guard against is the
half that carried the substance being filtered away.

## 11. Reliability

**Retries follow the HTTP method, and the exception is named.** GET,
PUT, DELETE and PATCH are retried with truncated exponential backoff —
`min((2^n) + jitter, 32s)`, which is Google's own documented algorithm.
`freebusy.query` is a POST and is a pure read, so it is retried; the call
site says why.

`events.insert` is the one that cannot be. §2.11 makes it *nearly*
idempotent — the server generates the id — but Google declines to
guarantee collision detection, so an ambiguous failure is reported as
`ambiguous_outcome` with the id it used, so the caller can look rather
than double-book.

`events.move` is a POST that is not idempotent and is not retried.

**`events.patch` IS retried, and the cost is a duplicate notification.**
A patch is a stated end state, so a retry lands in the same place — but
it carries `sendUpdates`, so a retry after a 429 or a 503 can ask Google
to mail the guest list a second time. The alternative is failing a write
that would have succeeded, on a transient error, and leaving the caller
to decide whether it landed. A second invitation is an annoyance; a
write reported failed that actually succeeded is the `ambiguous_outcome`
this design works hardest to avoid. So the retry stays, and this
paragraph exists so it is a decision rather than an accident.

**Rate limiting.** 600 requests per user per minute on a sliding window
(§2.14). The fan-out tools — `search_events` across calendars,
`check_availability` across batches — are where a single tool call can
spend dozens of requests, so they are the ones with a concurrency limit
and a budget stated in the result. A 403 or 429 `usageLimits` becomes
`[rate_limited]` and is retried with backoff; exhausting the retries
reports how many requests the call had already spent.

**Clock skew.** The server's own clock is used only for token expiry and
for resolving a relative window, and in the second case the resolved
absolute window is always printed (§4.5), so a skewed container clock
shows up in the output instead of silently shifting a query.

## 12. Distribution and setup

As the standard's §10 and §10b, with nothing calendar-specific: six
platform archives, `checksums.txt`, an SBOM per archive, a keyless cosign
signature over the checksums, `actions/attest-build-provenance`, built
with `-trimpath` and `mod_timestamp`. A `.mcpb` bundle on every release,
packed in Go from the universal binary's post hook, named in
`checksum.extra_files` and `release.extra_files`, with the manifest
validated against the staged tree by a gate that runs on every commit —
all six referential checks of the standard, each watched failing before
it is believed. A `doctor` subcommand that checks credentials, scopes and
API reachability. The MCP registry entry last.

The one thing to say in the manifest's `long_description`, because it is
the first-run failure: the bundle does not log you in, and cannot — the
user needs their own OAuth Desktop client and one `login` from a
terminal first.

## 13. Testing

- **`internal/gapi/caltest`** — an in-memory Calendar covering calendars,
  events, recurrence expansion, ACL rules and free/busy, generated and
  never recorded (§9.1). It is the bulk of the test surface.
- **Table tests on `internal/when` and `internal/recur`**, and this is
  where the effort goes. Daylight-saving boundaries in both directions,
  the southern hemisphere, a zone with a non-hour offset, a zone whose
  rules changed, all-day events either side of UTC, and a recurrence
  crossing a transition. §4.1 is a claim and these are what make it one
  that can fail.
- **Golden files** for the renderer, so a change to how a schedule reads
  is visible in the diff.
- **A coverage floor of 80% per package**, not an average.
- **`scripts/livecal`** — the live driver, against a calendar it creates
  and deletes, with a step per tool option, held by the `live-cover`
  gate; every print routed through the one redactor, held by the
  `transcript` gate.
- **`scripts/evals`** (phase 4) — tasks a model must complete through the tools,
  scored. The three that matter most are the three failures of §3: an
  all-day event created from a user in a negative-offset zone, a weekly
  recurrence that must survive a daylight-saving change, and an
  invitation that must actually reach an external guest.

`make check` and CI run the same set, asserted by the `parity` gate:
`fmt`, `vet` (including the tagged tests), `tidy`, `lint`, `cover`,
`vuln`, `licenses`, `secrets`, `api-coverage`, `api-fields`, `classes`,
`leaks`, `transcript`, `live-cover`, `parity`, `pins`, `schema-diff`,
`smoke`, `staleness` — nineteen targets.

`schema-diff` compares the built tool surface against the last tag, and
against `testdata/schema-baseline.json` when there is no tag. The
fallback exists because there was no tag: the gate reported "no previous
tag" on every run from the first commit onwards, which is the whole
stretch where the surface changes most — inert exactly when it was most
needed. `make schema-baseline` records the current surface, and
refreshing it is the deliberate act of saying the change has been looked
at, which is what tagging says at a larger scale. The gate reports and
never fails: removing a tool is sometimes right, and the definition of
done says a person reads this one.

`mcpb`, the bundle manifest gate, is the one this list named before it
existed. It arrives with the bundle in phase 4; until then it is in §16
as owed rather than here as done. Three gates were named here while
absent — `api-fields`, `live-cover` and `mcpb` — and the first two were
built in phase 1. A list of gates is not a gate, which is the same
mistake, one level up, that this family of checks exists to catch.

**Green gates are not done.** Anything touching the write path or a
response shape gets a live run before it counts, and **the transcript is
read** — a sibling's driver twice reported success while its results were
wrong.

## 14. Confirmed decisions and their consequences

Not to be reopened without a reason written into §18.

1. Go, one language, no interpreter in `make check`.
2. Raw REST, own wire types (§4.8).
3. Patch always, PUT never (§4.4) — and therefore four methods written
   off in §8a and a gate holding it.
4. `notify` required, no default, on every write that can reach a person
   (§4.3).
5. `scope` required on every write to a recurring event (§4.2).
6. A date is never an instant (§4.1).
7. Availability from `freebusy.query`, never from a list (§4.6).
8. `cancel_event` is not behind the destructive flag; `delete_calendar`
   and `clear_calendar` are (§9).
9. No webhooks, no local replica, no migration, no quickAdd (§1).
10. Tool names disjoint from the three sibling servers (§1).

## 15. What must be verified live

Spikes, each a question the documentation does not answer, run against a
real account before the phase that depends on it ships. A spike that
prints something looking like an answer to a question it did not ask is
the failure mode here — a sibling's spike did exactly that — so each one
states its question and its verdict separately.

- **Spike A — the notification truth.** §2.6 says some mail is sent even
  with `none`. Which mail? Create an event under each of `none`,
  `externalOnly` and `all`, and record who actually received what.
  §4.3's wording depends on the answer.

  **Three guests, not two, and the third is the one that matters.** This
  spike was written as "an internal and an external guest", which embeds
  the assumption §18 row 40 refutes: `externalOnly` means *non-Google
  Calendar*, not *outside the organisation*. So it needs a guest inside
  the domain, a guest outside it who is still on Google Calendar, and a
  guest who is not on Google Calendar at all — and only the third
  exercises `externalOnly`.

  **No driver can return a verdict here.** Who received mail is visible
  in an inbox and nowhere in the API, so the driver creates the events,
  says exactly what to look for, and the verdict is written into §18 by
  hand. A spike that scored itself green on this would be scoring
  something else.

  **Answered 2026-09-16, over four runs, and the fourth is the one that
  made the first three readable.** Three guests in the end: one inside
  the organiser's domain, one outside it on Gmail, one outside it and
  not on Google Calendar at all.

  - `externalOnly` reached **both** out-of-domain guests and not the
    same-domain one, whatever calendar system they use. The axis is the
    Workspace domain, and the discovery document's "non-Google Calendar
    guests only" is wrong about its own parameter (§18 row 40).
  - `all` reached the same-domain guest and the non-Google guest, and
    never the Gmail guest — across three runs and both orderings. That
    looked like an API behaviour until the non-Google address was put on
    the same events, at which point one send was visible at two
    receivers and the difference turned out to be Gmail (§18 row 42).
  - `none` reached nobody, in a run where every other arm demonstrably
    did, so §2.6's "some mail is sent even with none" does not reproduce
    on insert (§18 row 41).

  **Three consistent runs were consistent because the instrument was.**
  Repetition looked like evidence and was not; what settled it was a
  second receiver on the same event, not a fourth attempt.
- **Spike B — `sendUpdates: none` on insert.** Google warns it can lose
  events (§2.7). Reproduce or fail to reproduce. If it reproduces, §4.3
  may need to refuse `none` on insert outright.

  **Confirmed 2026-09-16, and the mechanism is plainer than the warning.**
  A non-Google address was invited to an event inserted with `none`. It
  received nothing — in a run where the same address had just received
  both `all` and `externalOnly`, so the channel was working. A guest
  outside Google Calendar has no calendar for the event to appear in, so
  mail is the only way they can learn of it, and `none` removes the only
  way. The event exists with them attached and they cannot discover it.

  So §4.3 refuses `none` when a guest is outside the organiser's domain
  rather than warning about it (§18 row 44). The refusal is on the
  domain rather than on the calendar system because the domain is what
  the server can actually tell from an address.
- **Spike C — daylight saving.** A weekly recurrence at 09:00 local,
  spanning a transition, written with a zone and written without one.
  Confirm the drift and confirm its absence. **Both halves answered,
  2026-09-15.** The zoned half holds its wall clock (§18 row 23). The
  unzoned half never drifts, because Google **refuses it**: HTTP 400,
  "Missing time zone definition for start time". §2.2's "required" is
  the API's rule and not the reference page's, so the drift this spike
  was built to reproduce is unreachable through a recurrence.
- **Spike D — all-day events west of UTC.** Create one from a
  negative-offset zone and read it back from a positive-offset one.
  This is §3's first row and the most common defect in the category.
- **Spike E — "this and following".** Build the two-call pattern over a
  series that already has an exception after the target, and confirm the
  exception is reset (§2.8), so §4.2's warning is accurate. **Confirmed,
  2026-09-16.** An eight-occurrence weekly series, the sixth occurrence
  moved half an hour later, split at the fourth: the original kept
  `COUNT=3`, the new series took `COUNT=5`, and the moved occurrence came
  back at its **scheduled** time. The exception was reset, so §4.2's
  warning is accurate and `this_and_following` must say so in its result.
  The split came from `Set.Split` in `internal/recur` rather than from arithmetic
  invented in the spike, so this also holds phase 1's implementation
  against Google — a spike that computes the answer its own way tests
  nothing the write path will do.
- **Spike F — the duplicate insert.** Send the same client-generated id
  twice, concurrently, and see whether the collision is caught (§2.11).
  Determines whether `ambiguous_outcome` is the right class or an
  over-cautious one. **Answered 2026-09-16: the collision WAS caught** —
  one insert returned 200 and the other 409, from two requests in flight
  together. That does not retire `ambiguous_outcome`, and the reason is
  worth stating rather than assuming: §2.11 declines to *guarantee* this,
  so one observation is not a promise; and the class is also for the
  retry after a transport failure, where the caller never saw the first
  answer at all and Google's 409 would arrive for an event the caller
  itself created. The class stays, with one fewer reason to fear it.
- **Spike J — does `events.move` honour `If-Match`?** §4.4 puts every
  write under it, and `events.move` was the one this server could not
  place: a POST with no body, and neither the reference nor the discovery
  document says whether the header applies. The server sent none and said
  so in the result, which is an assumption wearing a disclaimer.

  **Answered 2026-09-16: it is HONOURED.** A probe event was created, its
  etag read, the event patched so that etag went stale, and the move sent
  with the stale one: **412**. So the exception was a hole rather than a
  fact, `move_event` takes an etag now, and §4.4 covers every write
  without qualification.

  The discriminator is the point. A CURRENT etag would have succeeded
  whether the header is honoured or ignored and settled nothing — which
  is the failure this section opens by naming, and which spike I
  committed once already.
- **Spike G — ACL scopes.** Confirm §2.15: that `acl.list` fails under
  `calendar.readonly` alone. It is a scope-set decision and a 403 weeks
  later if wrong.
- **Spike H — free/busy on an unreadable calendar.** Query a calendar the
  user cannot read and confirm the per-calendar error shape §4.6 depends
  on. **Confirmed, 2026-09-15.** The query succeeds; the unreadable
  calendar comes back as its own entry carrying an error, which the
  server reports UNKNOWN with "Do not treat this as free"; and the free
  gaps say they were computed from 1 of 2 calendars. §4.6 stands on the
  shape the API actually returns.
- **Spike I — the 50-calendar ceiling.** Confirm `calendarExpansionMax`
  behaves as documented at 50 and at 51. It goes through the driver's own
  API rather than the tool, because the server batches at 50 and would
  never produce the request the spike is about. **Answered 2026-09-15,
  on the second attempt — and the first attempt is the lesson.** It sent
  one calendar id 51 times. Google keys the response by calendar id, so
  51 copies came back as one entry, and the spike read "fewer than asked
  for" as a silent truncation and reported it as its verdict. It was
  deduplication. That is precisely the failure this section opens by
  naming, committed by a spike written to avoid it, and no gate can
  catch it — only reading the transcript did. The second draft made the
  same mistake one level down: it sent 51 distinct ids and also set
  `calendarExpansionMax` to 50, so a response carrying 50 would have
  been the driver's own cap read as Google's ceiling. With distinct ids
  and no cap, all 51 come back, neither refused nor trimmed. Fifty of them were
  unreadable, so it does not settle 51 readable calendars on its own.
  That half is built and gated behind `-spike-ceiling`, and **it has been
  run: Google refused to create the 39th calendar**, 403 `quotaExceeded`,
  "Calendar usage limits exceeded" (§18 row 36). An account cannot hold
  enough calendars to reach the ceiling, so the readable half is not
  merely unrun but unrunnable, and §2.10 stays unsettled for readable
  calendars by decision. The flag stays, and stays off: the limit counts
  creations and is not refunded by deleting them, so running it spends a
  quota the driver's own scratch calendar needs (§18 rows 12 and 36).

## 16. Delivery phases

Each phase is one session and ends in a tagged release that waits for an
explicit "go". The next session starts from this repository alone.

**Phase 0 — skeleton, gates, time and reading (v0.0.1). Built and run
live 2026-09-15.** Done: the scaffolding, the gates, `internal/when`
with its table tests, the read client and the in-memory Calendar, the
six read tools, both API records, the redactor and the transcript gate,
and `make check` green on Linux across seventeen targets. The live
driver ran twice against a real Workspace account: 19 steps, none
failed. Spike D passed from UTC-10 and UTC+13; the DST assertion passed
on real data. Reading the transcript found the two defects the status
line names, neither of which any test against a fake would have
produced. Outstanding: **spike G's negative half**, and CI has never run
on macOS or Windows. Not tagged: `main` is the maintainer's. The
scaffolding of §12 and §13: Makefile, golangci, govulncheck,
go-licenses, gitleaks, goreleaser, CI, CodeQL and release workflows,
Dependabot, issue and PR templates, `CONTRIBUTING.md`.
`login/logout/status/doctor`; `config`, `credentials`, `userconfig`,
`auth`; `gapi` with the read methods; **`internal/when` complete with the
table tests of §13**, because everything else stands on it; `render` for
the calendar card and the schedule; `caltest` with calendars, events and
free/busy; tools `list_calendars`, `get_calendar`, `list_events`,
`get_event`, `search_events`, `get_settings`; the API surface snapshot
and both coverage records of §8a and §8b; the gates green on three
platforms, including the closed error-class gate, which is built now
rather than later because it costs an afternoon and is the difference
between a vocabulary and a habit. Spikes D and G. A live run whose
transcript is read.

**Phase 1 — recurrence and availability (v0.1.0). Built and run live
2026-09-15.** `internal/recur`: RFC 5545 rules parsed once for the
whole server, expansion that walks dates and carries the wall clock
(§2.2), EXDATE and RDATE, and the three scopes of §4.2 with the
`this_and_following` arithmetic phase 2 will call. `list_instances` and
`check_availability`, taking the read surface to eight tools. The
free-gap arithmetic of §7.3, in `internal/model` beside the busy
intervals it works on. The `expand`/`series` decision was already
built in phase 0.

Phase 1 also repaid two of phase 0's debts, both of them gates this
document listed as done while they did not exist: **`api-fields`** (§8b,
81 verdicts) and **`live-cover`**, which holds every published tool to
having a step in the live driver — it caught both new tools immediately.
`testdata/golden/` was an empty directory; the renderers have golden
files now. Outstanding: **the `mcpb` bundle gate**, which belongs with
the bundle in phase 4, and **spike G's negative half** from phase 0.

Spikes C, H and I have run and are answered in §15. Spike G's negative
half is still owed, and still needs a profile granted without the ACL
scopes; §18 row 24 argues why the cost of leaving it is contained.

**What the live run found, which is the argument for having it.** Three
runs against a real account: 29 steps, and the first run failed one and
was wrong about two more that passed.

The failure was `list_instances` accepting an **occurrence** id as a
series id. The refusal was reactive — it explained the mistake when
Google answered 404 or 400 — and §18 row 31 had recorded, honestly, that
nobody knew what Google actually did. What it does is answer **200 and
expand that occurrence**; a *cancelled* occurrence expands to nothing,
so the call succeeded with an empty list and the tool reported "No
occurrences" for a series that has three. A wrong answer, confidently
phrased, from the reactive design. The server reads the id's shape now:
an event id is base32hex, so an `_` cannot occur in one, and an
occurrence id is refused up front naming the series to use instead.

The second defect passed its step. `list_events` with `no_expand` showed
a row reading `(undated) (no start) (no title)`, and counted it.
`showDeleted=false` does not filter a cancelled **instance** when
`singleEvents` is false — the discovery document says so in the
parameter's own description — and Google sends such an instance bare,
with no start and no summary. The server had passed the parameter and
trusted it. It filters cancelled events itself now. `caltest` had been
hiding them, which is why no test could have caught it, and it
reproduces Google's behaviour now instead.

The third was in a spike, and it is the one worth remembering. **Spike I
reported a verdict it had not established**: it sent one calendar id 51
times, Google deduplicated to a single entry, and the branch reading
"fewer came back than were asked for" announced a silent truncation.
§15's opening paragraph names that exact failure mode, and the spike
written under that warning committed it. Distinct ids give the real
answer, and the spike now states what it cannot settle as well as what
it can.

And a fourth, which is neither a defect in the server nor in a test:
**the transcript printed a dozen of the account's real calendar names.**
§9.1 promises the driver reads only a calendar it created, but
`list_calendars` is account-wide by nature, and the redactor is anchored
on shapes — an address, an id, a URL — while a display name has none. No
pattern could have caught them (§18 row 34).

The first fix for it was a flag on the step, and **it was wrong in the
way §9.1 forbids**: a flag is a matter of care, and the review found the
proof in the same commit — `get_settings` is account-wide too and had
not been marked. What ships instead derives the answer from the step's
own arguments. A step names the calendars it reads; if every one of them
is an id this driver invented, the body can only hold what the driver
wrote, and a step naming none is account-wide by construction. It fails
closed, and on the next run it withheld `get_settings` without anybody
marking it.

**What the phase 1 review found, and what was left.** Thirteen defects,
none of which the test suite caught — every gate was green when the review
started, and the reviews that found them ran the code rather than
reading it.

Six were in `internal/recur`, and they share a shape: the expansion was
right about the hard thing (a wall clock across a transition) and wrong
about the ordinary ones. An `RDATE` was appended after the walk and so
escaped the window; `BYDAY` was walked in the order it was written, so
`WE,MO` and `MO,WE` answered differently; `BYDAY` and `BYMONTHDAY` were
treated as alternatives where RFC 5545 intersects them; a yearly rule
ignored `BYDAY` entirely, which is every "fourth Thursday in November"
holiday; a series ending exactly at the read limit was reported as
endless, in the refusal text §4.2 makes a scope decision from; and
"this and following" counted visible occurrences where a COUNT counts
generated ones, so an excluded date lost an occurrence and an added one
made the two halves overlap. Each is now a test written as the
reproduction that found it.

Two were about paging, and they are the same mistake twice: a page is
the unit Google's token points past. `list_instances` asked for a page
of 250, kept the caller's `max_events`, and handed back the token for
the whole page — losing everything in between while saying the read was
resumable. `list_events` decided truncation from the overflow alone, so
a read that stopped exactly at its budget called itself complete. The
fake was complicit: it ignored `maxResults`, so neither could be
reproduced against it. It honours it now, which is what Google does.

And two in the tool surface: `check_availability` resolved every calendar
reference through `ResolveCalendar`, which re-listed the account's
calendars once per reference because the cache only ever held the list
*without* hidden calendars — and then spent two more failing round trips
per address that was not in it. Asking about 55 colleagues cost 168 HTTP
requests to set up a query the result reported as 2, which is §4.7's
"one tool call is one API request" failing quietly in the direction the
result cannot show. One cached list and no resolution for an address
takes it to 4, and a test now fails if the count creeps back. A second
defect: with every calendar unknown, the text said free time could not
be computed while the structured half still offered the whole window as
free — the two halves of one result disagreeing about exactly the thing
§4.6 exists to prevent. The decision moved into the service, where both
halves read it.

Three smaller ones, all of them things a reader would have seen and a
test did not: `list_instances` and `list_events` had separate tag lists,
so an occurrence never said it was out of office, had no end time set,
or carried a guest list Google had truncated; an `UNTIL` that is an
instant was honoured on a timed series and ignored on an all-day one;
and a free gap crossing midnight rendered as `17:00-09:00`, which reads
as ending before it began.

Four suggestions were considered and not taken, recorded here rather
than lost:

- **One generic expansion driver in `internal/recur`.** `ExpandDates`
  and `ExpandTimes` share the date walker — the part that matters — but
  repeat the loop around it. The duplication had already caused one
  drift, an instant `UNTIL` honoured on the timed path and ignored on
  the all-day one; that is fixed and tested. The generic version is
  worth doing when phase 2 gives the all-day path its first caller,
  which is also when it can be tested against real use.
- **One table-driven record gate.** `api-coverage`, `api-fields` and
  `live-cover` are the same shape three times: a published set, a
  hand-written verdict set, failure in both directions, a reason on
  every write-off, a tally. Collapsing them restructures phase 0's gates
  and phase 1 is not the place. The concrete gap that review found *is*
  fixed: `api-coverage` never implemented the "a client call with no row
  fails" direction this document and CLAUDE.md rule 11 both promised.
- **`live-cover`'s exemptions as a TSV**, like the other two records.
  Kept as a map in the driver's own source deliberately: an exemption
  says why a tool cannot be driven, and it belongs beside the steps it
  is an exception to.
- **`MaxFreeBusyCalendars` as a setting.** §7.3 decided the two ceilings
  differ, not that both should be configurable. One more knob whose
  right value is the API's own limit is a knob nobody should turn.

**Phase 2 — writing events (v0.2.0). Built and run live 2026-09-16.** `internal/plan` — the typed draft, the patch body and the three
guards — plus `create_event`, `update_event`, `cancel_event`,
`move_event` and `respond_to_event`, taking the surface to thirteen
tools. The three declared-but-unemitted error classes are all emitted
now, so `gapi.Planned` is empty and the class gate holds all twelve from
both sides for the first time.

**What the phase decided, beyond building what §7.4 listed.**

- **`gcal.EventPatch`, with every field a pointer.** `Event`'s own
  `omitempty` strings cannot tell "leave it" from "clear it", so a patch
  built from `Event` makes "remove the location" unsayable while looking
  like it worked. A nil field is absent from the JSON; a non-nil field
  pointing at an empty value clears it.
- **An all-day `end` is the LAST day, inclusive, on the way in.** Google's
  wire end is exclusive and the renderer has always shown it inclusive,
  so taking it inclusive here is what makes the value a caller reads the
  value a caller writes. The conversion is in one place and the tool
  descriptions say it.
- **`unsupported` is emitted where THIS server cannot build the
  operation, not where Google is guessed at.** §2.8 says there is no
  server-side "this and following": the only way to have one is to
  truncate the original series and insert a new one, which takes the
  right to rewrite the series. An attendee answering an invitation has no
  such right and a move between calendars has nothing to split, so for
  those two the operation does not exist. That is a fact about this
  construction rather than an unverified claim about the API, which is
  what rule 13 asks for.
- **`Set.Split` was refactored rather than copied** for the all-day path.
  §16's phase-1 note said the generic version was worth doing when phase
  2 gave the all-day path its first caller; it did, so `splitAt` now owns
  the COUNT arithmetic that was wrong twice, and `Split` and `SplitDates`
  are two ways of counting the head. `Reach` and `ReachDates` went the
  same way.
- **`cancel_event` is its own tool Kind.** It registers without
  `GCAL_ENABLE_DESTRUCTIVE` — §9's argument stands — but its annotation
  says destructive, because a client deciding whether to ask a person
  deserves the truthful hint. The flag and the hint are different
  questions and the code now treats them that way.
- **The insert is ambiguous only when the answer is.** A 400 or a 403
  definitely did not land. A transport failure or a 5xx may have, so
  those become `[ambiguous_outcome]` naming the id to read. A
  `this_and_following` write that truncates and then fails to insert gets
  the same class with a sharper sentence: the later occurrences are gone
  until somebody recreates them.

**What the fake was hiding, which is the phase's own lesson.**
`caltest` did not resolve Google's `primary` alias, and nothing noticed
because `Seed` gave a calendar the literal id `primary`. No real account
has one — a primary calendar's id is the account's email address, which
is also what §4.3.5 splits the guest count on. The first write test
against a realistic fixture failed with "no calendar with that id". A
second fixture problem was the same shape: `Seed`'s event ids contain
hyphens, which base32hex forbids, so an occurrence address — series id,
underscore, scheduled start — could not be exercised against it at all.
Both are fixed, and both are the §13 point restated: a fake that is
easier than the API makes a whole path untestable while every test is
green.

**The live run, which is where three more defects came from.** Six runs,
55 steps, and the last of them green. Three of the first run's five
failures were the driver's own: an assertion that greps a whole rendered
page for a date or a clock time can be satisfied by a line it is not
about, and a probe seeded on 19 March at 13:00 satisfied two of them. An
assertion now looks at the rows carrying the event's own title, and at
those with a clock on them when it is asking about a clock. A fourth was
a step cancelling an occurrence the seed had already cancelled.

The fifth was real and is §18 row 50: **Google's `self` flag is not set
on the account's own attendee row on a secondary calendar**, so §4.3.2's
"a write that reaches nobody does not ask" counted the caller as their
own guest and `respond_to_event` was refused for reaching one person —
itself. `model.Event.Guests` takes the account's address now and
excludes it whatever the flag says.

Then the transcript, which is the part §13 insists on, showed three the
count could not. **A successful move reported itself as `[cancelled]`**,
because `events.move` answers with that status while the event sits
confirmed on the destination (§18 row 48) — the one word a caller acts
on, exactly inverted; and a cancellation dry run printed "Deleted the
event" under "DRY RUN — nothing was written", a result contradicting
itself in six lines. And `list_instances` returned its occurrences in
whatever order Google sent them, which is not date order — a cancelled
24 March after 7 April, in the list a caller reads to find out which
dates are gone. They are sorted after the budget cut rather than before
it, so which occurrences come back is unchanged and only the order
differs; sorting first would keep a different set than the page token
accounts for, which is phase 1's defect one tool over. The fake
reproduces the move's answer now, so that one is held offline.

And **spike J closed the question this phase had left open with a
disclaimer.** `move_event` took no etag, because `events.move` is a POST
with no body and nothing Google publishes says `If-Match` applies. A
stale etag is refused with 412 (§18 row 49). The exception was a hole,
not a fact; §4.4 covers every write now.

**And §4.3 was finally exercised live through the tools.** Every write
step above has no guests by construction, and spikes A and B go through
the driver's own REST calls rather than through the server — so the
notification path a caller actually uses had been green offline and
unproven live for the whole phase. Five steps, armed by `-spike-notify`
alongside the spikes because they are the only ones here that reach a
person, now drive `create_event`, `update_event` and `cancel_event` with
a real guest: the invitation, the reschedule, the proper withdrawal, and
§18 row 43 through the tool — a cancellation with `notify: none` that
leaves the meeting on the guest's calendar and reports that it has. The
last one is a deliberate mess: the account cannot withdraw what it
cancelled quietly, which is the finding rather than a side effect, so the
run says at the end who has to delete it.

What those steps CANNOT do is score themselves, and the driver says so
rather than guessing: who received what is visible in an inbox and
nowhere in the API (§15). With only a same-domain guest configured, the
`externalOnly` arm and spike B's non-Google case stay untested — that
needs the other two addresses of §15's spike A.

The write steps need a second scratch calendar for `move_event`, which
spends one more of the creation quota of §18 row 36, once, and is adopted
on every run after.

**What the reviews found, and it is the argument for running them.**
Every gate was green before they started, and `/simplify` and
`/code-review high` between them turned up seventeen defects, two of
which were live bugs in the tool surface.

The two bugs share a shape: **choosing a scope and applying it are two
decisions, and only the first had an owner.** `plan.Scope` parsed the
caller's word, and then each operation wrote out "which event does that
actually mean" by hand. `move_event` had no version of it at all — so
`scope:series` on an occurrence id moved one occurrence while the result
printed `scope: series`, and `scope:instance` on a series id moved the
whole series. `respond_to_event` was missing the refusal, so
`scope:instance` against a series id would have patched the whole
series' attendee array, which is the exact thing its own description
says it exists to prevent; only a fixture with no guests on the parent
hid it. `plan.Target` owns it now, and all four operations go through
one service helper.

Five more that mattered, each fixed with the reproduction as a test:

- **An RFC3339 string was compared as a string.** `end <= start` on
  formatted timestamps, and either side of a daylight-saving fold the two
  carry different offsets — so a valid 45-minute event across the
  Copenhagen fall-back was refused and a genuinely backwards one was
  accepted and sent. §4.1's argument for carrying a zone rather than an
  offset, failing inside the guard written to enforce it. They are
  compared as instants now.
- **`api_requests` was a field incremented by hand**, so it missed
  everything the shared setup spent: a create reported 1 and made 4, and
  a **dry run reported 0 while spending three**. It is read off a counter
  in the context that the client increments, so it counts the retries of
  §11 too and cannot drift. A test asserts reported equals served for
  every write. The read path still hand-counts, and §11 now says so
  rather than a comment implying otherwise.
- **An etag plus `scope:series` was refused every time.** The caller's
  etag came from the occurrence they read; the write was redirected to
  the parent and compared against the parent's. Unrecoverable, too —
  re-reading the occurrence returns the same etag that was just rejected.
  The caller's etag is now checked against the event they addressed, and
  `If-Match` carries the etag of the event actually written.
- **`notify:none` was refused for a colleague** on any secondary
  calendar. Such an event is organised by the CALENDAR, whose id is an
  address with a domain of its own, so every guest counted as outside the
  organisation and §4.3.4 fired with a sentence that was simply false.
  The organiser is now taken as a person only when it is not the calendar
  being written.
- **A 412 on a delete was reported as "already gone".** `classify` maps
  410 and 412 to the same class and the handler matched on the class, so
  a write refused *because somebody had edited the event* told the caller
  their meeting no longer existed. It matches on the status now.

And the smaller ones, which are the ordinary yield: a dry run on update
and cancel printed the event unchanged while the change list said
otherwise — cancel printed it alive under the words "Deleted the event";
the truncate half of a `this_and_following` write sent no `sendUpdates`
while the result said all guests had been notified; the note quoted the
computed rule rather than the one the new series carries; a split
silently dropped the conference link, because Google ignores
`conferenceData` without `conferenceDataVersion=1`; a misspelled `scope`
was accepted in silence on a non-repeating event; a one-sided time change
was never crossed against the end that stays; the live driver could
address the operator's own primary calendar when its second scratch
calendar failed to appear, which §9.1 forbids structurally; and the fake
read its event map outside its own mutex.

The duplication `/simplify` found is worth one line each, because the
cost is always the same: the `EventPatch` fold was written out in the
service AND in the fake, so a field added to one would have made a test
green over behaviour that never happened; "who counts as a guest" — the
rule §4.3.2 refuses on — had a definition in `model` and another in
`plan`, so one result could have quoted two guest counts; the recur
sentinel was hand-wrapped at four sites, two of which stripped the
package prefix and two of which did not; and the dry-run verb was
assigned at eight sites, of which three said "nothing was sent" and five
forgot. Each now has one owner.

**Not taken:** a generic wrapper around the five tool handlers, which
would have saved fifteen lines at the cost of making the write
registrations read differently from the read ones; and dropping the
instance read on a series-scoped write, which costs one request and is
what produces the refusal that tells a caller their `original_start`
named no occurrence.

**Phase 3 — calendars and sharing (v0.3.0).** `create_calendar`,
`manage_calendar`, `list_sharing`, `share_calendar`,
`unshare_calendar`, and the two gated tools. `GCAL_SHARING=off`.

**Phase 4 — the model's experience (v1.0.0).** Resources; the evals of
§13 with the three tasks named there; a second MCP client; the `.mcpb`
bundle exercised from a real install; and whatever §17 is still holding.

### 16a. Found by review, and fixed

Five defects the phase 1 review turned up in code phases 0 and 1 had
already committed. All five are fixed; each is listed with what it was,
because the shape of the mistake is the useful part.

1. **Multi-calendar paging handed one calendar's token to every
   calendar.** `ListEvents` fans out, and a Google page token is scoped
   to one calendar and one query — but the fan-out kept whichever
   calendar produced a token last and passed that one back to all of
   them, discarding the rest. Continuing a truncated two-calendar read
   resumed the wrong calendar from an unrelated offset and lost the
   others' pages, while the result said "Pass page_token to continue."
   `next_page_token` is now a cursor carrying **one token per calendar**,
   naming only the calendars with more to read, so a continuation never
   re-reads a calendar that finished. It stays one opaque string, so no
   tool schema changed.

   Fixing it exposed a second defect underneath, which is why the first
   attempt still lost events: the budget was applied **twice**, once per
   calendar and again to the combined list. The second cut threw away
   events whose page token had already moved past them — unreachable
   from the cursor the caller was handed. The budget is divided across
   the calendars up front now, and nothing fetched is discarded.
2. **`search_events` advertised a `page_token` it did not accept.** Its
   input had no such field, but it returns the schedule result, which
   renders the sentence and carries the token. It takes one now, which
   1 made worth having.
3. **An invalid time zone was classified `[unavailable]`, which is
   retryable.** `Service.Zone` returned `internal/when`'s error
   unwrapped, so it never became a `gapi.Error` and fell through to the
   default class: a caller passing a zone that does not exist was told to
   retry a request that could never succeed. It is `[invalid]` now, as
   `s.window` already was, and the live driver holds the class.
4. **`sortKey` mixed UTC and local.** Timed events sorted by their UTC
   instant and all-day events by their local date, while the renderer
   groups by local date. East of UTC the two disagree — in Asia/Tokyo an
   08:00 event carries a key on the previous day — so an all-day event
   landed in the middle of its own day instead of at the front, which is
   what the function's own comment promised. The golden fixture is
   Europe/Copenhagen with the all-day event alone on its day, so it
   could not catch it.
5. **The event-id rule lived in two places.** `validEventID` was in the
   live driver and the occurrence-id grammar was in `internal/service`,
   each depending on the other's premise and neither owning it.
   `gcal.ValidEventID` and `gcal.SplitOccurrenceID` own both now, beside
   the wire types they describe, which is also where §2.11's
   client-supplied id on insert will need them in phase 2. The grammar
   is a grammar and not a policy: `get_event` still accepts an
   occurrence id, because that is how one occurrence is addressed.

## 17. Open decisions

1. **Incremental sync.** `syncToken` is designed for a client with a
   replica and this server has none (§1). But a "what changed since I
   last looked" tool is genuinely useful and the token is the only
   correct way to build it. Deferred to a phase that can carry the
   invalidation story (410, §2) honestly. **Open.**
2. **Working hours.** There is no working-hours field in the API; the
   `workingLocation` event type is adjacent but not the same thing. Free
   gaps at 03:00 are technically correct and useless. Whether the server
   takes a working-window parameter, or leaves the filtering to the
   model, is undecided. Leaning toward a parameter with no default, on
   §4.3's reasoning. **Open.**

   Phase 1 shipped `check_availability` without one, and the argument
   that should decide it is now clear. **A window is one interval;
   working hours are a daily mask.** Over a single day the two are the
   same thing and the model can narrow the window itself. Over "next
   week" they are not: the model cannot express 09:00–17:00 on five days
   as one window, so it either accepts a fifteen-hour overnight gap in
   the answer or makes five calls, which is five requests against §11's
   budget. `min_minutes` does not help — an overnight gap is the longest
   one in the list and passes any minimum.

   Adding an optional parameter later is additive, so shipping without
   one blocks nothing. The decision is which of the two the server
   should own, and the daily-mask point is the one to decide it on.
3. **Conferencing.** Creating a Meet link is a `conferenceData`
   `createRequest` with a caller-generated `requestId`, and it is in
   scope by §1's boundary. Whether it is a parameter on `create_event` or
   its own tool, and what happens when the domain forbids it, is
   undecided. **Open.**
4. **quickAdd, argued against.** §1 writes it off and the counter-argument
   is recorded rather than lost: it is one call where the structured path
   is several, and users type strings like that. It stays out because it
   gives no control over the zone — which is §4.1, the thing this server
   exists to get right — and because its result does not say what it
   parsed. Revisit only with a spike showing what it does with an
   ambiguous zone. **Decided, with the argument kept.**
5. **Attendee limits.** §2 notes that response status is not propagated
   above 200 guests. Whether the server warns above that threshold, and
   what it does at the hard limit, needs a number the documentation does
   not state. **Open, needs a spike.**

## 18. Evidence log: conventions checked, changed, or rejected

Sources: the Calendar API v3 discovery document (revision 20260826,
fetched 2026-09-15), the Calendar guides for events, recurrence, sync and
quotas, RFC 5545, RFC 8252, the MCP specification, and the public
calendar MCP servers of §1. Checked 2026-09-15.

**Three tiers, and the plan says which is which rather than letting them
blur.** (1) Verified here against a primary source. (2) Taken from a
sibling server's own evidence log, which was verified there. (3)
Asserted from the documentation and **not yet probed live** — these are
what §15 exists to settle, and they are marked.

| # | Convention or assumption | How checked | Verdict |
|---|---|---|---|
| 1 | Default `sendUpdates` to `none` everywhere, as the sibling Drive server does for mail | Discovery document, `events.insert` enum description | **Refuted.** Google warns `none` "can have significant adverse effects, including events not syncing to external calendars or events being lost altogether". §4.3 was rewritten around this; it is the largest change any evidence made to this document |
| 2 | Google's own defaults are a coherent baseline to adopt | Discovery document, across methods | **Refuted.** Events default to not notifying; `acl.insert/patch/update` default `sendNotifications` to **true**. Same API, opposite defaults. §4.3 takes neither |
| 3 | `none` means no mail is sent | Discovery document, every affected method | **Refuted.** "Some emails might still be sent even if you set the value to false." §4.3.3 forbids the server from promising silence |
| 4 | `calendar.readonly` covers every read | Discovery document, per-method scopes | **Refuted.** `acl.list` requires `calendar.acls` or `calendar.acls.readonly`; `acl.get` is covered. §10 requests the extra scope; spike G confirms live |
| 5 | `events.update` is the method for editing an event | Discovery document and the events reference | **Rejected for use.** PUT, replaces whole; drops attendees and recurrence when omitted. §4.4, and §8a writes it off by name |
| 6 | ETags and `If-Match` are available for optimistic concurrency | Calendar version-resources guide | **Confirmed.** 412 on mismatch; `If-Match: *` forces through. §4.4 is buildable as the standard states it, with no deviation recorded |
| 7 | "This and following" is a single operation | Recurring-events guide | **Refuted.** Two calls, and it "resets any exceptions happening after the target instance". §4.2 performs it and says so |
| 8 | `timeZone` is optional on an event's start and end | Discovery document, `EventDateTime.timeZone` | **Refuted for recurrence.** "For recurring events this field is required." §4.1 sends it always |
| 9 | An all-day event can be modelled as midnight UTC | The surveyed servers, and an issue reproducing it | **Refuted.** It renders a day early for every user west of UTC. §4.1 keeps dates as dates; spike D confirms live |
| 10 | A client may supply an event id, making insert idempotent | Discovery document, `Event.id` | **Confirmed, with a caveat that changes the design.** Ids are allowed, but "we cannot guarantee that ID collisions will be detected at event creation time". Nearly idempotent only — hence `ambiguous_outcome` in §6.5 and spike F |
| 11 | `singleEvents` is a display preference | Discovery document and the events reference | **Refuted.** It changes what the method returns — parents or instances — and `orderBy: startTime` requires it. §7.2 makes the caller choose |
| 12 | `freebusy.query` handles any number of calendars | Discovery document, `calendarExpansionMax`; **spike I live, 2026-09-15** | **Confirmed with a limit, and the limit does not do what the batching assumed.** Maximum documented value 50, and §4.6 batches there. Live, 51 *distinct* ids with **no expansion cap set** came back as **51 entries** — neither refused nor trimmed. The cap is deliberately omitted: a spike that sets the ceiling it is measuring reports its own parameter as Google's behaviour. Fifty were unreadable, so this does not settle 51 *readable* calendars: the ceiling may count only the calendars it expands, and proving that would mean creating 51 calendars on somebody's account. The batching stays, and §4.6's rule that a calendar missing from the response is **unknown** is what the design actually rests on |
| 13 | Availability can be computed from an event list | The surveyed servers | **Rejected.** A list misses events whose details the caller cannot read, and ignores `transparency`. §4.6 |
| 14 | `calendars.transferOwnership` is a normal calendar operation | Discovery document | **Rejected for use.** Requires Workspace admin privilege and `useAdminAccess: true`, which this server never requests. §8a |
| 15 | Push notifications could give live updates | Discovery document, the four `watch` methods | **Rejected.** They POST to a public HTTPS endpoint; a stdio server has none. §1 |
| 16 | Quotas are generous enough to ignore in fan-out tools | Quota guide | **Refuted.** 600 requests per user per minute, sliding window. `search_events` and `check_availability` can spend dozens per call. §11 budgets them |
| 17 | Truncated exponential backoff, `min((2^n)+jitter, 32s)` | Quota guide | **Confirmed**, and it is Google's own stated algorithm. §11 |
| 18 | Loopback IP literal, random port, PKCE S256, no OOB | The shared standard §3b, itself from RFC 8252 §7.3 and §8.1 | **Adopted unchanged.** Tier 2: verified in the siblings, not re-derived here |
| 19 | Both `content` and `structuredContent` on every tool with an output schema | The shared standard §2, from the MCP spec and a client bug | **Adopted unchanged.** Tier 2 |
| 20 | Destructive tools unregistered behind an env flag | The shared standard §3 | **Adopted, with the line drawn differently.** `cancel_event` is deliberately *not* gated; §9 argues why a flag everybody turns on protects nobody |
| 21 | An event id may use any lowercase letter | Live probe, 2026-09-15 | **Refuted, and it cost a run.** Ids are base32hex: `a`–`v` and the digits only, so `w`, `x`, `y` and `z` are refused. The driver's ids used `y` and Google answered HTTP 400 "Invalid resource id value", naming neither the field nor the rule. `validEventID` in the driver now holds it locally, so a bad id is refused before a request is built |
| 22 | An all-day event stays on its date when read from any zone (§4.1) | **Spike D, live, 2026-09-15** | **Confirmed.** An all-day event on 2026-03-20 read back from Pacific/Honolulu (UTC-10) and Pacific/Auckland (UTC+13) rendered `2026-03-20  all day` in both, with no time of day. This is the defect every surveyed server has, and it is the reason `internal/when` has no `Date` → `Zoned` conversion |
| 23 | A recurrence written with an IANA zone holds its wall clock across a transition (§2.2) | **Live, 2026-09-15** | **Confirmed, and the transcript shows both halves.** A weekly 14:00 series spanning the 29 March European transition rendered 14:00 on 17, 24 and 31 March in Europe/Copenhagen. The same series read from Pacific/Honolulu rendered 03:00, 03:00 and **02:00** — the instant moved by an hour because Copenhagen went to UTC+2, which is exactly what a zone-as-a-rule produces and what a stored UTC offset cannot |
| 24 | `acl.list` needs `calendar.acls.readonly` (§2.15) | Discovery document; **spike G live, 2026-09-15 — positive half only** | **Confirmed from the primary source; the negative half is deliberately not probed.** With the scope granted, `acl.list` succeeds. Proving it is *refused* without the scope needs a grant that never had it — and Google stores a grant per OAuth client and user, so a later authorization asking for less does not revoke what was already given. Getting a clean negative would mean revoking this server's access at the account level and logging in again, which throws away the working login to re-confirm what the discovery document already states plainly: `acl.get` lists `calendar.readonly` among its scopes and `acl.list` does not. **The consequence is contained**, which is why this is acceptable: if §2.15 is wrong, the only cost is one scope on the consent screen that nothing needs, and `get_calendar` already reports a refused sharing read as a note rather than as an empty list (§7.1). Spike G stays in §15 and answers itself the first time anyone runs the driver under a narrower grant |
| 27 | A profile's recorded scopes are the ones the account granted | Live, 2026-09-15 | **Refuted.** `login` stored what it *asked for*, so `status` presented a request as a fact about the grant — a scope Google refused would have been listed as held. It now records what `tokeninfo` reports the token actually carries, falling back to the request only when that call fails. Spike G reads the live token for the same reason: the recorded list would let it answer confidently and wrongly |
| 25 | `tokeninfo` reports which account signed in | Live, 2026-09-15 | **Refuted.** It returns an email only when an email scope was granted, and this server asks for none — so the account was silently blank and `status` had a field that could never populate. The account is now read from the primary calendar's id, which is the address, and costs no extra scope. Asking for `userinfo.email` was rejected: a calendar server should not need to read a profile to say whose calendar it is looking at |
| 28 | The `Event` resource has 44 published properties, and the four resources this server models have 81 between them | Discovery document, revision 20260826, fetched 2026-09-15 | **Confirmed, and now held by a gate.** `api-diff` records the field list beside the methods and `api-fields` holds one verdict per field: 56 modelled, 25 written off. The count in §8b is no longer a number somebody typed |
| 29 | `colorId` is how an event's colour is set | Discovery document, `Event.eventLabelId` | **Superseded, and worth knowing before phase 2 writes a colour.** `eventLabelId` "supersedes the index-based colorId property" and refers to a label defined on the calendar (`Calendar.labelProperties`). Both are written off for now; §8b's row says so rather than leaving the newer field unmentioned |
| 30 | A daylight-saving transition is an hour | tzdata, through `internal/recur`'s table tests | **Refuted.** Australia/Lord_Howe shifts by 30 minutes, so the week containing its transition is 168h30m. The expansion walks dates and carries the wall clock, so the size of the shift never enters the arithmetic — the test asserts the gap to prove the transition was really there |
| 31 | `events.instances` takes any event id | Discovery document; **live, 2026-09-15** | **Answered, and it is none of the three guesses.** Google returns **200 and expands the occurrence the id names**. A cancelled occurrence expands to nothing, so the call succeeds with an *empty list* — and `list_instances` reported "No occurrences" for a series that has three. A refusal was owed and the API never gives one, so the server reads the id's shape instead: an event id is base32hex (row 21), so an `_` cannot occur in one, and `{id}_{yyyymmdd}[T{hhmmss}Z]` is refused up front with `[invalid]` naming the series. `caltest` reproduces the 200 rather than the 400 it used to guess. What Google does for a plain non-recurring event is still unprobed and still a choice in the fake |
| 32 | `EXDATE` and `RDATE` carry a `TZID` parameter, or a `VALUE=DATE` form on an all-day series | RFC 5545 §3.8.5, and the shape Google returns in `recurrence` | **Asserted from the specification, not probed live. Tier 3.** `internal/recur` parses both and resolves a bare local time in the series' own zone. An all-day point stays a date and yields no instant, which is §4.1 again. A series carrying anything this package cannot expand — `EXRULE`, a second `RRULE`, a frequency below DAILY — is refused rather than expanded, because a count that is quietly too large is the number a caller would put in front of a user |
| 26 | Redirecting the config directory and the environment isolates a test | Live, the hard way, 2026-09-15 | **Refuted, having been written down here first.** The OS keyring cannot be redirected by either, so `go test ./cmd/...` found the maintainer's real refresh token under the default profile, revoked the grant at Google and deleted it. `TestMain` now substitutes the keyring for the whole package, with a decoy test that fails if that is ever dropped. The sibling servers carry the same warning; having it in the source did not prevent it |
| 36 | An account can hold as many calendars as a test needs | **Live, 2026-09-16** | **Refuted, and it closes a spike by making it unrunnable.** Spike I's readable half creates 51 calendars to ask whether the free/busy ceiling counts only calendars it can expand. Google refused the **39th**: HTTP 403, `reason: quotaExceeded`, "Calendar usage limits exceeded". So the readable half cannot be run on an account at all, and §2.10's ceiling stays unsettled for readable calendars by decision rather than by neglect. **Two consequences beyond the spike.** The limit is on *creation* and is not refunded by deleting — the 38 calendars were deleted and the quota stayed spent, so a later run can fail to create even its one scratch calendar until Google resets it. And phase 3's `create_calendar` meets this exact 403: `classify` already maps `quotaExceeded` to `[rate_limited]`, which is correct and retryable, but the message reads "Google is rate limiting this account", which invites an immediate retry of something that may not succeed for a day. Phase 3 owes that message a better sentence |
| 37 | Deleting an event releases its id for reuse | **Live, 2026-09-16** | **Refuted.** A re-insert under a deleted event's id is answered 409. The live driver had fixed seed ids and emptied its scratch calendar before seeding; the emptying succeeded and the seed still failed. Ids are generated per run now — Go's `strconv.FormatInt(n, 32)` uses `0123456789abcdefghijklmnopqrstuv`, which is exactly base32hex's alphabet, so a formatted integer is a legal event id by construction rather than by inspection (row 21) |
| 38 | "This and following" preserves exceptions after the target | Recurring-events guide; **spike E live, 2026-09-16** | **Refuted, as the guide says and §4.2 warns.** An eight-occurrence weekly series with its sixth occurrence moved 30 minutes later, split at the fourth: the original kept `COUNT=3`, the new series took `COUNT=5`, and the moved occurrence returned at its scheduled time. The exception was **reset**. `this_and_following` must say so in its result, because nobody expects it. The split was computed by `Set.Split` in `internal/recur`, so phase 1's arithmetic is confirmed against Google rather than against itself |
| 39 | A duplicate client-generated id may pass undetected at creation (§2.11) | **Spike F live, 2026-09-16** | **Not reproduced, and the class stays anyway.** Two inserts of one id in flight together: one 200, one 409. The collision was caught. `ambiguous_outcome` is not retired, for two reasons worth keeping: the discovery document declines to *guarantee* detection, so a single observation is not a promise; and the class also covers the retry after a transport failure, where the caller never saw the first answer and Google's 409 would be reporting the caller's own event back at it. One fewer reason to fear the class, not a reason to drop it |
| 40 | `externalOnly` means "guests outside your organisation" | Discovery document, `events.insert.sendUpdates`, revision 20260826; **spike A live, 2026-09-16** | **Confirmed by the API and refuted by its own documentation — the probe wins (hard rule 13).** The enum description says "Notifications are sent to **non-Google Calendar** guests only", and this document was corrected to match it. Then spike A put one guest inside the organiser's Workspace domain and one outside it on a consumer Gmail account, and inserted the same event three times. Under `externalOnly` the **out-of-domain guest was mailed and the same-domain guest was not** — even though both demonstrably use Google Calendar, which is the axis the description names. The real axis is the organiser's Workspace domain. §4.3's `dry_run` can therefore split its count exactly, from the organiser's own primary calendar id, and does. **Two corrections in one day from one parameter description**: it is the second field in this phase whose published description was the misleading thing, after `showDeleted` (row 33) |
| 41 | `none` still sends some mail (§2.6) | **Spike A live, 2026-09-16** | **Not reproduced, and the rule stands anyway.** Two inserts carrying `sendUpdates=none` with two guests mailed neither of them. Google's warning is that mail "might still be sent", not that it is, so one silent run is not a promise of silence — §4.3 rule 3 keeps its refusal to report `none` as silence for the same reason spike F did not retire `ambiguous_outcome` (row 39). What this does remove is the fear that `none` is routinely noisy: it is not, on this shape of write. **Caveat held deliberately:** the out-of-domain guest also did not receive the `all` invitation, which it should have, so something filtered mail on that side and the absence of the `none` mail there is not clean evidence. The same-domain observation is clean, because that guest did receive `all` |
| 42 | `all` notifies all guests | **Spike A live, four runs, 2026-09-16** | **Confirmed — and the three runs that seemed to refute it were measuring the receiver.** An out-of-domain Gmail guest received `externalOnly` every time and `all` never, across three runs and both orderings, which was recorded here as "the API accepted the invitation and did not deliver it". The fourth run put a **non-Google address on the same events as that Gmail address**, so one send could be watched at two receivers: the non-Google guest received **both** `all` and `externalOnly`; the Gmail guest received only `externalOnly`. Same event, same moment, one delivered and one not — so Google sent the `all` notification and Gmail did not surface it. **The correction matters more than the finding.** Three consistent runs were consistent because the instrument was, and repetition looked like evidence. What broke it was not a fourth run but a second receiver, which is the only thing that could separate sending from delivery |
| 43 | Deleting an event removes it from the guests' calendars | **Live, 2026-09-16** | **Refuted when `sendUpdates=none`, and it is the sharpest argument §4.3 has.** An event created with two guests was deleted with `sendUpdates=none`. It is gone from the organiser's calendar — confirmed by listing that calendar, which now holds only later events — and it is **still on a guest's calendar**, showing both attendees and awaiting a response. The organiser believes the meeting is cancelled; the guest still has it. Found because the live driver was doing it: its own cleanup deleted guest-carrying probe events with `none` for a day and left them on two real calendars. The driver now cancels anything with attendees using `all`. For phase 2 this is `cancel_event` with `notify: none`, and §4.3's refusal to treat `none` as harmless now rests on a demonstration rather than on a warning in a document |
| 44 | `sendUpdates=none` on insert can lose an event (§2.7) | **Spike B live, 2026-09-16** | **Confirmed, and it is structural rather than a defect.** A non-Google address was invited to an event inserted with `none`. It received nothing — in a run where the same address demonstrably did receive both `all` and `externalOnly` minutes earlier, so the channel was working. A guest outside Google Calendar has no calendar for the event to appear in, so mail is the only way they can learn of it, and `none` removes the only way. The event exists, with them attached, and they cannot discover it by any means. That is §2.7's "events being lost altogether for some users" with the mechanism visible. §4.3 refuses `none` when a guest is outside the organiser's domain rather than warning about it, because a warning is something a caller skims |
| 45 | `leaks-history` protects the repository before it goes public | **Run for the first time, 2026-09-16** | **It could never have passed.** The allow-list carried `@noreply.anthropic.com`; the address in every commit's attribution trailer is `noreply@anthropic.com`, the other shape. So the gate failed on fourteen of fifteen commit messages, and had done since the first commit — unnoticed because it is the one gate `make check` deliberately does not run, being reserved for "before going public". A gate nobody runs is a gate nobody knows is broken, which is the same lesson as the three gates phase 1 found named but absent, one level further out: that failure was a list claiming a gate existed, this one is a gate that exists and was never executed. The entry is added as §9.1 requires, argued rather than widened: a vendor's non-routable no-reply address in a Co-Authored-By trailer, structurally identical to the GitHub noreply already allowed, saying nothing about a deployer, an organisation or anyone's calendar |
| 46 | `make check` passing locally means it passes on a fresh clone | **First CI run, 2026-09-16** | **Refuted twice in one run, and neither failure was platform-specific in the way "CI has never run on macOS or Windows" implied.** The `staleness` gate failed on **all three** platforms, Ubuntu included, because `internal/plan`, `scripts/evals` and a third under `scripts/` existed locally as **empty directories** and git does not carry those. The gate had been passing on a working tree that no clone could reproduce — including every contributor's. It also caught a real documentation error on the way: that third directory never existed at all — the live probes live in `scripts/livecal` — and both this document and CLAUDE.md had said otherwise since phase 0. The gate now accepts a path a later phase builds only when the text names that phase, so a plan and a stale reference are told apart by the author rather than guessed at. Separately, `gofmt` listed every `.go` file on Windows: git checked the tree out with CRLF and Go's tooling assumes LF, so a `.gitattributes` pins `eol=lf`. **The lesson is the empty directories**: a gate is only as honest as the tree it runs on, and a local tree is not the artifact anybody else gets |
| 47 | The file fallback's 0600 protects the refresh token on every platform | **First CI run on Windows, 2026-09-16**; Go's `os` documentation; `golang.org/x/sys/windows` | **Refuted, and the warning was asserting it.** Go's file modes do not map to Windows ACLs — on Windows the mode only decides the read-only attribute — so the token file was written 0600 and landed at **0666**, readable by any account on the machine, while both warnings said "mode 0600" on every read and every save. A sentence a user would rely on, false on one of three supported platforms, and the same failure as promising `none` means silence (§4.3 rule 3): claiming a guarantee the platform declines to make. **Fixed rather than documented away.** The file is given an explicit DACL granting only the current user's SID, set with `PROTECTED_DACL_SECURITY_INFORMATION` so the entries inherited from the parent directory are replaced rather than added to — a grant without that flag widens access instead of restricting it. Administrators and SYSTEM are deliberately not named: they can take ownership regardless, so listing them would only make the list longer. The ACL is applied after the rename, because on Windows it belongs to the file at its final path. A Windows-only test reads the list back and asserts it is protected and holds exactly one entry, rather than trusting the call that set it |
| 48 | `events.move` returns the moved event, so its response describes where the event landed | **Live, 2026-09-16** | **Refuted, and it made a successful move read as a cancellation.** A move that worked answered with `status: cancelled`, so the result rendered `[cancelled]` next to an event that had just been moved — the one word a caller would act on, and the exact opposite of what happened. Reading the event on the DESTINATION immediately afterwards showed it confirmed and intact, which is how the response was caught lying rather than the move. `move_event` reads the event back from the destination now and owns the extra request in its count. Every gate was green; only the transcript showed it, which is §13's whole claim about green gates |
| 49 | `events.move` cannot carry `If-Match`, because it is a POST with no body and nothing documents the header | **Spike J live, 2026-09-16** | **Refuted: it is HONOURED.** A stale etag on a move is refused with **412**. The server had been sending none and saying in the result that this was the one write without the protection — an assumption stated honestly and still wrong. §4.4 has no exception now, `move_event` takes an etag and a `force` flag like every other write, and the fake refuses a stale one so the behaviour is held offline. The lesson is narrow and repeatable: "the documentation does not say" is a question, not an answer, and asking cost one probe |
| 50 | Google's `self` flag marks the signed-in account on its own attendee row | **Live, 2026-09-16** | **Refuted on a secondary calendar, and it made a write that reached nobody demand a notification decision.** The driver put the account on its own event, on a calendar that account owns, and the attendee came back WITHOUT `self` — so §4.3.2's "a write that reaches nobody does not ask" counted the caller as their own guest and `respond_to_event` was refused with "this write reaches 1 guest". The flag appears to be relative to the calendar in the request rather than to the authenticated user, and a secondary calendar is not a person; that mechanism is a reading of one observation and the fix does not rest on it. `model.Event.Guests` takes the account's own address and excludes it whatever the flag says |
| 33 | `showDeleted=false` means Google filters cancelled events out | Discovery document, `events.list.showDeleted`; **live, 2026-09-15** | **Refuted, in the one case the parameter names itself.** "Cancelled instances of recurring events (but not the underlying recurring event) will still be included if showDeleted and singleEvents are both False." The server passed the parameter and trusted it, so a `no_expand` read returned the cancelled occurrence — and Google sends such an instance **bare**, with an id, a status, its series and its original date but no start and no summary. It rendered as a row with no date and no title and was counted among the results. The service filters cancelled events itself now, in `drain`, where the budget counts what the caller sees. `caltest` had been hiding them, which is why no test caught it |
| 35 | An occurrence id is `{seriesId}_{yyyymmdd}[T{hhmmss}Z]`, and the split is safe because an event id cannot contain `_` | **Live, 2026-09-15**, plus row 21 | **Confirmed, and it had to be, because a user-visible refusal now rests on it.** `events.instances` returned ids of exactly that shape (`…_20260317T130000Z`), and row 21 establishes that an event id is base32hex — `a`–`v` and the digits — so `_` cannot occur in one. `list_instances` refuses an id matching the shape and names both the series and the occurrence's start. Recorded as its own row because row 31 establishes the API's *behaviour*, not the id *grammar*, and the live driver's own comment declines to compose an instance id on the grounds that the format is undocumented — the server adopts it, so it owes the verdict. Both halves of the rule now live in `internal/gcal` — `ValidEventID` and `SplitOccurrenceID` — beside the wire types they describe, which is where §2.11's client-supplied id on insert will need them in phase 2. The live driver calls the same function it used to keep its own copy of |
| 34 | The transcript redactor makes the live driver's output safe to paste | The first live run of phase 1, read | **Refuted for one step, and the gap is structural.** The redactor is anchored on *shapes* — an `@` with a dot-suffixed domain, a known URL prefix, a token's literal prefix (§9.1) — and **a display name has no shape**. `list_calendars` is the one step that reads past the calendar the driver created, and its body printed a dozen of the account's real calendar titles, one of them a private rename. No rule could have caught them. So the fix is scope, not pattern: a step marked `wholeAccount` never prints its body, on success or on failure, and its check reports what it verified instead. §9.1's promise — the driver reads only what it wrote — now holds for what reaches the terminal, which is where it was being broken |

### Deviations from the shared Go MCP server standard

The standard at `~/.claude/mcp-server-standard.md`, read 2026-09-15.
One deviation, and one place where this server can hold a rule a sibling
could not.

| The standard says | Here | Why |
|---|---|---|
| Errors use six classes: `invalid`, `not_found`, `auth`, `conflict`, `unavailable`, `unsupported` | Twelve, adding `forbidden`, `stale`, `ambiguous`, `blocked`, `rate_limited`, `ambiguous_outcome` | `stale` (412) and `conflict` ask the caller to do different things; `ambiguous_outcome` is forced by §2.11, where a retry may double-book. The vocabulary is closed, derived from the code and asserted in both directions (§6.5) |
| Never overwrite: compute a minimal diff | Held as written | Recorded because the sibling Sheets server had to deviate here and a reader may expect the same. Calendar supports partial updates and `If-Match` properly (§2.3, §2.4), so the rule is implementable: §4.4 patches always and §8a writes off all four PUT methods |

Everything else is adopted as written, including the preamble's three
obligations for any rule adopted — make it a test, derive the list from
the code rather than typing it out, and have the checker assert a floor
on how much it read.
