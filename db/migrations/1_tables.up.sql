CREATE TYPE ticket_status  AS ENUM ('available', 'held', 'sold');
CREATE TYPE booking_status AS ENUM ('held', 'confirmed', 'expired', 'failed');

CREATE TABLE events (
    id             BIGINT PRIMARY KEY,
    name           TEXT        NOT NULL,
    total_tickets  INT         NOT NULL,
    remaining      INT         NOT NULL CHECK (remaining >= 0),
    sale_starts_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE bookings (
    id         BIGSERIAL PRIMARY KEY,
    event_id   BIGINT         NOT NULL REFERENCES events(id),
    user_id    BIGINT         NOT NULL,
    qty        SMALLINT       NOT NULL CHECK (qty BETWEEN 1 AND 4),
    status     booking_status NOT NULL DEFAULT 'held',
    expires_at TIMESTAMPTZ,                         -- NULL in Phase 1 (no holds yet)
    created_at TIMESTAMPTZ    NOT NULL DEFAULT now(),
    UNIQUE (event_id, user_id)                      -- one booking per user per event; also the idempotency key
);

CREATE TABLE tickets (
    id         BIGSERIAL PRIMARY KEY,
    event_id   BIGINT        NOT NULL REFERENCES events(id),
    ticket_no  VARCHAR(20)   NOT NULL,              -- e.g. 'GA-0000001'
    status     ticket_status NOT NULL DEFAULT 'available',
    booking_id BIGINT        REFERENCES bookings(id),
    UNIQUE (event_id, ticket_no)
);

-- Lets the claim query find available tickets without scanning past every sold row.
CREATE INDEX tickets_available_idx ON tickets (event_id, id) WHERE status = 'available';