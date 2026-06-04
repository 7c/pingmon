package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/7c/pingmon/classes/store"
)

// addStoreFlags registers --datafolder on a command's flag set, matching the
// server default so CLI commands and the server share the same data.
func addStoreFlags(fs *flag.FlagSet) (dataFolder *string) {
	return fs.String("datafolder", defaultDataFolder, "Directory for persistent data (database is <datafolder>/pingmon.db)")
}

// openStoreFromFlags resolves the data folder to an absolute path (creating it
// if needed) and opens the store at <datafolder>/pingmon.db. Returns the
// resolved db path for display.
func openStoreFromFlags(dataFolder string) (*store.Store, string, error) {
	abs, err := filepath.Abs(dataFolder)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, "", err
	}
	dbPath := resolveDBPath(abs)
	st, err := store.New(dbPath)
	if err != nil {
		return nil, dbPath, err
	}
	return st, dbPath, nil
}
