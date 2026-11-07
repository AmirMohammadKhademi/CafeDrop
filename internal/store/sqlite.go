package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct{ DB *sql.DB }
type UploadFile struct {
	ID           int64  `json:"id"`
	OriginalName string `json:"original_name"`
	StoredName   string `json:"stored_name"`
	SizeBytes    int64  `json:"size_bytes"`
}

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
CREATE INDEX IF NOT EXISTS idx_uploads_code ON uploads(code);
CREATE TABLE IF NOT EXISTS client_sessions (
 token TEXT PRIMARY KEY,
 ip TEXT NOT NULL,
 user_agent TEXT NOT NULL,
 created_at TEXT NOT NULL,
 last_seen TEXT NOT NULL,
 expires_at TEXT NOT NULL,
 grace_until TEXT NOT NULL DEFAULT '',
 status TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_client_sessions_status ON client_sessions(status, created_at);
CREATE TABLE IF NOT EXISTS session_config (
 id INTEGER PRIMARY KEY CHECK(id=1),
 max_active INTEGER NOT NULL DEFAULT 12,
 duration_minutes INTEGER NOT NULL DEFAULT 60,
 grace_minutes INTEGER NOT NULL DEFAULT 5
);
INSERT OR IGNORE INTO session_config(id,max_active,duration_minutes,grace_minutes) VALUES(1,12,60,5);
CREATE TABLE IF NOT EXISTS tracking_counter (
 id INTEGER PRIMARY KEY CHECK(id=1),
 generation INTEGER NOT NULL DEFAULT -1,
 sequence INTEGER NOT NULL DEFAULT 0
);
INSERT OR IGNORE INTO tracking_counter(id,generation,sequence) VALUES(1,-1,0);`); err != nil {
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

func (s *Store) ListUploadFiles(uploadID int64) ([]UploadFile, error) {
	rows, err := s.DB.Query(`SELECT id,original_name,stored_name,size_bytes FROM upload_files WHERE upload_id=? ORDER BY id ASC`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UploadFile{}
	for rows.Next() {
		var f UploadFile
		if err := rows.Scan(&f.ID, &f.OriginalName, &f.StoredName, &f.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) FindUploadFile(uploadID, fileID int64) (*UploadFile, error) {
	var f UploadFile
	err := s.DB.QueryRow(`SELECT id,original_name,stored_name,size_bytes FROM upload_files WHERE upload_id=? AND id=?`, uploadID, fileID).Scan(&f.ID, &f.OriginalName, &f.StoredName, &f.SizeBytes)
	if err != nil {
		return nil, err
	}
	return &f, nil
}

// StartTrackingRun advances the persistent run number once per successful server start.
func (s *Store) StartTrackingRun() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE tracking_counter SET generation=generation+1, sequence=0 WHERE id=1`); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	return nil
}

// NextTrackingCode allocates the next short, sequential code in the current run.
func (s *Store) NextTrackingCode() (string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	for attempts := 0; attempts < 100000; attempts++ {
		if _, err = tx.Exec(`UPDATE tracking_counter SET sequence=sequence+1 WHERE id=1`); err != nil {
			return "", err
		}
		var sequence, generation int64
		if err = tx.QueryRow(`SELECT sequence,generation FROM tracking_counter WHERE id=1`).Scan(&sequence, &generation); err != nil {
			return "", err
		}
		code := fmt.Sprintf("%d-%d", generation, sequence)
		var existing int
		err = tx.QueryRow(`SELECT 1 FROM uploads WHERE code=? LIMIT 1`, code).Scan(&existing)
		if errors.Is(err, sql.ErrNoRows) {
			if err = tx.Commit(); err != nil {
				return "", err
			}
			return code, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not allocate unused tracking code")
}

// SearchUploads matches a complete code, either numeric component, or a partial code.
func (s *Store) SearchUploads(q string, limit int) ([]Upload, error) {
	rows, err := s.DB.Query(`SELECT id,code,created_at,status,file_count,total_bytes,folder FROM uploads WHERE code LIKE ? ESCAPE '\' ORDER BY id DESC LIMIT ?`, "%"+escapeLike(q)+"%", limit)
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
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}
func (s *Store) LatestCompletedID() (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM uploads WHERE status='completed'`).Scan(&id)
	return id, err
}
