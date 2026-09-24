#!/usr/bin/env bash
# Creates the two throwaway PostgreSQL clusters used by demo.tape, with
# realistic demo data. Debian/Ubuntu (pg_createcluster); run as root.
#   docs/demo/setup.sh            # create (idempotent)
#   docs/demo/setup.sh --drop     # remove both clusters
set -euo pipefail
PGVER=${PGVER:-18}

if [ "${1:-}" = "--drop" ]; then
	pg_dropcluster "$PGVER" demo_prod --stop 2>/dev/null || true
	pg_dropcluster "$PGVER" demo_dev --stop 2>/dev/null || true
	exit 0
fi

psqlp() { local port=$1; shift; (cd /tmp && sudo -u postgres psql -p "$port" -q "$@"); }

for c in "demo_prod 5434" "demo_dev 5435"; do
	set -- $c
	if ! pg_lsclusters -h | awk '{print $2}' | grep -qx "$1"; then
		pg_createcluster "$PGVER" "$1" --port "$2" --start >/dev/null
	fi
	psqlp "$2" -c "ALTER USER postgres PASSWORD 'demo'"
done

if ! psqlp 5434 -Atc "select 1 from pg_database where datname='shop'" | grep -q 1; then
	psqlp 5434 <<'SQL'
ALTER SYSTEM SET shared_buffers = '1GB';
ALTER SYSTEM SET max_connections = 200;
CREATE ROLE app_rw LOGIN PASSWORD 'demo';
CREATE ROLE app_ro LOGIN PASSWORD 'demo';
CREATE ROLE reporting LOGIN PASSWORD 'demo' CONNECTION LIMIT 10;
CREATE ROLE etl_job LOGIN PASSWORD 'demo';
CREATE ROLE analyst LOGIN PASSWORD 'demo' CREATEDB;
CREATE DATABASE shop OWNER app_rw;
CREATE DATABASE analytics OWNER reporting;
CREATE DATABASE embeddings OWNER app_rw;
SQL
	systemctl restart "postgresql@$PGVER-demo_prod"
	psqlp 5434 -d shop <<'SQL'
SET ROLE app_rw;
CREATE TABLE customers (id bigserial PRIMARY KEY, name text, email text UNIQUE, created_at timestamptz DEFAULT now());
CREATE TABLE orders (id bigserial PRIMARY KEY, customer_id bigint REFERENCES customers, status text, total numeric(10,2), created_at timestamptz);
CREATE TABLE order_items (id bigserial PRIMARY KEY, order_id bigint REFERENCES orders, sku text, qty int, price numeric(10,2));
INSERT INTO customers (name, email) SELECT 'Customer ' || g, 'c' || g || '@example.com' FROM generate_series(1, 50000) g;
INSERT INTO orders (customer_id, status, total, created_at)
  SELECT 1 + (random() * 49999)::int, (ARRAY['paid','shipped','pending','refunded'])[1 + (random() * 3)::int],
         round((random() * 900 + 10)::numeric, 2), now() - random() * interval '365 days'
  FROM generate_series(1, 400000);
INSERT INTO order_items (order_id, sku, qty, price)
  SELECT 1 + (random() * 399999)::int, 'SKU-' || (random() * 5000)::int, 1 + (random() * 4)::int, round((random() * 200 + 1)::numeric, 2)
  FROM generate_series(1, 900000);
CREATE INDEX ON orders (customer_id); CREATE INDEX ON orders (created_at); CREATE INDEX ON order_items (order_id);
GRANT SELECT ON ALL TABLES IN SCHEMA public TO app_ro, reporting;
GRANT SELECT, UPDATE ON orders TO etl_job;
SQL
	psqlp 5434 -d embeddings <<'SQL'
CREATE EXTENSION IF NOT EXISTS vector;
SET ROLE app_rw;
CREATE TABLE documents (id bigserial PRIMARY KEY, title text, body text, embedding vector(384));
INSERT INTO documents (title, body, embedding)
  SELECT 'Doc ' || g, repeat('lorem ipsum ', 20), (SELECT array_agg(random())::vector(384) FROM generate_series(1, 384) WHERE g > 0)
  FROM generate_series(1, 20000) g;
CREATE INDEX ON documents USING hnsw (embedding vector_cosine_ops);
SQL
	psqlp 5434 -d analytics -c "SET ROLE reporting; CREATE TABLE daily_revenue AS SELECT d::date AS day, round((random() * 50000)::numeric, 2) AS revenue FROM generate_series(now() - interval '2 years', now(), interval '1 day') d"
fi

if ! psqlp 5435 -Atc "select 1 from pg_database where datname='app_dev'" | grep -q 1; then
	psqlp 5435 -c "CREATE DATABASE app_dev"
	psqlp 5435 -d app_dev -c "CREATE EXTENSION IF NOT EXISTS vector; CREATE TABLE notes (id serial PRIMARY KEY, body text, embedding vector(3)); INSERT INTO notes (body, embedding) SELECT 'note ' || g, '[1,2,3]' FROM generate_series(1, 500) g"
fi
echo "demo clusters ready: prod-db1 on 5434, dev-local on 5435 (password: demo)"
