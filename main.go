package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	_ "net/http/pprof" // registers /debug/pprof on http.DefaultServeMux, served on PPROF_ADDR only
	"os"
	"os/signal"
	"stampede/appconfig"
	"stampede/db"
	"stampede/models"
	"stampede/router"
	"syscall"
	"time"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	if err := appconfig.AppConfig(); err != nil {
		log.Fatal("Failed to load app config:", err)
	}
	cfg := appconfig.Cfg

	sqlDB, err := appconfig.DB.DB()
	if err != nil {
		log.Fatal("Failed to get sql.DB:", err)
	}

	warmCtx, cancelWarm := context.WithTimeout(context.Background(), 30*time.Second)
	err = db.Warm(warmCtx, appconfig.DB)
	cancelWarm()
	if err != nil {
		log.Fatal("Failed to warm connection pool:", err)
	}
	if err := db.LogPoolStats(appconfig.DB); err != nil {
		log.Fatal("Failed to read pool stats:", err)
	}

	booker := models.NewBookingRepo(appconfig.DB, cfg.SoldOutShortCircuit)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		pprofSrv := &http.Server{Addr: cfg.PprofAddr, Handler: http.DefaultServeMux, ReadHeaderTimeout: 5 * time.Second}
		if err := pprofSrv.ListenAndServe(); err != nil {
			slog.Error("pprof server stopped", "err", err)
		}
	}()

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router.NewRouter(booker),
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      cfg.RequestTimeout + 3*time.Second, // must stay above the booking deadline
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.RequestTimeout+3*time.Second)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening",
		"addr", cfg.HTTPAddr,
		"pprof_addr", cfg.PprofAddr,
		"event_id", cfg.EventID,
		"db_max_conns", sqlDB.Stats().MaxOpenConnections,
		"request_timeout", cfg.RequestTimeout.String(),
		"sold_out_short_circuit", cfg.SoldOutShortCircuit,
		"log_requests", cfg.LogRequests,
	)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal("HTTP server failed:", err)
	}
}
