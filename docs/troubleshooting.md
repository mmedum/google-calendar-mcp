# Troubleshooting

What to do when something does not work, then the maintenance tasks.
Start with these two commands:

```bash
google-calendar-mcp status
google-calendar-mcp doctor
```

`status` asks Google nothing. It prints the profile, the account, where
the token is stored, the scopes Google granted and the switches in
effect. `doctor` checks the OAuth client JSON, the stored token, the
granted scopes, whether the Calendar API answers, and whether the account
has a time zone. Each failed check says what to do.

## `[auth] not signed in: run google-calendar-mcp login`

The same cause shows as `credentials: no refresh token found`, and
`status` says the profile is not signed in.

**Cause:** this profile has no refresh token, or Google refuses the one it
has. Google ends a refresh token when you revoke access or it goes unused
for six months. On an External app in testing, every refresh token
expires after seven days, as
[Google's OAuth documentation](https://developers.google.com/identity/protocols/oauth2)
says under "Refresh token expiration".

**Fix (resolves it):** check that `status` shows the profile you expect,
then run `google-calendar-mcp login` again. For the seven-day expiry,
logging in again is a workaround; publishing the app out of testing
resolves it.

The server still starts and lists its tools; every call answers `[auth]`
until you log in.

## `[auth] the signed-in account has not granted a scope this call needs`

`login` warns `these scopes were not granted, so some tools will fail`,
and `doctor` fails its `granted scopes` check, naming each one.

**Cause:** Google lets you untick scopes at consent, so a partial grant is
a normal state. On an External app, a scope missing from the consent
screen's list is never offered at all.

**Fix (resolves it):** on an External app, add the scopes
[`docs/gcp-setup.md`](gcp-setup.md#2-oauth-consent-screen) lists. Then run
`login` again and accept every scope. The tools you did grant keep
working meanwhile.

## `could not read who this calendar is shared with`

`get_calendar` and `list_sharing` say this, and that it is not the same
as the calendar being shared with nobody.

**Cause:** reading the list of sharing rules needs
`calendar.acls.readonly`, which `calendar.readonly` does not cover. The
token predates the scope, or it was unticked at consent.

**Fix (resolves it):** run `login` again and grant the scope. On an
External app, add it to the consent screen first.

A different sentence, `that needs OWNER access`, means you do not own the
calendar. Google shows the sharing list only to an owner, and nothing on
your side changes that.

## `doctor` fails `Calendar API reachable`

**Cause:** the Google Calendar API is not enabled in the Cloud project
your OAuth client belongs to. It is the most common first-run failure,
and Google's message names the API.

**Fix (resolves it):** enable **Google Calendar API** in that project, as
[`docs/gcp-setup.md`](gcp-setup.md#1-project-and-api) says, wait a minute, and
run `doctor` again.

## `client secret JSON is not a Desktop app client`

`login` adds `this is a Web application client; create a Desktop app
client instead` when it is one.

**Cause:** the loopback redirect this server uses is a native-app flow,
which only a Desktop app client allows.

**Fix (resolves it):** create an OAuth client of type **Desktop app**, as
[`docs/gcp-setup.md`](gcp-setup.md#3-oauth-client) says, and pass its JSON to
`login --client-secret`.

## `auth: timed out waiting for the browser`

**Cause:** the browser could not reach the callback. Over SSH, the
callback goes to the loopback interface of the remote machine, while the
browser runs on yours.

**Fix (resolves it):** run `login --no-browser`. It prints the
authorization URL and the exact `ssh -L` line to run first, with the port
it picked.

## `keyring unavailable (…); refresh token saved in plaintext`

`status` names `file` as the token store, and every later use warns
`refresh token read from the plaintext file`.

**Cause:** the OS keyring could not be reached, for example on a headless
Linux machine with no Secret Service running.

**Fix (resolves it):** once a keyring is available, run `login` again.
The token goes into the keyring and the file is deleted.

**Impact:** the file is `0600` beside the profile's `config.json`; on
Windows it is restricted to your account by an explicit access control
list. That is less protection than a keyring entry. Anyone who can read
the file holds every scope you granted, until you revoke it.

## `the profile records a refresh token in the OS keyring and the keyring returned nothing`

**Cause:** the keyring holds the token and will not hand it over. It is
more likely locked, or its session bus unreachable, than empty.

**Fix (resolves it):** unlock the keyring and try again.

**Workaround:** `login` writes a fresh token beside the first, which
leaves the cause in place.

## `Google returned no refresh token`

**Cause:** Google completed the sign-in without a refresh token, which
the server needs to stay signed in.

**Fix (resolves it):** remove the app at
[your Google account's third-party access page](https://myaccount.google.com/permissions),
then run `login` again.

## `[invalid] no time zone available`

`doctor` fails its `account time zone` check the same way.

**Cause:** the call gave no `time_zone`, the calendar has none, and your
Google Calendar settings have none. The server never falls back to UTC or
the machine's zone, because either would answer in a zone you never
chose.

**Fix (resolves it):** set a time zone in Google Calendar's settings.

**Workaround:** pass `time_zone` on the call, as an IANA name such as
`Europe/Copenhagen`.

## A tool you expect is missing

**Cause:** the tools that cannot be taken back are registered only on
request. `status` shows which switches are on.

- `delete_calendar`, `clear_calendar`, `publish_calendar` and
  `widen_calendar_access` need `GCAL_ENABLE_DESTRUCTIVE=true`.
- The sharing tools need `GCAL_SHARING=on`, the default.
- `create_event_declining_all` needs `GCAL_ENABLE_DECLINE_ALL=true`.
- `GCAL_READ_ONLY=true` removes every write.

**Fix (resolves it):** set the switch in your client's configuration, as
[`docs/configuration.md`](configuration.md) shows, and restart the server.

## `GCAL_READONLY was removed` or `GCAL_REQUIRE_PROMPT was removed`

The server refuses to start.

**Cause:** these are removed settings. The server refuses one that is
turned on rather than ignore it.

**Fix (resolves it):** use `GCAL_READ_ONLY` in place of `GCAL_READONLY`.
Unset `GCAL_REQUIRE_PROMPT`; your client's approval decides each call.
Leave `GCAL_ENABLE_DESTRUCTIVE` and `GCAL_ENABLE_DECLINE_ALL` off for a
client that runs with nobody watching.

## `[blocked] … outside your organization, and notify:none is refused there`

**Cause:** a guest outside the organizer's domain may have no Google
Calendar for the event to appear in. Email is the only way they learn of
it.

**Fix (resolves it):** pass `notify: external_only` to email exactly them, or
`notify: all`. There is no way to add such a guest silently.

## `[stale] this changed since you read it`

**Cause:** someone changed the event or calendar after the read the write
was planned from, so Google refused the write rather than overwrite them.

**Fix (resolves it):** read it again, check it is still what you mean, and
reapply the change. Nothing was written.

## `[stale] Google has discarded this sync token`

`list_changes` says this, or `Google no longer keeps changes from as far
back as …` under `updated_since`.

**Cause:** Google expires sync tokens, and keeps changes for a limited
time.

**Fix (resolves it):** call `list_changes` with neither `sync_token` nor
`updated_since` for a baseline and a new token. With `updated_since`, a
later moment also works.

**Impact:** treat anything you held from before as unreliable, not merely
out of date.

## `[rate_limited] Google is rate limiting this account`

**Cause:** Google allows each user a number of requests per minute, on a
sliding window. From `create_calendar`, it is the separate limit on
creating calendars.

**Fix (resolves it):** wait and retry. Creating calendars is the exception: deleting
one does not give the quota back, and retrying soon does not help. It can
take up to a day.

## `[ambiguous_outcome] this event may or may not have been created`

The same class follows a move, a share or a new calendar.

**Cause:** the request failed without an answer the server can trust, so
the write may have landed.

**Fix (resolves it):** read before retrying. The message names the event id to read
with `get_event`, or the title to look for in `list_calendars`.

**Impact:** a blind retry of `create_calendar` makes a second calendar,
because Google mints its id.

## `[ambiguous_outcome] the original series … was truncated`

**Cause:** a `this_and_following` change is two calls. The first cut the
series short; the second, which creates the new series, failed.

**Fix (resolves it):** read the id the message names with `get_event`. If it does not
exist, recreate the later occurrences with `create_event`.

**Impact:** the later occurrences are gone from the calendar until the
new series exists.

## `[unsupported] … Google does not let an owner remove their own calendar from their list`

**Cause:** `manage_calendar` with `unsubscribe` on a calendar this account
owns. Google refuses it for the calendar's owner.

**Workaround:** `hidden: true` keeps it out of your way, and `delete_calendar`
removes it for everybody.

## A live run warns that the status event may still be on the primary calendar

The warning starts `WARNING: the status event`, and names the event's
title, its start and a record file.

**Cause:** `make live` makes one status event on the primary calendar
and could not delete it, or read it back gone.

**Fix (resolves it):** delete the event the warning names by hand in Google Calendar,
then the record file.

**Impact:** until then, an invented out-of-office, focus-time or
working-location event stays on your primary calendar.

## Tasks

### Rotating the OAuth client secret

Add a second secret to the same OAuth client in Google Cloud and download
its JSON. Then:

```bash
google-calendar-mcp logout
google-calendar-mcp login --client-secret ./new_client_secret.json
```

`logout` revokes the grant at Google, so no copy of the old token keeps
working. If your client configuration sets `GCAL_CLIENT_SECRET`, point it
at the new file. Delete the old secret in Google Cloud last.

### Revoking access

```bash
google-calendar-mcp logout
```

It revokes the grant at Google, then deletes the stored token and the
profile's state. Google revokes the grant rather than one token, so
`logout` names every other profile that uses the same OAuth client first.
If the revoke fails, it says so and removes the local copy anyway; revoke
then from [your Google account's third-party access page](https://myaccount.google.com/permissions).
A token in `GCAL_REFRESH_TOKEN` is out of its reach.

If you have lost the machine, revoke from that page. The grant ends there.

### Two accounts on one machine

Profiles keep separate client secrets, accounts and tokens:

```bash
google-calendar-mcp login --profile work --client-secret ./work_client_secret.json
google-calendar-mcp login --profile personal --client-secret ./personal_client_secret.json
```

Then give each client entry its own `GCAL_PROFILE`, as
[`docs/configuration.md`](configuration.md) shows.

### Reporting a problem

Include the `doctor` output, the version (`google-calendar-mcp
--version`), how you installed it, which client you use, and the `GCAL_*`
switches you set. For a failing call, add the server's log at
`GCAL_LOG_LEVEL=debug`, which your client keeps from the server's stderr.

The logs carry no event titles, descriptions, locations, addresses or
search terms, so a debug log is safe to paste. Never paste a calendar id,
a guest list or your OAuth client JSON.
