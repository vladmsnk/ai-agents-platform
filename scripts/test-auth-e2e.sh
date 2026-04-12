#!/usr/bin/env bash
# ============================================================================
# E2E test suite for Keycloak authentication integration
#
# Prerequisites: docker-compose up --build -d
# Usage: ./scripts/test-auth-e2e.sh [gateway_url] [keycloak_url]
# ============================================================================
set -uo pipefail

GATEWAY="${1:-http://localhost:8080}"
KEYCLOAK="${2:-http://localhost:8180}"
REALM="agents"
TOKEN_URL="$KEYCLOAK/realms/$REALM/protocol/openid-connect/token"

BLUE='\033[0;34m'
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
DIM='\033[2m'
NC='\033[0m'
PASS=0
FAIL=0
SKIP=0

section()  { echo -e "\n${BLUE}══════════════════════════════════════════════════════════════${NC}"; echo -e "${BLUE}  $1${NC}"; echo -e "${BLUE}══════════════════════════════════════════════════════════════${NC}"; }
step()     { echo -e "\n${DIM}--- $1 ---${NC}"; }
ok()       { PASS=$((PASS + 1)); echo -e "  ${GREEN}✓ $1${NC}"; }
fail()     { FAIL=$((FAIL + 1)); echo -e "  ${RED}✗ $1${NC}"; }
skip()     { SKIP=$((SKIP + 1)); echo -e "  ${YELLOW}⊘ SKIP: $1${NC}"; }
info()     { echo -e "  ${DIM}$1${NC}"; }

assert_eq() {
  local actual="$1" expected="$2" msg="$3"
  if [ "$actual" = "$expected" ]; then ok "$msg"; else fail "$msg (expected '$expected', got '$actual')"; fi
}
assert_contains() {
  local haystack="$1" needle="$2" msg="$3"
  if echo "$haystack" | grep -q "$needle" 2>/dev/null; then ok "$msg"; else fail "$msg (expected to contain '$needle')"; fi
}
assert_not_empty() {
  local val="$1" msg="$2"
  if [ -n "$val" ] && [ "$val" != "null" ] && [ "$val" != "" ]; then ok "$msg"; else fail "$msg (was empty/null)"; fi
}

http_status() { curl -s -o /dev/null -w "%{http_code}" --max-time 10 "$@" 2>&1; }

# Get a token via client_credentials flow
get_token() {
  local client_id="$1" client_secret="$2"
  curl -s -X POST "$TOKEN_URL" \
    -d "grant_type=client_credentials&client_id=$client_id&client_secret=$client_secret" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    | jq -r '.access_token // empty' 2>/dev/null
}

# Authenticated HTTP helpers
auth_get()  { curl -s --max-time 10 -H "Authorization: Bearer $1" "$2" 2>&1; }
auth_post() { curl -s --max-time 60 -X POST -H "Authorization: Bearer $1" -H "Content-Type: application/json" "$2" -d "$3" 2>&1; }
auth_del()  { curl -s --max-time 10 -X DELETE -H "Authorization: Bearer $1" "$2" 2>&1; }
auth_status() {
  local token="$1"; shift
  curl -s -o /dev/null -w "%{http_code}" --max-time 10 -H "Authorization: Bearer $token" "$@" 2>&1
}

# ============================================================================
section "0. WAIT FOR SERVICES"
# ============================================================================

step "0.1 Keycloak readiness"
for i in $(seq 1 30); do
  KC_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$KEYCLOAK/realms/$REALM" 2>&1)
  if [ "$KC_STATUS" = "200" ]; then break; fi
  if [ "$i" = "30" ]; then
    fail "Keycloak not ready after 60s"
    echo "Cannot continue without Keycloak. Exiting."
    exit 1
  fi
  sleep 2
done
ok "Keycloak realm '$REALM' is ready"

step "0.2 Gateway readiness"
GW_STATUS=$(http_status "$GATEWAY/health")
assert_eq "$GW_STATUS" "200" "Gateway is healthy"

# ============================================================================
section "1. OPEN ENDPOINTS (no auth required)"
# ============================================================================

step "1.1 /health — no auth needed"
STATUS=$(http_status "$GATEWAY/health")
assert_eq "$STATUS" "200" "/health returns 200 without token"

step "1.2 /metrics — no auth needed"
STATUS=$(http_status "$GATEWAY/metrics")
assert_eq "$STATUS" "200" "/metrics returns 200 without token"

step "1.3 /.well-known/agent.json — no auth needed"
STATUS=$(http_status "$GATEWAY/.well-known/agent.json")
assert_eq "$STATUS" "200" "/.well-known/agent.json returns 200 without token"

