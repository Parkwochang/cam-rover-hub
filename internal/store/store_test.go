package store

import (
	"path/filepath"
	"testing"
)

func TestOpenCreatesUsableSchema(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "nested", "rover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO maps(name, status) VALUES ('home', 'mapping')`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM maps`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("map count = %d, err = %v", count, err)
	}
}
