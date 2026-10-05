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

## Phase 2a — hot row removed (step 1: counter out of the transaction)

Change: the booking transaction no longer reads or updates `events.remaining`. It is now insert booking →
claim tickets → commit. The ticket rows alone prevent overselling. The sold-out flag is set when a claim comes
up short **and** no committed ticket is still available. `/availability` counts available tickets.
The `bookings.event_id` foreign key is still in place (step 2).

| Run | Pool | Peak RPS | Short-circuit | Peak booked/s | Avg booked/s | p99 (booked) | booked | already_booked | sold_out | timeout_pool | timeout_db | Ambiguous | k6 dropped | Unsold | Sold out after |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 2a-1 | 20 | 5000 | on | 1,894 | 743 | 1.97 s | 40,043 | 169 | 1,558 | 332,371 | 5,870 | 69 | 12,174 (3%) | 0 | 53s |
| 2a-2 | 20 | 5000 | on | 1,808 | 908 | 1.97 s | 39,888 | 194 | 2,430 | 132,689 | 5,163 | 52 | 10,787 (5.6%) | 0 | 43s |

### 2a-1 notes (2026-10-05)

- **vs baseline-1:** sold out in **53 s instead of 2m20s**; peak **1,894 bookings/s vs 618 (3.1×)**, average
  743 vs 283 (2.6×). Code change only — no schema or infrastructure change.
- **Invariants:** all 8 passed with no counter at all: bookings = tickets = 1,00,000, 0 unsold. The ticket
  rows alone prevent overselling.
- **Still overloaded:** demand (up to 5,000 req/s) is still above capacity (~1,900 bookings/s), so 87% of
  requests were `timeout_pool`. k6 stopped at sell-out (`STOP_ON_SOLD_OUT`), so almost the whole run was the
  overloaded peak — unlike baseline-1, whose percentages include minutes of cheap post-sell-out `sold_out`.
  Compare the bookings/s columns, not the outcome percentages.
- **Machine saturation:** 280 `client_error` (no answer in 5 s), `timeout_db` answers up to 4.98 s despite the
  2 s deadline, and 3% k6 dropped iterations — k6, Go and Postgres competing for the laptop's CPU.
- **Ambiguous timeouts:** 69 (vs 26), consistent with late answers from the saturated machine.
- **Caveat found after the run:** k6's stop-on-sold-out check called `/availability` once per `sold_out`
  answer — 1,558 calls (http_reqs − iterations). They bunched up at the sell-out and competed for the same
  20-connection pool with no deadline (waiting up to k6's 5 s), which explains the 5 s client timeouts,
  `timeout_db` answers up to 4.98 s, and the test running ~97 s for a 53 s sale. Fixed before 2a-2: the server now
  returns `event_sold_out: true` and k6 stops on it with no extra requests; `/availability` has a 2 s deadline.
- **Remaining suspects** for the next limit: the `bookings.event_id` FK lock on event row 1 (MultiXact), all
  claims hitting the lowest ticket ids (same index/heap pages), commit fsync. Wait snapshots not captured.

### 2a-2 notes (2026-10-05)

- Clean run: k6 stopped at 45 s on `event_sold_out`, no `/availability` traffic. 0 `client_error`, slowest
  `timeout_db` 2.08 s (deadline respected). The whole sale fit inside the 40 s peak window.
- Sold out in **43 s**; peak 1,808 bookings/s (consistent with 2a-1's 1,894), average **908**/s.
- 8,895 requests were in flight when k6 stopped and are not in the outcome counts; ambiguous was still only 52.
- All 8 invariants passed; 0 unsold.

## Phase 2a — step 2: event row untouched (FK + counter column dropped)

Change: migration `2_remove_event_hot_row` drops the `bookings.event_id` foreign key (no more `FOR KEY SHARE`
lock on event row 1 per booking insert) and the unused `events.remaining` column. Booking code unchanged
from step 1.

| Run | Pool | Peak RPS | Short-circuit | Peak booked/s | Avg booked/s | p99 (booked) | booked | already_booked | sold_out | timeout_pool | timeout_db | Ambiguous | k6 dropped | Unsold | Sold out after |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 2a-s2-1 | 20 | 5000 | on | 1,976 | 975 | 1.97 s | 39,900 | 182 | 174 | 115,648 | 4,625 | 55 | 10,232 (6%) | 0 | 40s |

### 2a-s2-1 notes (2026-10-05)

- Sold out in **40 s** (vs 43 s for 2a-2); peak 1,976 bookings/s (+9%), average 975/s (+7%).
- The gain is within run-to-run noise (2a-1 vs 2a-2 peaks differed by ~5%), so the `bookings.event_id` FK was a
  small cost at most; the counter update (step 1) was the real bottleneck.
- All 8 invariants passed without the FK and without `events.remaining`; 0 unsold; 55 ambiguous.
- k6 stopped at 41 s; 0 `client_error`; slowest response 2.58 s.

## Phase 2a summary

| | baseline-1 | 2a step 1 (2a-2) | 2a step 2 (2a-s2-1) |
|---|---|---|---|
| Sold out after | 2m20s | 43 s | 40 s |
| Peak bookings/s | 618 | 1,808 | 1,976 |
| Avg bookings/s | 283 | 908 | 975 |

- Removing the shared counter row from the booking transaction (a code change) made the sale **~3× faster**.
  Dropping the FK and the dead column added a few percent at most.
- Correctness held in every run: ticket rows alone prevent overselling.
- Demand at the peak (up to 5,000 req/s) is still 2–3× capacity (~1,900 bookings/s), so most peak requests
  still end in `timeout_pool`. Making the transaction faster cannot fix that; keeping losers away from the
  database (Phase 2b, Redis admission) can.
- Runs so far: 2 for step 1, 1 for step 2 — short of the 3-run median.

## Commands

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
