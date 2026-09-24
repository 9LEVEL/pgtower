#!/usr/bin/env bash
# Simulated application traffic for the demo recording: an app connection
# pool, a long-running report, and a lock chain (one idle-in-transaction
# holder, two sessions waiting on it). Everything ends after $DURATION seconds
# and every transaction rolls back, so it can be re-run any number of times.
set -euo pipefail
PORT=${PORT:-5434}
DURATION=${DURATION:-600}
export PGHOST=127.0.0.1 PGPORT=$PORT PGPASSWORD=demo PGDATABASE=shop

bg() { # app_name role sql...
	local app=$1 role=$2; shift 2
	( printf '%s\n' "$@"; sleep "$DURATION" ) | PGAPPNAME=$app psql -q -U "$role" >/dev/null 2>&1 &
}

# Start clean and idempotent: drop sessions left by a previous run and make
# sure the contended row exists (the demo never changes data for good).
PGAPPNAME=demo-setup psql -q -U postgres -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity
  WHERE application_name IN ('checkout-api','catalog-api','metabase','billing-worker','nightly-etl')" >/dev/null
psql -q -U app_rw -c "INSERT INTO orders (id, customer_id, status, total, created_at)
  VALUES (42, 1, 'pending', 99.90, now()) ON CONFLICT (id) DO NOTHING" >/dev/null

for _ in 1 2 3 4; do bg checkout-api app_rw "SELECT 1;"; done
for _ in 1 2; do bg catalog-api app_ro "SELECT count(*) FROM orders WHERE status = 'paid';"; done
bg metabase reporting "SELECT pg_sleep($DURATION), count(*) FROM orders o JOIN order_items i ON i.order_id = o.id;"
bg billing-worker app_rw "BEGIN;" "UPDATE orders SET status = 'paid' WHERE id = 42;"
sleep 1
bg checkout-api app_rw "UPDATE orders SET status = 'shipped' WHERE id = 42;"
bg nightly-etl etl_job "BEGIN;" "SELECT id FROM orders WHERE id = 42 FOR UPDATE;"
echo "demo traffic running for ${DURATION}s"
