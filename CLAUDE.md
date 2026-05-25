# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`spy` is a macOS-only Slack CLI written in Go. It auto-authenticates by lifting the signed-in user's token and cookie out of the local Slack desktop app — there is no OAuth flow, no app install, no token entry. It is a rewrite of a Node.js predecessor; the rewrite exists specifically to eliminate that tool's Python/openssl/sqlite3/curl shellouts (only `security` for Keychain remains), replace manual arg parsing with cobra, add `--json` output, and prepare for an MCP server mode.

## Build / run

```bash
go build -o bin/spy ./cmd/spy        # build
go run ./cmd/spy <subcommand>        # run without installing
go vet ./...                         # lint
go test ./...                        # tests (none yet)
```

There is no Makefile. The first invocation that touches the Keychain will trigger a macOS "Always Allow" prompt — that is the user's job to click, not a bug.

## Global flags (defined on `rootCmd`)

- `--json` — emit JSON instead of formatted text. **Every subcommand must honor this.**
- `--workspace, -w <domain|team_id>` — target a specific workspace. Also reads `SPY_WORKSPACE`.
- `--refresh` — bypass caches. Re-extracts workspace tokens AND invalidates users/channels caches when used with a resolver.

## Adding a subcommand — the pattern every command follows

Each new command is a file in `cmd/spy/` shaped like this:

1. Define a typed result struct (e.g. `type fooResult struct { ... }`) — this is what gets JSON-encoded.
2. Define `runFoo(ctx, deps...) (*fooResult, error)` as a **pure function** that does the work and returns the result. This is what the MCP server mode will reuse.
3. Define the cobra command. Its `RunE` calls `runFoo`, then picks the renderer: `if flagJSON { return emitJSON(res) }` else format text.
4. If the command needs user/channel lookups, build dependencies via `newClientResolver()` (returns `*slack.Client`, `*resolve.Resolver`). Otherwise `newClient()`. Both honor `--workspace`/`--refresh`/`SPY_WORKSPACE`.
5. Slack API verbs are registered in `internal/slack/methods.go` — **add the method there before calling it**, or `client.Call` will refuse it with a clear error. Slack's docs are wrong about GET vs POST for several `client.*` and `users.prefs.*` endpoints; trust the existing map, not the docs.
6. For paginated endpoints, use the generic `slack.Paginate[Page, Item](...)` — don't roll your own cursor loop.

`channels.go`, `users.go`, `dms.go`, and `read.go` are the canonical references for the pattern.

## Architecture (the parts you can't infer from file names)

### Layer cake

```
cmd/spy/*            ← cobra wrappers, rendering, flag parsing
internal/resolve     ← per-workspace name↔ID resolution + on-disk cache
internal/slack       ← Web API client, verb registry, generic pagination
internal/auth        ← credential extraction, workspace discovery, config
internal/cache       ← generic TTL'd JSON cache (used by resolve)
```

Higher layers depend on lower layers only. `internal/slack` imports `internal/auth` to read credentials; nothing imports `cmd/`.

### Credential extraction (the load-bearing magic)

The flow `auth.DefaultSource()` → `auth.SharedCookie()` + `auth.ListWorkspaces()` produces a `Workspace` (`team_id`, `team_domain`, `user_*`, `token`) plus a shared `cookie`.

- Slack stores its session as two pieces: a per-workspace `xoxc-...` token (in LevelDB) and an account-level `xoxd-...` cookie (in the Cookies SQLite DB, AES-128-CBC encrypted by a Keychain-stored passphrase). One cookie unlocks every workspace.
- `internal/auth/cookies.go` does the cookie decrypt: `security` shellout for the keychain key → PBKDF2-SHA1 (salt `"saltysalt"`, **1003 iterations**, 16 bytes) → AES-128-CBC with a 16-space IV → PKCS7 unpad → strip `v10` prefix. The constants are not negotiable; they are what Chromium uses.
- `internal/auth/leveldb.go` reads the LevelDB store via `github.com/syndtr/goleveldb`. **Both the Cookies DB and the LevelDB dir must be copied to /tmp before opening** — Slack holds exclusive locks while running. An earlier approach failed when Slack compacted the LevelDB with Snappy; goleveldb handles Snappy natively, which is why we use it instead of grepping raw `.ldb` files.
- App Store and direct-download installs have different paths and different Keychain account names. `Source` probes both; `KeychainKey(isAppStore)` tries account names in a specific order.

### Multi-workspace model

The user can be signed into N workspaces. Token is per-workspace, cookie is shared.

