package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAddressSettingsSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hub.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveRoverAddress(context.Background(), db, "http://cam-rover.local"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := RoverAddress(context.Background(), db)
	if err != nil || got != "http://cam-rover.local" {
		t.Fatalf("address=%q err=%v", got, err)
	}
}
