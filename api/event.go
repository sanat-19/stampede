package api

import (
	"context"
	"log/slog"
	"net/http"
	"stampede/models"
	"strconv"
	"time"
)

const availabilityTimeout = 2 * time.Second

type EventHandler struct {
	eventRepo  *models.EventRepo
	ticketRepo *models.TicketRepo
}

func NewEventHandler(eventRepo *models.EventRepo, ticketRepo *models.TicketRepo) *EventHandler {
	return &EventHandler{eventRepo: eventRepo, ticketRepo: ticketRepo}
}

func (h *EventHandler) GetEvents(w http.ResponseWriter, r *http.Request) {
	// Implement the logic to retrieve events from the database
	// and return them as a JSON response.

}

// Availability serves GET /availability?event_id=1. Not used by the load test.
func (h *EventHandler) Availability(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(r.URL.Query().Get("event_id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"outcome": "invalid", "error": "event_id is required"})
		return
	}

	// Same pool as bookings: bound the wait so a saturated pool cannot hang this endpoint.
	ctx, cancel := context.WithTimeout(r.Context(), availabilityTimeout)
	defer cancel()

	exists, err := h.eventRepo.Exists(ctx, eventID)
	if err == nil && !exists {
		writeJSON(w, http.StatusNotFound, map[string]string{"outcome": "invalid", "error": "unknown event_id"})
		return
	}

	var remaining int64
	if err == nil {
		// Counted from the tickets themselves; events has no remaining counter.
		remaining, err = h.ticketRepo.CountAvailable(ctx, eventID)
	}
	if err != nil {
		slog.Error("availability failed", "event_id", eventID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"outcome": "error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"event_id": eventID, "remaining": remaining})
}
