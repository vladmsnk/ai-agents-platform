#!/usr/bin/env bash
# Generates realistic LLM traffic to populate monitoring metrics.
# Usage: ./scripts/load-test.sh [requests] [concurrency]
#   requests    — total requests to send (default: 30)
#   concurrency — parallel workers (default: 3)

set -euo pipefail

GATEWAY="http://localhost:8080"
TOTAL=${1:-30}
CONCURRENCY=${2:-3}

MODELS=(
  "google/gemma-4-26b-a4b-it:free"
  "openai/gpt-4o-mini"
  "anthropic/claude-3.5-haiku"
)

PROMPTS=(
  "Explain what a load balancer does in 2 sentences."
  "What is the capital of France? Reply in one word."
  "Write a haiku about distributed systems."
  "List 3 benefits of microservices architecture."
  "What is the difference between TCP and UDP? Be brief."
  "Explain REST API in simple terms."
  "What is Docker? One paragraph."
  "Name 3 popular programming languages and their main use case."
  "What is CI/CD? Explain in 2 sentences."
  "Describe the CAP theorem briefly."
)

sent=0
errors=0
ok=0

send_request() {
  local model=${MODELS[$((RANDOM % ${#MODELS[@]}))]}
  local prompt=${PROMPTS[$((RANDOM % ${#PROMPTS[@]}))]}
  local stream="false"
  # 30% of requests are streaming
  if (( RANDOM % 10 < 3 )); then
    stream="true"
  fi

  local payload
  payload=$(cat <<EOF
{
  "model": "$model",
  "stream": $stream,
  "max_tokens": 100,
  "messages": [{"role": "user", "content": "$prompt"}]
}
EOF
)

  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "$GATEWAY/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "$payload" \
    --max-time 30 2>/dev/null || echo "000")

  if [[ "$status" == 2* ]]; then
    echo "  ✓ $model (stream=$stream) → $status"
  else
    echo "  ✗ $model (stream=$stream) → $status"
  fi
}

echo "═══════════════════════════════════════════"
echo "  Load Test: $TOTAL requests, $CONCURRENCY concurrent"
echo "  Gateway:   $GATEWAY"
echo "  Models:    ${MODELS[*]}"
echo "═══════════════════════════════════════════"
echo ""

# Send requests with controlled concurrency
running=0
for ((i = 1; i <= TOTAL; i++)); do
  send_request &
  running=$((running + 1))

  if (( running >= CONCURRENCY )); then
    wait -n 2>/dev/null || true
    running=$((running - 1))
  fi

  # Small delay to spread requests
  if (( i % CONCURRENCY == 0 )); then
    sleep 0.5
  fi
done

wait
echo ""
echo "Done. Check metrics at $GATEWAY/monitoring"
