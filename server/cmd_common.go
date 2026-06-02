package main

import (
	"flag"
	"os"
	"path/filepath"

	"github.com/7c/pingmon/classes/store"
)

// addStoreFlags registers --datafolder/--db on a command's flag set, matching
// the server defaults so CLI commands and the server share the same data.
func addStoreFlags(fs *flag.FlagSet) (dataFolder, db *string) {
	dataFolder = fs.String("datafolder", "data", "Directory for persistent data")
	db = fs.String("db", "pingmon.db", "Database filename (within --datafolder) or an absolute path")
	return
}

// openStoreFromFlags resolves the data folder + db path (creating the folder if
// needed) and opens the store. Returns the resolved db path for display.
func openStoreFromFlags(dataFolder, db string) (*store.Store, string, error) {
	abs, err := filepath.Abs(dataFolder)
	if err != nil {
		return nil, "", err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, "", err
	}
	dbPath := resolveDBPath(abs, db)
	st, err := store.New(dbPath)
	if err != nil {
		return nil, dbPath, err
	}
	return st, dbPath, nil
}
