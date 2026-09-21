#!/usr/bin/env bash
# Curl happy path against a running compose/local stack.
set -euo pipefail
API="${API:-http://localhost:8080}"
ACTOR_H=(-H "X-Actor: alice" -H "Content-Type: application/json")

echo "== healthz"
curl -sf "$API/api/v1/healthz"
echo

echo "== break probes"
curl -sf -X POST "$API/api/v1/demo/break" -H "Content-Type: application/json" -d '{"target":"all"}'
echo

echo "== promote (expect 409)"
code=$(curl -s -o /tmp/sg-promo.json -w "%{http_code}" "${ACTOR_H[@]}" \
  -d '{"from":"staging","to":"prod"}' "$API/api/v1/projects/prj_checkout/promotions")
echo "HTTP $code"
python3 -c 'import json; p=json.load(open("/tmp/sg-promo.json")); print(p.get("status"), p.get("error","")[:80])'
test "$code" = "409"

echo "== restore probes"
curl -sf -X POST "$API/api/v1/demo/restore" -H "Content-Type: application/json" -d '{}'
echo

echo "== promote (expect pending_approval 201)"
code=$(curl -s -o /tmp/sg-promo.json -w "%{http_code}" "${ACTOR_H[@]}" \
  -d '{"from":"staging","to":"prod"}' "$API/api/v1/projects/prj_checkout/promotions")
echo "HTTP $code"
python3 -c 'import json; p=json.load(open("/tmp/sg-promo.json")); print(p["status"], p["id"])'
test "$code" = "201"
PMT=$(python3 -c 'import json; print(json.load(open("/tmp/sg-promo.json"))["id"])')

echo "== alice approve (expect 403)"
code=$(curl -s -o /tmp/sg-appr.json -w "%{http_code}" -H "X-Actor: alice" -X POST "$API/api/v1/promotions/$PMT/approve")
echo "HTTP $code"
test "$code" = "403"

echo "== bob approve (expect 200)"
code=$(curl -s -o /tmp/sg-appr.json -w "%{http_code}" -H "X-Actor: bob" -H "Content-Type: application/json" -d '{}' \
  -X POST "$API/api/v1/promotions/$PMT/approve")
echo "HTTP $code"
python3 -c 'import json; print(json.load(open("/tmp/sg-appr.json"))["status"])'
test "$code" = "200"

echo "== audit tail"
curl -sf "$API/api/v1/projects/prj_checkout/audit" | python3 -c 'import json,sys; rows=json.load(sys.stdin)["audit"][:8];
[print(r["seq"], r["actor"], r["action"], r["result"]) for r in rows]'

echo "== rollback"
curl -sf -H "X-Actor: carol" -H "Content-Type: application/json" -d '{}' \
  -X POST "$API/api/v1/promotions/$PMT/rollback" | python3 -c 'import json,sys; print(json.load(sys.stdin)["status"])'

echo "OK"
