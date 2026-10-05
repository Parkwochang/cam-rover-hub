package store

import (
	"context"
	"database/sql"
	"errors"
)

func RoverAddress(ctx context.Context, db *sql.DB) (string, error) {
	var address string
	err := db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key='rover_address'`).Scan(&address)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return address, err
}

func SaveRoverAddress(ctx context.Context, db *sql.DB, address string) error {
	_, err := db.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES('rover_address',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, address)
	return err
}
