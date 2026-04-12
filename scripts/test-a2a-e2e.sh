#!/usr/bin/env bash
# ============================================================================
# Comprehensive E2E test suite for the AI Agents Platform
#
# Prerequisites: docker-compose up --build -d
# Usage: ./scripts/test-a2a-e2e.sh [gateway_url]
# ============================================================================
set -uo pipefail

GATEWAY="${1:-http://localhost:8080}"
KEYCLOAK="${KEYCLOAK_URL:-http://localhost:8180}"
REALM="${KEYCLOAK_REALM:-agents}"
AUTH_TOKEN=""
BLUE='\033[0;34m'
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
DIM='\033[2m'
NC='\033[0m'
PASS=0
FAIL=0
SKIP=0
TOTAL=0

section()  { echo -e "\n${BLUE}══════════════════════════════════════════════════════════════${NC}"; echo -e "${BLUE}  $1${NC}"; echo -e "${BLUE}══════════════════════════════════════════════════════════════${NC}"; }
step()     { TOTAL=$((TOTAL + 1)); echo -e "\n${DIM}--- $1 ---${NC}"; }
ok()       { PASS=$((PASS + 1)); echo -e "  ${GREEN}✓ $1${NC}"; }
fail()     { FAIL=$((FAIL + 1)); echo -e "  ${RED}✗ $1${NC}"; }
skip()     { SKIP=$((SKIP + 1)); echo -e "  ${YELLOW}⊘ SKIP: $1${NC}"; }
info()     { echo -e "  ${DIM}$1${NC}"; }

# Helpers — automatically inject auth token when available
_auth_header() { if [ -n "$AUTH_TOKEN" ]; then echo "-H" "Authorization: Bearer $AUTH_TOKEN"; fi; }
jq_or_raw() { jq "$@" 2>/dev/null || cat; }
http_post() { curl -s --max-time 60 -X POST $(_auth_header) "$1" -H "Content-Type: application/json" -d "$2" 2>&1; }
http_get()  { curl -s --max-time 10 $(_auth_header) "$1" 2>&1; }
http_del()  { curl -s --max-time 10 -X DELETE $(_auth_header) "$1" 2>&1; }
http_put()  { curl -s --max-time 10 -X PUT $(_auth_header) "$1" -H "Content-Type: application/json" -d "$2" 2>&1; }
http_status() { curl -s -o /dev/null -w "%{http_code}" --max-time 10 $(_auth_header) "$@" 2>&1; }
a2a_send()  { http_post "$GATEWAY/a2a$1" "$2"; }

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
assert_ge() {
  local actual="$1" min="$2" msg="$3"
  if [ "$actual" -ge "$min" ] 2>/dev/null; then ok "$msg"; else fail "$msg (expected >= $min, got '$actual')"; fi
}

# Try to acquire an auth token (if Keycloak is available)
KC_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$KEYCLOAK/realms/$REALM" 2>&1)
if [ "$KC_STATUS" = "200" ]; then
  AUTH_TOKEN=$(curl -s -X POST "$KEYCLOAK/realms/$REALM/protocol/openid-connect/token" \
    -d "grant_type=client_credentials&client_id=test-agent&client_secret=test-secret" \
    -H "Content-Type: application/x-www-form-urlencoded" \
    | jq -r '.access_token // empty' 2>/dev/null)
  if [ -n "$AUTH_TOKEN" ]; then
    echo -e "${GREEN}Auth: acquired token from Keycloak (test-agent)${NC}"
  else
    echo -e "${YELLOW}Auth: Keycloak reachable but token acquisition failed — running without auth${NC}"
  fi
else
  echo -e "${DIM}Auth: Keycloak not available — running without auth${NC}"
fi

# ============================================================================
section "1. INFRASTRUCTURE HEALTH"
# ============================================================================

step "1.1 Gateway /health endpoint"
STATUS=$(http_status "$GATEWAY/health")
assert_eq "$STATUS" "200" "Gateway health returns 200"

