-- Seeds event 1 with :N general-admission tickets. Run on a fresh schema
-- (after reset.sql + migrate), passing N on the command line:
--   docker exec -i stampede-db psql -U sanat -d stampede -v N=100000 < benchmark/seed.sql

INSERT INTO events (id, name, total_tickets, remaining, sale_starts_at)
VALUES (1, 'Concert 2026', :N, :N, now());

INSERT INTO tickets (event_id, ticket_no)
SELECT 1, 'GA-' || lpad(g::text, 7, '0')
FROM generate_series(1, :N) AS g;

VACUUM ANALYZE;  -- fresh statistics + visibility map before every test
CHECKPOINT;      -- flush the seed now so its checkpoint does not land mid-test
