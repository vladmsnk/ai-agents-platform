#!/usr/bin/env bash
# ============================================================================
# Advanced E2E tests — complex scenarios, cross-agent flows, edge cases
#
# Run AFTER test-a2a-e2e.sh passes. Requires all 8 agents registered.
# Usage: ./scripts/test-a2a-advanced.sh [gateway_url]
# ============================================================================
set -uo pipefail

GATEWAY="${1:-http://localhost:8080}"
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

http_post() { curl -s --max-time "${3:-60}" -X POST "$1" -H "Content-Type: application/json" -d "$2" 2>&1; }
http_get()  { curl -s --max-time 10 "$1" 2>&1; }
http_del()  { curl -s --max-time 10 -X DELETE "$1" 2>&1; }
http_put()  { curl -s --max-time 10 -X PUT "$1" -H "Content-Type: application/json" -d "$2" 2>&1; }
http_status() { curl -s -o /dev/null -w "%{http_code}" --max-time 10 "$@" 2>&1; }
a2a_send()  { http_post "$GATEWAY/a2a$1" "$2" "${3:-60}"; }

assert_eq() { if [ "$1" = "$2" ]; then ok "$3"; else fail "$3 (expected '$2', got '$1')"; fi; }
assert_contains() { if echo "$1" | grep -q "$2" 2>/dev/null; then ok "$3"; else fail "$3 (missing '$2')"; fi; }
assert_not_contains() { if echo "$1" | grep -q "$2" 2>/dev/null; then fail "$3 (unexpectedly contains '$2')"; else ok "$3"; fi; }
assert_not_empty() { if [ -n "$1" ] && [ "$1" != "null" ]; then ok "$2"; else fail "$2 (empty/null)"; fi; }
assert_ge() { if [ "$1" -ge "$2" ] 2>/dev/null; then ok "$3"; else fail "$3 (expected >= $2, got '$1')"; fi; }
assert_gt_float() {
  local result
  result=$(echo "$1 $2" | awk '{print ($1 > $2) ? "yes" : "no"}')
  if [ "$result" = "yes" ]; then ok "$3"; else fail "$3 ($1 not > $2)"; fi
}

# ============================================================================
section "PREFLIGHT — Verify all 8 agents are registered"
# ============================================================================

AGENTS=$(http_get "$GATEWAY/api/agents")
AGENT_COUNT=$(echo "$AGENTS" | jq 'length' 2>/dev/null || echo 0)
EXPECTED_AGENTS="translator summarizer code-reviewer sentiment-analyzer data-extractor content-writer math-solver security-scanner"
echo "  Found $AGENT_COUNT agents"
for A in $EXPECTED_AGENTS; do
  EXISTS=$(echo "$AGENTS" | jq -r ".[] | select(.id==\"$A\") | .name" 2>/dev/null)
  if [ -n "$EXISTS" ]; then
    echo -e "  ${GREEN}✓${NC} $A"
  else
    echo -e "  ${RED}✗ MISSING: $A${NC}"
    FAIL=$((FAIL + 1))
  fi
done
if [ "$AGENT_COUNT" -lt 8 ]; then
  echo -e "\n${RED}Not all agents registered. Run: docker-compose up --build -d${NC}"
  echo "Continuing anyway — some tests may fail."
fi

# ============================================================================
section "1. SEMANTIC DISCOVERY — RANKING ACCURACY (8 agents)"
# ============================================================================

run_discover() {
  local query="$1" expected_top="$2" label="$3"
  local resp
  resp=$(http_post "$GATEWAY/api/agents/discover" "{\"query\":\"$query\",\"top_n\":8,\"min_score\":0.0}")
  local top
  top=$(echo "$resp" | jq -r '.[0].agent.name' 2>/dev/null)
  local top_score
  top_score=$(echo "$resp" | jq -r '.[0].score' 2>/dev/null)
  if [ "$top" = "$expected_top" ]; then
    ok "$label → $top (score: $top_score)"
  else
    fail "$label → got '$top' (expected '$expected_top', score: $top_score)"
    info "Full ranking:"
    echo "$resp" | jq -r '.[] | "    \(.agent.name): \(.score)"' 2>/dev/null
  fi
}

