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
	RemainingTickets int       `json:"remaining" gorm:"column:remaining"` // seeded only; not updated by bookings since Phase 2a (hot row removed)
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

// Exists reports whether the event exists.
func (r *EventRepo) Exists(ctx context.Context, eventID int64) (bool, error) {
	var ids []int64
	err := r.db.WithContext(ctx).
		Model(&Event{}).
		Where("id = ?", eventID).
		Limit(1).
		Pluck("id", &ids).Error
	return len(ids) > 0, err
}
