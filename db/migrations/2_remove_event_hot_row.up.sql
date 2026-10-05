-- Phase 2a, step 2: stop bookings from touching the events row at all.

-- The FK made every booking insert take a FOR KEY SHARE lock on event row 1, so all
-- concurrent bookings share-locked the same row (MultiXact contention). The column
-- and UNIQUE (event_id, user_id) stay; the API only accepts the configured event,
-- and every booking's tickets still reference events through tickets.event_id.
ALTER TABLE bookings DROP CONSTRAINT bookings_event_id_fkey;

-- No longer updated by bookings since step 1; ticket rows are the source of truth.
-- Its CHECK (remaining >= 0) is dropped with it.
ALTER TABLE events DROP COLUMN remaining;
