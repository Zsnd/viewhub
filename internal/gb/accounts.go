package gb

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const DefaultSpaceID = 1
const LoginDuration = 12 * time.Hour

var usernamePattern = regexp.MustCompile("^[a-z0-9][a-z0-9._-]{2,39}$")
var ErrAccountExists = errors.New("account already exists")
var ErrCredentials = errors.New("invalid credentials")
var ErrAccountFields = errors.New("invalid account fields")

type Account struct {
	ID         int64  `json:"id"`
	SpaceID    int64  `json:"-"`
	Username   string `json:"username"`
	Space      string `json:"space"`
	Admin      bool   `json:"admin"`
	MustChange bool   `json:"mustChange"`
}

func migrateAccounts(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version >= 1 {
		return nil
	}
	_, err = tx.Exec(`
 CREATE TABLE spaces (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
 INSERT INTO spaces(id,name) VALUES(1,'默认空间');
 CREATE TABLE accounts (
  id INTEGER PRIMARY KEY, space_id INTEGER NOT NULL REFERENCES spaces(id),
  username TEXT NOT NULL UNIQUE, password_hash BLOB NOT NULL,
  admin INTEGER NOT NULL DEFAULT 0, must_change INTEGER NOT NULL DEFAULT 0
 );
 CREATE TABLE web_sessions (
  token_hash TEXT PRIMARY KEY, account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
  expires INTEGER NOT NULL
 );
 CREATE INDEX web_sessions_account ON web_sessions(account_id);
 ALTER TABLE devices ADD COLUMN space_id INTEGER NOT NULL DEFAULT 1;
 CREATE INDEX devices_space ON devices(space_id);
 PRAGMA user_version=1;
 `)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func NormalizeUsername(name string) string { return strings.ToLower(strings.TrimSpace(name)) }
func passwordHash(password string) ([]byte, error) {
	if len(password) < 12 || len(password) > 72 {
		return nil, ErrAccountFields
	}
	return bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
}
func (s *Store) EnsureAdmin(username, password string) error {
	var exists bool
	if err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM accounts WHERE admin=1)").Scan(&exists); err != nil || exists {
		return err
	}
	username = NormalizeUsername(username)
	if !usernamePattern.MatchString(username) {
		return ErrAccountFields
	}
	hash, err := passwordHash(password)
	if err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO accounts(space_id,username,password_hash,admin) VALUES(?,?,?,1)", DefaultSpaceID, username, hash)
	return err
}
func (s *Store) CreateCustomer(username, space, password string) (Account, error) {
	username = NormalizeUsername(username)
	space = strings.TrimSpace(space)
	if !usernamePattern.MatchString(username) || space == "" || len(space) > 150 {
		return Account{}, ErrAccountFields
	}
	hash, err := passwordHash(password)
	if err != nil {
		return Account{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Account{}, err
	}
	defer tx.Rollback()
	var exists bool
	if err = tx.QueryRow("SELECT EXISTS(SELECT 1 FROM accounts WHERE username=?)", username).Scan(&exists); err != nil {
		return Account{}, err
	}
	if exists {
		return Account{}, ErrAccountExists
	}
	res, err := tx.Exec("INSERT INTO spaces(name) VALUES(?)", space)
	if err != nil {
		return Account{}, err
	}
	spaceID, err := res.LastInsertId()
	if err != nil {
		return Account{}, err
	}
	res, err = tx.Exec("INSERT INTO accounts(space_id,username,password_hash,must_change) VALUES(?,?,?,1)", spaceID, username, hash)
	if err != nil {
		return Account{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Account{}, err
	}
	return Account{ID: id, SpaceID: spaceID, Username: username, Space: space, MustChange: true}, tx.Commit()
}
func (s *Store) Accounts() ([]Account, error) {
	rows, err := s.db.Query("SELECT a.id,a.username,sp.name,a.admin,a.must_change FROM accounts a JOIN spaces sp ON sp.id=a.space_id ORDER BY a.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Account{}
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Username, &a.Space, &a.Admin, &a.MustChange); err != nil {
			return nil, err
		}
		result = append(result, a)
	}
	return result, rows.Err()
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("unused fallback password"), bcrypt.DefaultCost)

func (s *Store) Login(username, password string) (string, error) {
	if len(password) > 72 {
		return "", ErrCredentials
	}
	var id int64
	hash := dummyHash
	err := s.db.QueryRow("SELECT id,password_hash FROM accounts WHERE username=?", NormalizeUsername(username)).Scan(&id, &hash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if bcrypt.CompareHashAndPassword(hash, []byte(password)) != nil || id == 0 {
		return "", ErrCredentials
	}
	var raw [32]byte
	if _, err = rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw[:])
	result, err := s.db.Exec("INSERT INTO web_sessions SELECT ?,id,? FROM accounts WHERE id=? AND password_hash=?", SessionKey(token), time.Now().Add(LoginDuration).Unix(), id, hash)
	if err == nil {
		if n, _ := result.RowsAffected(); n != 1 {
			return "", ErrCredentials
		}
	}
	return token, err
}
func SessionKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
func (s *Store) Session(key string) (Account, error) {
	var a Account
	err := s.db.QueryRow(`SELECT a.id,a.space_id,a.username,sp.name,a.admin,a.must_change FROM web_sessions ws
 JOIN accounts a ON a.id=ws.account_id JOIN spaces sp ON sp.id=a.space_id
 WHERE ws.token_hash=? AND ws.expires>?`, key, time.Now().Unix()).Scan(&a.ID, &a.SpaceID, &a.Username, &a.Space, &a.Admin, &a.MustChange)
	return a, err
}
func (s *Store) Logout(key string) error {
	_, err := s.db.Exec("DELETE FROM web_sessions WHERE token_hash=?", key)
	return err
}
func (s *Store) ChangePassword(a Account, current, newPassword string) error {
	if len(current) > 72 || current == newPassword {
		return ErrAccountFields
	}
	var old []byte
	if err := s.db.QueryRow("SELECT password_hash FROM accounts WHERE id=?", a.ID).Scan(&old); err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword(old, []byte(current)) != nil {
		return ErrCredentials
	}
	hash, err := passwordHash(newPassword)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE accounts SET password_hash=?,must_change=0 WHERE id=? AND password_hash=?", hash, a.ID, old)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return ErrCredentials
	}
	if _, err = tx.Exec("DELETE FROM web_sessions WHERE account_id=?", a.ID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) PurgeSessions() {
	_, _ = s.db.Exec("DELETE FROM web_sessions WHERE expires<=?", time.Now().Unix())
}
