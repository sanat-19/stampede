-- Drops everything so the schema and data can be recreated from scratch before every test:
--   docker exec -i stampede-db psql -U sanat -d stampede < benchmark/reset.sql
--   docker compose run --rm migrate      (recreates the tables)
--   then seed the tickets.
DROP SCHEMA public CASCADE;
CREATE SCHEMA public;

CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
SELECT pg_stat_statements_reset();  -- per-query timings cover this run only
SELECT pg_stat_reset();             -- commits/rollbacks, dead tuples, autovacuum counters for this run only
