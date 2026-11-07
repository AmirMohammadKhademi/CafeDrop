package store

import (
	"database/sql"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }
type Upload struct {
	ID         int64     `json:"id"`
	Code       string    `json:"code"`
	CreatedAt  time.Time `json:"created_at"`
	Status     string    `json:"status"`
	FileCount  int       `json:"file_count"`
	TotalBytes int64     `json:"total_bytes"`
	Folder     string    `json:"-"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;
CREATE TABLE IF NOT EXISTS uploads (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 code TEXT NOT NULL UNIQUE,
 created_at TEXT NOT NULL,
 status TEXT NOT NULL,
 file_count INTEGER NOT NULL DEFAULT 0,
 total_bytes INTEGER NOT NULL DEFAULT 0,
 folder TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS upload_files (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 upload_id INTEGER NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
 original_name TEXT NOT NULL,
 stored_name TEXT NOT NULL,
 size_bytes INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_uploads_code ON uploads(code);`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) CreateUpload(code, folder string) (int64, error) {
	res, err := s.DB.Exec(`INSERT INTO uploads(code,created_at,status,folder) VALUES(?,?,?,?)`, code, time.Now().UTC().Format(time.RFC3339Nano), "uploading", folder)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}
func (s *Store) AddFile(uploadID int64, original, stored string, size int64) error {
	_, err := s.DB.Exec(`INSERT INTO upload_files(upload_id,original_name,stored_name,size_bytes) VALUES(?,?,?,?)`, uploadID, original, stored, size)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(`UPDATE uploads SET file_count=file_count+1,total_bytes=total_bytes+? WHERE id=?`, size, uploadID)
	return err
}
func (s *Store) FinishUpload(id int64, status string) error {
	_, err := s.DB.Exec(`UPDATE uploads SET status=? WHERE id=?`, status, id)
	return err
}
func (s *Store) ListUploads(limit int) ([]Upload, error) {
	rows, err := s.DB.Query(`SELECT id,code,created_at,status,file_count,total_bytes,folder FROM uploads ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upload{}
	for rows.Next() {
		var u Upload
		var ts string
		if err := rows.Scan(&u.ID, &u.Code, &ts, &u.Status, &u.FileCount, &u.TotalBytes, &u.Folder); err != nil {
			return nil, err
		}
		u.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) FindUpload(code string) (*Upload, error) {
	var u Upload
	var ts string
	err := s.DB.QueryRow(`SELECT id,code,created_at,status,file_count,total_bytes,folder FROM uploads WHERE code=?`, code).Scan(&u.ID, &u.Code, &ts, &u.Status, &u.FileCount, &u.TotalBytes, &u.Folder)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
	return &u, nil
}

func ErrNoRows() error        { return sql.ErrNoRows }
func IsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