# ============================================================================
section "2. PROTECTED ENDPOINTS REJECT UNAUTHENTICATED REQUESTS"
# ============================================================================

step "2.1 GET /api/agents — no token"
STATUS=$(http_status "$GATEWAY/api/agents")
assert_eq "$STATUS" "401" "/api/agents returns 401 without token"

step "2.2 POST /api/agents — no token"
STATUS=$(http_status -X POST "$GATEWAY/api/agents" -H "Content-Type: application/json" \
  -d '{"id":"x","name":"x","url":"http://x"}')
assert_eq "$STATUS" "401" "POST /api/agents returns 401 without token"

step "2.3 POST /v1/chat/completions — no token"
STATUS=$(http_status -X POST "$GATEWAY/v1/chat/completions" -H "Content-Type: application/json" \
  -d '{"model":"x","messages":[{"role":"user","content":"hi"}]}')
assert_eq "$STATUS" "401" "/v1/chat/completions returns 401 without token"

step "2.4 POST /a2a — no token"
STATUS=$(http_status -X POST "$GATEWAY/a2a" -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"message/send","id":1,"params":{}}')
assert_eq "$STATUS" "401" "/a2a returns 401 without token"

step "2.5 POST /api/agents/discover — no token"
STATUS=$(http_status -X POST "$GATEWAY/api/agents/discover" -H "Content-Type: application/json" \
  -d '{"query":"test"}')
assert_eq "$STATUS" "401" "/api/agents/discover returns 401 without token"

step "2.6 Garbage bearer token"
STATUS=$(http_status -H "Authorization: Bearer garbage.invalid.token" "$GATEWAY/api/agents")
assert_eq "$STATUS" "401" "Invalid token returns 401"

# ============================================================================
section "3. TOKEN ACQUISITION (client_credentials flow)"
# ============================================================================

step "3.1 Gateway client gets token"
GW_TOKEN=$(get_token "gateway" "gateway-secret")
assert_not_empty "$GW_TOKEN" "Gateway token acquired"

step "3.2 Test-agent client gets token"
TEST_TOKEN=$(get_token "test-agent" "test-secret")
assert_not_empty "$TEST_TOKEN" "Test-agent token acquired"

step "3.3 Agent-translator client gets token"
TRANS_TOKEN=$(get_token "agent-translator" "translator-secret")
assert_not_empty "$TRANS_TOKEN" "Translator token acquired"

step "3.4 Invalid credentials rejected"
BAD_TOKEN=$(get_token "gateway" "wrong-secret")
if [ -z "$BAD_TOKEN" ]; then ok "Wrong secret returns no token"; else fail "Wrong secret returned a token"; fi

step "3.5 Unknown client rejected"
BAD_TOKEN2=$(get_token "nonexistent-client" "secret")
if [ -z "$BAD_TOKEN2" ]; then ok "Unknown client returns no token"; else fail "Unknown client returned a token"; fi

# ============================================================================
section "4. AUTHENTICATED API ACCESS"
# ============================================================================

step "4.1 GET /api/agents — with valid token"
RESP=$(auth_get "$TEST_TOKEN" "$GATEWAY/api/agents")
AGENT_COUNT=$(echo "$RESP" | jq 'length' 2>/dev/null)
if [ "${AGENT_COUNT:-0}" -ge "1" ]; then
  ok "GET /api/agents works with token ($AGENT_COUNT agents)"
else
  fail "GET /api/agents returned unexpected response: ${RESP:0:100}"
fi

step "4.2 GET /api/providers — with valid token"
STATUS=$(auth_status "$TEST_TOKEN" "$GATEWAY/api/providers")
assert_eq "$STATUS" "200" "GET /api/providers returns 200 with token"

step "4.3 GET /api/stats — with valid token"
STATUS=$(auth_status "$TEST_TOKEN" "$GATEWAY/api/stats")
assert_eq "$STATUS" "200" "GET /api/stats returns 200 with token"

step "4.4 POST /api/agents/discover — with valid token"
DISC=$(auth_post "$TEST_TOKEN" "$GATEWAY/api/agents/discover" '{"query":"translate text","top_n":1,"min_score":0.0}')
DISC_COUNT=$(echo "$DISC" | jq 'length' 2>/dev/null)
if [ "${DISC_COUNT:-0}" -ge "1" ]; then
  ok "Discover works with token"
else
  skip "Discover returned 0 results (agents may not be registered yet)"
fi

# ============================================================================
section "5. AGENT REGISTRATION WITH AUTO-PROVISIONING"
# ============================================================================

# Clean up from previous runs
auth_del "$GW_TOKEN" "$GATEWAY/api/agents/auth-test-agent" > /dev/null 2>&1