step "1.2 Gateway agent card (/.well-known/agent.json)"
CARD=$(http_get "$GATEWAY/.well-known/agent.json")
CARD_NAME=$(echo "$CARD" | jq -r '.name' 2>/dev/null)
CARD_SKILLS=$(echo "$CARD" | jq -r '.skills | length' 2>/dev/null)
assert_eq "$CARD_NAME" "LLM Gateway" "Agent card has correct name"
assert_ge "${CARD_SKILLS:-0}" "1" "Agent card has skills"

step "1.3 Prometheus metrics endpoint"
STATUS=$(http_status "$GATEWAY/metrics")
assert_eq "$STATUS" "200" "Metrics endpoint returns 200"

step "1.4 Providers loaded"
PROVIDERS=$(http_get "$GATEWAY/api/providers")
PROV_COUNT=$(echo "$PROVIDERS" | jq 'length' 2>/dev/null || echo 0)
assert_ge "$PROV_COUNT" "1" "At least 1 provider loaded ($PROV_COUNT found)"

# ============================================================================
section "2. EMBEDDINGS ENDPOINT"
# ============================================================================

step "2.1 Embeddings with real provider"
EMB_RESP=$(http_post "$GATEWAY/v1/embeddings" '{"model":"google/gemini-embedding-001","input":"hello world"}')
EMB_OBJ=$(echo "$EMB_RESP" | jq -r '.object' 2>/dev/null)
EMB_DIM=$(echo "$EMB_RESP" | jq -r '.data[0].embedding | length' 2>/dev/null)
if [ "$EMB_OBJ" = "list" ] && [ "${EMB_DIM:-0}" -gt "100" ]; then
  ok "Real embeddings returned (dim=$EMB_DIM)"
  REAL_EMBEDDINGS=true
else
  info "Real embeddings unavailable, mock fallback will be tested"
  REAL_EMBEDDINGS=false
fi

step "2.2 Embeddings with unknown model (mock fallback)"
MOCK_RESP=$(http_post "$GATEWAY/v1/embeddings" '{"model":"nonexistent-model","input":"test"}')
MOCK_OBJ=$(echo "$MOCK_RESP" | jq -r '.object' 2>/dev/null)
MOCK_DIM=$(echo "$MOCK_RESP" | jq -r '.data[0].embedding | length' 2>/dev/null)
assert_eq "$MOCK_OBJ" "list" "Mock embeddings return valid response"
assert_eq "${MOCK_DIM:-0}" "256" "Mock embeddings are 256-dimensional"

step "2.3 Embeddings — deterministic mock (same input = same output)"
MOCK1=$(http_post "$GATEWAY/v1/embeddings" '{"model":"nonexistent-model","input":"deterministic test"}' | jq -r '.data[0].embedding[0]' 2>/dev/null)
MOCK2=$(http_post "$GATEWAY/v1/embeddings" '{"model":"nonexistent-model","input":"deterministic test"}' | jq -r '.data[0].embedding[0]' 2>/dev/null)
assert_eq "$MOCK1" "$MOCK2" "Same input produces same mock embedding"

step "2.4 Embeddings — missing model field"
ERR_RESP=$(http_post "$GATEWAY/v1/embeddings" '{"input":"no model"}')
assert_contains "$ERR_RESP" "model" "Missing model returns error mentioning 'model'"

step "2.5 Embeddings — invalid JSON"
ERR_RESP=$(curl -s -X POST "$GATEWAY/v1/embeddings" -H "Content-Type: application/json" -d 'not json' 2>&1)
assert_contains "$ERR_RESP" "invalid" "Invalid JSON returns error"

step "2.6 Embeddings — array input"
ARR_RESP=$(http_post "$GATEWAY/v1/embeddings" '{"model":"nonexistent-model","input":["hello","world"]}')
ARR_COUNT=$(echo "$ARR_RESP" | jq '.data | length' 2>/dev/null)
assert_eq "${ARR_COUNT:-0}" "2" "Array input returns 2 embeddings"

