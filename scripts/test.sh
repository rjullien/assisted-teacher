#!/usr/bin/env bash
set -euo pipefail

# Cours IA — Run all tests (backend + frontend)
# Usage: ./scripts/test.sh

cd "$(dirname "$0")/.."

PASS=0
FAIL=0

echo "╔═══════════════════════════════════════════════╗"
echo "║       cours-ia — Test Suite                   ║"
echo "╚═══════════════════════════════════════════════╝"
echo ""

# --- Backend Go tests ---
echo "▶ Backend Go tests"
echo "──────────────────────────────────────────────"
if cd backend && go test ./... -count=1 -timeout=30s; then
  echo "✅ Backend: ALL PASSED"
  PASS=$((PASS + 1))
else
  echo "❌ Backend: FAILED"
  FAIL=$((FAIL + 1))
fi
cd ..
echo ""

# --- Frontend React tests ---
echo "▶ Frontend React tests"
echo "──────────────────────────────────────────────"
if cd frontend && npx vitest --run; then
  echo "✅ Frontend: ALL PASSED"
  PASS=$((PASS + 1))
else
  echo "❌ Frontend: FAILED"
  FAIL=$((FAIL + 1))
fi
cd ..
echo ""

# --- Summary ---
echo "══════════════════════════════════════════════"
if [ "$FAIL" -eq 0 ]; then
  echo "✅ ALL TEST SUITES PASSED ($PASS/$((PASS + FAIL)))"
  exit 0
else
  echo "❌ $FAIL TEST SUITE(S) FAILED ($PASS passed, $FAIL failed)"
  exit 1
fi
