#!/usr/bin/env bash
set -euo pipefail

SUO5_BIN=${SUO5_BIN:-./suo5_test}
MBTEST_BIN=${MBTEST_BIN:-./mbtest}
NODE_SERVER=${NODE_SERVER:-assets/nodejs/suo5.js}
LOG_DIR=${RUNNER_TEMP:-/tmp}/suo5-node-tests
mkdir -p "$LOG_DIR"

SERVER_PID=""
CLIENT_PID=""
TARGET_PID=""

cleanup_client() {
  if [[ -n "${CLIENT_PID:-}" ]]; then
    kill "$CLIENT_PID" 2>/dev/null || true
    wait "$CLIENT_PID" 2>/dev/null || true
    CLIENT_PID=""
  fi
}

cleanup() {
  cleanup_client
  if [[ -n "${TARGET_PID:-}" ]]; then
    kill "$TARGET_PID" 2>/dev/null || true
    wait "$TARGET_PID" 2>/dev/null || true
  fi
  if [[ -n "${SERVER_PID:-}" ]]; then
    kill "$SERVER_PID" 2>/dev/null || true
    wait "$SERVER_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT

wait_http() {
  local url=$1
  for _ in $(seq 1 100); do
    if curl --silent --fail --output /dev/null "$url"; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

wait_port() {
  local port=$1
  for _ in $(seq 1 100); do
    if (echo >"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then
      return 0
    fi
    if [[ -n "${CLIENT_PID:-}" ]] && ! kill -0 "$CLIENT_PID" 2>/dev/null; then
      return 1
    fi
    sleep 0.1
  done
  return 1
}

start_client() {
  local mode=$1
  local port=$2
  local log="$LOG_DIR/client-${mode}.log"
  "$SUO5_BIN" -debug -t http://127.0.0.1:8080/ -mode "$mode" \
    --retry 0 -l "127.0.0.1:${port}" >"$log" 2>&1 &
  CLIENT_PID=$!
  if ! wait_port "$port"; then
    cat "$log"
    return 1
  fi
}

node tests/nodejs/generate.js --check
node --check assets/nodejs/suo5.js

node "$NODE_SERVER" >"$LOG_DIR/server.log" 2>&1 &
SERVER_PID=$!
if ! wait_http http://127.0.0.1:8080/; then
  cat "$LOG_DIR/server.log"
  exit 1
fi

proxy_port=11300
target_port=10000
for mode in full half classic; do
  echo "Run Node.js ${mode} reliability tests"
  start_client "$mode" "$proxy_port"
  if ! "$MBTEST_BIN" -listen "127.0.0.1:${target_port}" \
    -target "127.0.0.1:${target_port}" -proxy "127.0.0.1:${proxy_port}" \
    -echo-workers 10 -echo-requests 10; then
    cat "$LOG_DIR/client-${mode}.log"
    cat "$LOG_DIR/server.log"
    exit 1
  fi
  cleanup_client
  proxy_port=$((proxy_port + 1))
  target_port=$((target_port + 1))
done

echo "Verify full mode releases the target connection when the SOCKS client closes"
TARGET_PORT=10010 CONTROL_PORT=10011 node tests/nodejs/connection_target.js \
  >"$LOG_DIR/connection-target.log" 2>&1 &
TARGET_PID=$!
wait_http http://127.0.0.1:10011/connections
start_client full 11310
response=$(curl --silent --show-error --socks5-hostname 127.0.0.1:11310 \
  --request POST --data-binary abc http://127.0.0.1:10010/)
if [[ "$response" != "abc" ]]; then
  echo "unexpected echo response: $response"
  exit 1
fi

released=false
for _ in $(seq 1 50); do
  if [[ $(curl --silent --fail http://127.0.0.1:10011/connections) == "0" ]]; then
    released=true
    break
  fi
  sleep 0.1
done
if [[ "$released" != "true" ]]; then
  echo "Node.js target connection was not released after the SOCKS client closed"
  exit 1
fi

echo "PASS: Node.js integration and disconnect cleanup tests"
