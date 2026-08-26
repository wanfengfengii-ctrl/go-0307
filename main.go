// Command lyophilizer-sterilization-validation runs the freeze-dryer steam
// sterilization validation web service: a Go backend with an embedded
// operations page for plan locking, probe collection, lethality calculation,
// deviation retesting and sterile release.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"lyophilizer-sterilization-validation/internal/httpapi"
	"lyophilizer-sterilization-validation/internal/store"
)

func main() {
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "lyophilizer.db"
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Startup recovery: validate every snapshot checksum so a torn or corrupted
	// projection is detected before the service accepts traffic.
	if n, err := st.Recover(context.Background()); err != nil {
		log.Fatalf("recover: %v", err)
	} else {
		log.Printf("recovered %d snapshot(s)", n)
	}

	addr := os.Getenv("ADDR")
	if addr == "" {
		addr = ":8080"
	}

	log.Printf("lyophilizer-sterilization-validation listening on %s (db=%s)", addr, dbPath)
	if err := http.ListenAndServe(addr, httpapi.NewServer(st)); err != nil {
		log.Fatal(err)
	}
}
