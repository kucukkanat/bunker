#!/usr/bin/env bash
# End-to-end test of the bunker CLI and local MCP server against a real
# `bunker dev` server. Usage: test/e2e/cli.test.sh  (from the repo root)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/bunker-e2e.XXXXXX")"
BIN="$WORK/bunker"
PORT="${PORT:-$(python3 -c 'import socket;s=socket.socket();s.bind(("127.0.0.1",0));print(s.getsockname()[1])' 2>/dev/null || echo 18431)}"
URL="http://127.0.0.1:$PORT"
SERVER_PID=""

cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  wait 2>/dev/null || true
  if [ "${KEEP:-}" = "" ]; then rm -rf "$WORK"; else echo "kept $WORK"; fi
}
trap cleanup EXIT

pass=0
ok()   { pass=$((pass+1)); printf '  \033[32m✓\033[0m %s\n' "$1"; }
die()  { printf '  \033[31m✗\033[0m %s\n' "$1" >&2; [ -f "$WORK/server.log" ] && tail -20 "$WORK/server.log" >&2; exit 1; }
need() { echo "$1" | grep -q -- "$2" || die "$3 (wanted '$2' in: $1)"; }
jget() { python3 -c 'import json,sys; d=json.load(sys.stdin);
for k in sys.argv[1].split("."):
    d = d[int(k)] if isinstance(d, list) else d[k]
print(d if not isinstance(d,(dict,list)) else json.dumps(d))' "$1"; }

echo "Building…"
(cd "$ROOT" && CGO_ENABLED=0 go build -o "$BIN" ./cmd/bunker)

echo "Starting bunker dev on $URL"
BUNKER_DATA_DIR="$WORK/data" BUNKER_LISTEN="127.0.0.1:$PORT" BUNKER_URL="$URL" "$BIN" dev >"$WORK/server.log" 2>&1 &
SERVER_PID=$!
for _ in $(seq 1 100); do curl -fsS "$URL/healthz" >/dev/null 2>&1 && break; sleep 0.1; done
curl -fsS "$URL/healthz" >/dev/null || die "server did not start"
ok "server is up"

# Isolated CLI config.
export BUNKER_CONFIG="$WORK/config.json" BUNKER_NO_BROWSER=1 BUNKER_NO_CLIPBOARD=1 NO_COLOR=1
unset BUNKER_SERVER BUNKER_TOKEN BUNKER_PROFILE
B() { "$BIN" "$@"; }

# Dev mode signs cookie-less requests in as the admin; mint a token with the CSRF header.
TOKEN="$(curl -fsS -X POST "$URL/api/v1/tokens" -H 'X-Bunker: 1' -H 'Content-Type: application/json' -d '{"name":"e2e"}' | jget token)"
[ "${TOKEN:0:3}" = "bk_" ] || die "no token"
ok "created a token via the API"

out="$(echo "$TOKEN" | B login "$URL" --token - 2>&1)"; need "$out" "Signed in" "login --token"
[ "$(stat -c %a "$BUNKER_CONFIG" 2>/dev/null || stat -f %Lp "$BUNKER_CONFIG")" = "600" ] || die "config is not 0600"
ok "login --token (config 0600)"

# Device flow: start login in the background, approve the code like the browser would.
DEV_CFG="$WORK/device.json"
BUNKER_CONFIG="$DEV_CFG" "$BIN" login "$URL" --no-browser --json >"$WORK/device.out" 2>"$WORK/device.err" &
LOGIN_PID=$!
CODE=""
for _ in $(seq 1 50); do CODE="$(grep -o '"userCode":"[A-Z-]*"' "$WORK/device.err" 2>/dev/null | cut -d'"' -f4 || true)"; [ -n "$CODE" ] && break; sleep 0.1; done
[ -n "$CODE" ] || die "device login printed no code: $(cat "$WORK/device.err")"
curl -fsS -X POST "$URL/api/v1/auth/device/$CODE" -H 'X-Bunker: 1' -H 'Content-Type: application/json' -d '{"approve":true}' >/dev/null || die "approve failed"
wait "$LOGIN_PID" || die "device login failed: $(cat "$WORK/device.err")"
grep -q '"email":"you@localhost"' "$WORK/device.out" || grep -q '"email": "you@localhost"' "$WORK/device.out" || die "device login output: $(cat "$WORK/device.out")"
grep -q '"token": "bk_' "$DEV_CFG" || die "device login should save a token"
ok "login via device flow"

out="$(B whoami --json)"; need "$out" '"email": *"you@localhost"' "whoami"
ok "whoami"

out="$(B doctor 2>&1)" || die "doctor failed: $out"; need "$out" "signed in as" "doctor"
ok "doctor"

# A sample site.
SITE_DIR="$WORK/my-lunch-app"
mkdir -p "$SITE_DIR/css" "$SITE_DIR/node_modules/x"
cat >"$SITE_DIR/index.html" <<'HTML'
<!doctype html><html><head><title>Friday Lunch</title><link rel="stylesheet" href="css/app.css"></head>
<body><h1>Lunch</h1><script src="app.js"></script></body></html>
HTML
echo 'h1{color:green}' >"$SITE_DIR/css/app.css"
echo 'console.log("hi")' >"$SITE_DIR/app.js"
echo 'secret notes' >"$SITE_DIR/notes.txt"
echo 'ignored' >"$SITE_DIR/node_modules/x/index.js"
printf 'notes.txt\n' >"$SITE_DIR/.bunkerignore"

out="$(cd "$SITE_DIR" && B deploy --json)"
SLUG="$(echo "$out" | jget site)"
[ "$(echo "$out" | jget files)" = "3" ] || die "first deploy should send 3 files: $out"
[ "$(echo "$out" | jget uploaded)" = "3" ] || die "first deploy should upload 3 blobs: $out"
[ "$(echo "$out" | jget created)" = "True" ] || die "first deploy should create the site: $out"
grep -q "\"site\": \"$SLUG\"" "$SITE_DIR/bunker.json" || die "bunker.json not written"
ok "first deploy created $SLUG (3 files, ignore rules applied, bunker.json written)"

curl -fsS "$URL/s/$SLUG/" >/dev/null || die "site page not served"

out="$(cd "$SITE_DIR" && B deploy -m "second" --json)"
[ "$(echo "$out" | jget uploaded)" = "0" ] || die "second deploy should upload 0 blobs: $out"
[ "$(echo "$out" | jget version)" = "2" ] || die "second deploy should be v2: $out"
ok "second deploy uploaded 0 new blobs (v2)"

echo 'h1{color:blue}' >"$SITE_DIR/css/app.css"
out="$(cd "$SITE_DIR" && B deploy -m "blue" --json)"
[ "$(echo "$out" | jget uploaded)" = "1" ] || die "third deploy should upload 1 blob: $out"
ok "changed file deploy uploaded 1 blob (v3)"

out="$(B deploy "$SITE_DIR/index.html" --site "$SLUG" -m "single" --json)"
[ "$(echo "$out" | jget version)" = "4" ] || die "single-file deploy: $out"
out="$(echo '<title>Piped</title><h1>stdin</h1>' | B deploy - --json)"
PIPED="$(echo "$out" | jget site)"
need "$PIPED" "piped" "stdin deploy title"
ok "single-file and stdin deploys"

out="$(B share "$SLUG" ada@example.com --role editor 2>&1)"; need "$out" "Shared" "share user"
out="$(B share "$SLUG" "Finance Team" 2>&1)"; need "$out" "Shared" "share group"
out="$(B api sharing.list "$SLUG")"; need "$out" "ada@example.com" "sharing list"
out="$(B unshare "$SLUG" "Finance Team" 2>&1)"; need "$out" "no longer" "unshare"
ok "share / unshare"

out="$(B versions "$SLUG")"; need "$out" "v4" "versions table"; need "$out" "live" "versions live marker"
out="$(B versions "$SLUG" --json)"; [ "$(echo "$out" | jget live)" = "4" ] || die "live should be 4"
ok "versions"

out="$(B rollback "$SLUG" 2 --yes --json)"
[ "$(echo "$out" | jget restored)" = "2" ] || die "rollback: $out"
[ "$(echo "$out" | jget version)" = "5" ] || die "rollback should publish v5: $out"
if B rollback "$SLUG" 3 </dev/null >/dev/null 2>&1; then die "rollback without --yes must refuse when not interactive"; fi
ok "rollback (and refuses without --yes)"

PULL="$WORK/pulled"
B pull "$SLUG" "$PULL" >/dev/null 2>&1 || die "pull failed"
grep -q "color:green" "$PULL/css/app.css" || die "pull should get v2 content after rollback"
[ -f "$PULL/bunker.json" ] || die "pull should write bunker.json"
ok "pull"

out="$(B sites ls --json)"; need "$out" "$SLUG" "sites ls"
out="$(B sites rename "$SLUG" "Friday Lunch Club" --json)"; need "$out" "Friday Lunch Club" "rename"
out="$(B ops --names)"; need "$out" "sites.create" "ops"
out="$(B api sites.get "$SLUG")"; need "$out" "\"slug\"" "api positional path param"
out="$(B api sites.create --title "From API" --json)"; need "$out" "from-api" "api body params"
out="$(B sites rm from-api --yes 2>&1)"; need "$out" "trash" "sites rm"
out="$(B tokens ls)"; need "$out" "e2e" "tokens ls"
ok "sites / ops / api / tokens"

out="$(B ai "a poll for lunch" 2>&1)" && die "ai should fail without the AI module" || true
need "$out" "AI" "ai error hint"
B completion bash | bash -n || die "bash completion has syntax errors"
ok "ai (helpful error when AI is off), completion"

INIT="$WORK/agent-project"
B init "$INIT" >/dev/null 2>&1
grep -q "bunker:start" "$INIT/AGENTS.md" || die "AGENTS.md section"
grep -q "@AGENTS.md" "$INIT/CLAUDE.md" || die "CLAUDE.md pointer"
B init "$INIT" >/dev/null 2>&1
[ "$(grep -c 'bunker:start' "$INIT/AGENTS.md")" = "1" ] || die "init must be idempotent"
ok "init (AGENTS.md + CLAUDE.md, idempotent)"

# Local MCP over stdio.
MCP_OUT="$(printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"e2e","version":"1"}}}' \
  '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
  '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"create_site_from_html","arguments":{"title":"MCP Page","html":"<h1>from mcp</h1>"}}}' \
  '{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"bunker://docs/sdk"}}' \
  | B mcp 2>/dev/null)"