# ============================================================================
section "3. AGENT REGISTRATION & CRUD"
# ============================================================================

# Clean up test agents if leftover
http_del "$GATEWAY/api/agents/test-agent-1" > /dev/null 2>&1
http_del "$GATEWAY/api/agents/test-agent-2" > /dev/null 2>&1

step "3.1 Register new agent"
REG_RESP=$(http_post "$GATEWAY/api/agents" '{
  "id": "test-agent-1",
  "name": "Test Agent One",
  "description": "An agent for end-to-end testing of registration flow",
  "url": "http://fake-host:9999",
  "version": "1.0.0",
  "skills": [{"id":"test","name":"testing","description":"run tests"}]
}')
# Registration response may be {agent:{...}, credentials:{...}} (with auth) or flat AgentCard (without auth)
REG_NAME=$(echo "$REG_RESP" | jq -r '.agent.name // .name' 2>/dev/null)
assert_eq "$REG_NAME" "Test Agent One" "Agent registered successfully"

step "3.2 Duplicate registration returns error"
DUP_STATUS=$(http_status -X POST "$GATEWAY/api/agents" -H "Content-Type: application/json" \
  -d '{"id":"test-agent-1","name":"Dup","description":"dup","url":"http://x"}')
# Should be 409 conflict or 500
if [ "$DUP_STATUS" != "201" ]; then ok "Duplicate blocked (HTTP $DUP_STATUS)"; else fail "Duplicate accepted (HTTP 201)"; fi

step "3.3 Registration with missing required fields"
BAD_STATUS=$(http_status -X POST "$GATEWAY/api/agents" -H "Content-Type: application/json" \
  -d '{"id":"","name":"","url":""}')
if [ "$BAD_STATUS" = "400" ]; then ok "Missing fields returns 400"; else fail "Missing fields returned $BAD_STATUS (expected 400)"; fi

step "3.4 Get agent by ID"
GET_RESP=$(http_get "$GATEWAY/api/agents/test-agent-1")
GET_NAME=$(echo "$GET_RESP" | jq -r '.name' 2>/dev/null)
GET_STATUS=$(echo "$GET_RESP" | jq -r '.status' 2>/dev/null)
assert_eq "$GET_NAME" "Test Agent One" "Get agent returns correct name"
# New agents without a reachable /health should become unhealthy or stay active
assert_not_empty "$GET_STATUS" "Agent has status field"

step "3.5 Get nonexistent agent returns 404"
MISS_STATUS=$(http_status "$GATEWAY/api/agents/nonexistent-agent-xyz")
assert_eq "$MISS_STATUS" "404" "Nonexistent agent returns 404"

step "3.6 Update agent"
UPD_RESP=$(http_put "$GATEWAY/api/agents/test-agent-1" '{
  "name": "Test Agent Updated",
  "description": "Updated description for testing",
  "url": "http://fake-host:9999",
  "version": "2.0.0",
  "skills": [{"id":"test","name":"testing","description":"run tests"},{"id":"qa","name":"QA","description":"quality assurance"}]
}')
UPD_NAME=$(echo "$UPD_RESP" | jq -r '.name' 2>/dev/null)
assert_eq "$UPD_NAME" "Test Agent Updated" "Agent updated successfully"

step "3.7 Verify update persisted"
VERIFY=$(http_get "$GATEWAY/api/agents/test-agent-1")
VERIFY_VER=$(echo "$VERIFY" | jq -r '.version' 2>/dev/null)
VERIFY_SKILLS=$(echo "$VERIFY" | jq '.skills | length' 2>/dev/null)
assert_eq "$VERIFY_VER" "2.0.0" "Updated version persisted"
assert_eq "${VERIFY_SKILLS:-0}" "2" "Updated skills count persisted"

