package main

import (
	"fmt"
	"io"

	"runova/internal/config"
	"runova/internal/store"
)

// dbCmd handles `runova db version`: the schema version stored in the
// database and the newest one this binary can migrate to. It only reads, so
// it is safe while the service runs and never creates the database.
func dbCmd(cfg config.Config, args []string, stdout, stderr io.Writer) int {
	if len(args) != 1 || args[0] != "version" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	current, latest, err := store.SchemaVersion(cfg.DBPath())
	if err != nil {
		fmt.Fprintf(stderr, "Tidak bisa membaca versi skema: %v\n", err)
		return 1
	}
	if current == 0 {
		fmt.Fprintln(stdout, "Skema database: 0 (belum dibuat)")
	} else {
		fmt.Fprintf(stdout, "Skema database: %d\n", current)
	}
	fmt.Fprintf(stdout, "Skema tertinggi binary ini: %d\n", latest)
	return 0
}