echo "$MCP_OUT" | grep -q '"protocolVersion":"2025-06-18"' || die "mcp initialize: $MCP_OUT"
echo "$MCP_OUT" | grep -q '"name":"sites_list"' || die "mcp tools/list"
echo "$MCP_OUT" | grep -q 'mcp-page' || die "mcp tools/call create_site_from_html"
echo "$MCP_OUT" | grep -q 'bunker.db' || die "mcp resources/read"
[ "$(echo "$MCP_OUT" | wc -l)" -eq 4 ] || die "mcp should answer 4 requests (not the notification)"
ok "bunker mcp (initialize, tools/list, tools/call, resources/read)"

# Remote MCP and agent docs.
code="$(curl -s -o /dev/null -w '%{http_code}' -X POST "$URL/mcp" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"ping"}')"
[ "$code" = "401" ] || die "/mcp without token should be 401 (got $code)"
curl -sS -D - -o /dev/null -X POST "$URL/mcp" -d '{}' | grep -qi 'resource_metadata=' || die "401 needs WWW-Authenticate resource_metadata"
out="$(curl -fsS -X POST "$URL/mcp" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}')"
need "$out" "deploy_files" "remote tools/list"
curl -fsS "$URL/.well-known/oauth-authorization-server" | grep -q code_challenge_methods_supported || die "AS metadata"
curl -fsS "$URL/.well-known/oauth-protected-resource/mcp" | grep -q authorization_servers || die "PRM"
curl -fsS "$URL/llms.txt" | grep -q "bunker deploy" || die "llms.txt"
ok "remote /mcp, OAuth metadata, llms.txt"

out="$(B logout 2>&1)"; need "$out" "revoked" "logout revokes"
if B whoami >/dev/null 2>&1; then die "whoami should fail after logout"; fi
ok "logout"

echo "All $pass checks passed."
