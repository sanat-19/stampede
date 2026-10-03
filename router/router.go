package router

import (
	"stampede/api"
	"stampede/appconfig"
	"stampede/models"

	"github.com/gorilla/mux"
)

func NewRouter() *mux.Router {
	eventRepo := models.NewEventRepo(appconfig.DB)
	eventHandler := api.NewEventHandler(eventRepo)

	// bookingRepo := models.NewBookingRepo(appconfig.DB)
	// ticketRepo := models.NewTicketRepo(appconfig.DB)

	r := mux.NewRouter()

	r.HandleFunc("/events", eventHandler.GetEvents).Methods("GET")

	return r
}
