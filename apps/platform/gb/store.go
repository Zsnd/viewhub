package gb

import (
	"database/sql"
	"errors"
	_ "modernc.org/sqlite"
	"regexp"
	"strings"
)

var codePattern = regexp.MustCompile("^[0-9]{20}$")

func ValidCode(s string) bool { return codePattern.MatchString(s) }

type Device struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	Kind        string      `json:"kind"`
	Online      bool        `json:"online"`
	Channels    []Channel   `json:"channels"`
	Diagnostics Diagnostics `json:"diagnostics"`
}
type Channel struct {
	PTZ       bool   `json:"ptz,omitempty"`
	PTZSource string `json:"ptzSource" xml:"-"`
	PTZType   string `json:"-" xml:"Info>PTZType"`
	ID        string `json:"id" xml:"DeviceID"`
	Name      string `json:"name" xml:"Name"`
	Parental  int    `json:"-" xml:"Parental"`
}
type Store struct{ db *sql.DB }

func OpenStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
 PRAGMA journal_mode=WAL;
 PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY,name TEXT NOT NULL,kind TEXT NOT NULL,secret TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS channels(device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,id TEXT NOT NULL,name TEXT NOT NULL,PRIMARY KEY(device_id,id));`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if err = migrateAccounts(db); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS walls(account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,space_id INTEGER NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,data TEXT NOT NULL,PRIMARY KEY(account_id,space_id));
 CREATE TABLE IF NOT EXISTS ptz_capabilities(device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,channel_id TEXT NOT NULL,enabled INTEGER NOT NULL,PRIMARY KEY(device_id,channel_id));
 CREATE TABLE IF NOT EXISTS ptz_catalog(device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,channel_id TEXT NOT NULL,ptz_type TEXT NOT NULL,PRIMARY KEY(device_id,channel_id));`); err != nil {
		db.Close()
		return nil, err
	}
	if err = clearRetiredWallSlots(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db}, nil
}
func (s *Store) Close() { s.db.Close() }

var ErrDeviceExists = errors.New("device already exists")
var ErrDeviceFields = errors.New("invalid device fields")

func (s *Store) SeedDevice(id, name, kind, secret string) error {
	err := s.Provision(DefaultSpaceID, id, name, kind, secret)
	if errors.Is(err, ErrDeviceExists) {
		return nil
	}
	return err
}
func (s *Store) Provision(space int64, id, name, kind, secret string) error {
	if !ValidCode(id) || len(name) == 0 || len(name) > 100 || len(secret) < 8 || len(secret) > 64 || (kind != "camera" && kind != "recorder") {
		return errors.New("invalid device fields")
	}
	res, err := s.db.Exec("INSERT INTO devices(id,name,kind,secret,space_id) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING", id, name, kind, secret, space)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrDeviceExists
		}
	}
	return err
}
func (s *Store) UpdateDevice(space int64, id, name, kind, secret string) error {
	name = strings.TrimSpace(name)
	if !ValidCode(id) || len(name) == 0 || len(name) > 100 || (kind != "camera" && kind != "recorder") || (secret != "" && (len(secret) < 8 || len(secret) > 64)) {
		return ErrDeviceFields
	}
	result, err := s.db.Exec("UPDATE devices SET name=?,kind=?,secret=COALESCE(NULLIF(?,''),secret) WHERE space_id=? AND id=?", name, kind, secret, space, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err == nil && changed == 0 {
		return sql.ErrNoRows
	}
	return err
}
func (s *Store) secret(id string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT secret FROM devices WHERE id=?", id).Scan(&value)
	return value, err
}
func (s *Store) saveChannels(id string, channels []Channel) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("DELETE FROM channels WHERE device_id=?", id); err != nil {
		return err
	}
	for _, c := range channels {
		if !ValidCode(c.ID) {
			continue
		}
		if _, err = tx.Exec("INSERT INTO channels VALUES(?,?,?) ON CONFLICT(device_id,id) DO UPDATE SET name=excluded.name", id, c.ID, c.Name); err != nil {
			return err
		}
		if _, err = tx.Exec("INSERT INTO ptz_catalog VALUES(?,?,?) ON CONFLICT(device_id,channel_id) DO UPDATE SET ptz_type=excluded.ptz_type", id, c.ID, strings.TrimSpace(c.PTZType)); err != nil {
			return err
		}
	}
	if _, err = tx.Exec("DELETE FROM ptz_catalog WHERE device_id=? AND channel_id NOT IN (SELECT id FROM channels WHERE device_id=?)", id, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) HasChannel(space int64, id, channel string) bool {
	var found int
	return s.db.QueryRow("SELECT 1 FROM channels c JOIN devices d ON d.id=c.device_id WHERE d.space_id=? AND c.device_id=? AND c.id=?", space, id, channel).Scan(&found) == nil
}
func (s *Store) devices(space int64) ([]Device, error) {
	rows, err := s.db.Query(`SELECT d.id,d.name,d.kind,c.id,c.name,
 COALESCE(p.enabled,pc.ptz_type IN ('1','4','5'),0),
 CASE WHEN p.enabled IS NOT NULL THEN 'manual' WHEN pc.ptz_type IN ('1','2','3','4','5') THEN 'catalog' ELSE 'unknown' END
 FROM devices d LEFT JOIN channels c ON c.device_id=d.id
 LEFT JOIN ptz_capabilities p ON p.device_id=d.id AND p.channel_id=c.id
 LEFT JOIN ptz_catalog pc ON pc.device_id=d.id AND pc.channel_id=c.id
 WHERE d.space_id=? ORDER BY d.id,c.id`, space)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		var cid, name sql.NullString
		var ptz bool
		var source string
		if err = rows.Scan(&d.ID, &d.Name, &d.Kind, &cid, &name, &ptz, &source); err != nil {
			return nil, err
		}
		if len(out) == 0 || out[len(out)-1].ID != d.ID {
			d.Channels = []Channel{}
			out = append(out, d)
		}
		if cid.Valid {
			out[len(out)-1].Channels = append(out[len(out)-1].Channels, Channel{ID: cid.String, Name: name.String, PTZ: ptz, PTZSource: source})
		}
	}
	return out, rows.Err()
}

func (s *Store) SetupSecret(space int64, id string) (string, error) {
	var secret string
	err := s.db.QueryRow("SELECT secret FROM devices WHERE space_id=? AND id=?", space, id).Scan(&secret)
	return secret, err
}