- `ListWorkspaces` extracts every `xoxc-` candidate from LevelDB, **groups them by the internal team prefix embedded in the token** (the `\d+` after `xoxc-`), keeps the longest candidate per group, and validates each via `auth.test`. Results are cached at `~/.local/spy/workspaces/<team_id>/workspace.json`.
- Workspace resolution priority: `--workspace` flag → `SPY_WORKSPACE` env → `config.json`'s `default_workspace` → if exactly one is signed in, use it → otherwise **error with the list of choices**. There is intentionally no implicit "first one wins" fallback for multiple workspaces.
- The `--workspace` identifier matches exact `team_id` (`T024…`) or `team_domain` (`shopifyalumnigroup`). Nothing fuzzy.
- On `invalid_auth` from any API call, `slack.Client.refreshCredentials` re-runs `ListWorkspaces(src, true)` and looks for the same `team_id`. This covers the case where the user signed out and back in — token rotated, identity preserved.

### On-disk layout

```
~/.local/spy/
├── config.json                       # {default_workspace: "T..."}
└── workspaces/
    └── <team_id>/
        ├── workspace.json            # identity + token
        ├── users.json                # 5-min TTL cache
        └── channels.json             # 5-min TTL cache
```

Each workspace owns a directory so its caches are isolated. The cache envelope is `{cached_at_ms, data}`; `cache.Load[T]` returns `(zero, false)` on any error or expiry — callers always have a clean "refetch" branch.

### Slack client

`slack.Client` is bound to one workspace. `Call(ctx, method, params, out)`:

1. Looks `method` up in `verbByMethod`; returns a clear error for unregistered methods.
2. Builds a GET (query string) or POST (JSON body) accordingly. **A few semantically-read endpoints require POST** (`client.counts`, `users.prefs.get`, `saved.list`) — this is real Slack behavior, not a workaround.
3. Honors HTTP 429 `Retry-After` (clamped to 120s so a hostile header can't pin us).
4. Re-auths on `invalid_auth` exactly once per call.

`out` must embed `slack.BaseResponse` so the client can read `ok`/`error`/`response_metadata.next_cursor`.

### Resolver

`resolve.Resolver` wraps a client and caches users + channels for one workspace (5-min TTL). `ResolveChannel(ref)` handles every form a user might type:

- `C…`/`D…`/`G…` → returned as-is
- `U…` → opens a DM, returns the resulting channel ID
- `@handle` or bare `handle` matching name/display_name/real_name → opens a DM
- channel name or `#name` → resolved via cached `conversations.list`

DM opens always go through `conversations.open` so we get the stable channel ID, not the user ID.

## Conventions worth knowing

- **Don't introduce shellouts.** `security` for the Keychain key is the only one. The whole point of this rewrite is pure Go.
- **Dates are strict `YYYY-MM-DD`.** `read.go`'s `parseDate` uses `time.Parse("2006-01-02", s)` and errors loudly. The JS predecessor silently produced `NaN` for malformed dates — don't reintroduce that.
- **Slack returns history newest-first.** `read.go` reverses before rendering so output reads top-to-bottom in time order. Apply the same reversal to any new "show me messages" command.
- **File permissions:** anything containing tokens is `0600`, parent dirs `0700`. The cache and config writers already do this — match them.
- **`internal/slack` must not import `cmd/`**. The `runX` functions exist precisely so the planned MCP server can call into the same logic without going through cobra.

## Status

All commands are ported: `auth`, `workspaces` (+ `use`, `refresh`), `channels` (`ch`), `users` (`u`), `dms` (`dm`), `read` (`r`), `send` (`s`), `search`, `thread` (`t`), `react`, `pins` (`pin`), `activity` (`a`), `unread` (`ur`), `starred` (`star`), `saved` (`sv`), `drafts` + `draft <ch> <msg>` / `draft thread` / `draft user` / `draft drop`.

Still planned: MCP server mode (`spy mcp`), which is the whole reason the cobra commands are split into `runX(ctx, deps...) (*xResult, error)` + cobra wrapper — the MCP server will reuse the `runX` functions directly without going through cobra.

Gotchas worth remembering:
- `drafts.delete` uses optimistic concurrency on `client_last_updated_ts`. Echoing back the value from `drafts.list` reliably hits `draft_has_conflict` because the user's running Slack desktop keeps bumping the server ts. Send `time.Now()` as a float instead — see `runDraftDrop`.
- `all_notifications_prefs` from `users.prefs.get` comes back as either a JSON-encoded string or a nested object depending on workspace. `parseMaybeStringJSON` in `cmd/spy/activity.go` handles both.
- `search.messages` returns matches under `messages.matches` (nested envelope), not a top-level `messages` array — it does not share the `historyResponse` shape.
