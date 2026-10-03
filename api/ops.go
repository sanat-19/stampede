package api

import (
	"context"
	"net/http"
	"time"

	"gorm.io/gorm"
)

type OpsHandler struct {
	db *gorm.DB
}

func NewOpsHandler(db *gorm.DB) *OpsHandler {
	return &OpsHandler{db: db}
}

// Healthz serves GET /healthz: 200 if the process is up and a DB ping succeeds.
func (h *OpsHandler) Healthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()

	sqlDB, err := h.db.DB()
	if err == nil {
		err = sqlDB.PingContext(ctx)
	}
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unhealthy", "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
