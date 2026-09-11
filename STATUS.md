# Project status

## Branches

- `main` — 1.3.0, released.
- `testing` — 1.4.0-testing: `person`, `list`, `sync export`. Deployed locally for a soak from 2026-09-11; merges to main after.

## Known issues

### `sync export` breaks Trakt's rate limit

`sync export` fires roughly 170 requests in a row (every kind, every page, history at 250 rows a page). On a real account that trips Trakt's limiter: the export itself completes, but the next unrelated command gets `TRAKT_RATE_LIMITED` (HTTP 429) with a `Retry-After` of about five minutes. Observed 2026-09-11 on the 1.4.0-testing build.

Must fix before 1.4.0 ships. Options, not yet decided:

- Read Trakt's `X-Ratelimit` headers and pace requests to stay under the window.
- Honour `Retry-After` inside the export loop: sleep and resume rather than fail the kind.
- Fetch kinds concurrently within the limit, so the wall time drops while the request count stays the same.

Whichever lands, the skill's export paragraph needs to say what to expect after an export.