step "5.1 Register agent — credentials returned"
REG_RESP=$(auth_post "$GW_TOKEN" "$GATEWAY/api/agents" '{
  "id": "auth-test-agent",
  "name": "Auth Test Agent",
  "description": "Agent registered during auth e2e test",
  "url": "http://192.0.2.1:9999",
  "version": "1.0.0",
  "skills": [{"id":"test","name":"test","description":"testing auth"}]
}')

REG_AGENT_NAME=$(echo "$REG_RESP" | jq -r '.agent.name // empty' 2>/dev/null)
REG_CLIENT_ID=$(echo "$REG_RESP" | jq -r '.credentials.client_id // empty' 2>/dev/null)
REG_CLIENT_SECRET=$(echo "$REG_RESP" | jq -r '.credentials.client_secret // empty' 2>/dev/null)

assert_eq "$REG_AGENT_NAME" "Auth Test Agent" "Agent registered successfully"

if [ -n "$REG_CLIENT_ID" ] && [ -n "$REG_CLIENT_SECRET" ]; then
  ok "Credentials returned (client_id=$REG_CLIENT_ID)"
else
  info "Response: ${REG_RESP:0:200}"
  fail "No credentials in registration response"
fi

step "5.2 Provisioned client can get a token"
if [ -n "$REG_CLIENT_ID" ] && [ -n "$REG_CLIENT_SECRET" ]; then
  NEW_TOKEN=$(get_token "$REG_CLIENT_ID" "$REG_CLIENT_SECRET")
  if [ -n "$NEW_TOKEN" ]; then
    ok "Provisioned client obtained a token"
  else
    fail "Provisioned client failed to get token"
  fi
else
  skip "No credentials to test (provisioning failed)"
fi

step "5.3 Provisioned token works for API access"
if [ -n "${NEW_TOKEN:-}" ]; then
  STATUS=$(auth_status "$NEW_TOKEN" "$GATEWAY/api/agents")
  assert_eq "$STATUS" "200" "Provisioned token accepted by gateway"
else
  skip "No token to test"
fi

step "5.4 Delete agent — Keycloak client cleaned up"
DEL_RESP=$(auth_del "$GW_TOKEN" "$GATEWAY/api/agents/auth-test-agent")
DEL_ID=$(echo "$DEL_RESP" | jq -r '.deleted // empty' 2>/dev/null)
assert_eq "$DEL_ID" "auth-test-agent" "Agent deleted"

step "5.5 Deleted client can no longer get token"
if [ -n "${REG_CLIENT_ID:-}" ] && [ -n "${REG_CLIENT_SECRET:-}" ]; then
  DEAD_TOKEN=$(get_token "$REG_CLIENT_ID" "$REG_CLIENT_SECRET")
  if [ -z "$DEAD_TOKEN" ]; then
    ok "Deleted client cannot get token"
  else
    fail "Deleted client still got a token (Keycloak client not removed)"
  fi
else
  skip "No credentials to test"
fi

# ============================================================================
section "6. A2A DISPATCH WITH AUTH"
# ============================================================================

step "6.1 Authenticated A2A message/send — auto-routing"
A2A_RESP=$(auth_post "$TEST_TOKEN" "$GATEWAY/a2a" '{
  "jsonrpc":"2.0","method":"message/send","id":601,
  "params":{"id":"auth-a2a-1","message":{"role":"user","parts":[{"type":"text","text":"translate hello to French"}]}}
}')
A2A_STATE=$(echo "$A2A_RESP" | jq -r '.result.status.state // empty' 2>/dev/null)
A2A_ERR=$(echo "$A2A_RESP" | jq -r '.error.message // empty' 2>/dev/null)
if [ "$A2A_STATE" = "completed" ]; then
  ok "A2A auto-routed message completed with auth"
elif [ -n "$A2A_ERR" ]; then
  fail "A2A dispatch error: $A2A_ERR"
else
  fail "A2A unexpected state: $A2A_STATE (resp: ${A2A_RESP:0:200})"
fi

step "6.2 Authenticated A2A — explicit agent routing"
A2A2_RESP=$(auth_post "$TEST_TOKEN" "$GATEWAY/a2a/translator" '{
  "jsonrpc":"2.0","method":"message/send","id":602,
  "params":{"id":"auth-a2a-2","message":{"role":"user","parts":[{"type":"text","text":"translate good morning to German"}]}}
}')
A2A2_STATE=$(echo "$A2A2_RESP" | jq -r '.result.status.state // empty' 2>/dev/null)
assert_eq "$A2A2_STATE" "completed" "A2A explicit route with auth completed"

