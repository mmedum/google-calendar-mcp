# Configuration

Every setting is an environment variable with a `GCAL_` prefix and a
command-line flag with the same name. A flag given explicitly beats the
environment; the environment beats the built-in default.

Environment variables are the ones that matter in practice: every MCP
client passes command, args and env to a stdio server, and nothing else.

## Settings

| Variable | Flag | Default | What it does |
|---|---|---|---|
| `GCAL_PROFILE` | `-profile` | `default` | Named configuration profile. Separate profiles keep separate tokens. |
| `GCAL_LOG_LEVEL` | `-log-level` | `info` | `debug`, `info`, `warn`, `error`. Logs go to stderr. |
| `GCAL_LOG_FORMAT` | `-log-format` | `text` | `text` or `json`. |
| `GCAL_READONLY` | `-read-only` | `false` | Register only read tools, and request only read scopes so the API itself refuses a write. |
| `GCAL_ENABLE_DESTRUCTIVE` | `-enable-destructive` | `false` | Register `delete_calendar` and `clear_calendar`. Each still needs `confirm` on the call. |
| `GCAL_SHARING` | `-sharing` | `on` | `on` or `off`. Off removes the calendar sharing tools entirely. |
| `GCAL_MAX_EVENTS` | `-max-events` | `250` | Default event budget for one read. The result says when it truncated. |
| `GCAL_MAX_CALENDARS` | `-max-calendars` | `25` | How many calendars one call may fan out across. The API caps free/busy expansion at 50. |
| `GCAL_CONCURRENCY` | `-concurrency` | `4` | Requests in flight during a fan-out. |
| `GCAL_HTTP_TIMEOUT` | `-http-timeout` | `60s` | Per-attempt timeout for a read. |
| `GCAL_WRITE_TIMEOUT` | `-write-timeout` | `120s` | Timeout for a write. |
| `GCAL_CLIENT_SECRET` | `-client-secret` | — | Path to the OAuth Desktop client JSON. Overrides what the profile remembers. |

Two more are read directly rather than through a flag:

| Variable | What it does |
|---|---|
| `GCAL_CONFIG_DIR` | Overrides where profile state is stored. Default is your OS config directory plus `google-calendar-mcp`. |
| `GCAL_REFRESH_TOKEN` | Supplies the refresh token directly, for CI and automation. It takes precedence over the keyring and the file. |

## Where things are stored

- Profile state (non-secret): `config.json` in the profile directory. It
  records the client JSON path, the account email, where the token went
  and which scopes were granted.
- The refresh token: your OS keyring, keyed by profile. If the keyring is
  unavailable, a `0600` file beside `config.json` — and the server warns
  about that on every use, not just once at login.

`google-calendar-mcp status` prints the profile directory and everything
above except the token itself.

## Read budgets

Reads are budgeted in **events**, not requests, because that is what
reaches a client's result limit. A truncated read says so in the text a
model sees, gives the counts, and returns a `next_page_token`.

A fan-out across calendars costs one request per calendar. The result
reports how many requests it spent. Google allows 600 requests per user
per minute, on a sliding window.
