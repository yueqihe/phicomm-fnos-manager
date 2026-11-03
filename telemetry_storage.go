package main

import (
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

const historyHour = 60 * sampleInterval

func (s *Store) openHistoryReaders(dir string) error {
	// Summaries are disposable; the existing minute records remain authoritative.
	// No scan/backfill runs at startup. Old databases populate summaries on demand.
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS telemetry_hours(device_id TEXT NOT NULL,hour INTEGER NOT NULL,revision INTEGER NOT NULL,value TEXT,PRIMARY KEY(device_id,hour)) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS telemetry_revision(device_id TEXT PRIMARY KEY,revision INTEGER NOT NULL) WITHOUT ROWID;`)
	if err != nil {
		return err
	}
	for _, event := range []string{"INSERT", "DELETE", "UPDATE"} {
		refs := []string{"NEW"}
		if event == "DELETE" {
			refs = []string{"OLD"}
		} else if event == "UPDATE" {
			refs = []string{"OLD", "NEW"}
		}
		body := ""
		for _, ref := range refs {
			body += fmt.Sprintf(`
INSERT INTO telemetry_hours(device_id,hour,revision) VALUES(%[1]s.device_id,(%[1]s.minute/60)*3600000,1)
 ON CONFLICT(device_id,hour) DO UPDATE SET revision=revision+1,value=NULL;
INSERT INTO telemetry_revision(device_id,revision) VALUES(%[1]s.device_id,1)
 ON CONFLICT(device_id) DO UPDATE SET revision=revision+1;`, ref)
		}
		if _, err = s.db.Exec("CREATE TRIGGER IF NOT EXISTS telemetry_changed_" + strings.ToLower(event) + " AFTER " + event + " ON telemetry BEGIN " + body + " END;"); err != nil {
			return err
		}
	}
	path, err := filepath.Abs(filepath.Join(dir, "manager.sqlite"))
	if err != nil {
		return err
	}
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // file:///D:/... on Windows
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)", "query_only(1)"}}
	u.RawQuery = q.Encode()
	open := func(limit int) (*sql.DB, error) {
		db, err := sql.Open("sqlite", u.String())
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(limit)
		db.SetMaxIdleConns(limit)
		if err = db.Ping(); err != nil {
			db.Close()
			return nil, err
		}
		return db, nil
	}
	if s.readDB, err = open(2); err != nil {
		return err
	}
	s.exportDB, err = open(1)
	return err
}
