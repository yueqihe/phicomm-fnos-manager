package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDataCSVExportsAllRawReadings(t *testing.T) {
	m, _, server := testManager(t)
	s := m.store
	id := "tc1-aabbcc000401"
	s.save(Obj{"id": id, "model": "TC1", "mac": "aabbcc000401", "name": "客厅,\"电视\"", "room": "=1+1", "password": "never-export-password"})
	s.save(Obj{"id": "m1-aabbcc000402", "model": "M1", "mac": "aabbcc000402", "name": "空气检测仪", "room": "书房"})
	at := time.Now().Add(-48 * time.Hour).Truncate(5 * time.Minute).UnixMilli()
	insert := func(id string, minute int64, value string) {
		t.Helper()
		_, err := s.db.Exec("INSERT INTO telemetry VALUES (?,?,?,?)", id, minute/sampleInterval, minute, value)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert(id, at, `{"power":0,"meter":1.23456789,"costTotal":0,"password":"never-export-password"}`)
	insert(id, at+sampleInterval, `{"power":2345.67,"meter":2.34567891,"costTotal":0.123456789}`)
	insert("m1-aabbcc000402", at, `{"pm25":75,"hcho":0.012,"temp":-2.5,"humidity":51,"co2":800}`)
	insert("m1-aabbcc000402", at+sampleInterval, `{"co2":null}`)
	insert("a1-aabbcc000403", at, `{"speed":85,"filter":92}`) // A removed device still has retained readings.
	res, err := http.Get(server.URL + "/api/data-export.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/csv; charset=utf-8" || res.Header.Get("Cache-Control") != "no-store" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), `attachment; filename="phicomm-history-`) {
		t.Fatal("invalid download headers", res.StatusCode, res.Header)
	}
	raw, err := io.ReadAll(res.Body)
	if err != nil || !bytes.HasPrefix(raw, []byte("\ufeff")) || bytes.Contains(raw, []byte("never-export-password")) {
		t.Fatal("missing BOM, read failure or credentials included", err)
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(raw), "\ufeff"))).ReadAll()
	if err != nil || len(records) != 6 || len(records[0]) != 20 {
		t.Fatal("lost raw rows or columns", err, records)
	}
	for _, row := range records {
		if len(row) != 20 {
			t.Fatal("incorrect CSV quoting", row)
		}
	}
	if !reflect.DeepEqual(records[1][0:6], []string{"a1-aabbcc000403", "", "A1", "aabbcc000403", "", time.UnixMilli(at).In(exportZone).Format("2006-01-02 15:04:05.000")}) || !reflect.DeepEqual(records[1][14:16], []string{"85", "92"}) {
		t.Fatal("removed device history lost", records[1])
	}
	if !reflect.DeepEqual(records[2][9:14], []string{"75", "0.012", "-2.5", "51", "800"}) || strings.Join(records[3][6:], "") != "" {
		t.Fatal("air readings changed or missing values invented", records[2:4])
	}
	if records[4][1] != "客厅,\"电视\"" || records[4][4] != "'=1+1" || !reflect.DeepEqual(records[4][6:9], []string{"0", "1.23456789", "0"}) || !reflect.DeepEqual(records[5][6:9], []string{"2345.67", "2.34567891", "0.123456789"}) {
		t.Fatal("readings aggregated, rounded or text escaped incorrectly", records[4:])
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = s.writeDataCSV(ctx, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled export continued", err)
	}
	if err = s.db.Ping(); err != nil {
		t.Fatal("export retained database connection", err)
	}
}

func TestDataCSVEmptyAndGatewayAuthentication(t *testing.T) {
	_, app, _ := testManager(t)
	app.dev = false
	app.base = "/app/phicomm-manager"
	for _, check := range []struct {
		local, admin bool
		user         string
		status       int
	}{{false, true, "1000", 401}, {true, false, "1000", 401}, {true, true, "", 401}, {true, true, "1000", 200}} {
		r := httptest.NewRequest("GET", "http://gateway/app/phicomm-manager/api/data-export.csv", nil)
		r = r.WithContext(context.WithValue(r.Context(), localSocketKey{}, check.local))
		r.Header.Set("X-Trim-Userid", check.user)
		if check.admin {
			r.Header.Set("X-Trim-Isadmin", "true")
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, r)
		if w.Code != check.status {
			t.Fatal("export authentication incorrect", check, w.Code)
		}
		if w.Code == 200 {
			records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
			if err != nil || len(records) != 1 || records[0][0] != "设备ID" {
				t.Fatal("empty export must have headers only", err, records)
			}
		} else if strings.Contains(w.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("unauthenticated download returned a CSV")
		}
	}
}