step "1.1 Domain-specific queries — each agent ranks first for its specialty"
run_discover "translate this document from English to Japanese" "translator" "Translation query"
run_discover "summarize the quarterly earnings report" "summarizer" "Summarization query"
run_discover "review my Python code for bugs" "code-reviewer" "Code review query"
run_discover "what is the sentiment of this customer review" "sentiment-analyzer" "Sentiment query"
run_discover "extract all email addresses and phone numbers from this text" "data-extractor" "Data extraction query"
run_discover "write a blog post about artificial intelligence" "content-writer" "Content writing query"
run_discover "solve this integral: integral of x^2 dx" "math-solver" "Math query"
run_discover "scan this code for SQL injection vulnerabilities" "security-scanner" "Security scan query"

step "1.2 Ambiguous queries — verify reasonable ranking"
# "analyze this text" could be sentiment, summarizer, or data-extractor
AMBIG1=$(http_post "$GATEWAY/api/agents/discover" '{"query":"analyze this text for insights","top_n":3,"min_score":0.0}')
AMBIG1_TOP=$(echo "$AMBIG1" | jq -r '.[0].agent.name' 2>/dev/null)
# Any of these three is reasonable
if echo "sentiment-analyzer data-extractor summarizer" | grep -q "$AMBIG1_TOP"; then
  ok "Ambiguous 'analyze text' → $AMBIG1_TOP (reasonable)"
else
  fail "Ambiguous 'analyze text' → $AMBIG1_TOP (unexpected)"
fi

# "fix the security issues in my code" — security-scanner or code-reviewer
AMBIG2=$(http_post "$GATEWAY/api/agents/discover" '{"query":"fix the security issues in my code","top_n":2,"min_score":0.0}')
AMBIG2_NAMES=$(echo "$AMBIG2" | jq -r '.[].agent.name' 2>/dev/null | tr '\n' ',')
if echo "$AMBIG2_NAMES" | grep -q "security-scanner\|code-reviewer"; then
  ok "Ambiguous 'fix security' → top 2 includes security or code-review ($AMBIG2_NAMES)"
else
  fail "Ambiguous 'fix security' → unexpected ranking: $AMBIG2_NAMES"
fi

step "1.3 Non-English query"
NE=$(http_post "$GATEWAY/api/agents/discover" '{"query":"この文章を要約してください","top_n":3,"min_score":0.0}')
NE_TOP=$(echo "$NE" | jq -r '.[0].agent.name' 2>/dev/null)
NE_SCORE=$(echo "$NE" | jq -r '.[0].score' 2>/dev/null)
assert_not_empty "$NE_TOP" "Non-English query returns results (top: $NE_TOP, score: $NE_SCORE)"

step "1.4 Very long query"
LONG_Q=$(printf 'summarize this: %.0s' {1..50})"This is a very long document that needs to be summarized into key points."
LONG=$(http_post "$GATEWAY/api/agents/discover" "{\"query\":\"$LONG_Q\",\"top_n\":1,\"min_score\":0.0}")
LONG_TOP=$(echo "$LONG" | jq -r '.[0].agent.name' 2>/dev/null)
assert_not_empty "$LONG_TOP" "Long query still returns results (top: $LONG_TOP)"

step "1.5 Discovery score ordering is strict descending"
ORDERED=$(http_post "$GATEWAY/api/agents/discover" '{"query":"translate and summarize","top_n":8,"min_score":0.0}')
SCORES=$(echo "$ORDERED" | jq -r '.[].score' 2>/dev/null)
IS_SORTED=$(echo "$SCORES" | awk 'NR>1 && $1 > prev { print "NO"; exit } { prev=$1 } END { print "YES" }')
assert_eq "$IS_SORTED" "YES" "Discover results sorted by descending score"

# ============================================================================
section "2. CROSS-AGENT DELEGATION CHAINS"
# ============================================================================