step "3.8 List agents includes test agent"
LIST=$(http_get "$GATEWAY/api/agents")
LIST_IDS=$(echo "$LIST" | jq -r '.[].id' 2>/dev/null)
assert_contains "$LIST_IDS" "test-agent-1" "List includes test-agent-1"

step "3.9 Delete agent"
DEL_RESP=$(http_del "$GATEWAY/api/agents/test-agent-1")
DEL_ID=$(echo "$DEL_RESP" | jq -r '.deleted' 2>/dev/null)
assert_eq "$DEL_ID" "test-agent-1" "Delete returns deleted ID"

step "3.10 Delete nonexistent agent returns 404"
DEL_STATUS=$(http_status -X DELETE "$GATEWAY/api/agents/nonexistent-agent-xyz")
assert_eq "$DEL_STATUS" "404" "Deleting nonexistent returns 404"

step "3.11 Verify deletion"
GONE_STATUS=$(http_status "$GATEWAY/api/agents/test-agent-1")
assert_eq "$GONE_STATUS" "404" "Deleted agent returns 404"

# ============================================================================
section "4. AGENT HEALTH CHECKING"
# ============================================================================

step "4.1 Mock agents are active"
AGENTS=$(http_get "$GATEWAY/api/agents")
for AGENT_NAME in translator summarizer code-reviewer; do
  STATUS=$(echo "$AGENTS" | jq -r ".[] | select(.id==\"$AGENT_NAME\") | .status" 2>/dev/null)
  assert_eq "${STATUS:-missing}" "active" "Agent $AGENT_NAME is active"
done

step "4.2 Health endpoint for specific agent"
HEALTH=$(http_get "$GATEWAY/api/agents/translator/health")
H_STATUS=$(echo "$HEALTH" | jq -r '.status' 2>/dev/null)
assert_eq "$H_STATUS" "active" "/health returns active for translator"

step "4.3 Health endpoint for nonexistent agent"
H_MISS=$(http_status "$GATEWAY/api/agents/nonexistent-xyz/health")
assert_eq "$H_MISS" "404" "Health for missing agent returns 404"

step "4.4 Unreachable agent becomes unhealthy"
# Register an agent with a fake URL — health checker should mark it unhealthy
http_del "$GATEWAY/api/agents/fake-unhealthy" > /dev/null 2>&1
http_post "$GATEWAY/api/agents" '{"id":"fake-unhealthy","name":"Fake Unhealthy","description":"Will fail health checks","url":"http://192.0.2.1:9999","version":"1.0.0"}' > /dev/null
info "Waiting 15s for health checker to run..."
sleep 15
FAKE_STATUS=$(http_get "$GATEWAY/api/agents/fake-unhealthy" | jq -r '.status' 2>/dev/null)
assert_eq "$FAKE_STATUS" "unhealthy" "Unreachable agent marked unhealthy"
http_del "$GATEWAY/api/agents/fake-unhealthy" > /dev/null 2>&1

# ============================================================================
section "5. SEMANTIC DISCOVERY"
# ============================================================================

step "5.1 Discover — translation query"
D1=$(http_post "$GATEWAY/api/agents/discover" '{"query":"translate text to Japanese","top_n":3,"min_score":0.0}')
D1_TOP=$(echo "$D1" | jq -r '.[0].agent.name' 2>/dev/null)
D1_COUNT=$(echo "$D1" | jq 'length' 2>/dev/null)
assert_eq "$D1_TOP" "translator" "Translation query ranks translator first"
assert_ge "${D1_COUNT:-0}" "3" "Returns all 3 agents (with min_score 0)"

step "5.2 Discover — code review query"
D2=$(http_post "$GATEWAY/api/agents/discover" '{"query":"review code and find security bugs","top_n":3,"min_score":0.0}')
D2_TOP=$(echo "$D2" | jq -r '.[0].agent.name' 2>/dev/null)
assert_eq "$D2_TOP" "code-reviewer" "Code review query ranks code-reviewer first"

