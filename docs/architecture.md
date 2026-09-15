# Architecture — google-calendar-mcp

**Status: phase 0 built and verified live (2026-09-15).** The
scaffolding, the gates, the time model and the six read tools are in;
`make check` is green across all seventeen of its targets; and the live
driver has run against a real Workspace account — 19 steps, none failed,
none undetermined, and the transcript was read rather than counted.
Spike D passed. Spike G answered its positive half. **One thing is still
owed: spike G's negative half**, which needs a profile logged in with
`GCAL_SHARING=off`, and which is the half the scope decision in §10
actually rests on.

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
public MCP calendar servers named in §1. §18 is the evidence log. Nine
of its twenty rows refute an assumption this design started out
holding, and one of the nine reversed the single most consequential
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

Four supporting rules:

1. **The refusal is specific.** `[invalid] this event has 4 guests; pass
   notify to say whether they are emailed` — with the count, never the
   addresses (§9).
2. **A write that reaches nobody does not ask.** An event with no
   attendees on a calendar shared with nobody has no `notify` decision to
   make, and demanding one is friction with no safety in it. The
   parameter is required only when the write can actually reach a person.
3. **`none` is never reported as silence.** Every result that used
   `none` says Google does not guarantee it (§2.6). The server will not
   make a promise the platform declines to make.
4. **`dry_run` shows the blast radius before anything is sent**: who
   would be notified, how many, internal versus external, and what the
   event would look like afterwards.

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
  `internal/auth/` loopback OAuth and the token source.
- `internal/gcal/` wire types; `internal/gapi/` the raw REST client, with
  `caltest/` the in-memory Calendar used by tests.
- `internal/when/` dates, zoned times, windows and the zone-resolution
  order of §4.1. No network, no clock of its own — the clock is injected.
- `internal/recur/` RRULE parsing and formatting, instance expansion for
  what the server must reason about locally, and the three scopes of
  §4.2.
- `internal/model/` the server's view of a calendar, an event and a busy
  interval; `internal/render/` text output; `internal/plan/` typed write
  ops and the guards; `internal/service/` orchestration and policy;
  `internal/tools/` the MCP tools; `internal/server/` SDK wiring and the
  schema dump.
- `internal/redact/` the log and transcript redactor.
- `scripts/gates/` the repository's own checks, as Go; `scripts/livecal/`
  the live driver; `scripts/evals/` the model-facing scoring harness;
  `scripts/spikes/` the live probes of §15.
- `testdata/` synthetic fixtures, renderer goldens, and the API surface
  snapshot and coverage record of §8a.

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

Phase 0 writes the record for every field; the write path fills the
verdicts in as the phases reach them.

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
2. **The live driver reads only what it wrote**, on a calendar it created
   for the run and deletes after — so the only content that can reach a
   log, a golden or a test failure is content the driver invented.

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
- **`scripts/evals`** — tasks a model must complete through the tools,
  scored. The three that matter most are the three failures of §3: an
  all-day event created from a user in a negative-offset zone, a weekly
  recurrence that must survive a daylight-saving change, and an
  invitation that must actually reach an external guest.

`make check` and CI run the same set, asserted by the `parity` gate:
`fmt`, `vet` (including the tagged tests), `tidy`, `lint`, `cover`,
`vuln`, `licenses`, `secrets`, `api-coverage`, `api-fields`, `classes`,
`leaks`, `transcript`, `live-cover`, `mcpb`, `parity`, `pins`,
`schema-diff`, `smoke`, `staleness`.

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
  with `none`. Which mail? Create an event with an internal and an
  external guest under each of `none`, `externalOnly` and `all`, and
  record who actually received what. §4.3's wording depends on the
  answer.
- **Spike B — `sendUpdates: none` on insert.** Google warns it can lose
  events (§2.7). Reproduce or fail to reproduce, with an external guest.
  If it reproduces, §4.3 may need to refuse `none` on insert outright.
- **Spike C — daylight saving.** A weekly recurrence at 09:00 local,
  spanning a transition, written with a zone and written without one.
  Confirm the drift and confirm its absence.
- **Spike D — all-day events west of UTC.** Create one from a
  negative-offset zone and read it back from a positive-offset one.
  This is §3's first row and the most common defect in the category.
- **Spike E — "this and following".** Build the two-call pattern over a
  series that already has an exception after the target, and confirm the
  exception is reset (§2.8), so §4.2's warning is accurate.
- **Spike F — the duplicate insert.** Send the same client-generated id
  twice, concurrently, and see whether the collision is caught (§2.11).
  Determines whether `ambiguous_outcome` is the right class or an
  over-cautious one.
- **Spike G — ACL scopes.** Confirm §2.15: that `acl.list` fails under
  `calendar.readonly` alone. It is a scope-set decision and a 403 weeks
  later if wrong.
- **Spike H — free/busy on an unreadable calendar.** Query a calendar the
  user cannot read and confirm the per-calendar error shape §4.6 depends
  on.
- **Spike I — the 50-calendar ceiling.** Confirm `calendarExpansionMax`
  behaves as documented at 50 and at 51.

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

**Phase 1 — recurrence and availability (v0.1.0).** `internal/recur`;
`list_instances` and `check_availability`; the `expand`/`series` decision
of §7.2 and the free-gap arithmetic of §7.3. Spikes C, H and I.

**Phase 2 — writing events (v0.2.0).** The write path: `plan` and its
guards, `If-Match`, the client-generated id, `dry_run`. `create_event`,
`update_event`, `cancel_event`, `move_event`, `respond_to_event`. §4.2's
required scope and §4.3's required notify, both with the refusal wording
spikes A, B, E and F settle. This is the phase that needs the most live
work and the one where the transcript matters most.

**Phase 3 — calendars and sharing (v0.3.0).** `create_calendar`,
`manage_calendar`, `list_sharing`, `share_calendar`,
`unshare_calendar`, and the two gated tools. `GCAL_SHARING=off`.

**Phase 4 — the model's experience (v1.0.0).** Resources; the evals of
§13 with the three tasks named there; a second MCP client; the `.mcpb`
bundle exercised from a real install; and whatever §17 is still holding.

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
| 12 | `freebusy.query` handles any number of calendars | Discovery document, `calendarExpansionMax` | **Confirmed with a limit.** Maximum 50. §4.6 batches; spike I confirms the boundary |
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
| 26 | Redirecting the config directory and the environment isolates a test | Live, the hard way, 2026-09-15 | **Refuted, having been written down here first.** The OS keyring cannot be redirected by either, so `go test ./cmd/...` found the maintainer's real refresh token under the default profile, revoked the grant at Google and deleted it. `TestMain` now substitutes the keyring for the whole package, with a decoy test that fails if that is ever dropped. The sibling servers carry the same warning; having it in the source did not prevent it |

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
