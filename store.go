package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Obj = map[string]any
type Store struct {
	db  *sql.DB
	key []byte
}

func token() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func openStore(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	keyfile := filepath.Join(dir, "secrets.key")
	key, err := os.ReadFile(keyfile)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err != nil {
			return nil, err
		}
		f, e := os.OpenFile(keyfile, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if e != nil {
			return nil, e
		}
		_, err = f.Write(key)
		ce := f.Close()
		if err != nil {
			return nil, err
		}
		if ce != nil {
			return nil, ce
		}
	} else if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("secrets.key 无效")
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "manager.sqlite"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS devices(id TEXT PRIMARY KEY,model TEXT NOT NULL,mac TEXT NOT NULL,value TEXT NOT NULL,UNIQUE(model,mac));
 CREATE TABLE IF NOT EXISTS history(id INTEGER PRIMARY KEY AUTOINCREMENT,time TEXT NOT NULL,device_id TEXT,name TEXT NOT NULL,action TEXT NOT NULL,outcome TEXT NOT NULL,message TEXT NOT NULL);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	_ = os.Chmod(filepath.Join(dir, "manager.sqlite"), 0600)
	return &Store{db, key}, nil
}
func (s *Store) get(key string, fallback any) any {
	var raw string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key=?", key).Scan(&raw)
	if err == sql.ErrNoRows {
		return fallback
	}
	if err != nil {
		panic(err)
	}
	var value any
	if err = json.Unmarshal([]byte(raw), &value); err != nil {
		panic(err)
	}
	return value
}
func (s *Store) set(key string, value any) {
	raw, err := json.Marshal(value)
	must(err)
	_, err = s.db.Exec("INSERT OR REPLACE INTO settings VALUES (?,?)", key, string(raw))
	must(err)
}
func (s *Store) save(d Obj) {
	raw, err := json.Marshal(d)
	must(err)
	_, err = s.db.Exec("INSERT INTO devices VALUES (?,?,?,?) ON CONFLICT(id) DO UPDATE SET value=excluded.value", d["id"], d["model"], d["mac"], string(raw))
	must(err)
}
func (s *Store) devices() []Obj {
	rows, err := s.db.Query("SELECT value FROM devices ORDER BY rowid")
	must(err)
	defer rows.Close()
	out := []Obj{}
	for rows.Next() {
		var raw string
		must(rows.Scan(&raw))
		var d Obj
		must(json.Unmarshal([]byte(raw), &d))
		out = append(out, d)
	}
	must(rows.Err())
	return out
}
func (s *Store) remove(id string) {
	_, err := s.db.Exec("DELETE FROM devices WHERE id=?", id)
	must(err)
}
func (s *Store) log(d Obj, action, outcome, message string) {
	name := "管理服务"
	var id any
	if d != nil {
		name = str(d["name"])
		id = d["id"]
	}
	_, err := s.db.Exec("INSERT INTO history(time,device_id,name,action,outcome,message) VALUES (?,?,?,?,?,?)", time.Now().UTC().Format(time.RFC3339Nano), id, name, action, outcome, message)
	must(err)
	_, err = s.db.Exec("DELETE FROM history WHERE id <= (SELECT MAX(id)-2000 FROM history)")
	must(err)
}
func (s *Store) history(before int64) []Obj {
	rows, err := s.db.Query("SELECT id,time,device_id,name,action,outcome,message FROM history WHERE id < ? ORDER BY id DESC LIMIT 100", before)
	must(err)
	defer rows.Close()
	out := []Obj{}
	for rows.Next() {
		var id int64
		var at, name, action, outcome, message string
		var did sql.NullString
		must(rows.Scan(&id, &at, &did, &name, &action, &outcome, &message))
		var device any
		if did.Valid {
			device = did.String
		}
		out = append(out, Obj{"id": id, "time": at, "device_id": device, "name": name, "action": action, "outcome": outcome, "message": message})
	}
	must(rows.Err())
	return out
}
func (s *Store) encrypt(value string) string {
	if value == "" {
		return ""
	}
	block, err := aes.NewCipher(s.key)
	must(err)
	gcm, err := cipher.NewGCM(block)
	must(err)
	iv := make([]byte, gcm.NonceSize())
	_, err = rand.Read(iv)
	must(err)
	return base64.StdEncoding.EncodeToString(gcm.Seal(iv, iv, []byte(value), nil))
}
func (s *Store) decrypt(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize()+gcm.Overhead() {
		return "", fmt.Errorf("加密凭据无效")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	return string(plain), err
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func obj(v any) Obj {
	if o, ok := v.(map[string]any); ok && o != nil {
		return o
	}
	return Obj{}
}
func str(v any) string { s, _ := v.(string); return s }
func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}
func boolean(v any) bool { b, _ := v.(bool); return b }
func arr(v any) []any {
	switch a := v.(type) {
	case []any:
		return a
	case []string:
		out := make([]any, len(a))
		for i, s := range a {
			out[i] = s
		}
		return out
	case []bool:
		out := make([]any, len(a))
		for i, b := range a {
			out[i] = b
		}
		return out
	}
	return nil
}
func clone(o Obj) Obj {
	raw, err := json.Marshal(o)
	must(err)
	var value Obj
	must(json.Unmarshal(raw, &value))
	return value
}