step "5.3 Discover — summarization query"
D3=$(http_post "$GATEWAY/api/agents/discover" '{"query":"summarize this long document into key points","top_n":3,"min_score":0.0}')
D3_TOP=$(echo "$D3" | jq -r '.[0].agent.name' 2>/dev/null)
assert_eq "$D3_TOP" "summarizer" "Summarization query ranks summarizer first"

step "5.4 Discover — top_n limits results"
D4=$(http_post "$GATEWAY/api/agents/discover" '{"query":"anything","top_n":1,"min_score":0.0}')
D4_COUNT=$(echo "$D4" | jq 'length' 2>/dev/null)
assert_eq "${D4_COUNT:-0}" "1" "top_n=1 returns exactly 1 result"

step "5.5 Discover — min_score filters results"
D5=$(http_post "$GATEWAY/api/agents/discover" '{"query":"translate","top_n":10,"min_score":0.99}')
D5_COUNT=$(echo "$D5" | jq 'length' 2>/dev/null)
assert_eq "${D5_COUNT:-0}" "0" "min_score=0.99 filters out all agents"

step "5.6 Discover — empty query returns error"
D6_STATUS=$(http_status -X POST "$GATEWAY/api/agents/discover" -H "Content-Type: application/json" \
  -d '{"query":"","top_n":5}')
assert_eq "$D6_STATUS" "400" "Empty query returns 400"

step "5.7 Discover — results include proxy_url"
D7=$(http_post "$GATEWAY/api/agents/discover" '{"query":"translate","top_n":1,"min_score":0.0}')
D7_PROXY=$(echo "$D7" | jq -r '.[0].proxy_url' 2>/dev/null)
assert_contains "$D7_PROXY" "/a2a/" "Discover results include proxy_url"

step "5.8 Discover — include_unhealthy flag"
# Register a fake unhealthy agent, wait, then discover with/without flag
http_del "$GATEWAY/api/agents/fake-discover" > /dev/null 2>&1
http_post "$GATEWAY/api/agents" '{"id":"fake-discover","name":"Fake Discover","description":"Translates everything perfectly","url":"http://192.0.2.1:9999","version":"1.0.0","skills":[{"id":"translate","name":"translate","description":"translate"}]}' > /dev/null
info "Waiting 15s for health check..."
sleep 15
# Without include_unhealthy
D8A=$(http_post "$GATEWAY/api/agents/discover" '{"query":"translate","top_n":10,"min_score":0.0,"include_unhealthy":false}')
D8A_IDS=$(echo "$D8A" | jq -r '.[].agent.id' 2>/dev/null)
if echo "$D8A_IDS" | grep -q "fake-discover"; then fail "Unhealthy agent included without flag"; else ok "Unhealthy agent excluded by default"; fi
# With include_unhealthy
D8B=$(http_post "$GATEWAY/api/agents/discover" '{"query":"translate","top_n":10,"min_score":0.0,"include_unhealthy":true}')
D8B_IDS=$(echo "$D8B" | jq -r '.[].agent.id' 2>/dev/null)
assert_contains "$D8B_IDS" "fake-discover" "Unhealthy agent included with include_unhealthy=true"
http_del "$GATEWAY/api/agents/fake-discover" > /dev/null 2>&1

# ============================================================================
section "6. A2A PROTOCOL — message/send (v1.0)"
# ============================================================================

