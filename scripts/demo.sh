#!/usr/bin/env bash
# scripts/demo.sh — curl-only smoke test against a running demo-mode
# ServiceDesk server. See DEMO.md for the account list this drives and what
# each check corresponds to. Safe to re-run against the same server: every
# check either tolerates already-existing state or creates a fresh row.
#
# Usage: ./scripts/demo.sh [base_url]   (default http://localhost:8080)
#        make demo-curl-test

set -u

BASE_URL="${1:-http://localhost:8080}"
JAR_DIR="$(mktemp -d)"
trap 'rm -rf "$JAR_DIR"' EXIT

PASS=0
FAIL=0

pass() { PASS=$((PASS + 1)); printf '  \033[32mPASS\033[0m %s\n' "$1"; }
fail() { FAIL=$((FAIL + 1)); printf '  \033[31mFAIL\033[0m %s\n' "$1"; }
step() { printf '\n\033[1m%s\033[0m\n' "$1"; }

# jar <name> — path to a fresh per-persona cookie jar.
jar() { echo "$JAR_DIR/$1.cookies"; }

# login <jar-name> <org> <username> <password> — POSTs /login and confirms
# the session cookie actually got set (a rejected login re-renders the same
# 200 page, so status code alone can't tell success from failure).
login() {
  local name="$1" org="$2" user="$3" pass_="$4" c
  c="$(jar "$name")"
  curl -s -o /dev/null -c "$c" -b "$c" -X POST "$BASE_URL/login" \
    --data-urlencode "org=$org" --data-urlencode "username=$user" --data-urlencode "password=$pass_"
  if grep -q sd_token "$c" 2>/dev/null; then
    pass "login as $user"
  else
    fail "login as $user (no session cookie set)"
  fi
}

step "Health check"
code="$(curl -s -o /dev/null -w '%{http_code}' "$BASE_URL/health")"
[ "$code" = "200" ] && pass "GET /health = 200" || fail "GET /health = $code, want 200"

step "Demo persona login (DEMO.md accounts)"
login admin "" "admin" "admin123"
login manager "" "demo.admin" "demo1234"
login engineer "" "demo.engineer1" "demo1234"
login customer "Acme Corp" "demo.customer1" "demo1234"

ADMIN_JAR="$(jar admin)"
ENG_JAR="$(jar engineer)"
CUST_JAR="$(jar customer)"

step "Custom fields on the ticket-create form (RELEASE/v_3.0.0.md)"
curl -s -o /dev/null -c "$ADMIN_JAR" -b "$ADMIN_JAR" -X POST "$BASE_URL/admin/custom-fields" \
  --data-urlencode "category=demoscript" --data-urlencode "name=vlan" \
  --data-urlencode "label=VLAN ID" --data-urlencode "type=text"
fragment="$(curl -s -c "$ADMIN_JAR" -b "$ADMIN_JAR" "$BASE_URL/custom-fields/for-category?category=demoscript")"
if echo "$fragment" | grep -q 'name="cf_vlan"'; then
  pass "custom field renders in the category fragment"
else
  fail "custom field missing from /custom-fields/for-category fragment"
fi

curl -s -o /dev/null -c "$ADMIN_JAR" -b "$ADMIN_JAR" -X POST "$BASE_URL/tickets" \
  --data-urlencode "title=demo.sh test ticket" --data-urlencode "description=d" \
  --data-urlencode "queue_id=1" --data-urlencode "priority=P3" \
  --data-urlencode "category=demoscript" --data-urlencode "cf_vlan=42"
tickets_page="$(curl -s -c "$ADMIN_JAR" -b "$ADMIN_JAR" "$BASE_URL/tickets")"
last_id="$(echo "$tickets_page" | grep -oE '/tickets/[0-9]+' | grep -oE '[0-9]+' | sort -n | tail -1)"
if [ -n "$last_id" ] && curl -s -c "$ADMIN_JAR" -b "$ADMIN_JAR" "$BASE_URL/tickets/$last_id" | grep -q '<strong>vlan:</strong> 42'; then
  pass "custom field value persists and renders on the ticket detail page"
else
  fail "custom field value did not render on ticket #$last_id"
fi

step "Knowledge Base match endpoint (RELEASE/v_3.0.0.md, matches the seeded published article)"
match="$(curl -s -c "$ADMIN_JAR" -b "$ADMIN_JAR" -X POST "$BASE_URL/tickets/match-symptom" \
  --data-urlencode "title=" \
  --data-urlencode "description=Legitimate vendor emails are landing in the junk spam folder instead of the inbox")"
if echo "$match" | grep -q '"title":"Vendor emails going to spam"'; then
  pass "match-symptom finds the seeded published article for close wording"
else
  fail "match-symptom did not find the seeded article: $match"
fi
nomatch="$(curl -s -c "$ADMIN_JAR" -b "$ADMIN_JAR" -X POST "$BASE_URL/tickets/match-symptom" \
  --data-urlencode "title=" --data-urlencode "description=completely unrelated printer jam issue")"
if [ "$nomatch" = "{}" ]; then
  pass "match-symptom returns no match for unrelated wording"
else
  fail "match-symptom unexpectedly matched unrelated text: $nomatch"
fi

step "Knowledge Base trust boundary (RELEASE/v_2.1.0.md): draft never reaches a Customer"
cust_kb="$(curl -s -c "$CUST_JAR" -b "$CUST_JAR" "$BASE_URL/kb")"
if echo "$cust_kb" | grep -q "Vendor emails going to spam"; then
  pass "Customer can see the published article at /kb"
else
  fail "Customer cannot see the published seeded article at /kb"
fi
if echo "$cust_kb" | grep -q "Printer shows offline"; then
  fail "Customer can see the still-draft article at /kb - trust boundary broken"
else
  pass "Customer cannot see the still-draft article at /kb"
fi
eng_review="$(curl -s -c "$ENG_JAR" -b "$ENG_JAR" "$BASE_URL/kb/review")"
if echo "$eng_review" | grep -q "Printer shows offline"; then
  pass "Engineer can see the draft in the curation queue"
else
  fail "Engineer cannot see the draft at /kb/review"
fi

step "Manager dashboard MTTx trend sparklines (RELEASE/v_3.0.0.md)"
MGR_JAR="$(jar manager)"
dash="$(curl -s -c "$MGR_JAR" -b "$MGR_JAR" "$BASE_URL/manager")"
svg_count="$(echo "$dash" | grep -o 'aria-label="trend, last' | wc -l | tr -d ' ')"
if [ "$svg_count" = "4" ]; then
  pass "all 4 MTTx sparklines render (MTTD/MTTA/MTTM/MTTR)"
else
  fail "expected 4 MTTx sparklines, found $svg_count"
fi

step "Summary"
printf '%d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
