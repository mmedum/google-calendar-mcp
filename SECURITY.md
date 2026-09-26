# Security policy

## Reporting a vulnerability

Please report security issues privately through GitHub's
[security advisory](https://github.com/mmedum/google-calendar-mcp/security/advisories/new)
form rather than opening a public issue.

Include what you did, what happened, and what you expected. Please do not
include real calendar data, email addresses or credentials in the report:
describe the shape of the problem instead.

You should get an acknowledgment within a week.

## Scope

This server runs locally, speaks MCP over stdio, and authenticates as one
user against Google's Calendar API with that user's own OAuth client.
There is no hosted component and no multi-tenant surface.

Things that are in scope and worth reporting:

- Anything that puts calendar content, an email address or a token into a
  log, an error message, a test fixture or this repository.
- Anything that makes a write happen without the guards it is supposed to
  pass, or that registers a destructive tool without its flag.
- Anything that lets stdout carry something other than a JSON-RPC frame,
  which corrupts the protocol.
- A dependency vulnerability `make vuln` does not catch.

## What the design already assumes

Tool annotations are **not** a control. The specification says clients
treat them as untrusted, and a host in an auto-approve permission mode
will run an annotated tool without prompting. Anything that must not run
unattended is not registered at all unless its environment flag is set.
Please do not report "the client did not prompt" as a vulnerability — but
do report a tool that is registered when its flag is off.
