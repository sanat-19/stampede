package api

import (
	"errors"
	"log/slog"
	"net/http"
	"stampede/models"
	"strconv"

	"gorm.io/gorm"
)

type EventHandler struct {
	eventRepo *models.EventRepo
}

func NewEventHandler(eventRepo *models.EventRepo) *EventHandler {
	return &EventHandler{eventRepo: eventRepo}
}

func (h *EventHandler) GetEvents(w http.ResponseWriter, r *http.Request) {
	// Implement the logic to retrieve events from the database
	// and return them as a JSON response.

}

// Availability serves GET /availability?event_id=1. Not used by the load test in Phase 1.
func (h *EventHandler) Availability(w http.ResponseWriter, r *http.Request) {
	eventID, err := strconv.ParseInt(r.URL.Query().Get("event_id"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"outcome": "invalid", "error": "event_id is required"})
		return
	}

	remaining, err := h.eventRepo.Remaining(r.Context(), eventID)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"outcome": "invalid", "error": "unknown event_id"})
	case err != nil:
		slog.Error("availability failed", "event_id", eventID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"outcome": "error"})
	default:
		writeJSON(w, http.StatusOK, map[string]int64{"event_id": eventID, "remaining": int64(remaining)})
	}
}
