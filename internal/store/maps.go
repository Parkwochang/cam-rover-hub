package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type Map struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	FilePath  string `json:"-"`
	CreatedAt string `json:"created_at"`
}

type Pose struct {
	X          float64 `json:"x"`
	Y          float64 `json:"y"`
	Heading    float64 `json:"heading"`
	Confidence float64 `json:"confidence"`
	CreatedAt  string  `json:"created_at"`
}

func CreateMap(ctx context.Context, db *sql.DB, name string) (Map, error) {
	result, err := db.ExecContext(ctx, `INSERT INTO maps(name,status) VALUES(?, 'mapping')`, name)
	if err != nil {
		return Map{}, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Map{}, err
	}
	return GetMap(ctx, db, id)
}

func GetMap(ctx context.Context, db *sql.DB, id int64) (Map, error) {
	var m Map
	err := db.QueryRowContext(ctx, `SELECT id,name,status,file_path,created_at FROM maps WHERE id=?`, id).Scan(&m.ID, &m.Name, &m.Status, &m.FilePath, &m.CreatedAt)
	return m, err
}

func LatestSavedMap(ctx context.Context, db *sql.DB) (Map, error) {
	var m Map
	err := db.QueryRowContext(ctx, `SELECT id,name,status,file_path,created_at FROM maps WHERE status='saved' ORDER BY id DESC LIMIT 1`).Scan(&m.ID, &m.Name, &m.Status, &m.FilePath, &m.CreatedAt)
	return m, err
}

func ListMaps(ctx context.Context, db *sql.DB) ([]Map, error) {
	rows, err := db.QueryContext(ctx, `SELECT id,name,status,file_path,created_at FROM maps ORDER BY id DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	maps := make([]Map, 0)
	for rows.Next() {
		var m Map
		if err := rows.Scan(&m.ID, &m.Name, &m.Status, &m.FilePath, &m.CreatedAt); err != nil {
			return nil, err
		}
		maps = append(maps, m)
	}
	return maps, rows.Err()
}

func SetMapStatus(ctx context.Context, db *sql.DB, id int64, status, filePath string) error {
	if status != "mapping" && status != "saved" && status != "failed" {
		return errors.New("invalid map status")
	}
	_, err := db.ExecContext(ctx, `UPDATE maps SET status=?,file_path=? WHERE id=?`, status, filePath, id)
	return err
}

func AddPose(ctx context.Context, db *sql.DB, id int64, p Pose) error {
	_, err := db.ExecContext(ctx, `INSERT INTO poses(map_id,x,y,heading,confidence,created_at) VALUES(?,?,?,?,?,?)`, id, p.X, p.Y, p.Heading, p.Confidence, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func ListPoses(ctx context.Context, db *sql.DB, id int64) ([]Pose, error) {
	rows, err := db.QueryContext(ctx, `SELECT x,y,heading,confidence,created_at FROM poses WHERE map_id=? ORDER BY id DESC LIMIT 2000`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	poses := make([]Pose, 0)
	for rows.Next() {
		var p Pose
		if err := rows.Scan(&p.X, &p.Y, &p.Heading, &p.Confidence, &p.CreatedAt); err != nil {
			return nil, err
		}
		poses = append(poses, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(poses)-1; i < j; i, j = i+1, j-1 {
		poses[i], poses[j] = poses[j], poses[i]
	}
	return poses, nil
}
