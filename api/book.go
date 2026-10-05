package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"stampede/models"
	"time"
)

const maxBookBodyBytes = 1 << 10

type BookHandler struct {
	booker      models.Booker
	eventID     int64
	timeout     time.Duration
	logRequests bool // one log line per booking with its timing; off under load (logging costs CPU)
}

func NewBookHandler(booker models.Booker, eventID int64, timeout time.Duration, logRequests bool) *BookHandler {
	return &BookHandler{booker: booker, eventID: eventID, timeout: timeout, logRequests: logRequests}
}

// Book serves POST /book. Every reply carries an outcome field, which k6 counts,
// and a Server-Timing header splitting the time into pool wait and DB time.
func (h *BookHandler) Book(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	req, msg := h.decode(r)
	if msg != "" {
		writeJSON(w, http.StatusBadRequest, models.BookResponse{Outcome: "invalid", Error: msg})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	res, err := h.booker.Book(ctx, *req.EventID, *req.UserID, *req.Qty)

	var status int
	var resp models.BookResponse
	switch {
	case err == nil && res.Replayed:
		status, resp = http.StatusOK, models.BookResponse{Outcome: "already_booked", BookingID: res.BookingID, Tickets: res.Tickets}
	case err == nil:
		status, resp = http.StatusCreated, models.BookResponse{Outcome: "booked", BookingID: res.BookingID, Tickets: res.Tickets}
	case errors.Is(err, models.ErrEventSoldOut):
		status, resp = http.StatusConflict, models.BookResponse{Outcome: "sold_out", EventSoldOut: true}
	case errors.Is(err, models.ErrSoldOut):
		status, resp = http.StatusConflict, models.BookResponse{Outcome: "sold_out"}
	case errors.Is(err, models.ErrTimeoutPool):
		status, resp = http.StatusServiceUnavailable, models.BookResponse{Outcome: "timeout_pool"}
	case errors.Is(err, models.ErrTimeoutDB):
		status, resp = http.StatusServiceUnavailable, models.BookResponse{Outcome: "timeout_db"}
	default:
		slog.Error("booking failed", "event_id", *req.EventID, "user_id", *req.UserID, "qty", *req.Qty, "err", err)
		status, resp = http.StatusInternalServerError, models.BookResponse{Outcome: "error"}
	}

	total := time.Since(start)
	w.Header().Set("Server-Timing", fmt.Sprintf("pool;dur=%.2f, db;dur=%.2f, total;dur=%.2f",
		ms(res.Timing.PoolWait), ms(res.Timing.DB), ms(total)))
	if h.logRequests {
		slog.Info("book",
			"outcome", resp.Outcome,
			"user_id", *req.UserID,
			"qty", *req.Qty,
			"pool_wait_ms", ms(res.Timing.PoolWait),
			"db_ms", ms(res.Timing.DB),
			"total_ms", ms(total),
		)
	}
	writeJSON(w, status, resp)
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
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
