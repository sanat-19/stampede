package models

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type BookingStatus string

const (
	Held      BookingStatus = "held"
	Confirmed BookingStatus = "confirmed"
	Expired   BookingStatus = "expired"
	Failed    BookingStatus = "failed"
)

type Bookings struct {
	ID       int64         `json:"id" gorm:"primaryKey"`
	EventID  int64         `json:"event_id" gorm:"column:event_id"`
	UserID   int64         `json:"user_id" gorm:"column:user_id"`
	Quantity int           `json:"qty" gorm:"column:qty"`
	Status   BookingStatus `json:"status" gorm:"column:status;type:booking_status"`
}

func (Bookings) TableName() string {
	return "bookings"
}

// BookRequest is the POST /book body. Pointers so a missing field can be told apart from a zero value.
type BookRequest struct {
	EventID *int64 `json:"event_id"`
	UserID  *int64 `json:"user_id"`
	Qty     *int   `json:"qty"`
}

// BookResponse is every POST /book reply; Outcome is always set.
type BookResponse struct {
	Outcome   string   `json:"outcome"`
	BookingID int64    `json:"booking_id,omitempty"`
	Tickets   []string `json:"tickets,omitempty"`
	Error     string   `json:"error,omitempty"`
	// EventSoldOut is true on a sold_out when the whole event has no tickets
	// left, so a client can stop retrying (the load test stops on it).
	EventSoldOut bool `json:"event_sold_out,omitempty"`
}

// Booking outcomes other than success. The HTTP layer maps them to responses.
var (
	ErrSoldOut = errors.New("sold out")
	// ErrEventSoldOut is a sold_out where the event is known to have no tickets
	// left (the sold-out flag is set). errors.Is(err, ErrSoldOut) is still true.
	ErrEventSoldOut = fmt.Errorf("%w: event has no tickets left", ErrSoldOut)
	ErrTimeoutPool  = errors.New("deadline exceeded waiting for a pool connection") // Postgres never saw the request
	ErrTimeoutDB    = errors.New("deadline exceeded inside the transaction")        // may have committed; a retry replays it
)

type BookingResult struct {
	BookingID int64
	Tickets   []string
	Replayed  bool // true for an idempotent replay of an existing booking (already_booked)
	Timing    BookingTiming
}

// BookingTiming splits where a booking's time went. It is filled on every
// return from Book, including errors, so timeouts can be timed too.
type BookingTiming struct {
	PoolWait time.Duration // waiting for a pool connection (+ the BEGIN round trip)
	DB       time.Duration // from BEGIN to commit/rollback (0 if no connection was obtained)
}

// Booker is what the HTTP layer depends on, so later phases can add another
// implementation (e.g. a Redis-gated booker) without touching handlers.
type Booker interface {
	Book(ctx context.Context, eventID, userID int64, qty int) (BookingResult, error)
}

// pgQueryCanceled is SQLSTATE 57014, raised when a statement is cancelled (deadline, statement_timeout).
const pgQueryCanceled = "57014"

// BookingRepo is the Postgres-only Booker.
type BookingRepo struct {
	db           *gorm.DB
	tickets      *TicketRepo
	shortCircuit bool
	soldOut      sync.Map // eventID → struct{}; set once the event is known to be sold out
}

func NewBookingRepo(db *gorm.DB, shortCircuit bool) *BookingRepo {
	return &BookingRepo{db: db, tickets: NewTicketRepo(db), shortCircuit: shortCircuit}
}

// SoldOut reports whether the in-process sold-out flag is set for the event.
func (r *BookingRepo) SoldOut(eventID int64) bool {
	_, ok := r.soldOut.Load(eventID)
	return ok
}

