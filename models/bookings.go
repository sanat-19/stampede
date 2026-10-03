package models

import "gorm.io/gorm"

type BookingStatus string

const (
	Held      BookingStatus = "held"
	Confirmed BookingStatus = "confirmed"
	Expired   BookingStatus = "expired"
	Failed    BookingStatus = "failed"
)

type Bookings struct {
	ID       int           `json:"id" gorm:"primaryKey"`
	EventID  int           `json:"event_id" gorm:"column:event_id"`
	UserID   int           `json:"user_id" gorm:"column:user_id"`
	Quantity int           `json:"qty" gorm:"column:qty"`
	Status   BookingStatus `json:"status" gorm:"column:status"`
}

func (Bookings) TableName() string {
	return "bookings"
}

type BookingRepo struct {
	db *gorm.DB
}

func NewBookingRepo(db *gorm.DB) *BookingRepo {
	return &BookingRepo{db: db}
}
