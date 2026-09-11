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

echo "## sync export routes (read-only; one row each, so the probe stays cheap)"
for ep in sync/watched/movies sync/watched/shows sync/watched/episodes \
          sync/collection/movies sync/collection/shows \
          sync/ratings/movies sync/ratings/shows sync/ratings/seasons sync/ratings/episodes \
          sync/watchlist/movies sync/watchlist/shows sync/watchlist/seasons sync/watchlist/episodes \
          sync/favorites/movies sync/favorites/shows \
          sync/history/movies sync/history/episodes; do
  printf "GET  /%-28s %s\n" "$ep" "$(code "${H[@]}" "${A[@]}" "https://api.trakt.tv/$ep?limit=1")"
done

echo "## export exclusions (why these three types are NOT fetched)"
echo "   (item counts are blank unless \$TOK is a real token: this script reads tokens.json,"
echo "    and a Keychain-backed install has none — the status codes above still discriminate)"
# history/shows and history/seasons return the same episode plays as
# history/episodes -- same item_count, same first row -- so fetching them would
# triple the largest pull for no new data. collection/seasons is not a route.
for ep in sync/history/shows sync/history/seasons sync/history/episodes; do
  printf "GET  /%-28s items=%s\n" "$ep" \
    "$(curl -s -D - -o /dev/null "${H[@]}" "${A[@]}" "https://api.trakt.tv/$ep?limit=1" \
       | tr -d '\r' | awk -F': ' 'tolower($1)=="x-pagination-item-count"{print $2}')"
done
printf "GET  /%-28s %s (expect 4xx: no such route)\n" "sync/collection/seasons" \
  "$(code "${H[@]}" "${A[@]}" https://api.trakt.tv/sync/collection/seasons)"

echo "## NOT fired live (would mutate account): PUT /sync/{watchlist,favorites} settings, reorder, update-item"
echo "   -> covered in Phase 4 with throwaway-list data."
