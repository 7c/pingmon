package store

import (
	"database/sql"
	"errors"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// ErrConflict is returned when a uniqueness constraint would be violated (e.g. a
// duplicate group name).
var ErrConflict = errors.New("conflict")

// notFoundIfNoRows inspects an UPDATE/DELETE result and returns ErrNotFound when
// no rows were affected.
func notFoundIfNoRows(res sql.Result, _ string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
