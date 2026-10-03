# Results

Local runs at 1:10 scale: **1,00,000 tickets, 2,00,000 users**. Go API, Postgres 16 (Docker Desktop) and k6
on one MacBook Pro (Apple Silicon). Each configuration is meant to run 3 times on a fresh reset; the median is
the reported number.

## Phase 1 — Postgres only

| Run | Pool | Peak RPS | Short-circuit | Peak booked/s | Avg booked/s | p99 (booked) | booked | already_booked | sold_out | timeout_pool | timeout_db | Ambiguous | k6 dropped | Unsold | Sold out after |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| baseline-1 | 20 | 5000 | on | 618 | 283 | 1.98 s | 39,882 | 74 | 71,932 | 431,079 | 5,950 | 26 | 9,905 (1.8%) | 0 | 2m20s |
| baseline-2 | 20 | 5000 | on | | | | | | | | | | | | |
| baseline-3 | 20 | 5000 | on | | | | | | | | | | | | |

Column notes:
- **Peak / Avg booked/s** come from `bookings.created_at` (query below), not from k6.
- **Ambiguous** = bookings in the DB − `booked` seen by k6: requests that timed out for the client but committed.
- **k6 dropped** = `dropped_iterations`; above zero means k6 could not send the full arrival rate.
- **Sold out after** = first booking → last booking.

### baseline-1 notes (2026-10-03)

- **Invariants:** all 8 checks passed; counter = bookings = tickets = 1,00,000. No oversell, no double booking.
- **Pool saturation:** 78.5% of requests were `timeout_pool`. Bookings held a connection ~1 s on average
  (avg booked latency 974 ms), so 20 connections carried only ~20 bookings at a time.
- **Pool 100 → 20** (vs. the earlier pool-100 smoke run at 500 RPS): `timeout_db` fell from 13% to 1.1%;
  failures moved to `timeout_pool`, where Postgres never saw the request.
- **Sold-out short-circuit:** after sell-out, `sold_out` answered in 0.3 ms median without touching the DB.
- **Unfairness:** most of the peak crowd got `timeout_pool` while tickets were still available; later
  arrivals got the tickets.
- **Postgres waits** (`pg_stat_activity` during the peak) were mostly `LWLock MultiXactMemberSLRU` /
  `MultiXactOffsetSLRU` and `BufferContent`, with occasional `IO WALSync` — not `Lock transactionid` as
  expected. Likely cause (not yet confirmed): every booking insert takes a `FOR KEY SHARE` lock on event
  row 1 for the `bookings.event_id` foreign key, so many transactions share-lock the same row (a MultiXact)
  while the counter update also writes it. The event row is hot twice: FK check + counter.
- The k6 run was stopped by hand at 4m27s, after the sell-out.

## Commands

Full run: reset → migrate → seed → restart API → k6 → verify → bookings/s query.

```bash
docker exec -i stampede-db psql -U sanat -d stampede < benchmark/reset.sql
docker compose run --rm migrate
docker exec -i stampede-db psql -U sanat -d stampede -v N=100000 < benchmark/seed.sql
DB_MAX_CONNS=20 go run .                      # terminal 1; restart every run (sold-out flag is in memory)
ulimit -n 65536                               # terminal 2, same shell as k6
k6 run -e PEAK_RPS=5000 -e SUMMARY_FILE=benchmark/baseline-run1.json benchmark/spike.js
go run ./benchmark                            # after k6 exits
```

Bookings per second and sell-out time:

```bash
docker exec stampede-db psql -U sanat -d stampede -c "WITH s AS (SELECT date_trunc('second', created_at) t, count(*) n, sum(qty) q FROM bookings GROUP BY 1) SELECT min(t) AS first_booking, max(t) AS sold_out_at, max(t) - min(t) AS took, max(n) AS peak_bookings_per_s, round(avg(n)) AS avg_bookings_per_s, max(q) AS peak_tickets_per_s FROM s;"
```

Postgres waits during the peak:

```bash
docker exec stampede-db psql -U sanat -d stampede -c "SELECT wait_event_type, wait_event, count(*) FROM pg_stat_activity WHERE state = 'active' GROUP BY 1, 2 ORDER BY 3 DESC;"
```
