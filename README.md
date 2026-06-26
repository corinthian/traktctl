# traktctl

JSON-first CLI wrapper over the [Trakt API](https://trakt.tv). A dumb transport layer: CLI args in, Trakt HTTP out, a stable `{ok,data,meta}` envelope back. Component of the Subtrakt orchestration system (metadata/intent layer).

## Build

```
./build.sh                 # universal macOS binary -> dist/traktctl
go build -o dist/traktctl ./cmd/traktctl   # single-arch dev build
```

Requires Go 1.26+. No runtime dependencies.

## Configuration

Resolution order (highest first): CLI flags > environment > `config.toml` > keychain.

- `config.toml` is searched in `--config`, `$TRAKTCTL_CONFIG`, `./config.toml` (dev), then `~/.config/traktctl/config.toml`.
- Tokens live in the macOS Keychain (service `traktctl`). File fallback: `./tokens.json` (dev) or `~/.config/traktctl/tokens.json`.
- Env: `TRAKT_CLIENT_ID`, `TRAKT_CLIENT_SECRET`, `TRAKT_ACCESS_TOKEN`, `TRAKT_REFRESH_TOKEN`, `TRAKT_BASE_URL`.

Example `config.toml`:

```toml
client_id     = "..."
client_secret = "..."
default_user  = "corinthian"
base_url      = "https://api.trakt.tv"
```

## Authentication

```
traktctl auth login        # OAuth device flow; prints a code + URL to stderr
traktctl auth status       # token state, expiry, storage location
traktctl auth refresh      # force a refresh
traktctl auth logout        # clear local storage
traktctl auth revoke --confirm   # revoke at Trakt, then clear
```

On a 401 the client refreshes the token once and retries transparently (a one-line note goes to stderr).

## Usage

```
traktctl movie get --id tron-legacy-2010
traktctl search query --type movie --q "the matrix" --limit 5
traktctl show next-episode --id breaking-bad
traktctl season episodes --show breaking-bad --season 1
traktctl user watchlist --type movies
traktctl sync activities
traktctl calendar all-movies --start 2026-06-26 --days 7
```

Global flags: `--extended`, `--page/--limit/--all` (`--really-all` to bypass the 100-page cap), `--filter k=v` (repeatable), `--id-type/--id`, `--raw/--ndjson/--terse`, `--confirm`, `--llm`.

Destructive verbs (any `remove`, `auth revoke`, sync `settings`/`reorder`/`update-item`, `playback remove`) require `--confirm` or `TRAKTCTL_CONFIRM=1`. Adds are idempotent and need no confirmation.

## Output

Default is the JSON envelope:

```json
{ "ok": true, "data": { ... }, "meta": { "endpoint": "...", "duration_ms": 187, "pagination": { ... } } }
```

Errors use `{ "ok": false, "error": { "code", "message", "http_status", "hint" } }`. Exit codes: 0 ok, 1 user error, 2 Trakt non-2xx, 3 transport, 4 internal, 5 auth missing.

`--raw` passes Trakt's body through, `--ndjson` emits one object per line, `--terse` emits a one-line summary.

## Discovery

```
traktctl commands          # full command tree as JSON
traktctl <cmd> --llm       # machine-readable help: usage, flags, examples, schema
traktctl <cmd> --help      # human help
```

## Testing

```
go test ./...                  # unit tests (client, output)
go test -tags=live ./test/...  # live smoke suite (needs repo config.toml + tokens.json)
./test/live_probes.sh          # account-safe existence probes for mutating routes
```

## Secrets

`config.toml` and `tokens.json` hold the client secret and live tokens. They are kept out of git via `.git/info/exclude` — never commit them. For distribution, tokens belong in the keychain and the client secret in the environment.

## Scope

v1 groups: `auth`, `search`, `movie`, `show`, `season`, `episode`, `calendar`, `recommend`, `sync`, `user`. Deferred to v2: bulk export, and the `checkin`/`cert`/`comment`/`list`/`media`/`note`/`person`/`scrobble`/lookup groups. See the build plan and spec in the Obsidian vault for the full surface.
