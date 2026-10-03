package models

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
}

// Booking outcomes other than success. The HTTP layer maps them to responses.
var (
	ErrSoldOut     = errors.New("sold out")
	ErrTimeoutPool = errors.New("deadline exceeded waiting for a pool connection") // Postgres never saw the request
	ErrTimeoutDB   = errors.New("deadline exceeded inside the transaction")        // may have committed; a retry replays it
)

type BookingResult struct {
	BookingID int64
	Tickets   []string
	Replayed  bool // true for an idempotent replay of an existing booking (already_booked)
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
	events       *EventRepo
	tickets      *TicketRepo
	shortCircuit bool
	soldOut      sync.Map // eventID → struct{}; set once the event is known to be sold out
}

func NewBookingRepo(db *gorm.DB, shortCircuit bool) *BookingRepo {
	return &BookingRepo{db: db, events: NewEventRepo(db), tickets: NewTicketRepo(db), shortCircuit: shortCircuit}
}

// SoldOut reports whether the in-process sold-out flag is set for the event.
func (r *BookingRepo) SoldOut(eventID int64) bool {
	_, ok := r.soldOut.Load(eventID)
	return ok
}

func (r *BookingRepo) Book(ctx context.Context, eventID, userID int64, qty int) (BookingResult, error) {
	// Safe to reject without the DB: the flag can only reject, never approve.
	if r.shortCircuit && r.SoldOut(eventID) {
		return BookingResult{}, ErrSoldOut
	}

	// Begun manually rather than db.Transaction(fn) so a failure here — waiting for
	// a pool connection — can be told apart from a failure inside the transaction.
	tx := r.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		if ctx.Err() != nil {
			return BookingResult{}, ErrTimeoutPool
		}
		return BookingResult{}, fmt.Errorf("begin: %w", tx.Error)
	}
	finished := false
	defer func() {
		if !finished {
			tx.Rollback()
		}
	}()

	// The booking row. UNIQUE (event_id, user_id) makes a retry conflict
	// instead of creating a second booking.
	booking := Bookings{EventID: eventID, UserID: userID, Quantity: qty, Status: Confirmed}
	res := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "event_id"}, {Name: "user_id"}},
		DoNothing: true,
	}).Create(&booking)
	if res.Error != nil {
		return BookingResult{}, txError(ctx, "insert booking", res.Error)
	}
	if res.RowsAffected == 0 {
		// Release this connection before the lookup so one request never holds two.
		tx.Rollback()
		finished = true
		return r.replay(ctx, eventID, userID)
	}

	// Check the counter before touching the tickets table: if fewer than qty are
	// left, reject without scanning for tickets. Unlocked read of the committed
	// value — it only rejects early; the decrement below still enforces the limit.
	left, err := r.events.RemainingTx(tx, eventID)
	if err != nil {
		return BookingResult{}, txError(ctx, "check remaining", err)
	}
	if left < qty {
		if left == 0 {
			r.soldOut.Store(eventID, struct{}{})
		}
		return BookingResult{}, ErrSoldOut
	}

	// Claim tickets. No partial fulfilment — fewer than qty means sold out.
	ticketNos, err := r.tickets.Claim(tx, eventID, booking.ID, qty)
	if err != nil {
		return BookingResult{}, txError(ctx, "claim tickets", err)
	}
	if len(ticketNos) < qty {
		return BookingResult{}, ErrSoldOut
	}

	// The hot counter, last, so its row lock is held for the shortest time.
	var event Event
	res = tx.Model(&event).
		Clauses(clause.Returning{Columns: []clause.Column{{Name: "remaining"}}}).
		Where("id = ? AND remaining >= ?", eventID, qty).
		Update("remaining", gorm.Expr("remaining - ?", qty))
	if res.Error != nil {
		return BookingResult{}, txError(ctx, "decrement remaining", res.Error)
	}
	if res.RowsAffected == 0 {
		r.soldOut.Store(eventID, struct{}{})
		return BookingResult{}, ErrSoldOut
	}

	if err := tx.Commit().Error; err != nil {
		return BookingResult{}, txError(ctx, "commit", err)
	}
	finished = true

	// The counter only reaches 0 once every ticket is committed as sold, so this
	// is the point where the event is definitely sold out.
	if event.RemainingTickets == 0 {
		r.soldOut.Store(eventID, struct{}{})
	}

	return BookingResult{BookingID: booking.ID, Tickets: ticketNos}, nil
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