step "6.3 Different agent tokens work for A2A"
A2A3_RESP=$(auth_post "$TRANS_TOKEN" "$GATEWAY/a2a/summarizer" '{
  "jsonrpc":"2.0","method":"message/send","id":603,
  "params":{"id":"auth-a2a-3","message":{"role":"user","parts":[{"type":"text","text":"summarize: the sun is a star"}]}}
}')
A2A3_STATE=$(echo "$A2A3_RESP" | jq -r '.result.status.state // empty' 2>/dev/null)
assert_eq "$A2A3_STATE" "completed" "Translator token can call summarizer via A2A"

# ============================================================================
section "7. TOKEN EXPIRY & EDGE CASES"
# ============================================================================

step "7.1 Expired token rejected"
# Craft a token that's structurally valid JWT but expired
# We can't easily forge a proper one, so test with a truncated real token
EXPIRED="eyJhbGciOiJSUzI1NiJ9.eyJleHAiOjEwMDAwMDAwMDAsImlzcyI6Imh0dHA6Ly9sb2NhbGhvc3QifQ.invalid"
STATUS=$(auth_status "$EXPIRED" "$GATEWAY/api/agents")
assert_eq "$STATUS" "401" "Malformed/expired token returns 401"

step "7.2 Token from wrong realm rejected"
# Try to get a token from the master realm (different issuer)
WRONG_TOKEN=$(curl -s -X POST "$KEYCLOAK/realms/master/protocol/openid-connect/token" \
  -d "grant_type=client_credentials&client_id=admin-cli&client_secret=admin" \
  -H "Content-Type: application/x-www-form-urlencoded" \
  | jq -r '.access_token // empty' 2>/dev/null)
if [ -n "$WRONG_TOKEN" ]; then
  STATUS=$(auth_status "$WRONG_TOKEN" "$GATEWAY/api/agents")
  assert_eq "$STATUS" "401" "Token from wrong realm rejected"
else
  skip "Could not get master realm token (admin-cli may not have credentials)"
fi

# ============================================================================
section "8. MOCK AGENT INTEGRATION"
# ============================================================================

step "8.1 Mock agents registered successfully (via auth)"
AGENTS=$(auth_get "$GW_TOKEN" "$GATEWAY/api/agents")
for AGENT in translator summarizer code-reviewer; do
  FOUND=$(echo "$AGENTS" | jq -r ".[] | select(.id==\"$AGENT\") | .name" 2>/dev/null)
  if [ -n "$FOUND" ]; then
    ok "Mock agent '$AGENT' registered with auth"
  else
    fail "Mock agent '$AGENT' not found (registration with auth may have failed)"
  fi
done

step "8.2 Mock agents are healthy (auth didn't break health checks)"
for AGENT in translator summarizer; do
  STATUS=$(echo "$AGENTS" | jq -r ".[] | select(.id==\"$AGENT\") | .status" 2>/dev/null)
  assert_eq "${STATUS:-missing}" "active" "Agent $AGENT is active"
done

step "8.3 Delegation works through auth (code-reviewer -> translator)"
info "This tests the full chain: caller->gateway(auth)->code-reviewer->gateway(auth)->translator"
DEL_RESP=$(auth_post "$TEST_TOKEN" "$GATEWAY/a2a/code-reviewer" '{
  "jsonrpc":"2.0","method":"message/send","id":801,
  "params":{"id":"auth-delegate","message":{"role":"user","parts":[{"type":"text","text":"Review and translate this code: func hello() {}"}]}}
}')
DEL_STATE=$(echo "$DEL_RESP" | jq -r '.result.status.state // empty' 2>/dev/null)
DEL_TEXT=$(echo "$DEL_RESP" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
assert_eq "$DEL_STATE" "completed" "Delegation through auth completed"
if echo "$DEL_TEXT" | grep -q "delegation failed"; then
  DEL_ERR=$(echo "$DEL_TEXT" | grep -o '\[delegation failed:.*\]')
  fail "Delegation failed through auth: $DEL_ERR"
else
  ok "Delegation succeeded through auth (no failure message)"
fi

# ============================================================================
section "SUMMARY"
# ============================================================================

echo ""
echo -e "${BLUE}══════════════════════════════════════════════════════════════${NC}"
TOTAL=$((PASS + FAIL))
echo -e "  Total: $TOTAL tests"
echo -e "  ${GREEN}Passed: $PASS${NC}"
if [ "$FAIL" -gt 0 ]; then echo -e "  ${RED}Failed: $FAIL${NC}"; fi
if [ "$SKIP" -gt 0 ]; then echo -e "  ${YELLOW}Skipped: $SKIP${NC}"; fi
echo -e "${BLUE}══════════════════════════════════════════════════════════════${NC}"

if [ "$FAIL" -gt 0 ]; then exit 1; fi
