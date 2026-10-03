package models

import "gorm.io/gorm"

type Tickets struct {
	ID           int    `json:"id" gorm:"primaryKey"`
	EventID      int    `json:"event_id" gorm:"column:event_id"`
	TicketNumber int    `json:"ticket_no" gorm:"column:ticket_no"`
	Status       string `json:"status" gorm:"column:status"`
	BookingID    int    `json:"booking_id" gorm:"column:booking_id"`
}

func (Tickets) TableName() string {
	return "tickets"
}

type TicketRepo struct {
	db *gorm.DB
}

func NewTicketRepo(db *gorm.DB) *TicketRepo {
	return &TicketRepo{db: db}
}
