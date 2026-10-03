package router

import (
	"stampede/api"
	"stampede/appconfig"
	"stampede/models"

	"github.com/gorilla/mux"
)

func NewRouter(booker models.Booker) *mux.Router {
	eventRepo := models.NewEventRepo(appconfig.DB)
	eventHandler := api.NewEventHandler(eventRepo)
	bookHandler := api.NewBookHandler(booker, appconfig.Cfg.EventID, appconfig.Cfg.RequestTimeout)
	opsHandler := api.NewOpsHandler(appconfig.DB)

	// ticketRepo := models.NewTicketRepo(appconfig.DB)

	r := mux.NewRouter()

	r.HandleFunc("/book", bookHandler.Book).Methods("POST")
	r.HandleFunc("/availability", eventHandler.Availability).Methods("GET")
	r.HandleFunc("/events", eventHandler.GetEvents).Methods("GET")
	r.HandleFunc("/healthz", opsHandler.Healthz).Methods("GET")

	return r
}
