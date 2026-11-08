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
	ID          int64     `json:"id"`
	Code        string    `json:"code"`
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"`
	FileCount   int       `json:"file_count"`
	TotalBytes  int64     `json:"total_bytes"`
	Folder      string    `json:"-"`
	AdminStatus string    `json:"admin_status"`
	AdminNote   string    `json:"admin_note"`
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
 folder TEXT NOT NULL,
 admin_status TEXT NOT NULL DEFAULT 'received',
 admin_note TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS upload_files (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 upload_id INTEGER NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
 original_name TEXT NOT NULL,
 stored_name TEXT NOT NULL,
 size_bytes INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_uploads_code ON uploads(code);
CREATE TABLE IF NOT EXISTS upload_events (
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 upload_id INTEGER NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
 created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_upload_events_upload ON upload_events(upload_id);
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
INSERT OR IGNORE INTO tracking_counter(id,generation,sequence) VALUES(1,-1,0);
CREATE TABLE IF NOT EXISTS network_settings (
 id INTEGER PRIMARY KEY CHECK(id=1),
 preferred_ip TEXT NOT NULL DEFAULT '',
 wifi_ssid TEXT NOT NULL DEFAULT '',
 shop_name TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO network_settings(id,preferred_ip,wifi_ssid) VALUES(1,'','');
CREATE TABLE IF NOT EXISTS blocked_ips (
 ip TEXT PRIMARY KEY,
 created_at TEXT NOT NULL,
 note TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_blocked_ips_created ON blocked_ips(created_at);`); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureUploadWorkflowColumns(db); err != nil {
		db.Close()
		return nil, err
	}
	if err := ensureNetworkSettingsColumns(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{DB: db}, nil
}

// ensureUploadWorkflowColumns upgrades databases created by earlier CafeDrop versions.
func ensureUploadWorkflowColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(uploads)`)
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !columns["admin_status"] {
		if _, err := db.Exec(`ALTER TABLE uploads ADD COLUMN admin_status TEXT NOT NULL DEFAULT 'received'`); err != nil {
			return err
		}
	}
	if !columns["admin_note"] {
		if _, err := db.Exec(`ALTER TABLE uploads ADD COLUMN admin_note TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

func ensureNetworkSettingsColumns(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(network_settings)`)
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "shop_name" {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !found {
		_, err = db.Exec(`ALTER TABLE network_settings ADD COLUMN shop_name TEXT NOT NULL DEFAULT ''`)
	}
	return err
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
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`UPDATE uploads SET status=? WHERE id=?`, status, id); err != nil {
		return err
	}
	if status == "completed" {
		if _, err = tx.Exec(`INSERT INTO upload_events(upload_id,created_at) VALUES(?,?)`, id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (s *Store) ListUploads(limit int) ([]Upload, error) {
	rows, err := s.DB.Query(`SELECT id,code,created_at,status,file_count,total_bytes,folder,admin_status,admin_note FROM uploads ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upload{}
	for rows.Next() {
		var u Upload
		var ts string
		if err := rows.Scan(&u.ID, &u.Code, &ts, &u.Status, &u.FileCount, &u.TotalBytes, &u.Folder, &u.AdminStatus, &u.AdminNote); err != nil {
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
	err := s.DB.QueryRow(`SELECT id,code,created_at,status,file_count,total_bytes,folder,admin_status,admin_note FROM uploads WHERE code=?`, code).Scan(&u.ID, &u.Code, &ts, &u.Status, &u.FileCount, &u.TotalBytes, &u.Folder, &u.AdminStatus, &u.AdminNote)
	if err != nil {
		return nil, err
	}
	u.CreatedAt, _ = time.Parse(time.RFC3339Nano, ts)
	return &u, nil
}

// UpdateUploadWorkflowStatus stores the attendant's hand-off decision separately
// from the technical upload status (completed/failed/uploading).
func (s *Store) UpdateUploadWorkflowStatus(code, status, note string) error {
	result, err := s.DB.Exec(`UPDATE uploads SET admin_status=?, admin_note=? WHERE code=? AND status='completed'`, status, note, code)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	return nil
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
	rows, err := s.DB.Query(`SELECT id,code,created_at,status,file_count,total_bytes,folder,admin_status,admin_note FROM uploads WHERE code LIKE ? ESCAPE '\' ORDER BY id DESC LIMIT ?`, "%"+escapeLike(q)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upload{}
	for rows.Next() {
		var u Upload
		var ts string
		if err := rows.Scan(&u.ID, &u.Code, &ts, &u.Status, &u.FileCount, &u.TotalBytes, &u.Folder, &u.AdminStatus, &u.AdminNote); err != nil {
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

type UploadEvent struct {
	ID       int64
	UploadID int64
}

func (s *Store) LatestUploadEventID() (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM upload_events`).Scan(&id)
	return id, err
}

func (s *Store) UploadEventsAfter(lastID int64) ([]UploadEvent, error) {
	rows, err := s.DB.Query(`SELECT id,upload_id FROM upload_events WHERE id>? ORDER BY id ASC LIMIT 500`, lastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]UploadEvent, 0)
	for rows.Next() {
		var event UploadEvent
		if err := rows.Scan(&event.ID, &event.UploadID); err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func (s *Store) LatestCompletedID() (int64, error) {
	var id int64
	err := s.DB.QueryRow(`SELECT COALESCE(MAX(id),0) FROM uploads WHERE status='completed'`).Scan(&id)
	return id, err
}

type NetworkSettings struct {
	PreferredIP string `json:"preferred_ip"`
	WiFiSSID    string `json:"wifi_ssid"`
	ShopName    string `json:"shop_name"`
}

type BlockedIP struct {
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
	Note      string    `json:"note"`
}

func (s *Store) GetNetworkSettings() (NetworkSettings, error) {
	var v NetworkSettings
	err := s.DB.QueryRow(`SELECT preferred_ip,wifi_ssid,shop_name FROM network_settings WHERE id=1`).Scan(&v.PreferredIP, &v.WiFiSSID, &v.ShopName)
	return v, err
}

func (s *Store) SaveNetworkSettings(v NetworkSettings) error {
	_, err := s.DB.Exec(`UPDATE network_settings SET preferred_ip=?,wifi_ssid=?,shop_name=? WHERE id=1`, v.PreferredIP, v.WiFiSSID, v.ShopName)
	return err
}

func (s *Store) IsIPBlocked(ip string) (bool, error) {
	var found int
	err := s.DB.QueryRow(`SELECT 1 FROM blocked_ips WHERE ip=? LIMIT 1`, ip).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) BlockIP(ip, note string) error {
	_, err := s.DB.Exec(`INSERT INTO blocked_ips(ip,created_at,note) VALUES(?,?,?) ON CONFLICT(ip) DO UPDATE SET note=excluded.note`, ip, time.Now().UTC().Format(time.RFC3339Nano), note)
	return err
}

func (s *Store) UnblockIP(ip string) error {
	_, err := s.DB.Exec(`DELETE FROM blocked_ips WHERE ip=?`, ip)
	return err
}

func (s *Store) ListBlockedIPs() ([]BlockedIP, error) {
	rows, err := s.DB.Query(`SELECT ip,created_at,note FROM blocked_ips ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BlockedIP{}
	for rows.Next() {
		var item BlockedIP
		var created string
		if err := rows.Scan(&item.IP, &created, &item.Note); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteUpload removes one upload record. Child file rows are removed by the
// uploads -> upload_files foreign-key cascade.
func (s *Store) DeleteUpload(code string) (string, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()

	var folder, status string
	if err = tx.QueryRow(`SELECT folder,status FROM uploads WHERE code=?`, code).Scan(&folder, &status); err != nil {
		return "", err
	}
	if status == "uploading" {
		return "", fmt.Errorf("upload is still in progress")
	}
	if _, err = tx.Exec(`DELETE FROM uploads WHERE code=?`, code); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return folder, nil
}

// CompletedUploadIDsAfter returns newly completed upload IDs in ascending order.
func (s *Store) CompletedUploadIDsAfter(lastID int64) ([]int64, error) {
	rows, err := s.DB.Query(`SELECT id FROM uploads WHERE status='completed' AND id>? ORDER BY id ASC LIMIT 500`, lastID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
