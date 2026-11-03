# Security model

The threat model and the invariants the code holds: the reference for "is
that safe?", in review or before you run this.
[`SECURITY.md`](../SECURITY.md) says how to report a vulnerability.

It applies to one binary, running as a subprocess of your MCP client, on
your machine, with your own OAuth client and your own token (D-01). There
is no network listener except while `login` waits for the browser, no
shared deployment, and nobody else's tokens.

## Trust boundaries

| Boundary | Crosses | Assumption |
|---|---|---|
| MCP client ↔ this server | An OS pipe | Both run as the same OS user, with no authentication between them. A process that can write to the server's stdin can already read its memory. |
| This server ↔ Google | TLS | Go's `crypto/tls` verifies every call against the system roots, the OAuth endpoints included. Nothing in the tree skips verification. |
| This server ↔ disk | The file system | The profile directory under your user config directory, or `GCAL_CONFIG_DIR`: the profile's `config.json`, and the refresh token only when the OS keyring was unavailable. On Windows the token file's protection is an explicit access control list, not its mode. |
| Browser ↔ the login listener | `127.0.0.1` on a random port | Alive only while `login` waits. PKCE and state are checked on the callback (D-62). |

The server opens no file a tool names. An event's attachments are listed
by their Drive file id and never fetched.

## What is worth stealing

- **The refresh token.** Long-lived, and it carries every scope you
  granted. It lives in the OS keyring where there is one, and in an
  owner-only file where there is not.
- **The access token in flight.** Short-lived, with the same scopes.
- **Calendar content.** Event titles, descriptions, locations, and every
  guest's address. They pass through tool results to your client, and
  nowhere else.
- **Your OAuth client secret.** Google treats a Desktop client's secret
  as public, which is why the loopback flow uses PKCE. It is still yours.
  The file is read where you keep it, never copied.

## Adversaries

**A malicious MCP client, or a model driving an honest one.** It can call
any registered tool under your credentials. A client may ignore tool
annotations, so registration is the control (D-55):

- `delete_calendar` and `clear_calendar`, and the wider shares
  `publish_calendar` and `widen_calendar_access`, register only with
  `GCAL_ENABLE_DESTRUCTIVE=true`, and each call still takes `confirm`.
  The wider shares also take the calendar's title as read (D-52).
- `create_event_declining_all` registers only with
  `GCAL_ENABLE_DECLINE_ALL=true`, a flag of its own (D-44).
- `share_calendar` reaches named people and groups inside the
  organization, below owner, and otherwise only lowers access already
  given (D-50). A group rule reaches everyone in the group, including
  people added later, and the result says so.
- Every write that can reach a person requires `notify` (D-21), and every
  write to a recurring event requires `scope` (D-18).

Past that, Google's per-scope authorization is the bound, and
`GCAL_READ_ONLY` is yours.

**A co-resident process on your machine.** It cannot take the login
socket, and the token file is readable by your account alone.

**Prompt injection from calendar content.** Anyone who knows your address
can send you an invitation, and its title and description reach the client verbatim.
This server cannot tell an instruction from a sentence. Know that before
pointing an unattended agent at your calendar.

## Invariants the code holds

A test or a gate holds each of these. A way to break one is a
vulnerability.

- **Stdout carries only JSON-RPC.** Logs go to stderr; forbidigo and the
  smoke test hold it (D-02).
- **Logs never carry the payload.** No address, title, description,
  location or search term at any level. A transport error is stripped of
  its URL, which can carry a search term; `TestURLErrorIsStripped` holds
  it (D-03).
- **The gated tools are absent without their flags**, and take `confirm`
  when present (D-55). `TestTheGatedToolsNeedTheFlag` holds it.
- **An ordinary share stops at the organization.** An address is inside
  only when its domain is the account's own, and any doubt counts as
  outside, so a personal Google account shares with nobody (D-51).
- **`none` is refused when a guest is outside the organizer's domain**
  (D-24), and no result claims what a guest received (D-23).
- **A dry run writes nothing** (D-30).
- **Read-only mode asks for read scopes only**, so Google refuses a write
  too (D-60). Login never asks for the broad `calendar` scope (D-61).
- **No tool changes the server's own settings** (D-59). The flags, the
  environment and the profile are the operator's.
- **The server asks the person nothing** (D-56). The
  `requiresUserInteraction` mark on the tools D-57 names is read by
  Claude Code alone, so it is never the control.
- **The token file is written only when the keyring was unavailable**,
  owner-only, and every use of it warns (D-62).
- **`logout` revokes at Google before deleting locally.** A deleted local
  copy of a token that still works is not a logout.
- **Nothing in the repository identifies a real person, account or
  calendar** (D-04). Fixtures are generated, never recorded (D-05), and
  the live driver reads only what it wrote (D-71).
- **Tests never reach the real keyring** (D-69).

## Not a vulnerability

Do not report these as vulnerabilities. Open an ordinary issue if one
surprises you.

- **The client did not prompt.** Annotations and marks are hints. A tool
  registered while its flag is off is a vulnerability.
- **What runs as you can act as you.** A compromised host or user
  account, or any process running as you, can read the token and drive
  the server.
- **A tool doing what its registration allows.** A client or a model
  calling a registered tool under your credentials is the design. The
  controls are `GCAL_READ_ONLY`, `GCAL_SHARING`, `GCAL_ENABLE_DESTRUCTIVE`
  and `GCAL_ENABLE_DECLINE_ALL`.
- **A model following instructions in an event.** That is the client's
  concern. An event that makes the server itself break an invariant is a
  vulnerability.
- **Your Workspace administrator reading your calendar.** They can, with
  or without this server.
- **Someone else's code.** A flaw in Google's OAuth service or APIs goes
  to Google, and one in a dependency, such as the MCP Go SDK, to that
  project. We will help route it.

## Dependency vulnerabilities

`govulncheck` runs in `make check` and in CI, on every pull request and
every push to `main`. It fails the build when this server's code can
reach a vulnerable symbol.

A CVE in a dependency is a vulnerability here only when `govulncheck`
reports it reachable. It is then fixed in a release whose CHANGELOG entry
names the CVE or GHSA. Otherwise the dependency is updated in the normal
course, and a report about it is not a vulnerability.

## What you are responsible for

- **Your own OAuth client and Cloud project.** Each person creates their
  own, so nobody else's rollout can revoke your access.
  [`docs/troubleshooting.md`](troubleshooting.md#tasks) says how to rotate the
  secret and revoke access.
- **The switches you turn on.** `GCAL_ENABLE_DESTRUCTIVE` and
  `GCAL_ENABLE_DECLINE_ALL` arm tools that cannot be taken back. Leave
  them off for a client that runs with nobody watching, and use
  `GCAL_READ_ONLY=true` where reading is enough.
- **The scopes you grant.** A tool whose scope you declined fails with
  `[auth]` naming it. That is a supported state, not a broken one.
- **Where the token lives.** Keep a keyring available, keep
  `GCAL_CONFIG_DIR` somewhere only you can read, and keep
  `GCAL_REFRESH_TOKEN` out of shared shells and logs.
- **Keeping the client secret file out of version control.**