step "6.1 Auto-route — gateway picks best agent"
R1=$(a2a_send "" '{
  "jsonrpc":"2.0","method":"message/send","id":101,
  "params":{"id":"auto-1","message":{"role":"user","parts":[{"type":"text","text":"Please translate hello to Spanish"}]}}
}')
R1_STATE=$(echo "$R1" | jq -r '.result.status.state' 2>/dev/null)
R1_ERR=$(echo "$R1" | jq -r '.error.message // empty' 2>/dev/null)
assert_eq "$R1_STATE" "completed" "Auto-routed task completed"
# Verify it went to translator (response should mention translator)
R1_TEXT=$(echo "$R1" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
info "Response: ${R1_TEXT:0:100}..."

step "6.2 Explicit route — /a2a/translator"
R2=$(a2a_send "/translator" '{
  "jsonrpc":"2.0","method":"message/send","id":102,
  "params":{"id":"explicit-1","message":{"role":"user","parts":[{"type":"text","text":"Translate hello to French"}]}}
}')
R2_STATE=$(echo "$R2" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$R2_STATE" "completed" "Explicit route to translator completed"

step "6.3 Explicit route — /a2a/summarizer"
R3=$(a2a_send "/summarizer" '{
  "jsonrpc":"2.0","method":"message/send","id":103,
  "params":{"id":"explicit-2","message":{"role":"user","parts":[{"type":"text","text":"Summarize: The sky is blue because of Rayleigh scattering"}]}}
}')
R3_STATE=$(echo "$R3" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$R3_STATE" "completed" "Explicit route to summarizer completed"

step "6.4 Explicit route — /a2a/code-reviewer"
R4=$(a2a_send "/code-reviewer" '{
  "jsonrpc":"2.0","method":"message/send","id":104,
  "params":{"id":"explicit-3","message":{"role":"user","parts":[{"type":"text","text":"Review: func main() { fmt.Println(42) }"}]}}
}')
R4_STATE=$(echo "$R4" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$R4_STATE" "completed" "Explicit route to code-reviewer completed"

step "6.5 Route to nonexistent agent"
R5=$(a2a_send "/nonexistent-xyz" '{
  "jsonrpc":"2.0","method":"message/send","id":105,
  "params":{"id":"bad-1","message":{"role":"user","parts":[{"type":"text","text":"hello"}]}}
}')
R5_ERR=$(echo "$R5" | jq -r '.error.message // empty' 2>/dev/null)
assert_contains "$R5_ERR" "not found" "Nonexistent agent returns error"

step "6.6 Empty message — routing fails"
R6=$(a2a_send "" '{
  "jsonrpc":"2.0","method":"message/send","id":106,
  "params":{"id":"bad-2","message":{"role":"user","parts":[]}}
}')
R6_ERR=$(echo "$R6" | jq -r '.error.message // empty' 2>/dev/null)
assert_not_empty "$R6_ERR" "Empty message returns error"

step "6.7 Auto-generated task ID"
R7=$(a2a_send "" '{
  "jsonrpc":"2.0","method":"message/send","id":107,
  "params":{"id":"","message":{"role":"user","parts":[{"type":"text","text":"translate hi to German"}]}}
}')
R7_TASK_ID=$(echo "$R7" | jq -r '.result.id // empty' 2>/dev/null)
assert_not_empty "$R7_TASK_ID" "Empty task ID gets auto-generated"

step "6.8 Invalid JSON-RPC version"
R8=$(a2a_send "" '{"jsonrpc":"1.0","method":"message/send","id":108,"params":{}}')
R8_ERR=$(echo "$R8" | jq -r '.error.message // empty' 2>/dev/null)
assert_contains "$R8_ERR" "2.0" "Invalid jsonrpc version returns error"

step "6.9 Unknown method"
R9=$(a2a_send "" '{"jsonrpc":"2.0","method":"unknown/method","id":109,"params":{}}')
R9_ERR=$(echo "$R9" | jq -r '.error.message // empty' 2>/dev/null)
assert_contains "$R9_ERR" "not found" "Unknown method returns error"

# ============================================================================
section "7. A2A PROTOCOL — BACKWARD COMPAT (v0.3)"
# ============================================================================

step "7.1 tasks/send still works (v0.3 compat)"
BC1=$(a2a_send "/translator" '{
  "jsonrpc":"2.0","method":"tasks/send","id":201,
  "params":{"id":"compat-1","message":{"role":"user","parts":[{"type":"text","text":"translate compat test"}]}}
}')
BC1_STATE=$(echo "$BC1" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$BC1_STATE" "completed" "tasks/send works (backward compat)"

step "7.2 tasks/sendSubscribe still accepted (v0.3 compat)"
BC2=$(curl -s --max-time 30 -X POST "$GATEWAY/a2a/translator" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"tasks/sendSubscribe","id":202,"params":{"id":"compat-stream","message":{"role":"user","parts":[{"type":"text","text":"compat stream test"}]}}}' 2>&1)
assert_contains "$BC2" "completed" "tasks/sendSubscribe works (backward compat)"

# ============================================================================
section "8. A2A PROTOCOL — TASK MANAGEMENT"
# ============================================================================

step "8.1 tasks/get — retrieve stored task"
TG=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/get","id":301,"params":{"id":"auto-1"}}')
TG_STATE=$(echo "$TG" | jq -r '.result.status.state // empty' 2>/dev/null)
if [ "$TG_STATE" = "completed" ]; then
  ok "tasks/get returns stored task (state=completed)"
else
  TG_ERR=$(echo "$TG" | jq -r '.error.message // empty' 2>/dev/null)
  fail "tasks/get failed: state='$TG_STATE' err='$TG_ERR'"
fi

step "8.2 tasks/get — nonexistent task"
TG2=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/get","id":302,"params":{"id":"nonexistent-task-xyz"}}')
TG2_ERR=$(echo "$TG2" | jq -r '.error.message // empty' 2>/dev/null)
assert_contains "$TG2_ERR" "not found" "tasks/get for missing task returns error"

step "8.3 tasks/list — returns all tasks"
TL=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/list","id":303}')
TL_COUNT=$(echo "$TL" | jq '.result | length' 2>/dev/null)
assert_ge "${TL_COUNT:-0}" "3" "tasks/list returns >= 3 tasks ($TL_COUNT found)"

step "8.4 tasks/list — filter by state"
TL2=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/list","id":304,"params":{"state":"completed"}}')
TL2_COUNT=$(echo "$TL2" | jq '.result | length' 2>/dev/null)
assert_ge "${TL2_COUNT:-0}" "1" "tasks/list with state=completed returns results"

step "8.5 tasks/cancel"
# First create a task to cancel
a2a_send "/translator" '{"jsonrpc":"2.0","method":"message/send","id":305,"params":{"id":"to-cancel","message":{"role":"user","parts":[{"type":"text","text":"cancel me"}]}}}' > /dev/null
TC=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/cancel","id":306,"params":{"id":"to-cancel"}}')
TC_STATE=$(echo "$TC" | jq -r '.result.status.state // empty' 2>/dev/null)
assert_eq "$TC_STATE" "canceled" "tasks/cancel sets state to canceled"

step "8.6 tasks/cancel — nonexistent task"
TC2=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/cancel","id":307,"params":{"id":"nonexistent-xyz"}}')
TC2_ERR=$(echo "$TC2" | jq -r '.error.message // empty' 2>/dev/null)
assert_contains "$TC2_ERR" "not found" "Cancel nonexistent task returns error"

# ============================================================================
section "9. A2A PROTOCOL — STREAMING (message/stream)"
# ============================================================================

step "9.1 message/stream returns SSE events"
STREAM=$(curl -s --max-time 30 -X POST "$GATEWAY/a2a/translator" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","method":"message/stream","id":401,"params":{"id":"stream-1","message":{"role":"user","parts":[{"type":"text","text":"translate stream test to French"}]}}}' 2>&1)
assert_contains "$STREAM" "event: status" "SSE stream contains status events"
assert_contains "$STREAM" "completed" "SSE stream ends with completed state"

# ============================================================================
section "10. AGENT-TO-AGENT DELEGATION"
# ============================================================================

step "10.1 Code-reviewer delegates to translator (keyword: translate)"
info "Sending task mentioning 'translate' to code-reviewer..."
info "Expected: code-reviewer does review → discovers translator → calls via proxy → combines"
DEL=$(a2a_send "/code-reviewer" '{
  "jsonrpc":"2.0","method":"message/send","id":501,
  "params":{"id":"delegate-1","message":{"role":"user","parts":[{"type":"text","text":"Review this code and translate your review to Japanese: func add(a, b int) int { return a + b }"}]}}
}')
DEL_STATE=$(echo "$DEL" | jq -r '.result.status.state' 2>/dev/null)
DEL_TEXT=$(echo "$DEL" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
assert_eq "$DEL_STATE" "completed" "Delegation task completed"
assert_contains "$DEL_TEXT" "code-reviewer result" "Response contains code-reviewer's own work"
assert_contains "$DEL_TEXT" "Delegated result" "Response contains delegation section"

step "10.2 Delegation actually reached translator"
# Check if the delegated part is NOT a failure message
if echo "$DEL_TEXT" | grep -q "delegation failed"; then
  DEL_ERR=$(echo "$DEL_TEXT" | grep -o '\[delegation failed:.*\]')
  fail "Delegation failed: $DEL_ERR"
else
  ok "Delegation to translator succeeded (no failure message)"
fi

step "10.3 No delegation when keyword absent"
NO_DEL=$(a2a_send "/code-reviewer" '{
  "jsonrpc":"2.0","method":"message/send","id":502,
  "params":{"id":"no-delegate","message":{"role":"user","parts":[{"type":"text","text":"Review this function: func add(a, b int) int { return a + b }"}]}}
}')
NO_DEL_TEXT=$(echo "$NO_DEL" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
if echo "$NO_DEL_TEXT" | grep -q "Delegated result"; then
  fail "Delegation triggered without keyword"
else
  ok "No delegation when keyword absent"
fi

# ============================================================================
section "11. LLM PROXY"
# ============================================================================

step "11.1 /v1/chat/completions basic request"
LLM=$(http_post "$GATEWAY/v1/chat/completions" '{
  "model":"google/gemma-4-26b-a4b-it:free",
  "messages":[{"role":"user","content":"Say hello in one word"}],
  "max_tokens":10
}')
LLM_CONTENT=$(echo "$LLM" | jq -r '.choices[0].message.content // empty' 2>/dev/null)
if [ -n "$LLM_CONTENT" ] && [ "$LLM_CONTENT" != "null" ]; then
  ok "LLM proxy returns response: ${LLM_CONTENT:0:50}"
else
  LLM_ERR=$(echo "$LLM" | jq -r '.error // empty' 2>/dev/null)
  if [ -n "$LLM_ERR" ]; then
    skip "LLM call failed (provider may be down): $LLM_ERR"
  else
    fail "LLM proxy returned empty response"
  fi
fi

step "11.2 /v1/chat/completions — unknown model"
LLM2_STATUS=$(http_status -X POST "$GATEWAY/v1/chat/completions" -H "Content-Type: application/json" \
  -d '{"model":"nonexistent/model-xyz","messages":[{"role":"user","content":"hi"}]}')
assert_eq "$LLM2_STATUS" "404" "Unknown model returns 404"

# ============================================================================
section "12. STATS"
# ============================================================================

step "12.1 Stats endpoint returns data"
STATS=$(http_get "$GATEWAY/api/stats")
STATS_TOTAL=$(echo "$STATS" | jq -r '.total_requests // 0' 2>/dev/null)
assert_ge "${STATS_TOTAL:-0}" "1" "Stats show requests recorded ($STATS_TOTAL total)"

step "12.2 Stats include provider breakdown"
BY_PROV=$(echo "$STATS" | jq '.by_provider | length' 2>/dev/null)
assert_ge "${BY_PROV:-0}" "1" "Stats have by_provider data"

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
