package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"stampede/models"
	"time"
)

const maxBookBodyBytes = 1 << 10

type BookHandler struct {
	booker  models.Booker
	eventID int64
	timeout time.Duration
}

func NewBookHandler(booker models.Booker, eventID int64, timeout time.Duration) *BookHandler {
	return &BookHandler{booker: booker, eventID: eventID, timeout: timeout}
}

// Book serves POST /book. Every reply carries an outcome field, which k6 counts.
func (h *BookHandler) Book(w http.ResponseWriter, r *http.Request) {
	req, msg := h.decode(r)
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, models.BookResponse{Outcome: "invalid", Error: msg})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	res, err := h.booker.Book(ctx, *req.EventID, *req.UserID, *req.Qty)
	switch {
	case err == nil && res.Replayed:
		writeJSON(w, http.StatusOK, models.BookResponse{Outcome: "already_booked", BookingID: res.BookingID, Tickets: res.Tickets})
	case err == nil:
		writeJSON(w, http.StatusCreated, models.BookResponse{Outcome: "booked", BookingID: res.BookingID, Tickets: res.Tickets})
	case errors.Is(err, models.ErrSoldOut):
		writeJSON(w, http.StatusConflict, models.BookResponse{Outcome: "sold_out"})
	case errors.Is(err, models.ErrTimeoutPool):
		writeJSON(w, http.StatusServiceUnavailable, models.BookResponse{Outcome: "timeout_pool"})
	case errors.Is(err, models.ErrTimeoutDB):
		writeJSON(w, http.StatusServiceUnavailable, models.BookResponse{Outcome: "timeout_db"})
	default:
		slog.Error("booking failed", "event_id", *req.EventID, "user_id", *req.UserID, "qty", *req.Qty, "err", err)
		writeJSON(w, http.StatusInternalServerError, models.BookResponse{Outcome: "error"})
	}
}

// decode parses and validates the request, returning a non-empty message when invalid.
func (h *BookHandler) decode(r *http.Request) (models.BookRequest, string) {
	var req models.BookRequest
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBookBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, "bad JSON: " + err.Error()
	}

	switch {
	case req.EventID == nil || req.UserID == nil || req.Qty == nil:
		return req, "event_id, user_id and qty are required"
	case *req.EventID != h.eventID:
		return req, "unknown event_id"
	case *req.UserID < 1:
		return req, "user_id must be positive"
	case *req.Qty < 1 || *req.Qty > 4:
		return req, "qty must be between 1 and 4"
	}
	return req, ""
}
