package main

import (
	"fmt"
	"io"
	"os"

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
		fmt.Fprintf(stderr, "Could not read the schema version: %v\n", err)
		return 1
	}
	switch _, statErr := os.Stat(cfg.DBPath()); {
	case current > 0:
		fmt.Fprintf(stdout, "Database schema: %d\n", current)
	case statErr == nil:
		fmt.Fprintln(stdout, "Database schema: 0 (not migrated yet; migrated when Runova opens it)")
	default:
		fmt.Fprintln(stdout, "Database schema: 0 (not created yet)")
	}
	fmt.Fprintf(stdout, "Highest schema of this binary: %d\n", latest)
	return 0
}