func (r *BookingRepo) Book(ctx context.Context, eventID, userID int64, qty int) (res BookingResult, err error) {
	// Safe to reject without the DB: the flag can only reject, never approve.
	if r.shortCircuit && r.SoldOut(eventID) {
		return BookingResult{}, ErrEventSoldOut
	}

	// Fill the timing on whatever result is returned below.
	start := time.Now()
	var dbStart time.Time
	defer func() {
		if dbStart.IsZero() {
			res.Timing.PoolWait = time.Since(start)
			return
		}
		res.Timing.PoolWait = dbStart.Sub(start)
		res.Timing.DB = time.Since(dbStart)
	}()

	// Begun manually rather than db.Transaction(fn) so a failure here — waiting for
	// a pool connection — can be told apart from a failure inside the transaction.
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		if ctx.Err() != nil {
			return BookingResult{}, ErrTimeoutPool
		}
		return BookingResult{}, fmt.Errorf("begin: %w", tx.Error)
	}
	dbStart = time.Now()
	finished := false
	defer func() {
		if !finished {
			tx.Rollback()
		}
	}()

	// The booking row. UNIQUE (event_id, user_id) makes a retry conflict
	// instead of creating a second booking.
	booking := Bookings{EventID: eventID, UserID: userID, Quantity: qty, Status: Confirmed}
	ins := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_id"}, {Name: "user_id"}},
		DoNothing: true,
	}).Create(&booking)
	if ins.Error != nil {
		return BookingResult{}, txError(ctx, "insert booking", ins.Error)
	}
	if ins.RowsAffected == 0 {
		// Release this connection before the lookup so one request never holds two.
		tx.Rollback()
		finished = true
		return r.replay(ctx, eventID, userID)
	}

	// Claim tickets. This is the only guard against overselling: each ticket row
	// can be claimed once (SKIP LOCKED + one booking_id slot). No shared counter
	// row is touched, so concurrent bookings no longer queue behind each other.
	// No partial fulfilment — fewer than qty means sold out.
	ticketNos, err := r.tickets.Claim(tx, eventID, booking.ID, qty)
	if err != nil {
		return BookingResult{}, txError(ctx, "claim tickets", err)
	}
	if len(ticketNos) < qty {
		// Roll back first so this request never holds two connections.
		tx.Rollback()
		finished = true
		if r.markSoldOutIfNoneLeft(ctx, eventID) {
			return BookingResult{}, ErrEventSoldOut
		}
		return BookingResult{}, ErrSoldOut
	}

	if err := tx.Commit().Error; err != nil {
		return BookingResult{}, txError(ctx, "commit", err)
	}
	finished = true

	return BookingResult{BookingID: booking.ID, Tickets: ticketNos}, nil
}

// markSoldOutIfNoneLeft sets the sold-out flag only when no committed ticket is
// still available. A short claim alone is not enough: SKIP LOCKED also skips
// tickets that in-flight transactions hold and may still roll back. Those stay
// 'available' to this read until they commit, so the flag is never set early.
// It reports whether the event is sold out.
func (r *BookingRepo) markSoldOutIfNoneLeft(ctx context.Context, eventID int64) bool {
	if ctx.Err() != nil {
		return false
	}
	left, err := r.tickets.AnyAvailable(ctx, eventID)
	if err != nil || left {
		return false
	}
	r.soldOut.Store(eventID, struct{}{})
	return true
}

// replay returns the user's existing booking for an idempotent retry. ON CONFLICT
// only reports a conflict against a committed row, so the booking is visible here.
func (r *BookingRepo) replay(ctx context.Context, eventID, userID int64) (BookingResult, error) {
	db := r.db.WithContext(ctx)

	var existing Bookings
	err := db.Select("id").
		Where("event_id = ? AND user_id = ?", eventID, userID).
		Take(&existing).Error
	if err != nil {
		return BookingResult{}, txError(ctx, "load existing booking", err)
	}

	ticketNos, err := r.tickets.NumbersForBooking(ctx, existing.ID)
	if err != nil {
		return BookingResult{}, txError(ctx, "load existing tickets", err)
	}

	return BookingResult{BookingID: existing.ID, Tickets: ticketNos, Replayed: true}, nil
}

// txError classifies an error raised after Begin: a deadline, cancellation or
// cancelled query is timeout_db; anything else is wrapped as an internal error.
func txError(ctx context.Context, step string, err error) error {
	var pgErr *pgconn.PgError
	if ctx.Err() != nil ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		(errors.As(err, &pgErr) && pgErr.Code == pgQueryCanceled) {
		return ErrTimeoutDB
	}
	return fmt.Errorf("%s: %w", step, err)
}
