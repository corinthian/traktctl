---
name: traktctl
description: >
  Trakt.tv control via the traktctl CLI. TRIGGER when: user wants to search
  movies/shows, look up a title's details, check or manage their watchlist /
  favorites / history / ratings, see recommendations, check the release
  calendar, view profile stats, or says "/traktctl". Parses natural-language
  intent into traktctl commands. Hides the JSON envelope and error codes —
  speaks plain English. Trakt is metadata + social + history, not playback.
argument-hint: "[phrase | command | query]"
allowed-tools:
  - "Bash(traktctl:*)"
  - "Bash(jq:*)"
---

# Traktctl Skill

Goal: human-friendly control over every traktctl surface. The user speaks intent; you run traktctl and report plainly. Never surface the raw `{ok,data,meta}` envelope, error codes, trakt IDs, exit codes, or HTTP status — unless `debug` is on.

Trakt is the **metadata/social/history** layer. It does NOT play anything. "Play this", "pause", "what's on my TV" → that belongs to whatever plays media, not this skill. Trakt answers "what is this", "what have I watched", "what's on my watchlist", "what's coming out", "what should I watch".

---

## Hard Rules

- **Stay inside `traktctl`.** Never run `curl`, python/requests, or any direct call to `api.trakt.tv`. If a task seems to need a raw API call, that's a traktctl gap — tell the user it's a missing feature worth reporting, and stop. Don't work around it.
- **Don't freeze flag names from memory — introspect.** This skill is written to the documented contract, not a running binary. For the *exact* flags, arg names, and output fields of any command, consult `traktctl <group> <verb> --llm` (JSON: usage, args, flags, examples, output_schema — a bare object, not the `{ok,data,meta}` envelope) or `traktctl commands` (the whole tree, JSON, `.data.subcommands[].name`, wrapped in the standard envelope). When a command errors on an unknown flag, check `--llm` rather than guessing again. The group/verb names below are stable; the fine-grain flags are authoritative only from `--llm`.
- **Don't invent limitations.** An `ok:true` empty result is not proof a feature is broken — a watchlist or search can genuinely be empty. Run the command, report plainly, let the user disambiguate. Treat a gap as real only if it's documented here or the user confirms it.
- **Confirm before destructive writes.** See [Confirmation Gates](#confirmation-gates). The CLI requires `--confirm` on these; the *decision* to proceed is yours to get from the user first.
- **Resolve titles, hide IDs.** Users say "Dune", not a trakt slug. Search to resolve, show a numbered list, act on the row they pick. Never make the user type or read a trakt ID. See [IDs & Row Numbers](#ids--row-numbers).

---

## Debug Mode

Triggered when `$ARGUMENTS` contains a leading `debug` token (`debug search Dune`) or `--debug` anywhere. Strip the trigger before parsing intent.

In debug mode — "maximum verbose exchanges":
1. Echo the exact `traktctl` command(s) in a fenced code block **before** the output.
2. Show the raw JSON envelope (`ok`, `data`, `meta`) verbatim — pretty-print with `jq` if long.
3. Show the raw `error` object (`code`, `message`, `http_status`, `hint`) and the exit code instead of translating.
4. Restore trakt IDs / slugs as a column on any table.

Default mode: none of the above. Plain English only, row numbers instead of IDs, translated errors.

---

## Output Strategy

Default output of every traktctl command is the JSON envelope. **You** format it — don't pass `--terse` (its one-line summary is thinner than the tables below; you read the JSON and render richer output yourself). Parse `data` with `jq`.

- **Lists** (search results, watchlist, history, calendar, recommendations) → the title table below.
- **Single title** (a `get`) → a short labelled block: title, year, type, runtime, rating, one-line overview.
- **Mutations** (add/remove/rate/follow) → a one-line confirmation: `Added Dune (2021) to your watchlist.`
- **Counts/stats** (`user stats`) → a compact summary, not the raw object.

### Title Table

Every list of titles uses one shape. Add a leading `#` column; keep the `#`→ID map in conversation context so follow-ups work ("add #2 to my watchlist", "tell me about #3").

| # | Title | Type | Year | <Context> |
|---|---|---|---|---|
| 1 | Dune | movie | 2021 | … |
| 2 | Breaking Bad | show | 2008 | … |

- **Title** — name only. For an episode row, Title is the show; put the locator `S0xE0y — Episode Title` in the Type column.
- **Type** — `movie` / `show` / `episode` / `person` / `list`.
- **Context** — the rightmost column, label swaps per command: `Overview` (search/get), `Added` (watchlist), `Watched` (history), `Rating` (ratings), `Airs` (calendar), `Score` (recommendations).
- Debug: append a trakt ID/slug column on the right.
- Empty result → plain line, e.g. `Your watchlist is empty.` / `Nothing found for "<q>".`

---

## IDs & Row Numbers

Trakt identifies titles by `ids: {trakt, slug, imdb, tmdb, tvdb}`. traktctl takes `--id-type {trakt|slug|imdb|tmdb|tvdb}` + `--id VALUE` (defaults to `trakt`, no sniffing).

Workflow for "do X to <title>":
1. `traktctl search query --type <movie|show> --q "<title>"` → resolve.
2. Show the numbered title table; if one obvious match, you may proceed and name it.
3. For the chosen row, use its slug (cleanest): `--id-type slug --id <slug>`.
4. Report by name, never by ID.

If the user supplies an explicit external ID ("the imdb is tt0468569"), pass `--id-type imdb --id tt0468569` directly — no search needed.

---

## Confirmation Gates

These verbs change real account state and the CLI requires `--confirm` (or `TRAKTCTL_CONFIRM=1`). Before running, state plainly what will change and get a go-ahead. Then run with `--confirm`.

| Needs confirmation | Why |
|---|---|
| any `sync ... remove` (collection/history/ratings/watchlist/favorites/playback) | deletes account data |
| `sync watchlist`/`favorites` `settings` / `reorder` / `update-item` | mutates account/list state |
| `auth revoke` | revokes app access |
| `user hidden add` / `user hidden remove` | changes what's hidden from recommendations/progress |
| `user follow`, `user block`, `user requests-respond`, `user report` | changes the social graph or reports content |
| any `user list-*` mutation (`list-create`, `list-update`, `list-delete`, `list-items-add`, `list-items-remove`, `list-items-reorder`, `list-item-update`, `lists-reorder`, `list-like`, `list-unlike`, `list-report`) | creates, edits, or destroys a list or its items |

**Ungated (idempotent adds)** — `sync collection/history/ratings/watchlist/favorites add` (duplicates merge at Trakt's layer) and `recommend hide-movie`/`hide-show` (idempotent DELETE) run with no confirmation. Reads never need confirmation.

---

## Error Handling

traktctl returns a structured error: `{ok:false, error:{code, message, http_status?, hint?}}` plus an exit code. `hint` is optional — it's populated for auth failures (`"Run: traktctl auth login"`) and some HTTP error bodies, but absent on most errors (including plain user mistakes). Don't expect one by default. **Do not show the raw object in default mode.**

Translation:
1. If the `code` is in the table below, show that line — except `BAD_CONFIG` (see the note below the table; it's a catch-all, not a single canned message).
2. Otherwise, if `hint` is present, relay it in plain language — e.g. `"Run: traktctl auth login"` → `You're not signed in to Trakt — want me to start login?`. If there's no hint, say `Something went wrong with Trakt. Run with \`debug\` for details.`

| Error code | Show user |
|---|---|
| `AUTH_REQUIRED` | `You're not signed in to Trakt. Want me to log you in?` |
| `AUTH_EXPIRED` | `Your Trakt session expired and auto-refresh failed. Want me to log you in again?` |
| `BAD_CONFIG` | *route by message — see below* |
| `TRAKT_NOT_FOUND` | `Trakt doesn't have that one.` |
| `TRAKT_VALIDATION` | `Trakt rejected that request — something about it wasn't valid.` |
| `TRAKT_RATE_LIMITED` | `Trakt's rate-limiting me — give it a moment and try again.` |
| `TRAKT_VIP_ONLY` | `That feature needs a Trakt VIP account.` |
| `TRAKT_LOCKED_USER` | `That account is locked.` |
| `TRAKT_DEACTIVATED` | `That account is deactivated.` |
| `TRANSPORT_TIMEOUT` | `Can't reach Trakt right now — connection timed out.` |
| `PARSE_ERROR` | `Trakt sent back something I couldn't read.` |
| `PAGINATION_RUNAWAY` | `That's a huge pull (100+ pages). Want me to fetch all of it anyway?` (then add `--really-all`) |

**`BAD_CONFIG` is a catch-all user-error code, not just "not set up."** It fires for at least three distinct cases — route by the `message` text, not one canned line:
- Message starts `"missing required ..."` → a required flag is missing. Ask the user for that specific value; don't imply Trakt isn't configured.
- Message is `"destructive: pass --confirm or set TRAKTCTL_CONFIRM=1"` → this is the confirmation gate firing (see [Confirmation Gates](#confirmation-gates)), not a real failure. Run the confirm flow, don't relay it as an error.
- Message actually mentions config/credentials/client-id → offer `Trakt isn't set up yet — want to run setup?`

Exit-code fallback when no code is parseable: 1 → bad request on my end, 2 → Trakt refused it, 3 → connection problem, 4 → traktctl bug, 5 → not signed in.

**Auto-refresh is silent.** On a 401 the CLI refreshes once and retries; the data comes back normally with only a stderr note. Don't mention it unless debug.

---

## Command Surface (v1 — 11 groups)

Group/verb names are stable; pull exact flags from `traktctl <group> <verb> --llm`. Pattern: `traktctl <group> <verb> [--flags]`.

### auth — sign-in lifecycle
`login` (device flow; prints a code + URL — relay them, the user approves in a browser), `status` (signed in? expiry — report plainly), `refresh`, `logout`, `revoke` (gated). "Log me in" → `auth login`; relay the user code and URL it prints.

### config — local setup
`init` writes `config.toml` (needs `--client-id`, optional `--client-secret`; `--login` chains into sign-in), `path` shows where config + tokens live. Use for first-time setup. Never echo the secret back.

### search — find a title
`query --type <movie|show|episode|person|list> --q "<text>"` for text search; `id --id-type <trakt|imdb|tmdb|tvdb> --id <value>` for external-ID lookup. This is your title-resolution workhorse. Render the title table.

### movie / show — title detail + discovery
Same shape (show adds episode-aware verbs). Discovery lists: `trending`, `popular`, `anticipated`, `favorited`, `played`, `watched`, `collected`, `streaming`, `updates` (movie also `boxoffice`). Single title: `get`, `aliases`, `translations`, `people` (cast/crew), `ratings`, `related`, `stats`, `studios`, `videos`, `comments`, `lists`, `sentiments`, `watching` (movie `releases`; show `certifications`). Show-only: `progress` (how far through a show), `next-episode` (what's next to watch — empty if none), `last-episode`. "What's <show> about" → `show get`. "What's next for <show>" → `show next-episode`.

### season / episode — within a show
`season summary|info|episodes|people|ratings|stats|comments|videos|...` (takes `--show --season`). `episode summary|comments|ratings|...` (takes `--show --season --episode`). "List season 1 of <show>" → `season episodes --show <slug> --season 1`.

### calendar — upcoming releases
`all-*` is public, `my-*` is the user's personal calendar (needs sign-in): `shows`, `new-shows`, `premieres`, `finales`, `movies`, `dvd`, `streaming` — each in `all-`/`my-` form. Take `--start DATE --days N`. "What's coming out this week" → `calendar my-shows --start <today> --days 7`. Render the title table with an `Airs` column.

### recommend — personalized picks
`movies`, `shows` (optional `--ignore-collected`/`--ignore-watchlisted`); `hide-movie`/`hide-show` to suppress a suggestion (ungated — idempotent). "What should I watch" → `recommend movies` or `recommend shows`.

**Recommendations always mean UNSEEN.** Any recommendation request — including curated/genre/era lists you assemble yourself (e.g. "recommend a noir from the 60s") — must exclude titles the user has already seen. The `recommend` endpoint does this for you. When you build a list from `search`/discovery/your own curation, cross-check it yourself and drop/flag the seen ones — don't hand back a list the user has mostly seen and ask afterward.

**"Seen" = watched OR rated OR collected — union all three.** The watched history is incomplete: a title can be rated (proof it was seen) without a watched entry, and vice-versa. A watched-only check gives false "unseen" results. To test seen-status, pull `sync watched get`, `sync ratings get`, and (optionally) `sync collection get` for the type, union them, and match by title+year. A title in ANY of those three is seen.

### sync — personal data (reads + mutations)
`activities` (cheap "has anything changed" poll). For `collection|history|ratings|watchlist|favorites`: `get` reads; `add` (idempotent, ungated) and `remove` (gated) mutate. Watchlist/favorites also have `settings`/`reorder`/`update-item` (all gated). `watched` is **read-only** (`get` only — it's a derived view; mutate seen-status via `sync history`). `playback` has `get` and `remove` (gated) only — there is no `playback add`. "Add Dune to my watchlist" → resolve, then `sync watchlist add` (no confirm needed). "Remove X from my history" → confirm first, then `sync history remove --confirm`.

### user — profile + social
~44 verbs. Reads: `profile`, `settings`, `stats`, `watchlist`, `watched`, `history`, `ratings`, `collection`, `favorites`, `comments`, `notes`, `likes`, `lists`, `watching`, `hidden`, `saved-filters`. List management (all gated — see [Confirmation Gates](#confirmation-gates)): `list`, `list-items`, `list-create`, `list-delete`, `list-update`, `list-items-add`, `list-items-remove`, `list-items-reorder`, `list-item-update`, `lists-reorder`, `list-like`/`list-unlike`, `list-comments`, `list-report`. Social (mutations gated): `follow`, `followers`, `following`, `friends`, `block`, `blocked`, `collaborations`, `requests-*`, `report`. `--user` defaults to the configured user, then `me`. "My stats" → `user stats`; summarize counts and totals (plays, watched, minutes, collected, ratings distribution) — don't dump the object. `user stats` returns **no genre breakdown**, and there's no v1 command for "favorite genres" (it would mean joining per-title genres across full history) — say it's not supported rather than attempting a tally.

---

## Not Built Yet (v2)

If the user asks for one of these, say it's not in traktctl yet (don't construct a command that will fail): `checkin`, `cert`, `comment`, `list` (curated/public lists — distinct from `user lists`), `media`, `note`, `person`, `scrobble`, the `country`/`genre`/`language`/`network` lookups, and `bulk export`. E.g. "Trakt has the data but traktctl doesn't expose people/cast lookup yet — it's a planned v2 group."

---

## Relationship to Playback

Trakt does metadata, social, history, recommendations. It never plays media. Cross-service requests — availability, queue, play — belong to whatever plays media on your setup, not here. If a request mixes both ("recommend something and play it"), do the Trakt half (recommend), then hand the playback half off.

---

## Self-Improvement

traktctl grows by capturing real session corrections to `~/.claude/skills/traktctl/LESSONS.md`. The file is local to the skill, NOT in the global memory dir. It is read at every invocation and referenced before novel decisions.

### Startup Recall
On every `/traktctl` invocation, before parsing intent:
1. Read `~/.claude/skills/traktctl/LESSONS.md` if it exists. If absent, skip silently.
2. For lessons with `seen: 3` or higher, treat them as **binding constraints** that override default behavior.
3. For lessons with `seen: 1` or `2`, treat as **soft guidance**.
4. Do not announce the recall.

### Reflection Triggers
After any of these events, append a lesson:

| Trigger | When to write |
|---|---|
| `resolution` | `search`/resolve picks the wrong title and the user corrects it |
| `new-error` | traktctl returns an `error.code` not in the [Error Handling](#error-handling) table |
| `output` | the user corrects output format (wrong column, too verbose, missing field, etc.) |
| `drift` | documented behavior here disagrees with the live binary — a renamed/missing flag, a wrong output field, `--llm` disagreeing with this file. This is traktctl's most common failure mode: it's authored to the contract, not the binary. |
| `correction` | general behavioral correction not covered above |

Do NOT write lessons for:
- Transient network / `TRAKT_RATE_LIMITED` / timeout errors (unless the same one recurs within a session — that's a pattern, use `new-error`)
- A one-off `ok:true` empty result (a watchlist or search can genuinely be empty)
- A title genuinely absent from Trakt
- Anything already documented in this file

### Lesson Format
Append to LESSONS.md:

```
---
trigger: <resolution|new-error|output|drift|correction>
date: YYYY-MM-DD
seen: 1
---
**Context:** <intent + command run>
**Mistake:** <what traktctl did or returned, or what this file got wrong>
**Correction:** <what's actually true, or what the user said to do>
**Apply when:** <future condition that should trigger this rule>
```

### Deduplication
Before appending, scan existing LESSONS.md. If a lesson with the same `trigger` and substantively the same `Apply when` exists, increment its `seen` counter and update `date` instead of duplicating.

### Synthesis Threshold
After writing or incrementing, count lessons sharing the same `trigger`:
- **< 3 lessons** → no further action.
- **≥ 3 lessons** → surface a synthesis proposal before continuing: "3+ lessons under `<trigger>`. Suggested SKILL.md edit: `<concrete change>`. Approve?" Wait for explicit approval — do NOT auto-edit SKILL.md. On approval: edit SKILL.md, mark the synthesized lessons `synthesized: true`, and collapse them to one index row (date, trigger, gist, destination).

No incident log for v1 — traktctl is stateless HTTP with no client-wedge telemetry worth tracking separately. If transport flakiness or rate-limiting becomes a recurring pattern, that's a `new-error` lesson, not a separate log.
