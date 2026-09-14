# Project status

## Branches

- `main` — 1.3.0, released. 1.4.0 built and deployed 2026-09-14 from `testing`; merge pending.
- `testing` — 1.4.0: `person`, `list`, `sync export`. Deployed locally for a soak from 2026-09-11; merges to main after.

## Known issues

### `sync export` breaks Trakt's rate limit

`sync export` fires roughly 170 requests in a row (every kind, every page, history at 250 rows a page). On a real account that trips Trakt's limiter: the export itself completes, but the next unrelated command gets `TRAKT_RATE_LIMITED` (HTTP 429) with a `Retry-After` of about five minutes. Observed 2026-09-11 on the 1.4.0-testing build.

Not a blocker for 1.4.0 (ruled 2026-09-14). Options, not yet decided:

- Read Trakt's `X-Ratelimit` headers and pace requests to stay under the window.
- Honour `Retry-After` inside the export loop: sleep and resume rather than fail the kind.
- Fetch kinds concurrently within the limit, so the wall time drops while the request count stays the same.

Whichever lands, the skill's export paragraph needs to say what to expect after an export.

## 1.4.0-testing soak checklist

Deployed 2026-09-11. Run these through `/traktctl` over the week, not by hand, so the skill is tested with the binary. Rollback at any point: `cp ~/.local/bin/traktctl.1.3.0.bak ~/.local/bin/traktctl` and `cp ~/.claude/skills/traktctl/SKILL.md.1.3.0.bak ~/.claude/skills/traktctl/SKILL.md`.

1. `traktctl --version` reports 1.4.0-testing.
2. Ask "who is in Breaking Bad". Expect a cast table with characters, no ID shown. << 👍
3. Ask "what else has Bryan Cranston been in". Expect a person search, then a filmography table with a Character/Job column.<< 👍
4. Ask "what's trending on Trakt lists". Expect list names with item counts. << 👍
5. Pick one of those lists and ask for its items. Expect the skill to chain the list id without asking you for it. << 👍
6. Ask for a Trakt backup to a folder. Expect 17 JSON files, a file list back, no raw data printed. << 👍
7. Immediately after step 6, run any other Trakt query. Expect a rate-limit message with a retry time, not a bare failure. This is the known issue; note how the skill words it. << no time out error. Operation continued uninterrupted.
8. Ask for your stats. Expect the skill to not offer a genre breakdown, or to warn about cost if it does. << worked perfectly. 👍
9. One mutation you'd do anyway (add to watchlist). Expect the 1.3.0 contract to behave as before. << 👍
10. Anything that surprised you goes in `~/.claude/skills/traktctl/LESSONS.md` via the skill's own reflection rule.

Pass = all ten behave as expected by 2026-09-18. Then merge `testing` to main, release 1.4.0. The rate-limit issue ships as known.
