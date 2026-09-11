#!/usr/bin/env bash
# Live existence probes for endpoints flagged "unconfirmed" in the build plan.
# SAFE BY DESIGN:
#   - /oauth/revoke is sent a BOGUS token, so the real OAuth token is never revoked.
#   - sync mutations use a non-existent trakt id (0) -> Trakt returns not_found, adds/removes nothing.
#   - settings/reorder/update-item are NOT fired (they would mutate real account state).
# Discriminator: 200/201 = route exists (processed/no-op); 400/401/403 = route exists (bad input / auth / scope);
#                404/405/412 = no such route.
set -u
cd "$(dirname "$0")/.."
CONFIG="${TRAKTCTL_CONFIG:-$HOME/.config/traktctl/config.toml}"
[ -f config.toml ] && CONFIG=config.toml
CID=$(grep client_id "$CONFIG" | head -1 | cut -d"'" -f2)
CSEC=$(grep client_secret "$CONFIG" | head -1 | cut -d"'" -f2)
TOK=$(jq -r .access_token tokens.json)
H=(-H "Content-Type: application/json" -H "trakt-api-version: 2" -H "trakt-api-key: $CID")
A=(-H "Authorization: Bearer $TOK")
code() { curl -s -o /dev/null -w "%{http_code}" "$@"; }
NOOP='{"movies":[{"ids":{"trakt":0}}]}'   # trakt id 0 -> not_found -> no mutation

echo "## oauth/revoke (bogus token; real token untouched)"
rev=$(code -X POST "${H[@]}" -d "{\"token\":\"DEADBEEFnot-a-real-token\",\"client_id\":\"$CID\",\"client_secret\":\"$CSEC\"}" https://api.trakt.tv/oauth/revoke)
printf "POST /%-28s %s\n" "oauth/revoke" "$rev"

echo "## sync mutations (trakt:0 -> no-op, account unchanged)"
for ep in sync/collection sync/collection/remove sync/watchlist sync/watchlist/remove sync/favorites sync/favorites/remove; do
  printf "POST /%-28s %s\n" "$ep" "$(code -X POST "${H[@]}" "${A[@]}" -d "$NOOP" https://api.trakt.tv/$ep)"
done

echo "## NOT fired live (would mutate account): PUT /sync/{watchlist,favorites} settings, reorder, update-item"
echo "   -> covered in Phase 4 with throwaway-list data."

echo "## person (B1, read-only, no auth required)"
for ep in people/bryan-cranston people/bryan-cranston/movies people/bryan-cranston/shows people/bryan-cranston/lists; do
  printf "GET /%-28s %s\n" "$ep" "$(code "${H[@]}" https://api.trakt.tv/$ep)"
done

echo "## B2 list group (all reads, optional auth; nothing mutated)"
for ep in lists/trending lists/popular; do
  printf "GET  /%-28s %s\n" "$ep" "$(code "${H[@]}" "${A[@]}" https://api.trakt.tv/$ep)"
done
# Resolve a real list id off trending so get/items/likes probe an existing
# list rather than a guessed one; falls back to a 404 probe id if that fails.
LID=$(curl -s "${H[@]}" "${A[@]}" "https://api.trakt.tv/lists/trending?limit=1" | jq -r '.[0].list.ids.trakt // empty')
[ -z "$LID" ] && LID=0
for ep in "lists/$LID" "lists/$LID/items" "lists/$LID/likes"; do
  printf "GET  /%-28s %s\n" "$ep" "$(code "${H[@]}" "${A[@]}" https://api.trakt.tv/$ep)"
done
