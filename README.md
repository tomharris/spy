# spy

A macOS Slack CLI that auto-authenticates from your already-signed-in Slack desktop app. No OAuth, no app install, no token entry.

```
$ spy auth
authenticated as Tom Harris @ Acme
  workspace: acme  (T024D6SRW80)
  user id:   U01ABCDEF
  url:       https://acme.slack.com/

$ spy ch
# general  (412 members)  C012ABCDEF
# random   (388 members)  C013BCDEFG
🔒 leadership  (12 members)  C014CDEFGH
...

$ spy r general 5
[2026-05-25 09:14:22] Anjali Patel:
  Coffee chat 10:30?
[2026-05-25 09:18:51] Marcus Lee [3 replies]:
  Deploy looks green — anyone object to rolling forward?
...
```

## Requirements

- macOS (only — the auth flow reads the Slack desktop app's local data)
- Slack for Mac, signed in to at least one workspace (App Store or direct download both work)
- Go 1.21+ to build from source

## Install

```bash
git clone https://github.com/tomharris/spy
cd spy
go build -o bin/spy ./cmd/spy
# put bin/spy somewhere on your PATH, or:
go install ./cmd/spy
```

The first command you run will trigger a macOS Keychain prompt asking permission to read the "Slack Safe Storage" entry. Click **Always Allow** — otherwise every subsequent invocation will prompt again.

## Commands

### Read

| Command | Alias | What it does |
| --- | --- | --- |
| `spy auth` | | Verify auth and print workspace/user info |
| `spy channels` | `ch` | List public + private channels |
| `spy users` | `u` | List workspace users |
| `spy dms` | `dm` | List DM conversations |
| `spy read <channel\|@user> [count]` | `r` | Read recent messages |
| `spy thread <channel> <ts> [count]` | `t` | Read replies in a thread |
| `spy search <query...> [count]` | | Search messages workspace-wide |
| `spy pins <channel>` | `pin` | List pinned items |
| `spy activity` | `a` | Unread + mention counts everywhere |
| `spy unread` | `ur` | Same as activity but unreads-only, excludes muted |
| `spy starred` | `star` | VIP users + starred items |
| `spy saved [count]` | `sv` | Saved-for-later items (`--all` includes completed) |

### Write

| Command | Alias | What it does |
| --- | --- | --- |
| `spy send <channel\|@user> <msg...>` | `s` | Send a message |
| `spy react <channel> <ts> <emoji>` | | Add an emoji reaction |
| `spy draft <channel> <msg...>` | | Save a draft (appears in Slack UI) |
| `spy draft thread <channel> <ts> <msg...>` | | Save a draft thread reply |
| `spy draft user <user> <msg...>` | | Save a draft DM |
| `spy draft drop <draft_id>` | | Delete a draft |
| `spy drafts` | | List active drafts |

### Workspaces

| Command | What it does |
| --- | --- |
| `spy workspaces` | List every signed-in workspace |
| `spy workspaces use <id>` | Set the default workspace |
| `spy workspaces refresh` | Re-extract tokens from the Slack app |

### Channel references

Anywhere a command takes a `<channel>` it accepts: a channel name (`general`, `#general`), a user handle (`@anjali`, `anjali`), a user ID (`U01…`), or a channel/DM ID directly (`C01…`, `D01…`).

### Read flags

`spy read` supports `--ts` (show raw Slack timestamps — useful to copy for `thread`/`react`), `--threads` (auto-expand all threads inline), and `--from`/`--to YYYY-MM-DD` (strict — invalid dates fail loudly instead of being silently coerced).

## Workspaces

If you're signed in to multiple workspaces, `spy` will not pick one for you. You must either:

- Pass `--workspace <domain|team_id>` (or `-w`) on each invocation, or
- Set `SPY_WORKSPACE` in your environment, or
- Run `spy workspaces use <id>` to persist a default.

Identifiers match exactly against either the URL subdomain (`acme` from `acme.slack.com`) or the team ID (`T024D6SRW80`). Fuzzy matching is intentionally absent — auth ambiguity should never be silent.

```bash
spy -w acme ch                       # one-off
SPY_WORKSPACE=acme spy ch            # per-shell default
spy workspaces use acme              # persistent default
```

Each workspace has its own cache directory, so caches don't cross-contaminate when you switch.

## JSON output

Every command accepts `--json` for scripting:

```bash
spy ch --json | jq '.channels[] | select(.is_private) | .name'
spy r general 100 --json --from 2026-05-20 | jq '.messages | length'
```

## How it works

Slack for Mac stores its session as two pieces:

- A per-workspace `xoxc-…` token in a LevelDB store under `~/Library/.../Slack/Local Storage/leveldb/`.
- An account-level `xoxd-…` session cookie in a SQLite Cookies file, encrypted with AES-128-CBC using a passphrase from your macOS Keychain (entry: "Slack Safe Storage").

`spy` copies those files to `/tmp` (Slack holds exclusive locks on the originals while running), decrypts the cookie in pure Go, scans LevelDB for token candidates, and validates each one via Slack's `auth.test` to learn the team identity. Tokens are cached at `~/.local/spy/workspaces/<team_id>/workspace.json` (mode `0600`) and reused on subsequent runs. If a token goes stale, `spy` re-extracts on the next `invalid_auth`.

The only shellout is `security find-generic-password` to read the Keychain entry — everything else is pure Go.

## Cache

Resolved user and channel lists are cached per-workspace for 5 minutes at `~/.local/spy/workspaces/<team_id>/{users,channels}.json`. Pass `--refresh` to bypass them. Cold lookup against a workspace with ~4000 users takes ~500ms; warm lookups are ~80ms.

## Privacy / scope

`spy` reads files that already exist on your machine and never sends them anywhere except to Slack's own API, using credentials you already authorized when you signed in to the Slack app. There is no telemetry and no third-party service in the loop. The on-disk cache contains your tokens — treat `~/.local/spy/` like you would any other credential store.

This project is not affiliated with or endorsed by Slack Technologies.
