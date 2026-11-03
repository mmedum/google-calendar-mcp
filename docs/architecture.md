# Architecture

How `google-calendar-mcp` is put together, and the decisions a
contributor should not undo without a better reason than the one that
put them here. Part 1 is the overview. Part 2, from
[Decisions](#decisions), is one entry per decision, cited elsewhere by
its D-ID. What was verified about Google's API lives in the comment at
the code that depends on it, with the source linked and the live step
that checks it named.

## Context

`google-calendar-mcp` is a Model Context Protocol server for Google
Calendar. One Go binary runs as a subprocess of an MCP client and speaks
MCP over stdio. It holds one person's OAuth credentials and calls the
Calendar API v3 over raw REST, with wire types of its own (D-01, D-06).
Nothing is hosted. Each deployer creates their own Google Cloud project
and OAuth Desktop client, and nothing deployer-specific is built into
the code, the repository or a release (D-04).

The parties around the server:

- **The person** signs in once with `google-calendar-mcp login`. Every
  call acts as them, on a Workspace or a consumer account, with their
  own access to each calendar.
- **The operator** decides what the server may do, through `GCAL_*`
  settings the client passes at start: read-only mode, sharing, the
  destructive switch and the decline-all switch (D-60, D-55, D-44). The
  operator is usually the person, setting up their own client.
- **The client** draws the approval prompt for each call from the tool
  annotations and its own rules. The server asks the person nothing
  itself (D-56).
- **The model** chooses the calls and their arguments. It can be wrong,
  or steered by an event description somebody else wrote, so nothing it
  sends widens what the operator allowed (D-59, D-49).

The server talks to Google through:

- the Calendar API v3, for calendars, the calendar list, events, sharing
  rules, free/busy, settings and colors;
- Google's OAuth endpoints, for login, refresh and revocation, and
  `tokeninfo`, for the scopes a token carries.

It answers *when* and hands off *what*. A meeting's recording, notes and
attached files belong to the servers built on the Drive and Docs APIs.
An event read lists each attachment with its Drive file id, which is
what those servers take, and never opens the file (D-09).

It is not an administrator tool, a webhook receiver or a migration tool
(D-82). It keeps no copy of a calendar between calls (D-83). Claude on
the web cannot reach a stdio server (D-01). The README lists these
under [goals and non-goals](../README.md#goals-and-non-goals).

It exists to be correct about the three things a calendar has that a
spreadsheet does not: time, recurrence and who gets emailed.

## Quality goals

In priority order:

1. **Time is never wrong by a day or an hour.** A date stays a date, a
   timed write carries its IANA zone, and every read says which window
   and zone it used (D-10, D-12).
2. **Nobody is emailed, declined or shared with by surprise.** `notify`
   and `scope` are required with no default, and every wider reach is a
   tool of its own behind a flag (D-18, D-21, D-24, D-44, D-49, D-50).
3. **A write never destroys what it did not see.** Every write is a
   patch under the etag of the read that planned it, and a calendar the
   server could not read is unknown, never free (D-27, D-28, D-34).
4. **The stream, the logs and the repository stay clean.** Stdout
   carries only protocol frames, logs carry no calendar content, and
   neither the repository nor a live transcript holds anything real
   (D-02, D-03, D-04, D-71).
5. **Beliefs about Google are checked against Google, and the surface is
   a contract.** Every method and field has a verdict, a released name
   or field keeps working, and a write-path change counts only after a
   live run (D-08, D-68, D-70).

## Codemap

- `cmd/google-calendar-mcp` is the program. With no subcommand it
  serves; `login`, `logout`, `status`, `doctor`, `--dump-schemas` and
  `--version` are the rest. It names stdout and stderr once and passes
  them down (D-02). It imports `time/tzdata` and starts without
  credentials (D-64).
- `internal/auth` runs Google's desktop OAuth flow, a loopback redirect
  with PKCE, and holds the scope set and the token source (D-61, D-62).
- `internal/config` reads the `GCAL_*` environment and the bound flags,
  validates them once at start, and keeps `config.Vars`, the registry of
  every variable the server reads (D-63).
- `internal/credentials` stores and finds the refresh token: the
  environment, then the OS keyring, then an owner-only file (D-62).
- `internal/fileperm` restricts a file to the account that wrote it,
  with a protected DACL on Windows (D-62).
- `internal/gapi` is the raw REST client: requests, `If-Match`, retries,
  the limiter and the error classes (D-28, D-65, D-66).
- `internal/gapi/caltest` is the in-memory Calendar the tests run
  against, generated rather than recorded, reproducing Google's quirks
  (D-05).
- `internal/gcal` holds the hand-written wire types and the event-id
  grammar: `ValidEventID`, `SplitOccurrenceID` and `NewEventID` (D-06,
  D-07, D-17, D-38).
- `internal/model` is the server's view of a calendar, an event and a
  busy interval, with the free-gap arithmetic (D-22, D-34).
- `internal/plan` holds the typed write operations and their guards:
  scope, notify and reach, the patch body, the status-event shapes and
  the sharing audience. It has no network and no client, so every guard
  tests alone (D-18, D-21, D-27, D-43, D-50).
- `internal/recur` parses and explains RRULEs, expands them carrying the
  wall clock, and does the scope and split arithmetic (D-19, D-20).
- `internal/redact` is the log and transcript redactor, anchored on
  shapes (D-03, D-71).
- `internal/render` writes the readable half of every result, held by
  golden files (D-12, D-31, D-33).
- `internal/server` wires the MCP SDK, holds the server instructions and
  the resources, and dumps the schemas (D-67, D-68).
- `internal/service` holds orchestration and policy: zone resolution,
  paging, fan-out budgets and the read before each write. It is the only
  place a Google failure becomes a `[class]` tool error (D-10, D-14,
  D-32).
- `internal/tools` defines the MCP tools, each added through `add`,
  whose `Kind` decides its annotations and whether it registers (D-54,
  D-58).
- `internal/userconfig` stores non-secret per-profile state: the client
  secret's path, the account and the granted scopes.
- `internal/version` holds the version the build stamps in.
- `internal/when` holds dates, zoned times, windows, working-hours masks
  and the zone-resolution order. It has no network, and the clock is
  injected (D-10, D-36).

Beside the packages, `scripts/gates` is every check the repository runs
on itself, written in Go. `scripts/livecal` is the live driver and its
probes (D-70, D-71, D-72). `scripts/evals` scores a model driving the
tools (D-73). `packaging/mcpb` holds the Claude Desktop bundle's
manifest and Linux launcher (D-78, D-79, D-80, D-81). `testdata` holds
the released surface, the API snapshot and its verdicts, and the
renderer goldens (D-08, D-68).

## Request flow

A tool call crosses the layers in one direction:

```
MCP client
   │  JSON-RPC over stdio; stdout carries nothing else
   ▼
internal/server     SDK session, tools, resources, call logging
   ▼
internal/tools      decode the input; add() applies the Kind:
   │                annotations, registration, the mark
   ▼
internal/service    zone, window, paging, read before write,
   │                request counting, [class] errors
   ├──► internal/plan     guards and the patch body, no network
   ├──► internal/when, internal/recur, internal/model
   └──► internal/render   the readable half
   ▼
internal/gapi       one request: limiter, If-Match, retry by method, classify
   ▼
internal/auth       refresh token → access token
   ▼
Google Calendar API v3, OAuth, tokeninfo
```

### Start

`main` loads the configuration and refuses a bad setting before it
announces itself. A removed setting that is turned on is refused with
the name of its replacement, as `GCAL_READONLY` and
`GCAL_REQUIRE_PROMPT` are (D-63, D-60, D-56). The server starts and
lists its tools with no credentials; each call then answers `[auth]`
naming `login` (D-64). `tools.Register` adds every tool through `add`.
A tool's `Kind` leaves it out when the settings exclude it, and a mark
on a kind that may not carry it panics at start, so the mistake fails
in every configuration (D-54, D-55, D-57). The registered surface does
not change for the life of the process (D-59).

### A read

A client calls `list_events` for two calendars and "next week". The
model resolves "next week" into dates; the server never parses relative
time (D-12). The zone comes from the call, else the calendar, else the
account's settings, and never from the process (D-10). The window is
echoed back as two absolute instants in that zone.

`list_events` expands a repeating event into its occurrences unless
`no_expand` asks for the series, and the result says which shape it
returned (D-13). Each calendar pages on its own Google token, packed
into one cursor bound to the whole query, so a continuation under
another query is refused (D-14). The read is budgeted in events, not
requests, and the server filters out the canceled rows Google sends
bare. The result names the window, the zone, where the zone came from
and whether the read was cut short, in both halves of the reply (D-12,
D-33).

### A write

A client calls `update_event` on one occurrence of a weekly meeting with
guests. `scope` is required because the event repeats, and here it is
`instance` (D-18). The service reads the event, and `internal/plan`
counts who the write reaches, by address (D-22). Because it reaches
somebody, `notify` is required, and `none` is refused when a guest is
outside the organizer's domain (D-21, D-24).

The patch carries only the fields that change, under the etag of the
read that planned it (D-27, D-28). If Google answers 412 to the
server's own etag, the server reads, plans and writes once more when
nothing changed in what the write does (D-29). With `dry_run`, the call
stops before the write and shows the change it would make (D-30). The
result shows the event before and after, the `sendUpdates` value asked
for and never what was delivered, and how many requests the call spent
(D-31, D-23, D-32).

A `this_and_following` write is two calls. The first truncates the
series at the target. The second inserts the rest as a new series,
copied whole with every field the server does not model. The result
says that exceptions after the target were reset (D-19, D-07).

### Login and refresh

`google-calendar-mcp login` opens Google's consent page and listens on a
loopback port, with PKCE (D-62). It stores the refresh token in the OS
keyring, or in an owner-only file with a warning when no keyring
answers. The profile records the scopes Google granted, which can be
fewer than were asked for. It also records the account, read from the
primary calendar's id, so no email scope is needed (D-61). At serve
time the token source trades the refresh token for an access token. A
refused refresh reaches the caller as `[auth]`, naming `login` (D-65).

## Invariants

- Stdout carries only MCP JSON-RPC frames (D-02).
- No log line carries an address, an event's title, description or
  location, or a search term (D-03).
- Nothing in the repository identifies a real person, account or
  calendar (D-04).
- An all-day event never becomes an instant, and every timed write
  carries an IANA zone (D-10).
- The process's time zone is never used (D-10).
- Every read states its window, its zone and whether it is complete
  (D-12).
- A write to a recurring event without `scope` is refused (D-18).
- A write that reaches a person without `notify` is refused (D-21).
- No result says a guest received mail, or that `none` kept everyone
  silent (D-23).
- `notify: none` with a guest outside the organizer's domain is refused
  (D-24).
- No PUT method has a client function (D-27).
- Every write to an existing event, calendar or rule carries `If-Match`
  from a read, or `*` under `force` (D-28).
- A dry run writes nothing (D-30).
- Every reply carries `structuredContent` and a text rendering (D-33).
- A calendar without a free/busy answer is never reported free (D-34).
- A destructive or wider-sharing tool is registered only under its
  flags, and still needs `confirm` (D-55).
- The server never asks the person a question (D-56).
- No tool changes the server's settings (D-59).
- A released tool name, output field or resource URI changes only in an
  acknowledged break (D-68).
- A default live run reaches nobody, and writes on the primary calendar
  only its one status event (D-71, D-72).

## Cross-cutting concerns

**Configuration.** Every setting is a `GCAL_*` variable with a flag
bound to it. A flag overrides the environment, and the environment
overrides the default. Validation runs once at start, and a removed
setting is refused with its replacement. Every reader goes through
`config.Lookup`, which drops the spaces around a value (D-63). The full
list is in [configuration.md](configuration.md).

**Time.** A caller passes a date, a zoned time or a window of two. A
date never becomes an instant, a zoned time carries an IANA zone, and
an all-day end is the last day, inclusive (D-10, D-11). The zone
resolves in one order, and every read names it and its source (D-12).
The server's own clock is used only for token expiry and for a relative
window, which the result prints, so a skewed clock shows in the output.

**Errors.** Every tool error starts with a class in brackets, and the
class tells the model what to do next:

- `[invalid]`: fix the arguments.
- `[not_found]`: check the address.
- `[auth]`: not signed in, or a scope is missing; run `login`.
- `[forbidden]`: signed in, but the role on the calendar is too low.
- `[conflict]`: the resource's state refuses the operation.
- `[stale]`: what was read has moved; read again.
- `[ambiguous]`: a title matched several calendars; pass an id.
- `[blocked]`: a guard refused what the API would allow; use the tool
  the refusal names, if it names one.
- `[rate_limited]`: Google's quota; back off.
- `[unavailable]`: a transient upstream failure; retry.
- `[unsupported]`: the operation cannot be built here.
- `[ambiguous_outcome]`: a write may have landed; read before retrying.

`make classes` holds the vocabulary closed from both sides (D-65).

**Notification.** Who a write reaches is counted by address. `notify` is
required on every write that reaches somebody, and a result reports
what was asked of Google, never what arrived (D-21, D-22, D-23, D-24,
D-25, D-26).

**Logging.** `slog` to stderr, as text or JSON. A line names the method,
the tool, the outcome, the duration and a failed tool call's error
class, and nothing from the calendar.
Transport errors are stripped of their URL before they are logged,
since a search term travels in the query string (D-03).

**Limits and retries.** Google meters requests per user per minute. The
fan-out tools have a concurrency limit and a budget, and every result
says how many requests it spent (D-32). A read retries a transient
failure with Google's backoff. A write retries only where a repeat lands
in the same place, and `[ambiguous_outcome]` covers the rest (D-66,
D-38).

**Scopes.** Login asks for the scopes the settings need: the read scopes
alone under read-only mode, and never the broad `calendar` scope.
Reading a calendar's sharing rules needs a scope of its own (D-60,
D-61). The list to paste into the consent screen is in
[gcp-setup.md](gcp-setup.md).

**Testing.** The unit suite runs against `internal/gapi/caltest`, which
encodes what we believe Google does (D-05). `make live` drives the
server against a real account and is the only check that can show a
belief wrong (D-70). `make live-cover` runs in `make check` and fails on
a tool no live step exercises. `make evals-check` holds each eval's
scorer to failing on an untouched calendar (D-73). The renderer has
golden files, and every package has a coverage floor. How to run a live
check is in [CONTRIBUTING.md](../CONTRIBUTING.md#the-live-run).

**Documentation.** A decision lives once, in part 2 here. What was
verified about Google lives in the comment at the line that depends on
it: the behavior, the source link, where the source is wrong or silent,
and the live step that checks it. A refuted belief goes at that line or
in a decision's `Rejected:` line. The docs gate holds every D-ID
citation to an entry, and every "Held by" cell to a gate or test that
exists. The staleness gate holds the README,
[configuration.md](configuration.md) and [gcp-setup.md](gcp-setup.md)
to the code.

## Live checks

A live probe asks one question and reports its verdict apart from its
output. Its input must be able to tell the answers apart: a stale etag
shows whether `If-Match` is honored, and a current one shows nothing. A
step whose answer the driver cannot see reports itself undetermined
rather than passing. Who received an invitation shows in an inbox and
nowhere in the API, so the steps that mail a real guest run only when
armed, and a person scores them (D-71). The driver writes nothing on the
primary calendar beyond its one status event (D-72).

## Decisions

| ID | Decision | Status | Held by |
| --- | --- | --- | --- |
| D-01 | One stdio binary per person, on the deployer's own OAuth client | accepted | smoke, leaks, TestLoginRejectsAWebClient, review |
| D-02 | Stdout carries only MCP JSON-RPC frames | accepted | forbidigo, smoke, TestServeOverTheInjectedStreams, TestDumpSchemasWritesJSONToStdout |
| D-03 | Logs record the call, never the payload | accepted | TestLogsNeverCarryThePayload, TestAnUnregisteredToolIsLoggedAsAFailure, TestTransportErrorsCarryNoURL, TestURLErrorIsStripped, TestInspectDoesNotLeakTheTokenOnATransportError |
| D-04 | Nothing in the repository identifies a real account, calendar or person | accepted | leaks, leaks-history, secrets, TestLeakRulesCatchTheirShapeAndAllowTheReserved, TestTheHistoryScanReadsATagButNotItsTagger, TestLeakGateRefusesATreeBelowTheFloor |
| D-05 | Fixtures are generated, and the fake reproduces Google's quirks | accepted | TestSeedIsSynthetic, TestSeedCoversTheShapesThatGoWrong, TestConferenceDataIsIgnoredWithoutTheVersion, TestStatusEventsFollowGooglesGuide, TestUpdatedMinIncludesDeletionsAndRefusesASyncToken |
| D-06 | Own wire types over raw REST | accepted | depguard, api-fields |
| D-07 | An event keeps every field it does not model | accepted | TestAnEventKeepsWhatItDoesNotModel, TestASplitCarriesWhatThisServerDoesNotModel, TestConferenceRoundTripKeepsWhatItDoesNotModel |
| D-08 | Every API method and field has a written verdict | accepted | api-coverage, api-fields |
| D-09 | The server answers when, hands off what, and names its tools apart | accepted | TestAReadShowsTheFilesOnAnEvent, TestInstructionsNameTheTraps, review |
| D-10 | A date is not a time, and every timed write carries an IANA zone from the call, the calendar or the settings | accepted | TestDateIsNotAnInstant, TestAllDayEventDoesNotSlipWestOfUTC, TestADateIsNotATime, TestAllDayNeverRendersATime, TestAllDayEventStaysADate, TestAnAllDayEventNeverRendersATimeThroughTheTools, TestEveryTimedWriteCarriesItsZone, TestZonedKeepsTheZoneNotJustTheOffset, TestExpandRefusesWithoutAZone, TestWeeklyHoldsItsWallClockAcrossSpringForward, TestHalfHourTransition, TestResolveOrder, TestResolveRefusesRatherThanFallingBack, TestNoZoneAnywhereRefuses, TestZoneFallsBackToTheAccountSettings, TestLoadLocationRejectsLocalAndEmpty |
| D-11 | An all-day end is the last day, inclusive, on the way in | accepted | TestAllDayEndIsInclusiveOnTheWayIn, TestOneDayAllDayEventDoesNotReadAsTwo |
| D-12 | Every read states its window, its zone and whether it is complete | accepted | TestScheduleStatesItsWindowZoneAndCompleteness, TestBudgetTruncatesAndSaysSo, TestListEventsWindowAndZone, TestZoneExplainNamesTheSource, TestGoldenScheduleTruncated |
| D-13 | The caller chooses occurrences or the series | accepted | TestExpandChoosesBetweenSeriesAndInstances, TestScheduleSaysWhichRecurrenceShapeItReturned, TestListEventsWarnsAboutTheRecurrenceChoice |
| D-14 | A page token is bound to the read that issued it | accepted | TestAPageTokenKeepsItsWholeQuery, TestPageTokenResumesEachCalendarSeparately, TestPageTokenFromOtherCalendarsIsRefused, TestAPageTokenDoesNotCarryTheSearchText, TestAnInstancesPageTokenKeepsItsRead, TestAForeignPageTokenIsRefusedByListChanges |
| D-15 | A search result is a suggestion, never proof of absence | accepted | TestSearchDescribesItsOwnUnreliability, TestQMatchesTheFieldsGoogleDocuments |
| D-16 | A calendar is `primary`, an id or a title that matches once | accepted | TestResolveCalendarRefusesAmbiguity, TestResolveCalendarFallsBackToAnUnsubscribedId, TestHiddenCalendarStillResolvesByTitle |
| D-17 | An event is a calendar plus an id, and an occurrence its own id or its series plus `original_start` | accepted | TestAnOccurrenceIsAddressableByItsScheduledStart, TestSplitOccurrenceID, TestInstancesRefusesAnOccurrenceID, TestICalUIDFindsTheEventAnInvitationIsAbout |
| D-18 | The recurrence scope is required, never inferred | accepted | TestScopeIsRequiredOnARecurringEvent, TestParseScopeRefusesWithTheThreeChoices, TestUpdateRefusesARecurringEventWithoutAScope, TestScopeSeriesFollowsAnInstanceToItsParent, TestScopeInstanceAgainstTheSeriesIDIsRefused, TestAMisspelledScopeIsRefusedEvenOnASingleEvent |
| D-19 | `this_and_following` is two calls that copy the series whole and say what they reset | accepted | TestThisAndFollowingIsTwoCallsAndSaysWhatItReset, TestASplitCarriesWhatThisServerDoesNotModel, TestSplitCountsWhatTheRuleGenerates, TestBothHalvesOfASplitCarryTheNotifyChoice, live |
| D-20 | A recurrence is the caller's RRULE, validated and explained, never composed | accepted | TestParseRule, TestExplain, TestUnsupportedLinesAreCarriedNotGuessed, TestExpandRefusesAFrequencyItCannotWalk |
| D-21 | `notify` is required on every write that can reach a person, with no default | accepted | TestNotifyIsRequiredWhenTheWriteReachesSomebody, TestUnknownNotifyIsRefusedWithTheChoices, TestCreateEventRefusesWithoutNotifyWhenItHasGuests, TestShareNotifyHasNoDefault, TestEveryWriteToolDocumentsNotifyAndDryRun |
| D-22 | The address decides who a write reaches | accepted | TestNotifyIsNotAskedWhenNobodyIsReached, TestARoomIsNotAGuest, TestAPersonPassedAsARoomIsStillReached, TestReachOfEventCountsTheGuestsTheWriteAdds, TestTheAccountIsNeverItsOwnGuestEvenWithoutTheSelfFlag, TestTheOrganizerIsNotAGuest, TestARemindersOnlyUpdateReachesNobody |
| D-23 | A result says what was asked of Google, never what a guest received | accepted | TestAllIsReportedAsAskedForNotAsDelivered, TestShareReportNeverClaimsDelivery, TestCancelingQuietlySaysTheGuestsStillHaveIt, TestAChoiceOnAWriteThatReachesNobodyStillReportsNobody |
| D-24 | `none` is refused when a guest is outside the organizer's domain | accepted | TestNoneIsRefusedForAnOutOfDomainGuest, TestCreateEventRefusesNoneForAnOutsideGuest, TestNoneIsAllowedInsideTheDomain, TestAnEventOrganizedByACalendarStillKnowsWhoIsInside, live |
| D-25 | A sharing change takes `all` or `none`, and removing access takes neither | accepted | TestShareNotifyRefusesExternalOnly, TestShareCalendarRequiresNotify, TestShareCalendarSendsFalseRatherThanNothing, TestTheSharingNotifyRefusalOffersOnlyWhatItAccepts, TestUnshareSaysNobodyIsTold |
| D-26 | Invitations go out through `notify` on the ordinary writes, not a gated sending tool | accepted | TestTheDefaultSurfaceIsEverythingButTheGatedTools, review |
| D-27 | Patch always, PUT never | accepted | api-coverage, TestPatchSendsOnlyWhatChanged, TestEventPatchDistinguishesClearingFromLeaving, TestGuestsAreModifiedNotReplaced, TestCalendarPatchSendsOnlyWhatChanged |
| D-28 | Every write is made under the etag of the read that planned it | accepted | TestUpdatePatchesUnderIfMatch, TestMoveIsMadeUnderIfMatch, TestAnEtagThatMovedIsStale, TestEachWriteCarriesItsOwnResourcesETag, TestForceWritesWithAStarAndSaysSo, TestAConcurrentEditOnADeleteIsStaleNotGone, live |
| D-29 | An event write under the server's own etag is planned again once after a 412 | accepted | TestEveryEventWriteIsMadeAfterOneFreshRead, TestAMoveStraightBackIsMadeAfterOneFreshRead, TestASecondTryThatWouldDoSomethingElseIsStale, TestAnEtagFromTheCallerIsNotReadAgainAfterA412, TestASecondTryKeepsWhatArrivedBetween, live |
| D-30 | A dry run writes nothing | accepted | TestDryRunWritesNothing, TestCreateCalendarDryRunWritesNothing, TestShareCalendarDryRunWritesNothingAndShowsTheResult, TestAFailedDryRunClaimsNothingLanded, TestSharingDryRunNotesClaimNothingHappened |
| D-31 | A write result shows the event before and after, and whom Google was asked to tell | accepted | TestGoldenWrite, TestADryRunProjectsTheChange, TestADryRunShowsBothHalvesOfWhatItWouldDo, TestAPartialManageCallSaysWhatAlreadyLanded |
| D-32 | One tool call is one API request, and a call that needs more says how many | accepted | TestTheReportedRequestCountIsTheTruth, TestAWriteSpendsTheRequestsItShould, TestTheCalendarWritesSpendWhatTheySay, TestFanOutCountsItsRequests, TestFanOutIsBounded, TestCalendarListIsReadOnce |
| D-33 | A reply carries both halves, in different forms | accepted | TestBothHalvesDiffer, TestEveryToolAnswers, TestCalendarsResultRendersAndCarriesBothHalves |
| D-34 | Availability comes from free/busy, and a calendar without an answer is unknown | accepted | TestUnknownIsNeverFree, TestAvailabilityNeverCallsAnUnreadableCalendarFree, TestCalendarMissingFromTheResponseIsUnknown, TestACalendarStillPastTheCapIsUnknown, TestAGroupWithAnUnreadMemberIsUnknown, TestNoCalendarReadableReportsNoFreeTime, TestAvailabilityDoesNotResolveEveryAddress |
| D-35 | Availability has a ceiling of its own, not a setting | accepted | TestAvailabilityRefusesTooManyCalendars, TestMaxCalendarsCannotExceedTheAPICeiling |
| D-36 | Working hours mask free time only when the caller gives them | accepted | TestAvailabilityWorkingHours, TestAvailabilityRefusesAMaskItCannotApply, TestHoursMaskOneDayPerKeptWeekday, TestHoursHoldTheWallClockAcrossATransition, TestUnmaskedHoursProduceNothing, TestGoldenAvailabilityWorkingHours |
| D-37 | `list_changes` hands the sync token to the caller and stores nothing | accepted | TestChangesReportsADeletionAListCannot, TestChangesBaselineHandsBackAToken, TestABaselinePagesPastItsBudgetToReachTheToken, TestAnUnfinishedIncrementalReadHandsBackNoSyncToken, TestAnExpiredTokenSaysToStartOver, TestUpdatedSinceIsRefusedAlongsideASyncToken, TestATokenFromAReadSinceAMomentChains, live |
| D-38 | `create_event` mints the event id, and an unseen answer is `[ambiguous_outcome]` | accepted | TestCreateEventMintsALegalIDAndSendsTheZone, TestNewEventIDIsAlwaysLegal, TestAnInsertThatMayHaveLandedIsAmbiguous, TestAnInsertGoogleRefusedIsNotAmbiguous |
| D-39 | `cancel_event` is an ordinary write, not a gated one | accepted | TestTheDefaultSurfaceIsEverythingButTheGatedTools, TestCancelingAWholeEventDeletesIt, TestCancelingOneOccurrenceIsAStatusPatch, TestCancelingQuietlySaysTheGuestsStillHaveIt |
| D-40 | An attendee write changes only what was asked: the caller's own answer, and a guest in the role asked | accepted | TestRespondChangesOnlyThisAccountsAnswer, TestRespondRefusesWhenThisAccountIsNotAGuest, TestRespondRefusesThisAndFollowingAsUnsupported, TestOptionalGuestsAreReachedLikeGuests, TestOptionalGuestsRefuseRoomsAndDoubleRoles, TestAnAddressAlreadyOnTheEventIsReportedNotSkipped, TestCreateEventBooksRoomsWithoutReachingAnybody |
| D-41 | An address arrives as one mailbox, and its display name goes nowhere | accepted | TestAMailboxIsTakenAsItsBareAddress, TestAGuestGivenAsAMailboxIsItsAddress, TestShareCalendarTakesAMailbox, TestAvailabilityTakesAMailbox, TestSomethingThatIsNotAnAddressIsRefused |
| D-42 | A Meet link is asked for on create or by `add_conference`, never copied by a split | accepted | TestConferenceRequestAsksForMeetWithTheEventID, TestAddConferenceAsksForAMeetLink, TestAddConferenceRefusesAnEventThatHasOne, TestAddConferenceDoesNotGoWithASplit, TestAddConferenceAfterTheFirstLinkWasRemoved, TestCreateEventRefusesAConferenceTheCalendarForbids, TestConferenceDataIsIgnoredWithoutTheVersion, live |
| D-43 | Status events are made by `create_event`, held to Google's guide before any request | accepted | TestAStatusEventIsBuiltAsGoogleRequires, TestAStatusEventGoogleWouldRefuseIsRefused, TestAStatusEventOnAnotherCalendarIsRefused, TestAStatusEventTakesNoGuestsOnAnyWrite, TestAStatusEventIsHeldToGooglesRulesOnEveryWrite, live |
| D-44 | Declining every meeting is `create_event_declining_all`, behind a flag of its own | accepted | TestCreateEventRefusesDecliningEveryMeeting, TestDecliningAllNeedsItsOwnFlag, TestDecliningEveryMeetingIsItsOwnToolThroughTheProtocol, TestCreateDecliningEventTakesOnlyTheStatusEventsThatDecline, TestEveryCoverThatGrowsIsSaid |
| D-45 | An event past Google's guest threshold says its answers are incomplete | accepted | TestACrowdedEventSaysItsRSVPsAreIncomplete, TestTheCrowdWarningCountsWhatGoogleCounts, TestACrowdedEventWarnsOnTheReadPathToo, TestAnOrdinaryEventDoesNotWarnAboutItsSize |
| D-46 | `manage_calendar` is one tool over two resources, and takes no etag | accepted | TestManageSeparatesTheCalendarFromTheSubscription, TestCalendarPatchesCarryTheirOwnETag, TestAPartialManageCallSaysWhatAlreadyLanded, TestManageNotificationsReplaceTheWholeList |
| D-47 | `delete_calendar` takes only a secondary calendar, and `clear_calendar` only the primary | accepted | TestDeleteCalendarRefusesThePrimary, TestClearCalendarRefusesASecondaryCalendar, TestClearCalendarEmptiesThePrimary, live |
| D-48 | Access is checked against the role Google documents, before `confirm` | accepted | TestShareCalendarRefusesWithoutOwnerAccess, TestManageRefusesTheSharedHalfWithoutAccess, TestManageOverridesWorkOnAReadOnlyCalendar, TestDeleteCalendarRefusesWhatThisAccountDoesNotOwn, TestCanWrite |
| D-49 | A risky form of a write is a tool of its own, never an argument | accepted | TestCreateEventRefusesDecliningEveryMeeting, TestAnOrdinaryShareStopsAtTheOrganization, TestTheGatedToolsNeedTheFlag |
| D-50 | Sharing is three tools by how far it reaches | accepted | TestAnOrdinaryShareStopsAtTheOrganization, TestAWideShareIsOutsideOrAnOwner, TestShareCalendarStopsAtTheOrganization, TestWidenCalendarAccessTakesOutsidersAndOwners, TestPublishCalendarOpensADomainAndTakesNoPerson, TestAPublicRuleIsNeverOwner, TestShareCalendarLowersAWiderRule, TestWithinRuleOnlyLowersOrKeepsAnExistingRule |
| D-51 | Inside the organization is the account's own domain, and any doubt counts as outside | accepted | TestOutsideIsAnyDomainButTheAccountsOwn, TestShareCalendarOnAPersonalAccountTakesNobody, TestAnUnknownOrganizerDomainCountsEverybodyAsOutside |
| D-52 | The wider shares name the calendar by the title they read | accepted | TestAWideShareRequiresTheCalendarTitle, TestAWideShareNamesTheCalendarItOpens |
| D-53 | A share reads the rules first, and says who could see the calendar before and after | accepted | TestShareCalendarGrantsAccessAndShowsExposureBothWays, TestShareCalendarPatchesAnExistingRule, TestParseAudienceReusesTheScopeTypeOfAnExistingRule, TestShareCalendarSaysHowItReadTheAddress, TestAGroupShareSaysWhoItReaches, TestListSharingExplainsEveryRole |
| D-54 | A tool's `Kind` decides its annotations and whether it registers | accepted | TestAnnotationsMatchTheKind, TestGatesAreServerSide, TestFullSurfaceRegistersEverything, TestFullSurfaceOpensEveryGate, TestSharingOffRemovesEverySharingTool |
| D-55 | Destructive tools exist only when the operator turns them on, and still take `confirm` | accepted | TestTheGatedToolsNeedTheFlag, TestDeleteCalendarNeedsConfirm, TestClearCalendarNeedsConfirm, TestPublishCalendarNeedsConfirmAndSaysItPublishes, TestDeleteCalendarThroughTheProtocolStillNeedsConfirm, TestClearCalendarDryRunNeedsNoConfirm, TestConfirmRefusesWithoutIt |
| D-56 | The server asks the person nothing | accepted | TestTheWriteToolsWorkThroughTheProtocol, TestEveryToolAnswers, TestARemovedSettingIsRefusedUnlessOff, live |
| D-57 | The `requiresUserInteraction` mark goes only where the prompt is the point | accepted | TestWhichToolsPromptEveryCall, TestOnlyAFlaggedToolMayPromptEveryCall |
| D-58 | Annotations say exactly what a tool changes | accepted | TestOnlyToolsThatAddSayTheyAreNotDestructive, TestReadToolsAreAnnotatedReadOnly, TestListSharingIsAnnotatedReadOnly, TestTheSharingWritesClaimAnOpenWorld, TestAnnotationsMatchTheKind |
| D-59 | No tool changes the server's own settings | accepted | TestNoToolWritesTheServersOwnSettings |
| D-60 | Read-only mode registers the reads and asks for read scopes only | accepted | TestReadOnlyModeDropsEveryWrite, TestReadOnlyKeepsEveryReadTool, TestReadOnlyAsksForNoWriteScope, TestServeRespectsReadOnly, TestARemovedSettingIsRefusedUnlessOff |
| D-61 | Login asks for the narrowest scopes and records what Google granted | accepted | TestScopesNeverIncludeTheBroadScope, TestACLReadNeedsItsOwnScope, TestLoginNamesTheAccountWithoutAnEmailScope, TestLoginWarnsAboutScopesGoogleDidNotGrant, TestMissingScopes, TestSetupScopeProblems, staleness |
| D-62 | Login is the loopback flow with PKCE, and the token is readable by its owner alone | accepted | TestLoginUsesLoopbackLiteralAndPKCE, TestLoginRejectsAStateMismatch, TestNoBrowserPrintsThePortToForward, TestFileIsOwnerOnly, TestRestrictToOwnerSetsAProtectedACL, TestFileFallbackWarnsEveryTime |
| D-63 | Every setting is a `GCAL_` variable, read through `config` | accepted | TestEnvThenFlag, TestEveryFlagNamesItsEnvVar, TestEveryReaderOfAVariableGoesThroughConfig, TestEveryVariableTheCodeNamesIsInEnvVars, TestVariableNamesAreSpelledFromConfig, TestASettingOfSpacesIsUnset, TestARemovedSettingIsRefusedUnlessOff, TestInvalidConfigurationFailsBeforeAnnouncingItself |
| D-64 | The server starts and lists its tools before anyone signs in | accepted | TestBuildServiceFailsWithoutCredentialsAndTheServerStartsAnyway, TestUnauthenticatedAnswersEveryCall, TestDumpSchemasNeedsNoCredentials, smoke |
| D-65 | Errors use a closed vocabulary, held from both sides | accepted | classes, TestEveryEmittedClassIsInTheVocabulary, TestErrorClassification, TestToolErrorsAreClassified, TestAnImpossibleZoneIsInvalidNotRetryable, TestAPlainForbiddenIsNotReportedAsAMissingScope |
| D-66 | A write is retried only where a repeat lands in the same place | accepted | TestANonIdempotentWriteIsSentOnce, TestANonIdempotentWriteIsNotRepeatedAfterADroppedConnection, TestADeleteRetriedIntoNotFoundIsAmbiguous, TestADeleteRetriedAfterARateLimitIntoNotFoundIsNotFound, TestAMoveThatMayHaveLandedIsAmbiguous, TestAShareThatMayHaveLandedIsAmbiguous, TestBackoffDoublesAndIsCapped, TestRetriesTransientFailures |
| D-67 | Resources mirror three reads, and no resource holds a schedule | accepted | TestResourcesAreListedWithTheirTemplates, TestAResourceCarriesTheToolsOwnText, TestACalendarIDWithAnAtSignReadsEitherWay, TestAURIThatNamesNothingIsNotFound |
| D-68 | The released tool surface is a contract | accepted | schema-diff, schema-ack, TestSchemaAckNeedsEveryItemNamed, TestABreakIsABreakOfARelease, TestDumpSchemasUsesTheFullSurface, TestTheDumpCarriesEveryField |
| D-69 | Tests never reach the real keyring | accepted | TestTheRealKeyringIsUnreachableFromTests |
| D-70 | A write-path change counts only after a live run whose transcript is read | accepted | live-cover, transcript, live |
| D-71 | The live driver reads only what it made, prints nothing it did not write, and by default reaches nobody | accepted | TestOnlyACalendarTheDriverMadeIsAdopted, TestEmptyingMailsNobodyUnlessASweepAsks, TestSyncAndPageTokensAreRedacted, transcript, TestAnUnarmedRunSendsNoRequestThatReachesAPerson, TestAnUnarmedRunCallsNoToolThatReachesAPerson, TestAnUnarmedRunCallsNoRealGuestStep, TestBothPathsToGooglePassTheReachGuard, TestSpikeNotifyNeedsEveryInviteeNamed, TestAnArmedRunReachesOnlyTheNamedInvitees |
| D-72 | One status event per run may live on the primary calendar, and nothing else is written there | accepted | TestTheGuardHoldsTheDriversOwnCalls, TestTheGuardLetsOneStatusEventOntoThePrimaryCalendar, TestTheStatusEventIsRecordedBeforeItIsAskedFor, TestTheCleanupReadsTheStatusEventBackGone, TestEveryStopSignalEndsTheStepsNotTheDriver, TestAClosedTranscriptPipeDoesNotEndTheDriver |
| D-73 | Evals score the calendar, never the model's account of it | accepted | evals-check |
| D-74 | Releases are `go install` and signed archives | accepted | release, pins, TestTagMajor, TestTheTagMajorMustBeTheModules, TestTheCommittedReleaseConfigIsValid, TestTheReleaseWorkflowTriggersOnATag |
| D-75 | The bundle ships inside the signature, and the release wiring is checked on every commit | accepted | release, TestTheWaysTheReleaseWiringBreaks, TestTheBuildMatrixIsTheOneDocumented, TestPackRunsEveryCheckTheGateRuns, TestThePackedManifestCarriesTheRealVersion |
| D-76 | The registry entry is published after the release, from its signed checksums | accepted | server-json, TestTheRegistryEntryIsBuiltFromTheRepository, TestTheRegistryEntryReadsTheVettedManifest, TestTheBundleRowMustBeExactlyOne, TestTheEntryRefusesATagOfAnotherMajor |
| D-77 | Every tool the release runs is pinned, and the rehearsal runs the same one | accepted | pins, TestATooledRehearsalRunsTheReleasesVersion, TestAnActionThatInstallsAToolMustPinTheTool, TestAWorkflowWrittenInFlowStyleIsStillRead, TestAPairedToolNoWorkflowInstallsIsReported |
| D-78 | The bundle asks for every setting the server reads, and its defaults configure nothing | accepted | mcpb, TestBundleVarsAreDerivedFromTheSettings, TestTheRefusalsTurnASwitchOnUnlessItDefaultsOn, TestTheRefusalsTurnSharingOff, TestTheConfigDiffNamesTheFieldsThatDiffer, TestDefaultsProblemsReportsEveryUnreadableDefaultAndTheRest, TestACredentialsFieldIsSensitive |
| D-79 | The bundle passes settings by `env` alone and starts its staged program from `${__dirname}` | accepted | mcpb, TestTheSixWaysAManifestBreaks, TestTheBundleSettingsRefuseEachBreak |
| D-80 | The gate, the packer and the registry entry read one vetted manifest | accepted | mcpb, TestRepeatedKeysNameEachPathOnce, TestTheManifestIsTheJSONThePackerWrites, TestTheStampedManifestIsHeldToTheSchema, TestAVendoredSchemaIsHeldToItsRecordedDigest, TestTheFloorComparesVersionsAsNumbers |
| D-81 | The bundle runs on every platform Claude Desktop does | accepted | mcpb, TestTheIdentityRefusesEachBreak, TestEveryStagedBinaryIsExecutable, TestAnIncompleteDistIsRefused, TestTheLauncherInstallHintIsGoModsModule |
| D-82 | Push notifications, imports and administrator access are out of scope | rejected | — |
| D-83 | The server keeps a local event cache, or the sync token | rejected | — |
| D-84 | `quickAdd` creates events from text | rejected | — |
| D-85 | The server confirms a write by asking the person through elicitation | rejected | — |
| D-86 | The Unix epoch reads as no time | rejected | — |
| D-87 | `tools.Kind` splits into an effect and a gate | rejected | — |
| D-88 | One table-driven gate holds the method, field and live-step records | rejected | — |
| D-89 | `live-cover`'s exemptions live in a TSV | rejected | — |
| D-90 | A generic wrapper serves the write tools' handlers | rejected | — |

### D-01 One stdio binary per person, on the deployer's own OAuth client

The server is one Go binary that runs as a subprocess of the MCP client
and speaks MCP over stdio. It holds one person's OAuth credentials and
calls the Calendar API directly, as that person, on a Workspace or a
consumer account. Nothing is hosted, and there is no shared deployment.

Every deployer creates their own Google Cloud project and OAuth client,
of the Desktop type. Nothing deployer-specific is built into the code,
the repository or the release artifacts, so a copy of the binary carries
no one's credentials. `login` refuses a web client's file, since the
loopback flow needs a Desktop one (D-62).

The reason is reach. A calendar holds the person's schedule and other
people's addresses. Per-user OAuth keeps every call inside the person's
own access to each calendar, and a deployer-owned client means no
credential is shared between people.

Rejected: an OAuth client shipped with the release, which would put one
deployer's credentials in every copy; a hosted deployment, which this
repository has nothing to run.
Cost: every person makes a Cloud project and runs `login` from a
terminal before the Claude Desktop bundle works, and Claude on the web
cannot reach a stdio server.

### D-02 Stdout carries only MCP JSON-RPC frames

MCP's stdio transport says the server "MUST NOT write anything to its
`stdout` that is not a valid MCP message", and "MAY write UTF-8 strings
to its standard error (`stderr`) for logging purposes"
([transports](https://modelcontextprotocol.io/specification/2025-06-18/basic/transports)).
A stray print corrupts the JSON-RPC stream, and the client silently
stops working.

So logs go to stderr through `slog`. `main` names the process's streams
once and passes them down as `io.Writer`. forbidigo refuses `fmt.Print*`
and `os.Stdout` everywhere else, with `cmd/` and `scripts/` excluded.
Its rules carry their own messages, so a settings block that fails to
load shows up as forbidigo's default message. The smoke gate drives the
binary over stdio and fails on any line that is not a frame.
`--dump-schemas` writes JSON to stdout because it does not serve.

Rejected: a print to stdout anywhere but `main`, even behind a debug
setting, since one stray line breaks the stream at any level.
Cost: only `main` names the process's streams; everything else takes an
`io.Writer`.

### D-03 Logs record the call, never the payload

A call line carries the method, the tool, the outcome, the duration and,
for a tool failure, its error class. It never carries the error message,
an email address, an event's title, description or location, a search
term, or a guest count in a context that identifies the event.

A calendar is other people's data. Every event carries the addresses of
people who never agreed to be in a log, and a log somebody is asked to
paste into a bug report has to be safe by construction rather than by
their care.

The subtle leak is the request URL. A transport failure renders the
whole URL, and a search puts its text in the query string, so `gapi`
strips the URL from transport errors before anything logs them. The
token check in `internal/auth` does the same for the access token in its
query. A failed call logs its class and no text, since a message can
quote Google's echo of the request. A failure the SDK makes itself, such
as invalid arguments or an unregistered tool, has no class to log.

`TestLogsNeverCarryThePayload` drives the tools with a payload full of
canaries and fails if one appears in a log at any level.

Rejected: payloads at debug level, since a debug log is still the file a
person pastes into a bug report.
Cost: a failure is diagnosed from the method, the tool, the outcome and
the error class alone.

### D-04 Nothing in the repository identifies a real account, calendar or person

The repository is public, and the server is built by running it against
a real account. Nothing deployer-specific lands here: no calendar ids or
URLs, no account or guest addresses, no organization names, no Cloud
project or OAuth client ids, and no title, description, location or
guest list from a real calendar. That holds for code, docs, fixtures,
goldens, transcripts, commit and tag messages, pull requests and logs.

An attendee list is personal data about people who never agreed to this
repository existing, so a leak here is a disclosure.

The leak gate is an allow-list anchored on shapes the server's own
fields cannot take: an `@` before a dotted domain, a known URL prefix, a
keyword before an id. `make leaks-history` reads every blob, commit
message and annotated tag. It allows exactly two shapes: GitHub's
support address alone, which Dependabot's sign-off carries, and an
annotated tag's tagger line, which git writes from the tagger's
identity. A tag's message is still read. gitleaks covers credentials,
which `make secrets` runs.

Rejected: a test that counts the exemption list, which would detect only
that the list changed.
Cost: each exemption is argued where it is written and has a test that
fails without it.

### D-05 Fixtures are generated, and the fake reproduces Google's quirks

`internal/gapi/caltest` is an in-memory Calendar: calendars, events,
recurrence expansion, sharing rules and free/busy. Its seed is
generated, never recorded, because a fixture copied from a live response
is itself the leak, whatever a scanner says about it.

The fake also behaves as Google does where Google surprises. It resolves
`primary` to an address-shaped id, gives events base32hex ids, honors
the page size, and sends a canceled occurrence bare, with no start and
no title. It ignores conference data without the version parameter,
refuses the status-event shapes Google's guide refuses, and includes
deletions in a read by modification time while refusing a sync token
beside it.

The reason is that a fake easier than the API makes a whole path
untestable while every test is green. A literal `primary` id hides the
primary calendar's address, and hyphenated ids hide the occurrence-id
grammar (D-17).

Rejected: a fixture copied from a live response.
Cost: the fake must reproduce each quirk, or the path it hides goes
untested. It encodes what we believe Google does, which only a live run
checks (D-70).

### D-06 Own wire types over raw REST

`internal/gapi` calls the Calendar REST endpoints directly, and
`internal/gcal` holds wire types written by hand for the fields the
server uses. Nothing imports `google.golang.org/api`.

That package drags in gRPC and telemetry for a handful of JSON calls.
Its generated types would also decide which fields an event keeps,
where this server needs to keep the ones it does not model (D-07).

The client stays small enough to read in full. Each method it calls has
a verdict in `testdata/api-coverage.tsv`, and each field of the
resources it models a verdict in `testdata/api-fields.tsv`, so "the
fields actually used" is an argued list rather than an accident (D-08).

Rejected: `google.golang.org/api`, which drags in gRPC and telemetry.
Cost: every field is hand-written and needs a verdict, and a field
Google adds stays unread until someone records it.

### D-07 An event keeps every field it does not model

An event decodes into the fields the server models, and keeps every
other field as Google sent it. A write that copies an event whole, the
new series of a `this_and_following` split, carries those fields too:
the attachments, a label, other applications' extended properties and a
status event's details.

The reason is that the split makes a new event from the old one. Built
from the modeled fields alone, the new series would lose what the person
attached and labeled. A working-location series would also reach Google
without the details its
[status-events guide](https://developers.google.com/workspace/calendar/api/guides/calendar-status)
says a create needs. The insert sets `supportsAttachments=true` when it
carries files and `eventLabelVersion=1` when it carries a label. It
leaves behind what Google sets itself, and the conference, which a
split never copies (D-42).

So for an event, `out` in `testdata/api-fields.tsv` means not read,
never lost.

Rejected: decoding an event into the modeled fields only.
Cost: a calendar, a sharing rule and a nested object such as a guest
still drop their `out` fields, so none of them can be copied whole.

### D-08 Every API method and field has a written verdict

`scripts/gates api-diff` writes `testdata/api-surface.json` from
Google's
[discovery document](https://www.googleapis.com/discovery/v1/apis/calendar/v3/rest),
and nobody edits it. `testdata/api-coverage.tsv` holds one hand-written
verdict per method: the tool that uses it, or why it is written off.
`testdata/api-fields.tsv` does the same for every field of `Event`,
`Calendar`, `CalendarListEntry` and `AclRule`, and of every schema they
reach by `$ref`. An object Google declares inline, such as an event's
`creator`, has one verdict for the whole object.

The gates fail in both directions. A published method or field with no
verdict fails, a client call with no row fails, and a verdict on
something missing from the snapshot fails. The four resources are
checked by name, so none can leave the record by an edit to the
snapshot.

The reason is the `Event` resource. It holds most of this API's
capability, so a list of methods hides fields nobody considered, such
as attachments or a guest's own nested fields.

Rejected: recording the methods alone, which hides an unconsidered field
inside a method the server already calls.
Cost: `make api-diff` is manual, so a field Google adds waits until a
maintainer refetches the discovery document.

### D-09 The server answers when, hands off what, and names its tools apart

The scope is the calendar, every capability of it: calendars and the
calendar list, events and their recurrences, availability, sharing,
colors and the person's own settings. A conference attached to an event
is a Calendar field, so it is in scope.

What a meeting produces rather than is stays out: the Meet recording,
the notes document and the attached file belong to the servers built on
the Drive and Docs APIs. Which files are attached is a fact about the
event, so a read lists each with its title, type, link and Drive file
id, which is what those servers take. This server never opens or writes
one. Google Tasks is a separate API.

Tool names are chosen so a client can run this server beside the Drive,
Docs and Sheets servers without a collision: `search_events` and
`list_calendars` here, where those say `search_files`,
`search_documents` and `search_spreadsheets`. The server instructions
name the boundary and the traps a model meets first.

Rejected: the Meet API's recordings and transcripts, which are what a
meeting produced rather than when it is.
Cost: a person who wants a meeting's recording or notes needs the Drive
or Docs server beside this one.

### D-10 A date is not a time, and every timed write carries an IANA zone from the call, the calendar or the settings

Google's `EventDateTime` is a `date` or a `dateTime`, never both. An
all-day event has no instant, and forcing one picks a day: read as UTC
midnight, it lands a day early for everyone west of UTC.

So `internal/when` has a `Date` and a `Zoned`, and no conversion from
one to the other. Only the renderer projects a date onto a zone, for
display, labeled.

Every `dateTime` carries an IANA zone, not just an offset. An offset is
a fact about one moment and cannot expand a recurrence across a
daylight-saving change. Google's
[events reference](https://developers.google.com/workspace/calendar/api/v3/reference/events)
says of `timeZone`: "For recurring events this field is required", and
refuses a recurring event without one. The server sends `timeZone` on
every write, recurring or not, so one code path runs every time.

The zone is read, never assumed: the zone the call names, else the
calendar's `timeZone`, else the account's `timezone` setting. Never the
process's zone, and never UTC as a fallback. Every read names which it
used. The binary imports `time/tzdata`, because Windows has no zone
database Go can read.

Rejected: an all-day event as UTC midnight; an offset alone; the
process's zone, or UTC, as a fallback.
Cost: the server has no opinion about "today" until a zone resolves, and
the zone database adds about 400 KB to the binary.

### D-11 An all-day end is the last day, inclusive, on the way in

Google's wire end for an all-day event is exclusive: the
[events reference](https://developers.google.com/workspace/calendar/api/v3/reference/events)
calls `end` "the (exclusive) end time of the event". A one-day event on
the 20th ends on the 21st.

The renderer shows an all-day end inclusive, as a person reads it. So
the tools take it inclusive too: the value a caller reads is the value a
caller writes. Taking Google's form on the way in would make a model
that copies a rendered event write one day short, or read a one-day
event as two.

The conversion lives in one place, and every tool description that
takes an all-day end says it is inclusive.

Rejected: Google's exclusive wire end in the tool inputs.
Cost: one conversion, and every description that takes an all-day end
must say "inclusive".

### D-12 Every read states its window, its zone and whether it is complete

A model asked "am I free Thursday afternoon" must not be able to answer
from a different Thursday, a different zone, or the first page of
three.

So every read result carries the absolute window it queried, the IANA
zone its times are shown in and where that zone came from (D-10), which
calendars it read, how many events matched and how many are shown, and
a continuation token when there are more. A truncated read says so in
the text a model sees, not only in a structured field it may not be
shown. Reads are budgeted in events rather than requests, and the
budget is stated.

Relative dates such as "next Tuesday" are the model's to resolve, since
the model knows what the person meant and the server does not. The
server makes the resolution checkable instead: it echoes the absolute
window, so a model that resolved "Tuesday" wrongly can see it did. The
server's own clock is used only for token expiry and for a relative
window, which is printed, so a skewed clock shows in the output.

Rejected: resolving "next Tuesday" in the server.
Cost: every read renders its window and zone, in both halves, even when
the caller did not ask.

### D-13 The caller chooses occurrences or the series

Google's `singleEvents` changes what `events.list` returns, not how it
looks. The
[events.list reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/list)
says it decides "whether to expand recurring events into instances and
only return single one-off events and instances of recurring events,
but not the underlying recurring events themselves". Ordering by start
time needs it set.

So the two shapes answer different questions. Expanded, each occurrence
is a row, which answers "what is on this week". As a series, a repeating
event is one row with its rule, which is what a caller needs before
changing the whole series.

`list_events` expands by default, and `no_expand` asks for the series.
Its description says the two are different, and the result says which
shape it returned, in both halves.

Rejected: picking one shape silently.
Cost: a caller who wants the series must ask for it, and a model that
misses the choice reads occurrences.

### D-14 A page token is bound to the read that issued it

Google resumes a page token only for the query that issued it. A
continuation under another query would skip or repeat rows while its
result named the new query.

So the token this server hands back carries everything that decides the
rows. For the schedule reads that is the calendars, the window as two
instants, the expansion choice, `show_canceled`, `event_types`,
`ical_uid`, and the search text as a digest, since a token goes back to
the caller and a search term is content. `list_instances` binds its
series, window and `show_canceled`. `list_changes` binds its calendar,
the sync token as a digest, and the `updated_since` instant. A
continuation that changes any of them is refused, naming what differs.

A read across several calendars holds one Google token per calendar,
naming only the calendars with more to read. Its budget is divided
across the calendars before the read, so nothing fetched is thrown away
past a token that already moved beyond it.

Rejected: one Google token for every calendar.
Cost: a token from an earlier version of the server is refused, and the
caller starts the read again.

### D-15 A search result is a suggestion, never proof of absence

`search_events` is `events.list` with `q`, fanned out across the
calendars named. Google's
[events.list reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/list)
documents `q` as "free text search terms to find events that match
these terms" in named fields: the title, description and location, each
guest's and the organizer's name and address, and a working location's
building, desk and labels. Built-in words such as "Out of office" match
those kinds of event in any language.

What it does not document is how words are matched, and there is no
field syntax, so a search cannot be narrowed to one field. An event the
caller sees only as busy has nothing to match.

So the tool description and the server instructions say all of this,
and a model treats an empty result as "the search found nothing", never
"there is nothing".

Rejected: reporting an empty search as proof that no such event exists,
which the undocumented matching cannot support.
Cost: an empty result proves nothing, so certainty needs a
`list_events` read over the window.

### D-16 A calendar is `primary`, an id or a title that matches once

A calendar reference takes three forms, tried in order. `primary` is the
API's own alias for the account's calendar, passed through. A calendar
id is the contract: the primary calendar's id is the account's address,
and a secondary calendar's is opaque. A title is resolved against the
calendar list.

A title that matches more than one calendar is `[ambiguous]`, with the
candidates and their ids, never the first match. A first match picks a
calendar the caller did not name, and every write after it lands there.

A calendar the account is not subscribed to does not appear in the
calendar list, so it cannot be found by title, though its id still
works. The refusal says so, because "not found" for a calendar the
caller can read is false. A hidden calendar is still in the list and
resolves by title. The list is read once and cached, and every write
that changes it updates the cache.

Rejected: the first title that matches.
Cost: a title two calendars share must be passed as an id, and an
unsubscribed calendar is reached by id alone.

### D-17 An event is a calendar plus an id, and an occurrence its own id or its series plus `original_start`

An event id is unique per calendar, not globally, so an id without a
calendar is not an address.

One occurrence of a series is addressed by its own id, or by the series
id plus `original_start`, the occurrence's scheduled start. The server
takes both and says which it used. `originalStartTime` stays stable
after the occurrence is moved.

An occurrence id is the series id, an underscore and the scheduled
start. An event id is base32hex: the
[events reference](https://developers.google.com/workspace/calendar/api/v3/reference/events)
allows "lowercase letters a-v and digits 0-9", so an underscore cannot
occur in one. That grammar lets `list_instances` refuse an occurrence id
up front and name the series. Google answers such a call with 200 and
expands the one occurrence, so a canceled occurrence would read as a
series with nothing in it.

`list_events` takes `ical_uid`, the UID an invitation email carries, to
find an event. The UID is never the primary address, because every
occurrence of a series shares it.

Rejected: `iCalUID` as the primary address.
Cost: a caller holding only an invitation's UID needs a `list_events`
read, inside a window, to find the event.

### D-18 The recurrence scope is required, never inferred

"Change the 10:00 standup to 10:30" is three different operations, and
the API makes them look like one. A tool that takes an event id and a
change cannot tell which was meant.

So every event write takes `scope`, with no default. `instance` is one
occurrence and makes an exception. `series` is the parent, and so every
occurrence that is not already an exception. `this_and_following` is the
split (D-19). A write to an event with a `recurringEventId` or a
`recurrence` and no `scope` is refused `[invalid]`, naming the three
choices and what each would do here, including how many occurrences
`series` reaches. A misspelled `scope` is refused even on an event that
does not repeat.

Choosing a scope and applying it are two decisions with one owner,
`plan.Target`. `series` given an occurrence id follows it to its parent,
and `instance` given a series id is refused.

Rejected: a default scope; dropping the read of the occurrence on a
series-scoped write, which is what refuses an `original_start` that
names no occurrence.
Cost: a series-scoped write spends one more request.

### D-19 `this_and_following` is two calls that copy the series whole and say what they reset

Google has no server-side "this and following". Its
[recurring-events guide](https://developers.google.com/workspace/calendar/api/guides/recurringevents)
builds it from two calls: end the original series before the target,
then insert a new series from the target. The guide warns that
"changing all following instances resets any exceptions happening after
the target instance."

The server performs both calls. The truncated series keeps a `COUNT` of
the occurrences its rule generates before the target, not the ones that
are visible, so an excluded or an added date cannot shift it. The new
series is the original copied whole, with every field the server does
not model (D-07), and both calls carry the caller's `notify` choice. The
conference stays behind, and the result says so (D-42).

The result says in plain words that exceptions after the target were
reset, because no caller expects it.

Rejected: a split that reports only success, since the reset exceptions
are what nobody expects.
Cost: exceptions after the target are reset. An insert that fails after
the truncate leaves the later occurrences gone, reported as
`[ambiguous_outcome]`. Whether the head's count can differ when the call
reads in another zone is open (#46).

### D-20 A recurrence is the caller's RRULE, validated and explained, never composed

A recurrence arrives as an RRULE string, as
[RFC 5545](https://www.rfc-editor.org/rfc/rfc5545) defines it. The
server parses it to validate it and to explain it back in prose, such as
"every two weeks on Tuesday, 10 times", and sends the caller's own
string.

The reason is the round trip. Built from structured fields and written
back, a rule loses what the struct does not model, and a `BYDAY` is the
first thing to go.

`internal/recur` expands a rule by walking dates and carrying the wall
clock, so a weekly 09:00 stays 09:00 across a daylight-saving change.
`EXDATE` and `RDATE` are read with their `TZID`. A line it does not
model is carried, never guessed at. A series it cannot expand, such as
one with an `EXRULE`, a second `RRULE` or a frequency below daily, is
refused rather than expanded, since a count that is quietly too large is
the number a caller would show a person.

Rejected: composing an RRULE from structured fields.
Cost: `EXRULE`, a second `RRULE` and rules below daily cannot be
expanded here.

### D-21 `notify` is required on every write that can reach a person, with no default

Google's defaults disagree with each other. An event write notifies
nobody unless asked, while sharing a calendar notifies by default.
Adopting Google's defaults makes the same choice end opposite ways in
two tools, for a reason nobody can see from the call.

A default of `none` is worse. Google's own
[events.insert reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert)
warns: "Using the value `none` can have significant adverse effects,
including events not syncing to external calendars or events being lost
altogether for some users."

So `notify` is required on every write that can reach another person,
and the call is refused without it. On an event it is `all`,
`external_only` or `none`. The refusal names how many people the write
would reach, never their addresses. A write that reaches nobody does not
ask (D-22).

Rejected: `none` as the default; Google's own defaults, which are
opposite for events and for sharing.
Cost: `create_event_declining_all` takes no `notify`. A status event has
no guests, and the decline reaches other events' organizers by Google's
own doing, which no parameter of the insert governs.

### D-22 The address decides who a write reaches

A write that reaches nobody does not ask for `notify`, since a choice
with nobody behind it is friction with no safety in it. So the server
counts who a write reaches, by address.

The count is the event's guests plus every guest the write adds. The
account itself is never its own guest, whatever Google's `self` flag
says: on a secondary calendar Google leaves the flag off the account's
own row. The organizer is not a guest, and is taken as a person only
when it is not the calendar being written. A room is not a person. An
address under Google's resource domain is a room whether it came as a
room or as a guest, and a person passed as a room is still a person,
because the attendee's `resource` flag holds whatever the caller sent.
An optional guest is a guest.

Reminders are the caller's own. Google's
[events reference](https://developers.google.com/workspace/calendar/api/v3/reference/events)
calls them "the event's reminders for the authenticated user", so a
change to them alone reaches nobody.

Rejected: the attendee's `resource` flag; Google's `self` flag.
Cost: a room outside Google's resource domain counts as a guest.

### D-23 A result says what was asked of Google, never what a guest received

The API reports nothing about delivery. `sendUpdates` is a request, and
Google says it may not be honored: its
[events.insert reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert)
notes that "some emails might still be sent even if you set the value
to false."

So a result names the choice the server sent and never its outcome.
`none` is never reported as silence. `all` is never reported as
delivery, because a notification Google sends can still fail to arrive,
and the difference lies in the receiving provider rather than in the
request.

A cancellation with `none` is the sharpest case. Deleting an event with
`none` removes it from the organizer's calendar and leaves it on the
guests', still awaiting an answer. So `cancel_event` says the guests
still have the meeting unless they were notified.

Rejected: reporting `none` as silence, or `all` as delivery.
Cost: no result promises silence or delivery, so a caller who needs to
know asks the guests.

### D-24 `none` is refused when a guest is outside the organizer's domain

A guest outside the organizer's domain may have no Google Calendar at
all, and then mail is the only way they learn of the event. Invited with
`none`, such a guest receives nothing and has no calendar for the event
to appear in. The organizer's copy shows them invited, and they cannot
discover the meeting by any means. That is the "lost altogether" Google
warns of on
[events.insert](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert),
with the mechanism visible, and it is structural rather than a defect.

So `none` is refused, not warned about, whenever a guest the write
reaches is outside the organizer's domain.

The axis is the organizer's domain, not the guest's calendar system. The
same reference describes `externalOnly` as "notifications are sent to
non-Google Calendar guests only". In practice it mails a guest outside
the organizer's domain and skips one inside it, both on Google Calendar.
The domain is also what the server can tell from an address. It comes
from the organizer's address, and an event organized by a secondary
calendar is judged by the account's own domain.

Rejected: a warning a caller can skim past.
Cost: an outside guest is never added quietly. The caller chooses `all`
or `external_only`, and that guest is mailed.

### D-25 A sharing change takes `all` or `none`, and removing access takes neither

`notify` is required on a share as on an event, and here Google's
default is to send. The requirement is what stops a quiet reshare
becoming a surprise email, and a deliberate one being suppressed.

The vocabulary differs. `acl.insert` and `acl.patch` take
`sendNotifications`, a boolean
([acl.insert](https://developers.google.com/workspace/calendar/api/v3/reference/acl/insert)).
So `external_only` has nothing to map to, and it is refused
`[unsupported]`: rounding it either way emails more people or fewer than
the caller asked for. The server always sends the value, `false`
included, rather than leaving Google's default in charge.

A share has no "reaches nobody" exemption. A group address expands to
people this server cannot count, a domain rule covers everybody in the
domain, and a public rule covers the internet.

Removing access notifies nobody.
[acl.delete](https://developers.google.com/workspace/calendar/api/v3/reference/acl/delete)
publishes no `sendNotifications`, and `acl.patch` says "there are no
notifications on access removal." So `unshare_calendar` takes no
`notify`, and its result says the person is not told; they find the
calendar gone.

Rejected: rounding `external_only` to a boolean; a `notify` on
`unshare_calendar`, which would be a choice wired to nothing.
Cost: every share asks for `notify`, even one that reaches a single
named person.

### D-26 Invitations go out through `notify` on the ordinary writes, not a gated sending tool

A common rule for servers that send on a person's behalf makes sending a
tool of its own, behind a flag, with drafts by default. Here the
invitations, updates and cancellations Google emails go out through
`notify` on the ordinary event writes.

Calendar has no draft. An event's email is part of the write the person
asked for, not a message of its own. Gating it would put a flag between
the model and creating a meeting, and a gate everybody turns on protects
nobody (D-39).

What holds instead is the choice itself. `notify` is required with no
default, so every write that emails says whom (D-21). `none` is refused
with a guest outside the organization (D-24). A dry run shows how many
guests a write would notify and how many are outside, before anything
is sent (D-30).

Rejected: a sending tool behind a flag, with drafts by default.
Cost: invitations go out on ordinary, ungated writes, and the client's
prompt on that write is the person's one look before mail leaves.

### D-27 Patch always, PUT never

`events.update`, `calendars.update`, `calendarList.update` and
`acl.update` are PUTs, and they replace the whole resource. Google's
[events.update reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/update)
says it "does not support patch semantics and always updates the entire
event resource." Omit `attendees` and every guest and answer is gone;
omit `recurrence` and the series is flattened.

So none of the four is ever called. `testdata/api-coverage.tsv` writes
each off by name, no client function exists for any of them, and adding
one fails `api-coverage`.

Every write is a patch carrying only the fields the caller asked to
change. `gcal.EventPatch` makes every field a pointer: a nil field is
absent from the JSON, and a field pointing at an empty value clears it.
An event's own `omitempty` fields cannot tell "leave it" from "clear
it". A guest list is read before it is written and modified, never
replaced. A field set to the value it already holds is not sent, so the
change list describes the write rather than the request.

Rejected: `events.update` and the other three PUTs.
Cost: four methods are written off, and a gate holds the client to it.

### D-28 Every write is made under the etag of the read that planned it

Calendar documents optimistic concurrency: an `If-Match` header with a
stale etag is refused with 412
([version resources](https://developers.google.com/workspace/calendar/api/guides/version-resources)).
`events.move` honors it too, though it is a POST with no body and no
page says so, and so do `calendars.patch` and `acl.patch`.

So every write to an existing event, calendar or rule carries `If-Match`
with the etag of the read that produced its plan. A 412 becomes
`[stale]`, "read again and try again", never a silent retry. A retry
would apply the caller's intent to a resource somebody else has since
changed, and a guest's answer that arrived in between would be
overwritten.

Each write carries its own resource's etag. A calendar and its entry in
the calendar list are two resources, and a series-scoped write is made
under the series' etag while the caller's is checked against the event
they addressed. A 412 on a delete is `[stale]`, never "already gone".

`If-Match: *` is there for the caller who means "whatever it says at
this moment", through `force`, and the result says it was forced.

Rejected: a silent retry on 412; a move without `If-Match`.
Cost: a write fails `[stale]` when Google itself changed the event,
which D-29 narrows.

### D-29 An event write under the server's own etag is planned again once after a 412

Google changes a just-moved event by itself, moments after the move.
What it changes is not known. Under D-28 alone, moving an event and then
moving it back would fail `[stale]` with nobody else involved.

So an event write made without the caller's `etag`, whether an update,
a split's truncate, a cancel, a move or an answer, reads the event again
after a 412 and plans the write again. If the new plan makes the same
changes and reaches the same guests, it is made once more, and the
result says so. Otherwise, and after a second 412, it is `[stale]`. A
guest's answer that arrived in between is kept, because the second plan
starts from the fresh read.

A caller's own `etag` stays strict, because it names the version they
decided on. The calendar and sharing writes keep the plain rule.

Rejected: plain `[stale]` under the server's own etag, which fails a
write nobody else touched.
Cost: one more read and plan after a 412.

### D-30 A dry run writes nothing

Every write tool takes `dry_run`. Under it the service goes as far as
the write would, then returns before its first write request, and the
result shows what would happen: the request, the event or calendar
after, and for an event how many guests would be notified and how many
of them are outside the organizer's domain.

A dry run may read what its preview needs: the event and its etag, the
sharing rules a share would change, the calendar list entry a patch
would touch. Where that entry does not exist yet, as when one call
subscribes and sets a color, the dry run supplies the entry it would be
patching. A dry run needs no `confirm`. A dry run that fails part way
claims nothing landed.

The branch sits in each service write rather than in the client, so
every write carries its own, and the tests hold each one.

Rejected: a preview built from the request alone, which could neither
show the before-state nor refuse what the write would refuse.
Cost: a dry run spends the reads its preview needs.

### D-31 A write result shows the event before and after, and whom Google was asked to tell

Every write returns what the resource looked like before, what changed,
what it looks like after, how many people were to be notified and by
which `sendUpdates` value, and the new etag. Where Google says silence
is not guaranteed, the result says that too (D-23). A forced write says
it was forced.

A write that reports only "ok" leaves the model to guess what happened,
and the model then tells the person.

The change list records what the call did, appended after each write
lands, so a call that fails part way names exactly what stands. A
`manage_calendar` call can be two patches, and when the second fails
the result says the first is in place and cannot be rolled back.

Rejected: a result that says only "ok".
Cost: a write's result is as long as a read of the event, and every
result type needs a rendering of before and after.

### D-32 One tool call is one API request, and a call that needs more says how many

Google meters requests per user per minute on a sliding window
([quota](https://developers.google.com/workspace/calendar/api/guides/quota)), and
a fan-out tool can spend dozens in one call.

So a call is one request wherever it can be, and the exceptions say
what they spent. A `this_and_following` write is two. An availability
question past one batch is a request per batch, and a search across
calendars a request per calendar. A calendar or sharing write is
preceded by the read that gives its etag and its before-state.

The count a result reports is read off a counter in the request's
context, which the client increments for every request it sends,
retries included. A test compares the reported count with what the fake
served for every write. The fan-out tools have a concurrency limit and
a budget, and stop at it saying what they spent. The calendar list is
read once and cached.

Rejected: a request count kept by hand at each call site, which drifts
from what the call spent.
Cost: the fan-out tools stop at a budget and say what they spent, so a
large question takes more than one call.

### D-33 A reply carries both halves, in different forms

Every tool result carries `structuredContent` under the output schema
and a readable rendering in `content`. The MCP specification asks for
both: "for backwards compatibility, a tool that returns structured
content SHOULD also return the serialized JSON in a TextContent block"
([tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)).

Neither half can be dropped, because a client shows the model one or
the other, and the failure to guard against is the half that carried
the substance being filtered away. The two are never the same bytes:
the rendering is written for a reader, with the window, the zone and
any truncation in words.

`internal/tools` constrains a tool's output type to one that renders,
so a tool cannot be added without its text half. `add` sets `content`
on every call; left unset, the SDK fills it with the JSON of the
output. `internal/render` holds the renderings, with golden files.

Rejected: the same bytes in both halves, which spends the model's
context twice and puts no reading in front of it.
Cost: every result type has a hand-written rendering, held by golden
files rather than by a check that it says everything the JSON says.

### D-34 Availability comes from free/busy, and a calendar without an answer is unknown

An event list cannot answer "is this person free". It misses every
event whose details the caller cannot read, and it ignores
`transparency`, so an event marked free counts as busy.
[freebusy.query](https://developers.google.com/workspace/calendar/api/v3/reference/freebusy/query)
returns busy intervals for calendars whose details the caller cannot
see, and errors per calendar rather than failing the query.

So `check_availability` is free/busy, in batches of 50, the most
`calendarExpansionMax` allows. A calendar that errored is unknown, never
free. So is one missing from the answer or carrying
`tooManyCalendarsRequested`, since Google does not say where a calendar
past the cap lands; those are asked about again on their own, and one
still unanswered stays unknown. A group answers under `groups`, its
members' busy time merged, and one unread member makes it unknown.

The result tells "no busy blocks" from "could not read this calendar",
and its free gaps say how many calendars they were computed from. An
address is asked about as given, because free/busy works on a calendar
this account cannot open. A title is resolved first.

Rejected: availability from an event list.
Cost: calendars past the cap are asked about again, one request each,
up to a fixed ceiling.

### D-35 Availability has a ceiling of its own, not a setting

`check_availability` asks about at most 100 calendars in one call. It
does not use `GCAL_MAX_CALENDARS`, the schedule reads' limit, because
the cost differs: a schedule read spends a request per calendar, and
free/busy one per batch of fifty. The refusal says which limit it hit,
so nobody changes the wrong setting.

The ceiling is fixed because its right value follows from the API's own
batch size, not from a deployment. A setting whose only right value is
the API's own limit is a setting nobody should turn.

Rejected: the free/busy ceiling as a setting of its own.
Cost: a fixed ceiling, so a larger question is split by the caller into
several calls.

### D-36 Working hours mask free time only when the caller gives them

The API has no working-hours field, and a free gap at 03:00 is correct
and useless. A window is one interval, and working hours are a daily
mask. Over a week the model cannot write 09:00 to 17:00 on five days as
one window, so it either accepts an overnight gap or makes five calls.
A minimum length does not help, since the overnight gap is the longest
in the list.

So `check_availability` takes `working_from`, `working_to` and
`working_days`, none with a default. Without them every hour of the
window counts. Days left out means every day. The mask is applied in
the zone the answer is shown in, one local day at a time, so 09:00 is
still 09:00 on the Sunday the clocks change. Both halves of the result
say which mask was used, because a gap list with the evenings removed
reads as "nobody is free then".

Rejected: a Monday-to-Friday default, which is wrong in every country
whose week runs Sunday to Thursday; a mask across midnight, since the
day a night shift belongs to is a choice for the person.
Cost: a caller who wants working hours passes them on every call.

### D-37 `list_changes` hands the sync token to the caller and stores nothing

A list cannot report a deletion: a deleted event stops matching the
window and looks exactly like one that never existed. A sync read
returns a canceled tombstone for it, so `list_changes` exists.

The caller holds the token, as with a page token: opaque, handed back,
passed in. Storing it would make the server stateful about "since
when?", which belongs to whoever asked; two people sharing one server
do not share a last look.

Google's rules are enforced before the call. Deletions are always
included, so `showDeleted` is forced on, and a baseline labels what is
already canceled as that. A window, a search, an ordering and
`updatedMin` cannot go with a token, so the tool offers none of them.
The
[events.list reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/list)
omits `nextSyncToken` "if further results are available", so a read
that stops at its budget hands back no token and says so. An expired
token is 410, and the cure is a fresh baseline.

`updated_since` is the one-off look back, sent as `updatedMin`. The
[sync guide](https://developers.google.com/workspace/calendar/api/guides/sync)
calls that the legacy way, so the tool leads with the token.

Rejected: a flag on `list_events`, which cannot show a deletion; a token
the server stores.
Cost: a baseline pages past its budget to reach the token.

### D-38 `create_event` mints the event id, and an unseen answer is `[ambiguous_outcome]`

A client may supply an event's id on insert. Google's
[events.insert reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert)
adds: "Due to the globally distributed nature of the system, we cannot
guarantee that ID collisions will be detected at event creation time."
So an insert with a known id is nearly idempotent, not idempotent.

`create_event` mints the id, base32hex by construction (D-17), so the
caller knows which event to look for whatever happens. The insert is
never retried. A 400 or a 403 did not land. A transport failure or a 5xx
may have, so it is `[ambiguous_outcome]`, naming the id to read before
trying again. A deleted event does not release its id, so a retry under
an old id answers 409.

The conference request on a create uses the event id as its request id,
so a retry of a create whose answer was never seen cannot make a second
conference.

Rejected: retiring `[ambiguous_outcome]` because Google once caught a
concurrent duplicate, since one observation is not the guarantee Google
declines to give.
Cost: the caller reads before retrying.

### D-39 `cancel_event` is an ordinary write, not a gated one

`cancel_event` is `events.delete` for a single event and a
`status: cancelled` patch for one occurrence of a series. The result
names which happened.

It is not behind the destructive flag. Canceling a meeting is what a
calendar is for, and Google keeps the canceled event. Gating it would
put a flag between the model and the most ordinary write there is, and
people would set `GCAL_ENABLE_DESTRUCTIVE=true` for good, which also
arms `clear_calendar`. A gate everybody turns on protects nobody.

Its protection is elsewhere. `scope` is required, so canceling a series
is never a slip (D-18). `notify` is required, so the guests' fate is a
choice (D-21). Its annotations say destructive, so a client that
prompts by the hint prompts for it (D-58).

Rejected: gating `cancel_event` behind the destructive flag.
Cost: a cancel with `notify: none` leaves the meeting on the guests'
calendars, and the result says so (D-23).

### D-40 An attendee write changes only what was asked: the caller's own answer, and a guest in the role asked

`respond_to_event` sets the account's own `responseStatus` and comment,
and nothing else. It is not part of `update_event`: answering is not
editing, the permissions differ, and a model that conflates them tries
to answer by patching the whole guest list. It is refused when the
account is not a guest, and `this_and_following` is `[unsupported]`
there, since a guest has no right to rewrite the series a split needs.

Guests are added in the role the caller names. `create_event` takes
`optional_guests` and `update_event` `add_optional_guests`, sent with
`optional: true`. An optional guest counts toward `notify` like any
guest, since nothing says Google mails one less. A room cannot be
optional, and one address asked for in two roles is refused. An address
the write adds that is already on the event keeps the role it has, and
the result names it rather than skipping it in silence.

Rejected: answering through `update_event`; changing a guest's role
from required to optional or back.
Cost: changing an existing guest's role is done in Google Calendar, not
here.

### D-41 An address arrives as one mailbox, and its display name goes nowhere

A mail server hands addresses over as
[RFC 5322](https://www.rfc-editor.org/rfc/rfc5322#section-3.4)
mailboxes, such as `"Sample Person" <person@example.com>`. A hand-off
from mail to calendar has to take that form.

So every address input is parsed with Go's `net/mail` as one mailbox:
guests, optional guests, rooms, `remove_guests`, a share's person or
group, and the calendars `check_availability` asks about. The bare
address replaces it before anything reads it, so the reach count sees
the domain the address has, not the bracket after it.

The display name goes nowhere: not to Google, not to a result, not to a
log. It is text somebody else wrote about a person, and nothing here
needs it.

One entry holds one address. A list written in one string is refused,
not split, since a split guesses at quoting the caller never wrote.

Rejected: splitting a list given in one string.
Cost: some strings Google would take, such as an address with two dots
in a row, are refused before a request.

### D-42 A Meet link is asked for on create or by `add_conference`, never copied by a split

A Meet link is a create request in an event's `conferenceData`, in scope
as a Calendar field. Google's
[events.insert reference](https://developers.google.com/workspace/calendar/api/v3/reference/events/insert)
says version 0 of `conferenceDataVersion` "ignores conference data in
the event's body", and Google then creates the event without the link.
So the client sets the version from the body, not from each caller.

`create_event` takes `conference: true`. `update_event` takes
`add_conference`, because a meeting often gets its link after it is
made. An event that already has a conference is refused `[blocked]`,
since Google replaces the field whole and would replace the link people
are using. A calendar whose published conference types exclude Meet is
refused `[unsupported]` before the write. The request id on a patch is
a hash of the event id and the etag, so a retry repeats it and a later
version gets a new one. The result reports what came back: the link, or
that Google is still making it.

A split never copies a conference. Its new series is a new event, so
`add_conference` with `this_and_following` is refused, and the split's
note names `add_conference` with `scope: series`.

Rejected: a tool of its own for conferences, a second way to make an
event; refusing a conference on an existing event.
Cost: an event with a conference cannot ask for another, and a split
gets none.

### D-43 Status events are made by `create_event`, held to Google's guide before any request

`create_event` takes `event_type`: `outOfOffice`, `focusTime` or
`workingLocation`. Google's
[status-events guide](https://developers.google.com/workspace/calendar/api/guides/calendar-status)
sets their rules, and the server holds each before a request, so a
refusal names the rule instead of a bare 400.

Only the primary calendar holds one: "Secondary calendars can't have
status events." Out of office and focus time are timed and opaque.
`auto_decline` is required for them with no default, because a decline
reaches another person. A working location is timed or one whole day,
public and transparent. The server sets transparency and visibility
itself and refuses a value that contradicts them.

Guests, rooms and a Meet link on a status event are refused `[blocked]`
on every write, since Google documents nothing about them there.
`update_event` holds a status event to the same shapes at every scope,
a split's new series included, because the guide says an update "must
maintain the required fields". The details blocks stay raw on the
event, so a read and a split carry a field Google adds later.

Rejected: a tool of its own for status events; status events as read
only.
Cost: guests, rooms and a Meet link on a status event are refused until
a live check shows what Google does with them.

### D-44 Declining every meeting is `create_event_declining_all`, behind a flag of its own

An out-of-office or focus-time event with `auto_decline: all` declines
every meeting it overlaps, accepted ones included, and each organizer
is told. The
[discovery document](https://www.googleapis.com/discovery/v1/apis/calendar/v3/rest)
describes `declineAllConflictingInvitations` as "all conflicting meeting
invitations that conflict with the event are declined". A client that
always allows `create_event` would let that happen unseen.

So `create_event` refuses `all` with `[blocked]`, before it checks the
calendar, naming `create_event_declining_all` and its flag. That tool
makes the same event, registers only with
`GCAL_ENABLE_DECLINE_ALL=true`, says `destructiveHint`, and takes no
guests and no `notify`. The flag is its own rather than the destructive
one: a flag people set to get out-of-office must not arm
`clear_calendar` too.

`update_event` can make such an event cover time it did not cover.
Whether Google then declines the meetings in that time is not known, so
the result, a dry run included, says Google may.

Rejected: `all` as a value of `create_event`'s argument, guarded by a
question or by typing it; the destructive flag.
Cost: nothing refuses an `update_event` that makes such an event cover
more time; the result says what may follow (#41).

### D-45 An event past Google's guest threshold says its answers are incomplete

Above 200 guests, Google stops propagating each guest's response, so a
caller counting acceptances on such an event is wrong with nothing
looking wrong.

So every result describing an event with more than 200 attendees says
its RSVPs are incomplete. The count is Google's own `attendees` field,
which carries the organizer's row and the rooms, because the threshold
is Google's and counting guests alone would stay silent on an event
Google already stopped tracking. The warning comes from the event a
write produced, not from the write that grew the list, so every write
and read that shows such an event says it. It carries the count, never
the addresses.

Google does not publish where it stops accepting guests at all, so the
warning says that rather than inventing a number. Nothing probes for
it, since that would mean putting hundreds of invented addresses on a
real event.

Rejected: probing for Google's hard limit.
Cost: the warning cannot name the hard limit.

### D-46 `manage_calendar` is one tool over two resources, and takes no etag

People do not tell a calendar from their subscription to it, and the API
splits them. `manage_calendar` covers both: renaming or re-zoning the
calendar itself (`calendars.patch`), subscribing and unsubscribing
(`calendarList.insert` and `delete`), and the account's own overrides,
such as color, hidden, selected and notifications (`calendarList.patch`).
Its description says that unsubscribing removes a calendar from your
list and does not delete it or affect anybody else.

It takes no `etag`. An event is one resource with one etag, so a
caller's etag has one thing to hold. A calendar here is two resources,
each with its own etag and method, so one parameter could stand for only
one of them while appearing to protect both. Each patch carries the etag
of the read made just before it, and the result shows before and after
for every field changed.

Notifications have one published method, so the tool takes the types
and fills the method in, and the list it is given replaces the old one.
Google refuses to let a calendar's data owner remove it from their own
list, so that unsubscribe is answered with the two things that work:
hiding it, or `delete_calendar`.

Rejected: one `etag` parameter for two resources.
Cost: a failure after one patch landed names what stands, and there is
no rollback.

### D-47 `delete_calendar` takes only a secondary calendar, and `clear_calendar` only the primary

Google's own descriptions draw the line.
[calendars.delete](https://developers.google.com/workspace/calendar/api/v3/reference/calendars/delete)
"Deletes a secondary calendar. Use calendars.clear for clearing all
events on primary calendars."
[calendars.clear](https://developers.google.com/workspace/calendar/api/v3/reference/calendars/clear)
"Clears a primary calendar." Asked to clear a secondary calendar, Google
answers 400, so that refusal is the API's rule rather than this
server's caution.

So `delete_calendar` refuses the account's primary calendar, naming
`clear_calendar`, and `clear_calendar` refuses anything else, naming
`delete_calendar`. Both refusals come before a request.

`clear_calendar` deletes every event on the primary calendar, and
Calendar cannot bring them back. It is the most destructive call this
API offers, which is why it sits behind the destructive flag with
`confirm` and carries the prompt mark (D-55, D-57).

Rejected: one tool that empties or removes whichever calendar it is
given, which neither method's description offers.
Cost: a secondary calendar is emptied only by deleting it, and the
primary calendar cannot be deleted here.

### D-48 Access is checked against the role Google documents, before `confirm`

A write the account's role would refuse is refused here first, with the
reason, before anything asks for `confirm`. Telling somebody to pass
`confirm` is useless advice when their access would refuse the call
anyway.

There are two thresholds, each where Google's documentation puts it.
Sharing needs owner: Google's
[sharing guide](https://developers.google.com/workspace/calendar/api/concepts/sharing)
says owner has "all of the permissions of the writer role with the
additional ability to modify access levels of other users". Changing the calendar itself needs
writer, the strongest claim the documentation supports: a reader plainly
cannot. Whether a writer may rename a calendar is documented neither
way, so Google answers that one. Deleting a calendar needs ownership.

The account's own half of `manage_calendar` needs no access at all. Its
color, its own name for a calendar and its notifications work on a
calendar it can only read. A calendar reached by id alone carries no
role, so the server does not refuse on a role it could not read.

Rejected: owner for both thresholds, which refuses edits Google allows.
Cost: whether a writer may rename a calendar is left to Google to
answer.

### D-49 A risky form of a write is a tool of its own, never an argument

Clients remember an approval per tool, never per argument value.
claude.ai, Codex, VS Code and Copilot remember "always allow" per tool,
and Claude Code matches an MCP argument only in a command-line deny
rule. A wider share carried as an argument of `share_calendar` rides
along unseen once `share_calendar` is always allowed. An argument such
as `allow_public` is a refusal the model lifts itself.

So each risky form is a tool of its own, behind a flag the operator
sets. A public or domain-wide share is `publish_calendar`, and a share
outside the organization or to a new owner is `widen_calendar_access`
(D-50). An event that declines every meeting is
`create_event_declining_all` (D-44). The ordinary tool refuses the risky
form `[blocked]`, naming the tool that takes it and its flag, so a
client's per-tool approval means what it says.

Rejected: `allow_public` and `auto_decline: all` as arguments of the
ordinary tools.
Cost: the surface has more tools, and a refusal can send the caller to
a tool the operator has not registered.

### D-50 Sharing is three tools by how far it reaches

`share_calendar` takes named people and groups inside the organization,
below owner, and registers with sharing on. `publish_calendar` takes
anyone or a domain, never as owner, and carries the prompt mark (D-57).
`widen_calendar_access` takes a person or group outside the
organization at any role, and an owner anywhere. The two wider tools
register only with sharing on and `GCAL_ENABLE_DESTRUCTIVE=true`, need
`confirm: true` (a dry run needs none), and say `destructiveHint`.

A public rule is the one sharing value that cannot be undone from the
other side: removing it does not take back what was seen.

`share_calendar` refuses a wider share `[blocked]` before it asks for a
role or `notify`, naming the tool and its flag. It keeps or lowers the
role of any rule the calendar already has, whoever that rule reaches,
since lowering widens nothing. Raising a wider rule goes through the
wider tools, and `unshare_calendar` removes any rule. Each wider tool
refuses what another takes with `[invalid]`. `GCAL_SHARING=off` removes
all of them, the read included.

Rejected: `share_calendar` with `allow_public: true`.
Cost: lowering the rule of somebody outside the organization with
`notify: all` may email them.

### D-51 Inside the organization is the account's own domain, and any doubt counts as outside

`share_calendar` and the `none` refusal both need to know who is inside
the organization. Inside is the account's own domain, read from the
primary calendar's id, which is the account's address.

Every doubt resolves to outside. A personal Google account has no
organization, so every address is outside it. So is every address when
the account cannot be read, and so is a secondary domain of the same
organization. A domain with characters past ASCII is outside too, since
Go's case folding would match a lookalike to the account's own domain
while Google is sent the address unchanged.

Each of these errs toward one refusal too many, never one share too
many. A refusal names the tool that takes the share, so the cost of an
error is a second call, while a share too many is access nobody meant
to give.

Rejected: asking Google whether the account is a Workspace account,
which needs a scope beyond Calendar's.
Cost: a personal account on a mail provider other than Gmail counts as
an organization, so its own provider's domain reads as inside (#45).

### D-52 The wider shares name the calendar by the title they read

A client's prompt shows the call's arguments, and nothing else. On the
wider shares, `calendar` may be an id or a fragment of a title, and
neither tells a person which calendar the call opens.

So `publish_calendar` and `widen_calendar_access` take `calendar_title`,
required in the schema, which is the calendar's title as
`list_calendars` shows it. It is compared with the title of the
calendar the call reached, exactly but for spaces at the ends. A title
that differs is refused `[stale]` before anything else, a dry run
included. The prompt then names the calendar in words a person
recognizes, and a call cannot open a calendar other than the one it
names.

Where the prompt cannot show something, the result still says it: what
a public or domain rule reaches, and who is emailed.

Rejected: an id or a title fragment alone.
Cost: a caller reads the calendar's title before a wider share, and a
rename between the read and the call refuses it.

### D-53 A share reads the rules first, and says who could see the calendar before and after

A share reads the calendar's sharing rules before it writes. That read
gives the result its before and after, and it settles the scope type of
an address the calendar already knows. A person and a group look the
same, so an existing rule is Google's own answer to a question the
server would otherwise guess at. When it does guess, the result says
so.

The after is computed, not read again: the rule Google returned is
folded into the list read before. That shows what Google stored rather
than what was asked, for one request instead of two. Roles are
explained rather than echoed, since `writerWithoutPrivateAccess` and
`freeBusyReader` explain nothing to a person.

A group rule reaches its members, later ones included. Google's
[help](https://support.google.com/a/users/answer/167101) says access
follows membership, and that a member outside the organization gets in
unless its sharing policy forbids it. This server reads no group's
members, so every group share says what a group rule reaches.

Rejected: reading the rules again after the share.
Cost: a group's members outside the organization are not seen, only
described (#44).

### D-54 A tool's `Kind` decides its annotations and whether it registers

Every tool is added through `add` with a `Def` whose `Kind` is one of
`Read`, `Write`, `IdempotentWrite`, `Sharing`, `WideSharing`,
`SharingRead`, `DecliningAll`, `Destructive` or `Adding`. From that
kind, `allowed` decides whether the tool registers under the settings,
and `annotationsFor` decides its hints. A rule kept by hand at each tool
is a rule somebody forgets at one of them.

The gates are server-side because the hints are only hints. The MCP
specification says "clients MUST consider tool annotations to be
untrusted unless they come from trusted servers"
([tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)).
So a tool the operator left off is not registered at all, and no client
mode or model can call it.

`FullSurface` lives beside `allowed` and opens every gate, so the
schema dump sees every tool, and a test fails when a gate is missing
from it.

Rejected: annotations and gates kept at each tool's definition; a kind
split into an effect and a gate (D-87).
Cost: `SharingRead` exists only because its effect, a read, and its
gate, sharing, disagree.

### D-55 Destructive tools exist only when the operator turns them on, and still take `confirm`

`delete_calendar`, `clear_calendar`, `publish_calendar` and
`widen_calendar_access` are registered only with
`GCAL_ENABLE_DESTRUCTIVE=true`, and the two shares also need sharing on.
A client that runs with nobody watching gets none of what the operator
left off. Read-only mode removes them whatever the flag says.

Each still needs `confirm: true` on the call, and a dry run needs none.
`confirm` is checked after `dry_run` and after the access check (D-48),
so the refusal is about the one thing left to decide.

`confirm` is not required in the tool schema. The SDK validates a
required field before the handler runs, so the caller would get
"missing properties" instead of the sentence naming what would be
destroyed. `notify` is optional in the schema for the same reason.

Rejected: `confirm` required in the tool schema.
Cost: `confirm` is a witness the model writes, never the person's
approval; the flag and the client's prompt are what hold.

### D-56 The server asks the person nothing

The MCP specification puts approval of each tool call on the client:
clients "SHOULD … prompt for user confirmation on sensitive operations"
([tools](https://modelcontextprotocol.io/specification/2025-06-18/server/tools)).
A server may ask through elicitation, but the specification never makes
the answer consent (D-85).

So the server asks nothing. What stays holds on every client: the
registration flags (D-55), `confirm` on the destructive tools, the
guards and witnesses such as `scope`, `notify` and the calendar's title,
`dry_run` on every write, the annotations (D-58) and the prompt mark
(D-57).

`GCAL_REQUIRE_PROMPT`, which refused the asking writes on a client that
could not ask, is refused at start when it is turned on. Ignored, it
would allow what its owner set it to stop.

The tools tests run every client with form elicitation declared and
fail on any question. The live driver does the same and fails the step
a question arrives in.

Rejected: confirming a write through elicitation (D-85);
`GCAL_REQUIRE_PROMPT` kept as a switch.
Cost: an unattended client gets every tool the flags register, so the
operator leaves the destructive and decline-all switches off for one.

### D-57 The `requiresUserInteraction` mark goes only where the prompt is the point

Claude Code reads `anthropic/requiresUserInteraction` as marking tools
"whose permission prompt is itself the point". It prompts on every call
of such a tool whatever the operator's rules say, and refuses the call
where no prompt can be drawn. No other client reads it, so it is never
the control; the flag is.

The mark goes where the prompt is the point and the call is never made
many times in a row. `delete_calendar` and `clear_calendar` remove
events Calendar cannot bring back. `publish_calendar` opens a calendar
to the internet or a domain, which removing the rule cannot take back.

`widen_calendar_access` is not marked: a share outside can be one of
many in a row, and a mark there brings back a prompt nobody reads.
`cancel_event` is not marked either, since Google keeps a canceled
event. `add` panics on the mark on any tool that is neither
`Destructive` nor `WideSharing`.

For every other tool the operator decides in Claude Code with its
allow and ask rules. The README gives `ask` rules for the writes that
share, cancel, move or delete, and the staleness gate holds each tool
name there to the published tools.

Rejected: the mark on every destructive tool; the mark only for a client
that cannot ask.
Cost: `widen_calendar_access` carries no mark.

### D-58 Annotations say exactly what a tool changes

A client draws its prompt from the hints, so each hint says exactly
what the tool changes.

`readOnlyHint` is on the read tools and `list_sharing` alone, so VS
Code, which prompts on every tool without it, prompts on every write.
`destructiveHint` is true on every write but `create_calendar` and
`create_event`. The MCP schema reads false as "the tool performs only
additive updates"
([tool annotations](https://modelcontextprotocol.io/specification/2025-06-18/schema#toolannotations)),
and an edit, a move, an answer, a calendar's settings and a sharing rule
all change what is there. An event that declines every meeting is
destructive too, since the decline reaches other organizers and is not
taken back by deleting the event. `idempotentHint` marks a write whose
repeat lands in the same place. `openWorldHint` is on the sharing kinds
alone, the one act whose effect leaves the account.

So a client that prompts by `destructiveHint`, as claude.ai and Codex
do, prompts for every write that changes or removes what is there.

Rejected: `destructiveHint: false` on a write that can be undone.
Cost: a client that prompts by the hint prompts on every write that is
not an add.

### D-59 No tool changes the server's own settings

The model chooses the calls, and it can be steered by text somebody
else wrote, such as an event's description. So nothing it sends may
widen what the operator allowed.

No tool writes the environment, the profile directory or any other
setting the server reads. The settings are read once at start, and the
registered surface stays as they left it for the life of the process. A
test calls every registered tool and holds that none changes the
environment or the profile directory.

This is what makes the registration flags a control. A flag the model
could flip would be a suggestion.

Rejected: a tool that turns a setting on for the session, which would
let the model lift the operator's limit itself.
Cost: turning a gated tool on means restarting the server with a new
setting.

### D-60 Read-only mode registers the reads and asks for read scopes only

`GCAL_READ_ONLY=true` registers the read tools alone and asks Google for
the read scopes alone. The token it holds then cannot write, which is a
limit no client mode or model can lift. `list_sharing` is dropped with
the sharing tools, since `get_calendar` already shows a calendar's
exposure.

The login's scope set is derived from the configuration, so a read-only
login asks for exactly the scopes the read tools use (D-61).

`GCAL_READONLY`, an earlier name for the setting, is not an alias. Unset
or off, it starts and does nothing. Turned on, or set to something that
is not a boolean, it is refused at start, naming `GCAL_READ_ONLY`.
Ignored, it would leave a server its owner meant to be read-only able
to write.

Rejected: keeping `GCAL_READONLY` as an alias, which keeps two names for
one switch.
Cost: a configuration that still turns the old name on refuses to
start.

### D-61 Login asks for the narrowest scopes and records what Google granted

Read-only asks for `calendar.readonly`, `calendar.settings.readonly` and
`calendar.acls.readonly`. Read-write adds `calendar.events`,
`calendar.calendars`, `calendar.calendarlist` and `calendar.acls`. The
broad `calendar` scope is never asked for: it grants everything,
including what the gated tools do, whatever the flags say.

Reading sharing rules needs a scope of its own.
[acl.list](https://developers.google.com/workspace/calendar/api/v3/reference/acl/list)
is not covered by `calendar.readonly`, and without the extra scope that
shows up as a 403 from one tool long after setup.

A profile records what `tokeninfo` reports the token carries, falling
back to the request only when that call fails. Google can grant fewer
scopes than were asked for, and `login` warns about each one missing.
The account is read from the primary calendar's id, which is the
address. `tokeninfo` returns an email only under an email scope, and a
calendar server has no need to read a profile.

The list a person pastes into the consent screen is in
[gcp-setup.md](gcp-setup.md), and the staleness gate holds it to the
code.

Rejected: the broad `calendar` scope; `userinfo.email` to name the
account.
Cost: listing a calendar's sharing rules needs a scope of its own.

### D-62 Login is the loopback flow with PKCE, and the token is readable by its owner alone

`login` follows the native-app flow of
[RFC 8252](https://www.rfc-editor.org/rfc/rfc8252): a loopback IP
literal on a random port (RFC 8252 §7.3), the redirect to
`http://127.0.0.1:{port}/callback`, and PKCE with S256 (RFC 8252 §8.1).
A redirect whose state does not match is refused. `--no-browser` prints
the URL and the port to forward, for a machine reached over SSH.

The refresh token goes to the OS keyring. Where no keyring answers, it
goes to a file only its owner can read, with a warning on every read and
write. On Windows a file mode decides only the read-only attribute, so
`internal/fileperm` sets a protected DACL that grants the owner alone.

The reason is the token: a refresh token grants the person's calendar
until it is revoked, so the flow never shows it to another process and
the file never shows it to another account.

Rejected: an out-of-band flow, where the person pastes a code.
Cost: on Windows the file needs an explicit protected DACL, and a
headless machine needs a port forward to sign in.

### D-63 Every setting is a `GCAL_` variable, read through `config`

Every setting is an environment variable prefixed `GCAL_`, with a flag
bound to the same name. A flag overrides the environment, and the
environment overrides the default. Validation runs once, before the
server announces itself. Spaces around a value are dropped, a value of
spaces alone is unset, and a removed setting turned on is refused with
its replacement.

`config.Vars` records every variable the server reads, the removed ones
included, and names the configuration directory and the refresh token
by constants `config` owns. Every reader goes through `config.Lookup`.
Outside `internal/config` a name is spelled from `config`'s constants,
never typed whole, and no code calls `os.Getenv` with one. A test walks
the server's code and `scripts/` and fails on a `GCAL_` name missing
from the registry.

The reason is derivation. One registry is what lets the bundle's
settings, the configuration page and the refusals come from the code
rather than be typed again (D-78).

Rejected: reading a variable with `os.Getenv`; resolving aliased imports
in the scan, which needs type information for a read nobody writes.
Cost: a name is spelled from `config`'s constants, never typed whole.

### D-64 The server starts and lists its tools before anyone signs in

A client starts the server and lists its tools before any call. A
server that refuses to start without credentials cannot be listed
before `login`, and every host logs that as a crash.

So the server starts without credentials. It lists every tool the
settings allow, and each call answers `[auth]`, naming `login`, so the
model can tell the person what to run. A bad setting still refuses to
start (D-63): the configuration is the operator's to fix before
anything runs, while a login is the person's to make at any time.

`--dump-schemas` needs no credentials either, so the schema diff runs
on any machine and in CI. The smoke gate starts the binary with none
and drives it over stdio, so a change that makes credentials a
condition of starting fails there.

Rejected: refusing to start without credentials.
Cost: a missing login shows on the first call rather than at start.

### D-65 Errors use a closed vocabulary, held from both sides

Every tool error starts with a class in brackets, and the class tells
the caller what to do next. The set is closed: `scripts/gates classes`
derives it from the code and fails when a class the code emits is
missing from the list, or a listed class is never emitted.

`stale` and `conflict` stay apart. A 412 means "read again and retry",
and a conflict means "reconsider"; folding them together loses the one
piece that makes a 412 actionable. `ambiguous_outcome` exists because
an insert is only nearly idempotent (D-38). `auth` is a missing login or
scope, and `forbidden` a role too low, so somebody who does not own a
calendar is not told to sign in again.

`unsupported` is for what this server cannot build, not a guess about
Google. `this_and_following` on an answer or a move is one: the split
needs the right to rewrite the series, which an attendee lacks and a
move does not use. An impossible time zone is `invalid`, never a
retryable class.

Rejected: a six-class vocabulary of `invalid`, `not_found`, `auth`,
`conflict`, `unavailable` and `unsupported`; `stale` folded into
`conflict`.
Cost: a larger vocabulary than callers may expect, held by
`make classes`.

### D-66 A write is retried only where a repeat lands in the same place

GET, PUT, DELETE and PATCH are retried with truncated exponential
backoff, `min((2^n) + jitter, 32s)`, the algorithm Google's
[quota guide](https://developers.google.com/workspace/calendar/api/guides/quota)
publishes.
`freebusy.query` is a POST and a pure read, so it is retried. A 403 or
429 `usageLimits` is `[rate_limited]` and retried, and running out of
retries reports the requests already spent.

`events.insert`, `calendars.insert`, `events.move` and `acl.insert` are
POSTs that are not idempotent and are never retried. A 5xx or a lost
answer on one is `[ambiguous_outcome]`: a repeated move lands on a 404,
and a repeated share emails the person twice.

A DELETE is retried, so its retry can find nothing left. After a 5xx or
a lost answer, that 404 or 410 is `[ambiguous_outcome]`, since the
first attempt most likely landed. After a 429 it stays `[not_found]`,
since Google did not act on the 429.

`events.patch` is retried. A patch is a stated end state, but it carries
`sendUpdates`, so a retry can mail the guests twice. The alternative
reports a write as failed that may have landed, which is the outcome
this design works hardest to avoid.

Rejected: not retrying `events.patch`.
Cost: a retried patch can mail the guests twice.

### D-67 Resources mirror three reads, and no resource holds a schedule

For clients that attach rather than call, the server offers
`gcal://calendars`, `gcal://calendars/{+calendar_id}` and
`gcal://calendars/{+calendar_id}/events/{+event_id}`. Each carries the
same content as the matching tool, computed once.

The `+` is reserved expansion
([RFC 6570 §3.2.3](https://www.rfc-editor.org/rfc/rfc6570#section-3.2.3)).
Simple expansion matches unreserved characters only, and a secondary
calendar's id is an address, so its at sign would match nothing and the
read would answer "not found". Reserved expansion also matches a slash,
so the calendar template matches an event URI too. Both templates share
one handler that parses the URI rather than resting on the order of
registration, and an empty event segment is not found rather than the
calendar.

No resource holds a schedule. A resource takes no arguments, and a
schedule read must state its window and its zone (D-12), so a schedule
URI would have to invent both.

Rejected: a schedule resource; simple-expansion templates.
Cost: a schedule is reached only through a tool call.

### D-68 The released tool surface is a contract

Tools keep their names and output fields, and resources their URIs.
`scripts/gates schema-diff` compares the built surface with
`testdata/schema-baseline.json`, the newest release's surface, recorded
in that release's commit by `make schema-baseline`.

It fails on what breaks a caller, at any depth: a tool, resource or
field removed; an input that takes fewer types or values; an output
that may return a type it did not, or may be missing where it was
required; an input newly required. Types compare as JSON Schema 2020-12
reads them, so an input that becomes nullable breaks nobody and an
output that becomes nullable does. Anything else that changed is
reported for a person to read.

A break goes through only when a **Breaking:** entry under
`[Unreleased]` names it, and `schema-baseline` refuses one unless the
release is a new major version. On a pull request, `schema-ack` needs a
commit footer naming each changed item. The dump runs with nowhere to
find a setting, so the maintainer's shell cannot change it.

Rejected: diffing against the last tag, which a shallow checkout does
not have and which leaves a deliberate break no way through.
Cost: a release commit must record the baseline, and the gate fails
when the baseline is not the newest release's.

### D-69 Tests never reach the real keyring

The `cmd` tests exercise `login`, `logout` and `status`, which read and
delete a refresh token. Redirecting the configuration directory and the
environment does not redirect the OS keyring. A logout test isolated
only by those would find the person's real refresh token under the
default profile, revoke the grant at Google and delete it.

So `TestMain` substitutes the keyring for the whole `cmd` package before
any test runs. Reaching the real keyring from a test is impossible
rather than discouraged, and a decoy test fails if a refactor drops the
substitution.

The reason for doing it structurally is that a warning in the source is
not a guard. A rule stated in a comment holds until somebody misses the
comment; a substitution in `TestMain` holds for every test anyone adds
to the package.

Rejected: redirecting the configuration directory and the environment
alone.
Cost: `TestMain` owns the keyring for the whole `cmd` package, so a
test that needs a real keyring cannot live there.

### D-70 A write-path change counts only after a live run whose transcript is read

Every fake in the unit suite is written from what we believe Google
does, so a unit test asserts the behavior the code was written to have.
Only a run against Google can show a belief wrong.

So green gates are not done. `make live` drives the server against a
real account through the same tools a client calls. A change that
touches the write path or a response shape counts only after a live run,
and the transcript is read rather than counted: a step can pass while
its output says something false, such as a successful move rendered as
canceled.

Two gates hold the driver to the surface. `make live-cover`, in
`make check`, fails on a published tool with no live step, and each
exemption says why its tool cannot be driven. `make transcript` holds
every print in the driver to the one redactor (D-71). A comment that
states a Google behavior names the live step that checks it.

Rejected: green gates as done.
Cost: a run needs a real account and spends calendar-creation quota,
which counts creations and is not refunded by deleting them. Some checks
need more than one account and are not run yet (#49).

### D-71 The live driver reads only what it made, prints nothing it did not write, and by default reaches nobody

The driver works on a scratch calendar it created and empties at the
start of every run, so the only content that can reach a log or a
failure is content it invented. A run deletes the calendar only if it
made it, and the next run adopts and empties it rather than spending
another creation. Adoption needs the title, a marker in the
description, the owner role and the signed-in account as the data owner;
anything missing makes a new calendar. Event ids are new each run,
because a deleted event keeps its id.

A display name has no shape a redactor can anchor on. So a step prints
its body only when every calendar it names is one the driver made, and
a step that names none is account-wide and prints nothing of its
result. Sync and page tokens are redacted by the label the renderer
prints before them.

A default run mails nobody. No event or share it writes names anyone
but the account or an address under `.test`, and no request asks Google
to notify. A guard on both of the driver's paths to Google, the tools
and its own REST calls, holds that. Only an arming flag lets a run
reach a person, and it is refused unless a second flag names every
guest; the guard then lets those addresses through and no other.
Emptying leaves an event that has a real guest, since removing it would
mail them.

Rejected: a flag on each account-wide step, which is a matter of care;
adopting a calendar by its title.
Cost: a run may spend a calendar creation, and the real-guest steps run
only when armed.

### D-72 One status event per run may live on the primary calendar, and nothing else is written there

Google keeps status events on the primary calendar alone, so no scratch
calendar can test one. The driver may make exactly one per run there: a
year or so ahead, with an invented title, declining nothing, read back
by its id and deleted by its id in the same run.

Before the create goes out, the driver writes a record of it to an
owner-only file named for the run, and a record that cannot be written
stops the create. The delete is deferred before the event is made.
Ctrl-C, SIGTERM and SIGHUP stop the steps, not the process, and SIGPIPE
is ignored, so the delete runs however the run ends. The event counts as
gone only when a read answers 404 or 410 or shows it canceled, and only
then is the record removed.

One guard sits on both of the driver's paths to Google. It refuses
every write that names the primary calendar, as the calendar written or
a move's destination, except that one create and the delete of the id
it made. A step on the primary calendar prints no body, since its title
is the person's own.

Rejected: leaving status events to the fake, which encodes only what
Google's guide says.
Cost: one write on the person's own calendar per run.

### D-73 Evals score the calendar, never the model's account of it

`scripts/evals` gives a model tasks to complete through the tools and
scores the result. The tasks are the failures this server exists to
avoid: an all-day event created by a person in a zone west of UTC, a
weekly recurrence that must survive a daylight-saving change, and an
invitation that must reach a guest outside the organization.

The model gets the server's own tool list over an in-memory session
against `internal/gapi/caltest`. The score reads the calendar afterward,
not what the model said it did. A model that says it created an all-day
event and wrote a timestamp has failed.

`make evals-check` runs in `make check`. It builds each task's calendar,
offers the tool list and asserts every task fails on a calendar nobody
touched, since a scorer that passes there scores nothing. That half
needs no model, so it costs nothing to run on every commit.

Rejected: scoring the model's account of what it did.
Cost: a run with a model costs money and is not deterministic, so it
runs by hand.

### D-74 Releases are `go install` and signed archives

There are two ways to install: `go install` of the module, and a signed
release archive. A tag runs `.github/workflows/release.yml`, which
builds an archive per platform with `-trimpath` and a `mod_timestamp`
from the commit, so rebuilding a tag reproduces it. It writes
`checksums.txt` and an SBOM per archive, signs the checksums with a
keyless cosign signature, and attests the build's provenance over every
published file.

The module path carries the major version, and a tag's major must be
the module's, so `go install …@latest` resolves to the release it
names. The procedure, what to check after the tag and how to recover
are in [release.md](release.md).

The reason for signing in the workflow is that only a workflow run holds
the OIDC token signing and provenance need, so a person verifying a
download can tie it to this repository's workflow at that tag.

Rejected: a release cut on a maintainer's machine, which no signature or
provenance can tie to the repository.
Cost: no local rehearsal reaches signing, so the first run after any
change to the workflow is watched.

### D-75 The bundle ships inside the signature, and the release wiring is checked on every commit

The Claude Desktop bundle is packed in Go from the universal macOS
binary's post hook, and named in both `checksum.extra_files` and
`release.extra_files`. So it is checksummed, signed and attested with
the archives, rather than packed by hand beside them.

A release configuration is read once and then trusted. The half that
binds it to this repository, the staged globs, the pack path and the
bundle reaching `checksums.txt`, is the half nothing notices being wrong
until a tag. So `scripts/gates release` holds it on every commit. Each
staged glob must resolve to exactly one directory the build matrix
produces. The post hook must pack to the path `MCPB_OUT` names. The
bundle must be both checksummed and uploaded, the archives must leave
out the universal binary, and the signature must write a bundle. Each
way of breaking it is watched failing in a test.

The version is stamped from goreleaser's `{{ .Version }}`, so a snapshot
agrees with itself in every place a version shows.

Rejected: packing the bundle by hand.
Cost: `scripts/gates release` tracks goreleaser's directory names, such
as `<id>_darwin_all`, and moves when they do.

### D-76 The registry entry is published after the release, from its signed checksums

The MCP registry entry is published by `.github/workflows/publish-mcp.yml`,
a workflow of its own that the release calls and a person can dispatch.
It is separate for privilege, since the publisher is a third-party
binary that would otherwise hold the release job's write access. It is
also separate for recovery, since a job inside the release cannot
publish an entry for a tag that shipped earlier.

The registry checks the bundle's download URL before it accepts an
entry, so the entry cannot exist before the release. The entry's
`fileSha256` is what a registry client checks its download against, so
the workflow verifies the cosign signature over `checksums.txt` before
it reads a hash from it. The certificate identity is pinned to this
repository's release workflow at the exact tag.

`scripts/gates server-json` builds `server.json` from the repository
and the vetted manifest (D-80), and refuses a tag of another major
version.

Rejected: publishing inside the goreleaser job.
Cost: an entry cannot be withdrawn, so a wrong one stands until a new
release replaces it.

### D-77 Every tool the release runs is pinned, and the rehearsal runs the same one

The Makefile pins goreleaser beside the other tools, and
`make release-rehearse` runs that version locally, unsigned. The release
workflow pins its own. `make pins` compares the two, both of them code,
and the runbook names a make target rather than a version.

An action that installs a tool is pinned, and so is the tool it
installs: cosign's release, syft's version and goreleaser's own. A newer
cosign made `--bundle` required, and a pinned action installing an
unpinned cosign fails rather than degrades. The check reads only a
step's `with:` block, so a `version:` under `env:` does not satisfy it.
It reads a workflow in either of YAML's list styles, and fails on one it
cannot read rather than reporting no problems.

Each action is paired with its tool in a table. The pairing is not
derived from the two names, since that looks free and gets
`anchore/sbom-action` wrong: it installs syft.

Rejected: a gate that keeps a version written in the docs equal to the
config; deriving the pairing from the names.
Cost: the Makefile pins goreleaser beside the other tools, and the two
pins move together.

### D-78 The bundle asks for every setting the server reads, and its defaults configure nothing

The bundle asks for what a person sets in another client's
configuration: the OAuth client file, the profile, read-only, the
destructive switch, sharing and the decline-all switch. Each is named
for its variable without `GCAL_`, lowercased. The keys come from
`config.Vars` (D-63), and each variable left out has a reason in
`bundleLeftOut`: logging, the read budgets, the concurrency, the
timeouts, the configuration directory and the refresh token.

Claude Desktop has nothing like Claude Code's ask rules, and a person
can set a tool to always allow. So a switch left at the server's own
default is the one guard a bundle install keeps. Each switch defaults
to the server's default: sharing on, the others off. The client file
and the profile default to `""`, since an optional key with no default
reaches the server as the unsubstituted placeholder.

`scripts/gates mcpb` starts the server's configuration from the
bundle's defaults and from no environment, and refuses any difference,
naming the fields. No default may hold `${...}`, which a host may
substitute. A credential's field is marked sensitive.

Rejected: a `tools` list in the manifest, which is display only and
goes stale; an optional key with no default.
Cost: a switch at its default is a bundle install's one guard.

### D-79 The bundle passes settings by `env` alone and starts its staged program from `${__dirname}`

`mcp_config.args` is present and empty, and an argument there is
refused. A platform override's `env` or `args`, an empty one included,
is refused too. The reference host substitutes an override for the base
rather than merging, so the settings would not reach that platform, and
an argument such as a flag would reach the server past every check on
`env`.

`entry_point` and every command name a staged program, never `LICENSE`
or `README.md`, and start with `${__dirname}/`. The
[MCPB manifest specification](https://github.com/anthropics/mcpb/blob/v2.1.2/MANIFEST.md)
shows a relative command once and never says what it resolves against,
and the reference host spawns it as written. `${__dirname}` is the one
form whose meaning the specification fixes. Each platform the bundle
claims spawns the file staged for it, and an override for a platform it
does not claim is refused.

Rejected: a relative command; an override's `env` or `args`.
Cost: only the reference host was read, and Claude Desktop itself is
not probed.

### D-80 The gate, the packer and the registry entry read one vetted manifest

`committedManifest` reads and decodes the committed manifest once and
runs every check over it. The `mcpb` gate, the packer and the registry
entry all start from it, so the release packs and describes no manifest
the gate would refuse.

A key an object repeats, or gives twice in two cases, is refused first,
naming its path. Go's decode into a struct matches a key in any case
and keeps the last, while the schema validator, the packer's stamp and
the host's `JSON.parse` keep the spellings apart. The checks would then
read a different document from the one shipped.

The packer refuses a version that is not Semantic Versioning, which the
schema allows, and holds the manifest it stamps, encoded again, to the
vendored schema. The vendored schema is held to a recorded digest. The
manifest declares version 0.3, since 0.4 adds only a server type this
bundle does not use.

Rejected: a second decode of the manifest for the registry entry;
declaring manifest version 0.4.
Cost: the vendored schemas need `make schema-refetch` before a tag that
changes them.

### D-81 The bundle runs on every platform Claude Desktop does

The bundle stages one program per platform under `server/`: a universal
macOS binary, a Windows `.exe`, a Linux binary for amd64 and one for
arm64, and `launch-linux.sh`, which picks between the two Linux
binaries. `LICENSE` and `README.md` sit at the root.

What the bundle says it is is held to what it stages. The manifest
claims exactly the platforms the packer stages an entry point for, so
dropping a platform together with its override is refused rather than
passed. Every staged binary must be executable, and a build output
missing one is refused before anything is packed. The launcher's
install hint is held to the module `go.mod` declares, which the
release's tag check and the registry entry read too.

The reason is that a bundle missing a platform fails only on that
platform, at install, for a person who cannot fix it.

Rejected: a manifest that claims a platform the packer stages nothing
for, or drops one together with its override.
Cost: every release builds and stages a binary for each platform, a
universal macOS one included.

### D-82 Push notifications, imports and administrator access are out of scope

Three parts of the API are written off in `testdata/api-coverage.tsv`
by decision.

Push notifications need a public HTTPS endpoint Google can POST to:
`events.watch`, `acl.watch`, `calendarList.watch`, `settings.watch` and
`channels.stop` all serve a webhook. A stdio server has nowhere to put
one.

`events.import` carries a private copy of an event between systems,
keeping its `iCalUID` and organizer. It is a migration tool, not a
scheduling one, and its failure modes are different.

`calendars.transferOwnership` needs a Workspace administrator's
privilege and `useAdminAccess: true`. This server signs in as one
ordinary person and never asks for administrator access.

Rejected: a watch channel, an import tool and an ownership transfer,
each for the reason above.
Cost: changes are found by asking, through `list_changes` (D-37), and
migration and administration happen outside this server.

### D-83 The server keeps a local event cache, or the sync token

Google's `syncToken` is designed for a client that keeps a replica of
the calendar. This server keeps none: it is stateless between calls,
and a replica it cannot invalidate is a replica that lies.

Storing the sync token instead would make the server stateful about
"since when?", a question whose answer belongs to whoever asked. Two
people sharing one server do not share a last look.

What is done instead is `list_changes`, which hands the token to the
caller and takes it back on the next call (D-37), and reads that state
their window, so a stale answer cannot pass for a current one (D-12).

Rejected: a local event cache; a sync token the server stores.
Cost: the caller holds the token and passes it back, and a read is
always a request to Google.

### D-84 `quickAdd` creates events from text

[events.quickAdd](https://developers.google.com/workspace/calendar/api/v3/reference/events/quickAdd)
creates an event from a string such as "Dinner Thursday 7pm". The case
for it is real: it is one call where the structured path is several,
and people type strings like that.

It stays out because it gives no control over the zone, which is what
this server exists to get right (D-10), and its result does not say
what it parsed or in which zone it read the time. The model calling this
server is a better parser than Google's, and the structured path makes
its reading visible. Revisit it only with evidence of what quickAdd does
with an ambiguous zone.

Rejected: `events.quickAdd` behind a tool of its own.
Cost: an event described in a sentence is parsed by the model into
`create_event`'s fields, several arguments instead of one string.

### D-85 The server confirms a write by asking the person through elicitation

Before the writes that cannot be taken back, the server could put a
form to the person through MCP elicitation and wait for an accept. It
does not.

The specification gives approval of each call to the client, and
[elicitation](https://modelcontextprotocol.io/specification/2025-06-18/client/elicitation)
never makes the answer consent. Clients answer the question with nobody
there: Claude Code's non-interactive mode cancels in milliseconds, and
Codex with full access accepts a form with no fields. Others do not draw
it, or leave it hanging, and no client's allow and ask rules match it.
Most widely used servers confirm nothing themselves. A question on
every call is answered without reading.

The `requiresUserInteraction` mark sent only to a client that cannot
ask goes with it, as does `GCAL_REQUIRE_PROMPT`, which refused the
asking writes on such a client (D-56).

Rejected: a form elicitation before the writes that cannot be taken
back; the mark only where the server cannot ask.
Cost: the confirmation is the client's prompt, which an unattended
client skips, so the registration flags carry the weight (D-55).

### D-86 The Unix epoch reads as no time

Some Google APIs write a time they do not have as the Unix epoch, and a
reader there should show the epoch as no time. Calendar has not been
seen to do it, and the rule does not fit its data.

An event's start and end are the calendar's content, and an event in
1970 is a real event that a person can create. Reading the epoch as no
time would hide it, or show it as undated. The times Google writes
about an event rather than in it, `created` and `updated`, are not
shown, so the rule has nowhere else to apply.

So a start or end is shown as given, whatever its value.

Rejected: reading the epoch as no time in an event's start or end.
Cost: if Calendar ever writes the epoch for a time it does not have,
the event shows as starting in 1970.

### D-87 `tools.Kind` splits into an effect and a gate

`tools.Kind` encodes two things: what a tool does to the world, which
decides its annotations, and which flag it registers behind. A kind
such as `SharingRead` exists only because the two disagree. A
`Def{Effect, Gate}` would replace the vocabulary with two fields.

It is declined. The split restructures every registration for no change
in behavior, and the two switches are already held by tests that assert
every combination: the full surface opens every gate, sharing off
removes every sharing tool, and each kind's annotations match.

The argument for doing it before the kinds grow is fair, and it is the
one to revisit if another kind appears only because effect and gate
disagree.

Rejected: splitting `Kind` into an effect and a gate.
Cost: a kind exists for each disagreement between effect and gate, and
the vocabulary grows with them (D-54).

### D-88 One table-driven gate holds the method, field and live-step records

`api-coverage`, `api-fields` and `live-cover` have one shape: a
published set, a hand-written verdict set, failure in both directions,
and a reason on every write-off. One table-driven gate could hold all
three.

It is declined. Collapsing them restructures three working gates for no
new check, and each record has its own source: the discovery document
for methods and fields, the driver's step tables for live coverage.

What mattered was each gate failing in both directions, and each does,
a client call with no verdict included.

Rejected: one generic record gate for the three.
Cost: three gates share a shape and are kept apart, so a fix to one
direction is made in each.

### D-89 `live-cover`'s exemptions live in a TSV

The method and field records are TSV files under `testdata`. The
exemptions `live-cover` allows, the tools the driver cannot exercise,
could be one too.

They stay a map in the live driver's own source. An exemption says why
a tool cannot be driven, and that belongs beside the steps it is an
exception to, where a person adding a step sees it and removes it. A
TSV beside the other records would put the reason one directory away
from the code it explains.

Rejected: the exemptions as a TSV under `testdata`.
Cost: the exemptions are not beside the other records, and a reader of
`testdata` does not see them.

### D-90 A generic wrapper serves the write tools' handlers

The write tools' handlers repeat a few lines: decode, call the service,
return the result. A generic wrapper could serve them all.

It is declined. It saves a few lines at the cost of making the write
registrations read differently from the read ones, and the registration
of every tool is where a reviewer checks its `Kind`, its description
and its handler side by side. Reading every tool the same way is worth
more there than the lines saved.

Rejected: a generic wrapper around the write tools' handlers.
Cost: each write handler repeats a few lines of the same shape.

Next ID: D-91