step "2.1 content-writer delegates translation to translator"
info "content-writer has DELEGATE_KEYWORD=translate → discovers translator → calls via proxy"
CW=$(a2a_send "/content-writer" '{
  "jsonrpc":"2.0","method":"message/send","id":1001,
  "params":{"id":"cw-delegate-1","message":{"role":"user","parts":[{"type":"text","text":"Write a product description and then translate it to French"}]}}
}' 120)
CW_STATE=$(echo "$CW" | jq -r '.result.status.state' 2>/dev/null)
CW_TEXT=$(echo "$CW" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
assert_eq "$CW_STATE" "completed" "content-writer → translator delegation completed"
assert_contains "$CW_TEXT" "content-writer result" "Response has content-writer's own work"
assert_contains "$CW_TEXT" "Delegated result" "Response has delegated section"
assert_not_contains "$CW_TEXT" "delegation failed" "Delegation to translator succeeded"

step "2.2 security-scanner delegates summary to summarizer"
info "security-scanner has DELEGATE_KEYWORD=summarize → discovers summarizer → calls via proxy"
SS=$(a2a_send "/security-scanner" '{
  "jsonrpc":"2.0","method":"message/send","id":1002,
  "params":{"id":"ss-delegate-1","message":{"role":"user","parts":[{"type":"text","text":"Scan this code for vulnerabilities and summarize your findings: SELECT * FROM users WHERE id = $input"}]}}
}' 120)
SS_STATE=$(echo "$SS" | jq -r '.result.status.state' 2>/dev/null)
SS_TEXT=$(echo "$SS" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
assert_eq "$SS_STATE" "completed" "security-scanner → summarizer delegation completed"
assert_contains "$SS_TEXT" "security-scanner result" "Response has security-scanner's work"
assert_contains "$SS_TEXT" "Delegated result" "Response has delegated summary"
assert_not_contains "$SS_TEXT" "delegation failed" "Delegation to summarizer succeeded"

step "2.3 code-reviewer delegates to translator (existing test — verify still works)"
CR=$(a2a_send "/code-reviewer" '{
  "jsonrpc":"2.0","method":"message/send","id":1003,
  "params":{"id":"cr-delegate-1","message":{"role":"user","parts":[{"type":"text","text":"Review func main(){} and translate findings to Spanish"}]}}
}' 120)
CR_STATE=$(echo "$CR" | jq -r '.result.status.state' 2>/dev/null)
CR_TEXT=$(echo "$CR" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
assert_eq "$CR_STATE" "completed" "code-reviewer → translator delegation completed"
assert_not_contains "$CR_TEXT" "delegation failed" "code-reviewer delegation succeeded"

step "2.4 Agent without delegation keyword — no delegation triggered"
for AGENT in translator summarizer sentiment-analyzer data-extractor math-solver; do
  NODL=$(a2a_send "/$AGENT" "{
    \"jsonrpc\":\"2.0\",\"method\":\"message/send\",\"id\":1010,
    \"params\":{\"id\":\"nodl-$AGENT\",\"message\":{\"role\":\"user\",\"parts\":[{\"type\":\"text\",\"text\":\"do something for me translate summarize\"}]}}
  }" 30)
  NODL_TEXT=$(echo "$NODL" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
  if echo "$NODL_TEXT" | grep -q "Delegated result"; then
    fail "$AGENT should NOT delegate but did"
  else
    ok "$AGENT correctly did NOT delegate"
  fi
done

# ============================================================================
section "3. AUTO-ROUTING ACCURACY (8 agents)"
# ============================================================================

step "3.1 Auto-route picks correct agent for each domain"

verify_auto_route() {
  local text="$1" expected_agent="$2" label="$3"
  local resp
  resp=$(a2a_send "" "{
    \"jsonrpc\":\"2.0\",\"method\":\"message/send\",\"id\":2001,
    \"params\":{\"id\":\"ar-$RANDOM\",\"message\":{\"role\":\"user\",\"parts\":[{\"type\":\"text\",\"text\":\"$text\"}]}}
  }" 30)
  local resp_text
  resp_text=$(echo "$resp" | jq -r '.result.artifacts[0].parts[0].text // empty' 2>/dev/null)
  if echo "$resp_text" | grep -qi "$expected_agent"; then
    ok "Auto-route '$label' → $expected_agent"
  else
    # Check which agent actually handled it
    local state
    state=$(echo "$resp" | jq -r '.result.status.state // empty' 2>/dev/null)
    local err
    err=$(echo "$resp" | jq -r '.error.message // empty' 2>/dev/null)
    if [ "$state" = "completed" ]; then
      info "Routed to different agent: ${resp_text:0:80}"
      fail "Auto-route '$label' → expected $expected_agent"
    else
      fail "Auto-route '$label' failed: state=$state err=$err"
    fi
  fi
}

verify_auto_route "translate this email to German" "translator" "translation task"
verify_auto_route "what is the sentiment of: I love this product" "sentiment" "sentiment task"
verify_auto_route "extract all dates from this contract" "data-extractor" "extraction task"
verify_auto_route "write me a catchy slogan for a coffee shop" "content-writer" "writing task"
verify_auto_route "calculate the derivative of x^3 + 2x" "math-solver" "math task"
verify_auto_route "check this for XSS vulnerabilities" "security-scanner" "security task"

# ============================================================================
section "4. CONCURRENT REQUESTS"
# ============================================================================

step "4.1 Parallel A2A calls to different agents"
info "Sending 8 requests simultaneously to all agents..."
PIDS=()
TMPDIR_CONC=$(mktemp -d)
for AGENT in translator summarizer code-reviewer sentiment-analyzer data-extractor content-writer math-solver security-scanner; do
  (
    RESP=$(a2a_send "/$AGENT" "{
      \"jsonrpc\":\"2.0\",\"method\":\"message/send\",\"id\":3001,
      \"params\":{\"id\":\"conc-$AGENT\",\"message\":{\"role\":\"user\",\"parts\":[{\"type\":\"text\",\"text\":\"concurrent test for $AGENT\"}]}}
    }" 30)
    STATE=$(echo "$RESP" | jq -r '.result.status.state // "error"' 2>/dev/null)
    echo "$STATE" > "$TMPDIR_CONC/$AGENT"
  ) &
  PIDS+=($!)
done

# Wait for all
for PID in "${PIDS[@]}"; do wait "$PID" 2>/dev/null; done

CONC_PASS=0
CONC_FAIL=0
for AGENT in translator summarizer code-reviewer sentiment-analyzer data-extractor content-writer math-solver security-scanner; do
  STATE=$(cat "$TMPDIR_CONC/$AGENT" 2>/dev/null || echo "missing")
  if [ "$STATE" = "completed" ]; then
    CONC_PASS=$((CONC_PASS + 1))
  else
    CONC_FAIL=$((CONC_FAIL + 1))
    info "$AGENT returned state=$STATE"
  fi
done
rm -rf "$TMPDIR_CONC"
assert_eq "$CONC_PASS" "8" "All 8 concurrent requests completed ($CONC_PASS/8)"

step "4.2 Parallel discovery calls"
info "Sending 5 discover requests simultaneously..."
PIDS2=()
TMPDIR_DISC=$(mktemp -d)
QUERIES=("translate to french" "summarize the report" "solve equation" "scan for vulnerabilities" "write marketing copy")
for i in "${!QUERIES[@]}"; do
  (
    RESP=$(http_post "$GATEWAY/api/agents/discover" "{\"query\":\"${QUERIES[$i]}\",\"top_n\":1,\"min_score\":0.0}" 10)
    echo "$RESP" | jq -r '.[0].agent.name // "error"' > "$TMPDIR_DISC/$i" 2>/dev/null
  ) &
  PIDS2+=($!)
done
for PID in "${PIDS2[@]}"; do wait "$PID" 2>/dev/null; done

DISC_OK=0
for i in "${!QUERIES[@]}"; do
  NAME=$(cat "$TMPDIR_DISC/$i" 2>/dev/null || echo "error")
  if [ "$NAME" != "error" ] && [ "$NAME" != "null" ]; then DISC_OK=$((DISC_OK + 1)); fi
done
rm -rf "$TMPDIR_DISC"
assert_eq "$DISC_OK" "5" "All 5 concurrent discover calls returned results"

# ============================================================================
section "5. DYNAMIC REGISTRATION + DISCOVERY"
# ============================================================================

step "5.1 Register new agent → immediately discoverable"
http_del "$GATEWAY/api/agents/dynamic-test" > /dev/null 2>&1
http_post "$GATEWAY/api/agents" '{
  "id":"dynamic-test","name":"dynamic-test",
  "description":"Expert at quantum physics and astrophysics calculations",
  "url":"http://192.0.2.1:9999","version":"1.0.0",
  "skills":[{"id":"quantum","name":"quantum physics","description":"quantum mechanics calculations"}]
}' > /dev/null
# Discover immediately
DYN=$(http_post "$GATEWAY/api/agents/discover" '{"query":"calculate quantum entanglement","top_n":1,"min_score":0.0}')
DYN_TOP=$(echo "$DYN" | jq -r '.[0].agent.name' 2>/dev/null)
assert_eq "$DYN_TOP" "dynamic-test" "Freshly registered agent is immediately discoverable"

step "5.2 Update agent description → re-ranks in discovery"
http_put "$GATEWAY/api/agents/dynamic-test" '{
  "name":"dynamic-test",
  "description":"Expert at cooking Italian food and recipes. Makes pasta, pizza, risotto.",
  "url":"http://192.0.2.1:9999","version":"2.0.0",
  "skills":[{"id":"cooking","name":"Italian cooking","description":"Italian recipes and cooking"}]
}' > /dev/null
# Old query should no longer match well
DYN2=$(http_post "$GATEWAY/api/agents/discover" '{"query":"calculate quantum entanglement","top_n":1,"min_score":0.0}')
DYN2_TOP=$(echo "$DYN2" | jq -r '.[0].agent.name' 2>/dev/null)
if [ "$DYN2_TOP" != "dynamic-test" ]; then
  ok "After description change, quantum query no longer matches dynamic-test (now: $DYN2_TOP)"
else
  fail "After description change, dynamic-test still matches quantum query"
fi
# New query should match
DYN3=$(http_post "$GATEWAY/api/agents/discover" '{"query":"how to make carbonara pasta","top_n":1,"min_score":0.0}')
DYN3_TOP=$(echo "$DYN3" | jq -r '.[0].agent.name' 2>/dev/null)
assert_eq "$DYN3_TOP" "dynamic-test" "Updated description matches new domain (cooking)"

step "5.3 Delete agent → no longer discoverable"
http_del "$GATEWAY/api/agents/dynamic-test" > /dev/null
DYN4=$(http_post "$GATEWAY/api/agents/discover" '{"query":"how to make carbonara pasta","top_n":8,"min_score":0.0}')
DYN4_IDS=$(echo "$DYN4" | jq -r '.[].agent.id' 2>/dev/null)
assert_not_contains "$DYN4_IDS" "dynamic-test" "Deleted agent no longer in discover results"

# ============================================================================
section "6. TASK LIFECYCLE & STATE MANAGEMENT"
# ============================================================================

step "6.1 Full lifecycle: create → get → cancel → get"
# Create
LC=$(a2a_send "/translator" '{
  "jsonrpc":"2.0","method":"message/send","id":4001,
  "params":{"id":"lifecycle-1","message":{"role":"user","parts":[{"type":"text","text":"lifecycle test"}]}}
}' 30)
LC_STATE=$(echo "$LC" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$LC_STATE" "completed" "Task created with completed state"
# Get
LC_GET=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/get","id":4002,"params":{"id":"lifecycle-1"}}')
LC_GET_STATE=$(echo "$LC_GET" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$LC_GET_STATE" "completed" "tasks/get returns completed"
# Cancel
LC_CANCEL=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/cancel","id":4003,"params":{"id":"lifecycle-1"}}')
LC_CANCEL_STATE=$(echo "$LC_CANCEL" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$LC_CANCEL_STATE" "canceled" "tasks/cancel changes state to canceled"
# Get again — should be canceled
LC_GET2=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/get","id":4004,"params":{"id":"lifecycle-1"}}')
LC_GET2_STATE=$(echo "$LC_GET2" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$LC_GET2_STATE" "canceled" "tasks/get after cancel returns canceled"

step "6.2 tasks/list state filter works"
# Send a few tasks to different agents
a2a_send "/translator" '{"jsonrpc":"2.0","method":"message/send","id":4010,"params":{"id":"filter-1","message":{"role":"user","parts":[{"type":"text","text":"a"}]}}}' 30 > /dev/null
a2a_send "/summarizer" '{"jsonrpc":"2.0","method":"message/send","id":4011,"params":{"id":"filter-2","message":{"role":"user","parts":[{"type":"text","text":"b"}]}}}' 30 > /dev/null
# Cancel one
a2a_send "" '{"jsonrpc":"2.0","method":"tasks/cancel","id":4012,"params":{"id":"filter-1"}}' > /dev/null
# Filter completed
FILT_C=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/list","id":4013,"params":{"state":"completed"}}')
FILT_C_COUNT=$(echo "$FILT_C" | jq '.result | length' 2>/dev/null)
assert_ge "${FILT_C_COUNT:-0}" "1" "Filter state=completed returns results ($FILT_C_COUNT)"
# Filter canceled
FILT_X=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/list","id":4014,"params":{"state":"canceled"}}')
FILT_X_COUNT=$(echo "$FILT_X" | jq '.result | length' 2>/dev/null)
assert_ge "${FILT_X_COUNT:-0}" "1" "Filter state=canceled returns results ($FILT_X_COUNT)"
# Filter nonexistent state
FILT_N=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/list","id":4015,"params":{"state":"nonexistent"}}')
FILT_N_COUNT=$(echo "$FILT_N" | jq '.result | length' 2>/dev/null)
assert_eq "${FILT_N_COUNT:-0}" "0" "Filter nonexistent state returns 0 tasks"

# ============================================================================
section "7. EDGE CASES & ERROR HANDLING"
# ============================================================================

step "7.1 Very large message payload"
BIG_TEXT=$(python3 -c "print('word ' * 2000)" 2>/dev/null || printf 'word %.0s' {1..2000})
BIG=$(a2a_send "/summarizer" "{
  \"jsonrpc\":\"2.0\",\"method\":\"message/send\",\"id\":5001,
  \"params\":{\"id\":\"big-1\",\"message\":{\"role\":\"user\",\"parts\":[{\"type\":\"text\",\"text\":\"Summarize: $BIG_TEXT\"}]}}
}" 30)
BIG_STATE=$(echo "$BIG" | jq -r '.result.status.state // empty' 2>/dev/null)
assert_eq "$BIG_STATE" "completed" "Large payload handled successfully"

step "7.2 Special characters in message"
SPEC=$(a2a_send "/translator" '{
  "jsonrpc":"2.0","method":"message/send","id":5002,
  "params":{"id":"spec-1","message":{"role":"user","parts":[{"type":"text","text":"Translate: Hello \"world\" <b>bold</b> & special chars: é ñ ü 日本語 🎉"}]}}
}')
SPEC_STATE=$(echo "$SPEC" | jq -r '.result.status.state // empty' 2>/dev/null)
assert_eq "$SPEC_STATE" "completed" "Special characters handled"

step "7.3 Multiple text parts in message"
MULTI=$(a2a_send "/translator" '{
  "jsonrpc":"2.0","method":"message/send","id":5003,
  "params":{"id":"multi-1","message":{"role":"user","parts":[
    {"type":"text","text":"Translate the following:"},
    {"type":"text","text":"Hello, how are you?"},
    {"type":"text","text":"Target language: Japanese"}
  ]}}
}')
MULTI_STATE=$(echo "$MULTI" | jq -r '.result.status.state // empty' 2>/dev/null)
assert_eq "$MULTI_STATE" "completed" "Multiple text parts handled"

step "7.4 Rapid sequential calls to same agent"
info "Sending 5 rapid calls to math-solver..."
RAPID_OK=0
for i in 1 2 3 4 5; do
  R=$(a2a_send "/math-solver" "{
    \"jsonrpc\":\"2.0\",\"method\":\"message/send\",\"id\":500$i,
    \"params\":{\"id\":\"rapid-$i\",\"message\":{\"role\":\"user\",\"parts\":[{\"type\":\"text\",\"text\":\"$i + $i = ?\"}]}}
  }" 15)
  RS=$(echo "$R" | jq -r '.result.status.state // empty' 2>/dev/null)
  if [ "$RS" = "completed" ]; then RAPID_OK=$((RAPID_OK + 1)); fi
done
assert_eq "$RAPID_OK" "5" "All 5 rapid sequential calls completed"

step "7.5 GET method to /a2a returns 405"
GET_A2A=$(http_status "$GATEWAY/a2a")
assert_eq "$GET_A2A" "405" "GET /a2a returns 405 Method Not Allowed"

step "7.6 Invalid JSON body to /a2a"
INV=$(curl -s -X POST "$GATEWAY/a2a" -H "Content-Type: application/json" -d '{bad json}' 2>&1)
INV_ERR=$(echo "$INV" | jq -r '.error.message // empty' 2>/dev/null)
assert_not_empty "$INV_ERR" "Invalid JSON returns error"

step "7.7 Malformed params"
MAL=$(a2a_send "" '{"jsonrpc":"2.0","method":"message/send","id":5010,"params":"not an object"}')
MAL_ERR=$(echo "$MAL" | jq -r '.error.message // empty' 2>/dev/null)
assert_not_empty "$MAL_ERR" "Malformed params returns error"

# ============================================================================
section "8. MIXED PROTOCOL VERSION CALLS"
# ============================================================================

step "8.1 Alternating v0.3 and v1.0 calls"
V03=$(a2a_send "/translator" '{
  "jsonrpc":"2.0","method":"tasks/send","id":6001,
  "params":{"id":"mixed-v03","message":{"role":"user","parts":[{"type":"text","text":"v0.3 call"}]}}
}')
V10=$(a2a_send "/translator" '{
  "jsonrpc":"2.0","method":"message/send","id":6002,
  "params":{"id":"mixed-v10","message":{"role":"user","parts":[{"type":"text","text":"v1.0 call"}]}}
}')
V03_STATE=$(echo "$V03" | jq -r '.result.status.state' 2>/dev/null)
V10_STATE=$(echo "$V10" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$V03_STATE" "completed" "v0.3 tasks/send completed"
assert_eq "$V10_STATE" "completed" "v1.0 message/send completed"

step "8.2 Both tasks retrievable via tasks/get"
GET_V03=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/get","id":6003,"params":{"id":"mixed-v03"}}')
GET_V10=$(a2a_send "" '{"jsonrpc":"2.0","method":"tasks/get","id":6004,"params":{"id":"mixed-v10"}}')
GET_V03_S=$(echo "$GET_V03" | jq -r '.result.status.state' 2>/dev/null)
GET_V10_S=$(echo "$GET_V10" | jq -r '.result.status.state' 2>/dev/null)
assert_eq "$GET_V03_S" "completed" "v0.3 task retrievable"
assert_eq "$GET_V10_S" "completed" "v1.0 task retrievable"

# ============================================================================
section "9. OBSERVABILITY"
# ============================================================================

step "9.1 Stats reflect test activity"
STATS=$(http_get "$GATEWAY/api/stats")
TOTAL_REQ=$(echo "$STATS" | jq -r '.total_requests // 0' 2>/dev/null)
assert_ge "${TOTAL_REQ:-0}" "20" "Stats total_requests >= 20 (got $TOTAL_REQ)"

step "9.2 Stats have model breakdown"
BY_MODEL=$(echo "$STATS" | jq '.by_model | length' 2>/dev/null)
assert_ge "${BY_MODEL:-0}" "1" "Stats have by_model data ($BY_MODEL models)"

step "9.3 Provider health"
HEALTH=$(http_get "$GATEWAY/api/health")
HEALTH_COUNT=$(echo "$HEALTH" | jq 'keys | length' 2>/dev/null)
assert_ge "${HEALTH_COUNT:-0}" "1" "Health endpoint has provider data ($HEALTH_COUNT providers)"

# ============================================================================
section "SUMMARY"
# ============================================================================

TOTAL=$((PASS + FAIL))
echo ""
echo -e "${BLUE}══════════════════════════════════════════════════════════════${NC}"
echo -e "  Total:   $TOTAL tests"
echo -e "  ${GREEN}Passed:  $PASS${NC}"
if [ "$FAIL" -gt 0 ]; then echo -e "  ${RED}Failed:  $FAIL${NC}"; fi
if [ "$SKIP" -gt 0 ]; then echo -e "  ${YELLOW}Skipped: $SKIP${NC}"; fi
echo -e "${BLUE}══════════════════════════════════════════════════════════════${NC}"

if [ "$FAIL" -gt 0 ]; then exit 1; fi
