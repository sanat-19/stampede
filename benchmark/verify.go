// Command verify checks the booking invariants after a load test. It prints a
// report and exits non-zero if any check fails. It talks to Postgres directly
// and imports nothing from the backend.
//
//	go run ./benchmark
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type check struct {
	name string
	ok   bool
	got  string
}

func main() {
	godotenv.Load()

	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Fatal("DATABASE_URL is not set")
	}
	eventID := int64(1)
	if v := os.Getenv("EVENT_ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			log.Fatalf("invalid EVENT_ID %q", v)
		}
		eventID = n
	}

	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)})
	if err != nil {
		log.Fatal("connect: ", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := verify(gdb.WithContext(ctx), eventID); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func verify(db *gorm.DB, eventID int64) error {
	var event struct {
		TotalTickets int64
		Remaining    int64
	}
	if err := db.Raw(`SELECT total_tickets, remaining FROM events WHERE id = ?`, eventID).Scan(&event).Error; err != nil {
		return fmt.Errorf("load event: %w", err)
	}

	queries := map[string]string{
		"sold_by_bookings": `SELECT coalesce(sum(qty), 0) FROM bookings WHERE event_id = ? AND status = 'confirmed'`,
		"sold_by_tickets":  `SELECT count(*) FROM tickets WHERE event_id = ? AND status = 'sold'`,
		"held_tickets":     `SELECT count(*) FROM tickets WHERE event_id = ? AND status = 'held'`,
		"sold_no_booking":  `SELECT count(*) FROM tickets WHERE event_id = ? AND status = 'sold' AND booking_id IS NULL`,
		"avail_w_booking":  `SELECT count(*) FROM tickets WHERE event_id = ? AND status = 'available' AND booking_id IS NOT NULL`,
		"qty_mismatches": `
			SELECT count(*)
			FROM bookings b
			LEFT JOIN (
				SELECT booking_id, count(*) AS n FROM tickets WHERE booking_id IS NOT NULL GROUP BY booking_id
			) t ON t.booking_id = b.id
			WHERE b.event_id = ? AND b.status = 'confirmed' AND coalesce(t.n, 0) <> b.qty`,
		"duplicate_users": `
			SELECT count(*) FROM (
				SELECT user_id FROM bookings WHERE event_id = ? GROUP BY user_id HAVING count(*) > 1
			) d`,
		"total_bookings": `SELECT count(*) FROM bookings WHERE event_id = ?`,
	}
	n := make(map[string]int64, len(queries))
	for name, q := range queries {
		var v int64
		if err := db.Raw(q, eventID).Scan(&v).Error; err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		n[name] = v
	}

	soldByCounter := event.TotalTickets - event.Remaining
	checks := []check{
		{"sold_by_counter = sold_by_bookings = sold_by_tickets",
			soldByCounter == n["sold_by_bookings"] && n["sold_by_bookings"] == n["sold_by_tickets"],
			fmt.Sprintf("counter=%d bookings=%d tickets=%d", soldByCounter, n["sold_by_bookings"], n["sold_by_tickets"])},
		{"remaining >= 0", event.Remaining >= 0, fmt.Sprint(event.Remaining)},
		{"sold_by_tickets <= total_tickets", n["sold_by_tickets"] <= event.TotalTickets,
			fmt.Sprintf("%d <= %d", n["sold_by_tickets"], event.TotalTickets)},
		{"no held tickets (Phase 1 has no holds)", n["held_tickets"] == 0, fmt.Sprint(n["held_tickets"])},
		{"every sold ticket has a booking_id", n["sold_no_booking"] == 0, fmt.Sprint(n["sold_no_booking"])},
		{"every available ticket has NULL booking_id", n["avail_w_booking"] == 0, fmt.Sprint(n["avail_w_booking"])},
		{"tickets per confirmed booking = qty", n["qty_mismatches"] == 0, fmt.Sprintf("%d mismatched bookings", n["qty_mismatches"])},
		{"at most one booking per user", n["duplicate_users"] == 0, fmt.Sprintf("%d users with duplicates", n["duplicate_users"])},
	}

	fmt.Printf("Invariant report — event %d\n\n", eventID)
	failed := 0
	for _, c := range checks {
		status := "PASS"
		if !c.ok {
			status = "FAIL"
			failed++
		}
		fmt.Printf("  [%s] %-48s %s\n", status, c.name, c.got)
	}

	fmt.Printf("\n  total bookings  %d\n", n["total_bookings"])
	fmt.Printf("  tickets total   %d\n", event.TotalTickets)
	fmt.Printf("  tickets sold    %d\n", n["sold_by_tickets"])
	fmt.Printf("  tickets unsold  %d\n\n", event.TotalTickets-n["sold_by_tickets"])

	if failed > 0 {
		return fmt.Errorf("%d check(s) failed", failed)
	}
	fmt.Println("All checks passed")
	return nil
}
