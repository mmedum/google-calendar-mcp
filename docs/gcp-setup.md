# Google Cloud setup

What a person pastes into the Cloud console. **This page is an input,
not a description.** It is written by hand, and the staleness gate
checks each scope block below against `internal/auth/auth.go`, so it
cannot fall behind what `login` asks for. On an External app, a scope
missing from your consent screen is a 403 from one tool weeks after a
working setup.

## 1. Project and API

Create a Google Cloud project, then enable the **Google Calendar API**
in it. This is the most common first-run failure, and
`google-calendar-mcp doctor` names it when it is missing.

## 2. OAuth consent screen

Configure the consent screen. If your account is not in a Workspace
organization, choose **External** and add yourself as a test user.

On an External app, add these scopes. Google asks for the list only from
apps used by people outside your Workspace organization: for an
**Internal** app, "scopes aren't listed on the consent screen", as
[Google's consent screen guide](https://developers.google.com/workspace/guides/configure-oauth-consent)
says, so an Internal app can skip this step. The set depends on how you
run the server:

### Read-only (`GCAL_READ_ONLY=true`)

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

With `GCAL_SHARING=off`, drop the `acls` scopes.

### Why the list looks like this

**The broad `calendar` scope is deliberately absent.** It grants
everything the API can do, including deleting a calendar and clearing
your primary one — the operations this server keeps behind
`GCAL_ENABLE_DESTRUCTIVE`. Asking for it would make that flag a label
rather than a limit.

**`calendar.acls.readonly` is separate on purpose.** Reading *one*
sharing rule is covered by `calendar.readonly`; reading the *list* of
them is not, as the Calendar v3 discovery document's scopes for
`acl.get` and `acl.list` show. Without it, `get_calendar` says it could
not read the sharing list, rather than show an empty list that would
read as "shared with nobody".

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
