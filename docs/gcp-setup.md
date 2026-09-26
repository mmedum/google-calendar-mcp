# Google Cloud setup

What a person must paste into the Cloud console. **This page is an input,
not a description** — the scope list below is generated from
`internal/auth/auth.go`, so it cannot fall behind the tool surface. A
scope missing from your consent screen is not a stale document, it is a
403 from one tool weeks after a working setup.

## 1. Project and API

Create a Google Cloud project, then enable the **Google Calendar API**
in it. This is the most common first-run failure, and
`google-calendar-mcp doctor` names it when it is missing.

## 2. OAuth consent screen

Configure the consent screen. If your account is not in a Workspace
organization, choose **External** and add yourself as a test user.

Add these scopes. The set depends on how you run the server:

### Read-only (`GCAL_READONLY=true`)

```
https://www.googleapis.com/auth/calendar.readonly
https://www.googleapis.com/auth/calendar.settings.readonly
https://www.googleapis.com/auth/calendar.acls.readonly
```

### Read-write (the default)

```
https://www.googleapis.com/auth/calendar.readonly
https://www.googleapis.com/auth/calendar.settings.readonly
https://www.googleapis.com/auth/calendar.acls.readonly
https://www.googleapis.com/auth/calendar.events
https://www.googleapis.com/auth/calendar.calendars
https://www.googleapis.com/auth/calendar.calendarlist
https://www.googleapis.com/auth/calendar.acls
```

With `GCAL_SHARING=off`, drop the two `acls` scopes.

### Two things worth knowing about this list

**The broad `calendar` scope is deliberately absent.** It grants
everything the API can do, including deleting a calendar and clearing
your primary one — the operations this server keeps behind
`GCAL_ENABLE_DESTRUCTIVE`. Asking for it would make that flag a label
rather than a limit.

**`calendar.acls.readonly` is separate on purpose.** Reading *one*
sharing rule is covered by `calendar.readonly`; reading the *list* of
them is not. Verified against the Calendar v3 discovery document. Without
it, `get_calendar` reports that it could not read the sharing list — and
says so, rather than showing an empty list that would read as "shared
with nobody".

## 3. OAuth client

Create credentials of type **OAuth client ID**, application type
**Desktop app**. Download the JSON.

It must be a Desktop app client. A Web application client will be
rejected with a message saying so: the loopback redirect this server uses
is a native-app flow.

## 4. Sign in

```
google-calendar-mcp login --client-secret ~/path/to/client_secret.json
google-calendar-mcp doctor
```

`login` prints the scopes it is about to ask for before opening a
browser, and warns if Google grants fewer than it asked for.
