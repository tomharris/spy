# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`spy` is a macOS + Linux Slack CLI written in Go. It auto-authenticates by lifting the signed-in user's token and cookie out of the local Slack desktop app — there is no OAuth flow, no app install, no token entry. It is a rewrite of a Node.js predecessor; the rewrite exists specifically to eliminate that tool's Python/openssl/sqlite3/curl shellouts (the only remaining shellout is macOS's `security` for the Keychain; Linux reads the Secret Service over pure-Go D-Bus), replace manual arg parsing with cobra, add `--json` output, and prepare for an MCP server mode.

## Build / run

```bash
go build -o bin/spy ./cmd/spy        # build
go run ./cmd/spy <subcommand>        # run without installing
go vet ./...                         # lint
go test ./...                        # tests (none yet)
```

There is no Makefile. On macOS, the first invocation that touches the Keychain triggers an "Always Allow" prompt — that is the user's job to click, not a bug. On Linux, if the login keyring is locked the Secret Service may pop a one-time unlock dialog (only for `v11` cookies; `v10`/"peanuts" needs none).

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

- Slack stores its session as two pieces: a per-workspace `xoxc-...` token (in LevelDB) and an account-level `xoxd-...` cookie (in the Cookies SQLite DB, AES-128-CBC encrypted). One cookie unlocks every workspace.
- `internal/auth/cookies.go` does the **platform-neutral** cookie decrypt: read `encrypted_value` from SQLite → read its 3-byte `v10`/`v11` prefix → ask the platform for `(password, iterations)` → `aesDecrypt` runs PBKDF2-SHA1 (salt `"saltysalt"`, 16 bytes) → AES-128-CBC with a 16-space IV → PKCS7 unpad → strip to `xoxd-`. The salt/IV/padding are what Chromium uses and are not negotiable.
- `internal/auth/leveldb.go` reads the LevelDB store via `github.com/syndtr/goleveldb`. **Both the Cookies DB and the LevelDB dir must be copied to /tmp before opening** — Slack holds exclusive locks while running. An earlier approach failed when Slack compacted the LevelDB with Snappy; goleveldb handles Snappy natively, which is why we use it instead of grepping raw `.ldb` files. This file is already cross-platform; only the directory path differs by OS.

#### Platform split (Go build tags)

The only OS-specific concerns are **where the Slack dir lives** and **how the cookie key is obtained**. Each is a per-platform file selected by `//go:build`:

| Concern | Shared | `//go:build darwin` | `//go:build linux` |
|---|---|---|---|
| Slack dir probe | `source.go` (`DefaultSource`) | `source_darwin.go` (`discoverSlackDir`) | `source_linux.go` (`discoverSlackDir`) |
| Cookie key | `cookies.go` (`DecryptCookie`, `aesDecrypt`) | `keychain_darwin.go` (`cookieKey`) | `keyring_linux.go` (`cookieKey`) |

- The shared `DecryptCookie` calls `cookieKey(src, prefix) ([]byte, int, error)`, implemented once per platform. **This is the seam** — add new platform support by adding another `source_<os>.go` + `<os>` key file, nothing else.
- **macOS**: `cookieKey` ignores the prefix and returns `(KeychainKey, 1003)`. `KeychainKey(isAppStore)` does the `security` shellout, trying App-Store vs direct-download account names in order. App Store and direct-download installs also have different *paths* — `discoverSlackDir` probes both and sets `IsAppStore`.
- **Linux**: the prefix decides the key. `v10` → hardcoded `"peanuts"`, `1` iteration; `v11` → key from the freedesktop Secret Service (GNOME Keyring/KWallet) read over pure-Go D-Bus (`github.com/godbus/dbus/v5`, Linux-only at link time), still `1` iteration. **Iteration count is the load-bearing difference: 1003 on macOS, 1 on Linux.** `discoverSlackDir` probes native (`~/.config/Slack`), Snap, and Flatpak paths.
- The Secret Service lookup matches the keyring item by attribute (`application`), falling back to the `"Slack Safe Storage"` label; a locked keyring is unlocked via the D-Bus `Prompt` flow (`keyring_linux.go`).

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
- **`internal/slack` must not import `cmd/`**. The `runX` functions exist so `cmd/spy/mcp.go` can call into the same logic without going through cobra.

## MCP server (`spy mcp`)

`cmd/spy/mcp.go` is a thin adapter that wraps every `runX` function as an MCP tool over stdio, using `github.com/modelcontextprotocol/go-sdk`. One workspace per server: it calls `newClientResolver()` once at startup and reuses the bound client/resolver for every tool call.

- Tool names are flat (`auth`, `channels`, `read`, `send`, `draft_channel`, `draft_drop`, …) — no slash-separated namespaces.
- Argument structs use `jsonschema:"description"` struct tags; the SDK auto-generates input schemas. Fields tagged `,omitempty` are optional, everything else is required.
- Output structs reuse the same `runX` result types, which gives clients a real `outputSchema` to validate against. Exception: `read` and `thread` use `Out=any` because `message.Replies []message` is self-referential and trips the jsonschema-go cycle detector. The JSON `content` is still returned; just no output schema.
- Don't call `newClient()` from inside a tool handler — the resolver is captured at startup, so `--workspace`/`SPY_WORKSPACE` is fixed for the process lifetime. Launch separate processes for multiple workspaces.

## Status

All commands ported, plus `spy mcp`.

Gotchas worth remembering:
- `drafts.delete` uses optimistic concurrency on `client_last_updated_ts`. Echoing back the value from `drafts.list` reliably hits `draft_has_conflict` because the user's running Slack desktop keeps bumping the server ts. Send `time.Now()` as a float instead — see `runDraftDrop`.
- `all_notifications_prefs` from `users.prefs.get` comes back as either a JSON-encoded string or a nested object depending on workspace. `parseMaybeStringJSON` in `cmd/spy/activity.go` handles both.
- `search.messages` returns matches under `messages.matches` (nested envelope), not a top-level `messages` array — it does not share the `historyResponse` shape.
- The MCP `read`/`thread` tools have `Out=any` to dodge a schema-inference cycle on `message.Replies`. If you add another command whose result transitively references itself, do the same.
