package main

import (
	"log"
	"net/http"
	"stampede/appconfig"
	"stampede/router"
	"time"
)

func main() {
	if err := appconfig.AppConfig(); err != nil {
		log.Fatal("Failed to load app config:", err)
	}

	srv := &http.Server{
		Addr:              ":8080",
		Handler:           router.NewRouter(),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      5 * time.Second, // must stay above the per-request DB deadline
		IdleTimeout:       60 * time.Second,
	}

	log.Fatal(srv.ListenAndServe())
}
