package models

import "gorm.io/gorm"

type Event struct {
	ID               int    `json:"id" gorm:"primaryKey"`
	Name             string `json:"name" gorm:"column:name"`
	TotalTickets     int    `json:"total_tickets" gorm:"column:total_tickets"`
	RemainingTickets int    `json:"remaining" gorm:"column:remaining"`
	SaleStartsAt     string `json:"sale_starts_at" gorm:"column:sale_starts_at"`
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
