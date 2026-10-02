package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMapLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "rover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	m, err := CreateMap(ctx, db, "living room")
	if err != nil {
		t.Fatal(err)
	}
	if m.Status != "mapping" {
		t.Fatalf("status = %q", m.Status)
	}
	if err := AddPose(ctx, db, m.ID, Pose{X: 1, Y: 2, Heading: 0.4, Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	if err := AddPose(ctx, db, m.ID, Pose{X: 3, Y: 4, Heading: 0.7, Confidence: 1}); err != nil {
		t.Fatal(err)
	}
	poses, err := ListPoses(ctx, db, m.ID)
	if err != nil || len(poses) != 2 || poses[0].X != 1 || poses[1].X != 3 {
		t.Fatalf("poses = %+v, %v", poses, err)
	}
	if err := SetMapStatus(ctx, db, m.ID, "saved", "/tmp/map.msg"); err != nil {
		t.Fatal(err)
	}
	saved, err := LatestSavedMap(ctx, db)
	if err != nil || saved.ID != m.ID {
		t.Fatalf("saved = %+v, %v", saved, err)
	}
	all, err := ListMaps(ctx, db)
	if err != nil || len(all) != 1 {
		t.Fatalf("maps = %+v, %v", all, err)
	}
	if err := SetMapStatus(ctx, db, m.ID, "unknown", ""); err == nil {
		t.Fatal("invalid status accepted")
	}
}
