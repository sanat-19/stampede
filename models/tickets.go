package models

import (
	"context"

	"gorm.io/gorm"
)

type TicketStatus string

const (
	TicketAvailable TicketStatus = "available"
	TicketHeld      TicketStatus = "held"
	TicketSold      TicketStatus = "sold"
)

type Tickets struct {
	ID           int64        `json:"id" gorm:"primaryKey"`
	EventID      int64        `json:"event_id" gorm:"column:event_id"`
	TicketNumber string       `json:"ticket_no" gorm:"column:ticket_no"`
	Status       TicketStatus `json:"status" gorm:"column:status;type:ticket_status"`
	BookingID    *int64       `json:"booking_id" gorm:"column:booking_id"` // NULL while available
}

func (Tickets) TableName() string {
	return "tickets"
}

// claimTicketsSQL claims qty available tickets and marks them sold in one round trip.
//
// tx.Raw exception: GORM's builder cannot express
// UPDATE ... FROM (SELECT ... FOR UPDATE SKIP LOCKED) ... RETURNING as a single statement.
// ORDER BY id matches tickets_available_idx, so the planner walks the partial index instead of sorting.
const claimTicketsSQL = `
WITH picked AS (
    SELECT id FROM tickets
    WHERE event_id = ? AND status = 'available'
    ORDER BY id
    LIMIT ?
    FOR UPDATE SKIP LOCKED
)
UPDATE tickets t
SET status = 'sold', booking_id = ?
FROM picked
WHERE t.id = picked.id
RETURNING t.ticket_no`

type TicketRepo struct {
	db *gorm.DB
}

func NewTicketRepo(db *gorm.DB) *TicketRepo {
	return &TicketRepo{db: db}
}

// Claim marks up to qty available tickets as sold for the booking and returns
// their ticket numbers. It runs on the caller's transaction, so the tickets are
// released if that transaction rolls back. SKIP LOCKED lets concurrent buyers
// take different rows without waiting, so fewer than qty may come back.
func (r *TicketRepo) Claim(tx *gorm.DB, eventID, bookingID int64, qty int) ([]string, error) {
	var ticketNos []string
	err := tx.Raw(claimTicketsSQL, eventID, qty, bookingID).Scan(&ticketNos).Error
	return ticketNos, err
}

// NumbersForBooking returns the ticket numbers belonging to a booking, in ticket order.
func (r *TicketRepo) NumbersForBooking(ctx context.Context, bookingID int64) ([]string, error) {
	var ticketNos []string
	err := r.db.WithContext(ctx).
		Model(&Tickets{}).
		Where("booking_id = ?", bookingID).
		Order("id").
		Pluck("ticket_no", &ticketNos).Error
	return ticketNos, err
}
