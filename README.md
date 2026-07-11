# traktctl

JSON-first CLI wrapper over the [Trakt API](https://trakt.tv). A dumb transport layer: CLI args in, Trakt HTTP out, a stable `{ok,data,meta}` envelope back.

## Install

Prebuilt universal macOS binary (arm64 + x86_64) from the [latest release](https://github.com/corinthian/traktctl/releases/latest). Download with `gh`, or grab the tarball straight from the releases page:

```
gh release download v1.0.1 -R corinthian/traktctl \
  -p 'traktctl-1.0.1-universal-macos.tar.gz' -p 'traktctl-1.0.1-SHA256SUMS'
shasum -a 256 -c traktctl-1.0.1-SHA256SUMS
tar -xzf traktctl-1.0.1-universal-macos.tar.gz
xattr -d com.apple.quarantine traktctl 2>/dev/null || true   # ad-hoc signed, not notarized
sudo mv traktctl /usr/local/bin/
traktctl --version
```

Per-arch tarballs (`-arm64-`, `-amd64-`) are attached to the same release.

Or, with Go 1.26+, install from source directly:

```
go install github.com/corinthian/traktctl/cmd/traktctl@latest
```

The binary lands in `$(go env GOPATH)/bin` — make sure that's on your `PATH`. To build locally instead, see below.

## Quick start

From zero to authenticated. You need a Trakt application's `client_id`/`client_secret` from [trakt.tv/oauth/applications](https://trakt.tv/oauth/applications).

```
# 1. Write config + log in, in one shot:
traktctl config init --client-id ID --client-secret SECRET --default-user you --login
#    Prints a device code + URL to stderr — open the URL, enter the code, approve.

# 2. Confirm and make your first authenticated call:
traktctl auth status
traktctl user watchlist
```

Already have a `config.toml`? Just `traktctl auth login`. Tokens land in the macOS Keychain; the client refreshes them automatically on a 401. See [Authentication](#authentication) and [Bootstrap from scratch](#bootstrap-from-scratch) for the full lifecycle.

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
default_user  = "you"
base_url      = "https://api.trakt.tv"
```

## Bootstrap from scratch

A fresh machine needs credentials then tokens:

```
traktctl config init --client-id ID --client-secret SECRET --default-user you
traktctl auth login
# or one shot:
traktctl config init --client-id ID --client-secret SECRET --login
```

`config init` writes `~/.config/traktctl/config.toml` (`0600`); `--config PATH` targets elsewhere, `--force` overwrites. Omit `--client-secret` to keep it in `TRAKT_CLIENT_SECRET` instead of the file. `traktctl config path` shows where config and tokens are read from.

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

## Command surface

Every command is `traktctl <group> <verb> [--flags]`. Run `traktctl <group> --help` for a group's verbs, `traktctl <group> <verb> --llm` for machine-readable help, or `traktctl commands` for the whole tree as JSON. v1 covers 11 groups:

- **auth** — authentication lifecycle: `login`, `logout`, `refresh`, `revoke`, `status`.
- **config** — local bootstrap: `init` (write `config.toml`), `path` (show config + token-store locations).
- **search** — `query` (text search; `--q`, `--type`) and `id` (external-id lookup; accepts `trakt|imdb|tmdb|tvdb`, excludes `slug`).
- **movie** / **show** — the title groups, same shape (show adds episode-aware verbs).
  - Discovery lists: `trending`, `popular`, `anticipated`, `favorited`, `played`, `watched`, `collected`, `streaming`, `updates`, `updated-ids` (movie also `boxoffice`).
  - Single title: `get`, `aliases`, `translations`, `people`, `ratings`, `related`, `stats`, `studios`, `videos`, `comments`, `lists`, `sentiments`, `watching` (movie `releases`; show `certifications`).
  - Show-only: `progress` (watched/collected), `next-episode` (204 if none), `last-episode`.
- **season** — `summary`, `info`, `episodes`, `people`, `ratings`, `stats`, `translations`, `comments`, `lists`, `videos`, `watching`, `report`.
- **episode** — `summary`, `comments`, `lists`, `people`, `ratings`, `stats`, `translations`, `videos`, `watching`.
- **calendar** — upcoming releases; `all-*` are public, `my-*` are your personal calendar: `shows`, `new-shows`, `premieres`, `finales`, `movies`, `dvd`, `streaming` (each in `all-`/`my-` form). Take `--start DATE --days N`.
- **recommend** — personalized: `movies`, `shows`, plus `hide-movie`/`hide-show` to suppress a title.
- **sync** — personal data reads + mutations: `activities` (cheap change-poll), `collection`, `watched`, `history`, `ratings`, `watchlist`, `favorites`, `playback`. Reads support `get`; mutating verbs (`add`/`remove`, and `settings`/`reorder`/`update-item` on watchlist/favorites) gate behind `--confirm`.
- **user** — profile and personal data (44 verbs):
  - reads: `profile`, `settings`, `stats`, `watchlist`, `watched`, `history`, `ratings`, `collection`, `favorites`, `comments`, `notes`, `likes`, `lists`, `watching`, `hidden`, `saved-filters`.
  - list management: `list`, `list-items`, `list-create`, `list-delete`, `list-update`, `list-items-add`, `list-items-remove`, `list-items-reorder`, `list-item-update`, `lists-reorder`, `list-like`/`list-unlike`, `list-comments`, `list-report`.
  - social: `follow`, `followers`, `following`, `friends`, `block`, `blocked`, `collaborations`, `requests-follower`, `requests-following`, `requests-respond`, `report`.

Deferred to v2 (not in the binary yet): `checkin`, `cert`, `comment`, `list`, `media`, `note`, `person`, `scrobble`, the country/genre/language/network lookups, and `bulk export`.

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
traktctl completion <shell># shell completion script (bash/zsh/fish/powershell)
```

## Testing

```
go test ./...                  # unit tests (client, output, commands, config)
go test -tags=live ./test/...  # live smoke suite (needs config.toml + a Keychain token; skips if absent)
go test -tags=live,refresh ./test/...  # adds the token-rotating refresh test
./test/live_probes.sh          # account-safe existence probes for mutating routes
```

## Secrets

`config.toml` holds the client secret; OAuth tokens live in the macOS Keychain (an on-disk `tokens.json` is only a dev fallback and is not present by default). Both `config.toml` and `tokens.json` are kept out of git via `.gitignore` — never commit them. For distribution, tokens belong in the keychain and the client secret in the environment.

## Skill

`.claude/skills/traktctl/` is a [Claude Code](https://claude.com/claude-code) skill that speaks natural language over this CLI — search, watchlist, ratings, recommendations, calendar, stats — without you needing to know the flags. Install it by copying the directory to `~/.claude/skills/traktctl/`. `LESSONS.md` starts empty; it's a local, per-install log that accumulates corrections as you use the skill, and it never needs to be shared or committed back here.
