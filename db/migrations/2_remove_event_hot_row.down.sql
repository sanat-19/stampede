ALTER TABLE events ADD COLUMN remaining INT NOT NULL DEFAULT 0 CHECK (remaining >= 0);
UPDATE events e
SET remaining = (SELECT count(*) FROM tickets t WHERE t.event_id = e.id AND t.status = 'available');
ALTER TABLE events ALTER COLUMN remaining DROP DEFAULT;

ALTER TABLE bookings ADD CONSTRAINT bookings_event_id_fkey FOREIGN KEY (event_id) REFERENCES events(id);
