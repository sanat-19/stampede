package api

import (
	"encoding/json"
	"net/http"
)

type TestHandler struct {
}

func NewTestHandler() TestHandler {
	return TestHandler{}
}

func (h *TestHandler) Test(w http.ResponseWriter, r *http.Request) {
	response := map[string]interface{}{
		"success": true,
		"message": "Test API working",
		"data": map[string]string{
			"name":   "Sanat",
			"status": "OK",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
