package models

import (
	"context"
	"time"

	"gorm.io/gorm"
)

type Event struct {
	ID               int64     `json:"id" gorm:"primaryKey"`
	Name             string    `json:"name" gorm:"column:name"`
	TotalTickets     int       `json:"total_tickets" gorm:"column:total_tickets"`
	RemainingTickets int       `json:"remaining" gorm:"column:remaining"`
	SaleStartsAt     time.Time `json:"sale_starts_at" gorm:"column:sale_starts_at"`
}

func (Event) TableName() string {
	return "events"
}

type EventRepo struct {
	db *gorm.DB
}

func NewEventRepo(db *gorm.DB) *EventRepo {
	return &EventRepo{db: db}
}

// Remaining returns the event's remaining counter, or gorm.ErrRecordNotFound.
func (r *EventRepo) Remaining(ctx context.Context, eventID int64) (int, error) {
	return remaining(r.db.WithContext(ctx), eventID)
}

// RemainingTx is Remaining on the caller's transaction. It takes no lock: it
// reads the last committed value, so it can only be used to reject early.
func (r *EventRepo) RemainingTx(tx *gorm.DB, eventID int64) (int, error) {
	return remaining(tx, eventID)
}

func remaining(db *gorm.DB, eventID int64) (int, error) {
	var remaining []int
	err := db.
		Model(&Event{}).
		Where("id = ?", eventID).
		Pluck("remaining", &remaining).Error
	if err != nil {
		return 0, err
	}
	if len(remaining) == 0 {
		return 0, gorm.ErrRecordNotFound
	}
	return remaining[0], nil
}
