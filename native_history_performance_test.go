package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func historyTestStore(t testing.TB) *Store {
	t.Helper()
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Compare every API field, including bucket/summary counts, extrema and latest
// values. Floating-point summation order is allowed to differ by roundoff only.
func sameHistory(t testing.TB, got, want Obj) {
	t.Helper()
	normalize := func(v Obj) any {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	var equal func(string, any, any)
	equal = func(path string, a, b any) {
		t.Helper()
		switch x := a.(type) {
		case float64:
			y, ok := b.(float64)
			if !ok || math.Abs(x-y) > 1e-9*max(1, math.Abs(y)) {
				t.Fatalf("%s: got %v, want %v", path, a, b)
			}
		case map[string]any:
			y, ok := b.(map[string]any)
			if !ok || len(x) != len(y) {
				t.Fatalf("%s: object mismatch", path)
			}
			for key, v := range x {
				equal(path+"."+key, v, y[key])
			}
		case []any:
			y, ok := b.([]any)
			if !ok || len(x) != len(y) {
				t.Fatalf("%s: array mismatch", path)
			}
			for i, v := range x {
				equal(fmt.Sprintf("%s[%d]", path, i), v, y[i])
			}
		default:
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("%s: got %v, want %v", path, a, b)
			}
		}
	}
	equal("history", normalize(got), normalize(want))
}

func seedHistory(t testing.TB, s *Store, id string, now int64, minutes int, gaps bool) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare("INSERT INTO telemetry(device_id,minute,at,value) VALUES(?,?,?,?)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rng := rand.New(rand.NewSource(917))
	for i := 0; i < minutes; i++ {
		if gaps && (i%701 < 130 || rng.Intn(9) == 0) {
			continue
		}
		at := now - int64(minutes-i)*sampleInterval + int64(rng.Intn(55000))
		values := Obj{"power": float64(i % 300), "voltage": 220.0, "current": float64(i%40) / 10,
			"meter": float64(i%1200) / 100, "costTotal": float64(i%3300) / 170,
			"pm25": float64(i % 110), "pm10": float64(i % 170), "temp": float64(i%50) - 15,
			"humidity": float64(i % 100), "hcho": float64(i%70) / 1000, "co2": float64(i%2300 + 400),
			"tvoc": float64(i % 200), "speed": float64(i % 100), "filter": float64(100 - i%100)}
		if gaps {
			if i%31 < 8 {
				values["meter"], values["costTotal"] = nil, nil
			}
			if i%13 == 0 {
				values = Obj{} // Invalid samples must still count as recorded rows.
			}
		} else {
			// Normal S30 record size for the performance fixture.
			values = Obj{"power": values["power"], "voltage": values["voltage"], "current": values["current"], "meter": values["meter"], "costTotal": values["costTotal"]}
		}
		raw, _ := json.Marshal(values)
		if _, err = stmt.Exec(id, at/sampleInterval, at, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestHourlyHistoryMatchesOriginalQuery(t *testing.T) {
	s := historyTestStore(t)
	now := time.Now().Truncate(time.Hour).UnixMilli()
	seedHistory(t, s, "fixture", now, 30*24*60+60, true)
	for _, model := range []string{"TC1", "S30-V1B", "M1", "DAIKIN", "A1"} {
		for _, span := range []string{"7d", "30d"} {
			for _, offset := range []int64{0, 23*sampleInterval + 12345, historyHour - 1} {
				at := now + offset
				want, err := s.referenceDataHistory(context.Background(), "fixture", model, span, at)
				if err != nil {
					t.Fatal(err)
				}
				got, err := s.hourlyDataHistory(context.Background(), "fixture", model, span, at)
				if err != nil {
					t.Fatal(err)
				}
				sameHistory(t, got, want)
			}
		}
	}
}

func TestHistoryCacheInvalidationBoundsAndCopies(t *testing.T) {
	s := historyTestStore(t)
	now := time.Now().Truncate(time.Minute).UnixMilli()
	id := "tc1-cache"
	write := func(at int64, power float64) {
		s.recordSample(Obj{"id": id, "model": "TC1", "state": Obj{"power": power, "energy": 0.0}}, at)
	}
	write(now-2*sampleInterval, 0)
	read := func(id string, at int64) Obj {
		t.Helper()
		data, err := s.dataHistory(context.Background(), id, "TC1", "24h", at)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	data := read(id, now)
	obj(obj(data["summary"])["power"])["avg"] = -999.0
	data["points"].([]Obj)[0]["values"] = Obj{"power": -999.0}
	if num(obj(obj(read(id, now)["summary"])["power"])["avg"]) != 0 {
		t.Fatal("caller mutated shared cache")
	}
	write(now, 100)
	if read(id, now)["samples"] != 2 {
		t.Fatal("new sample did not invalidate cache")
	}
	// External corrections/imports use the same trigger-based invalidation.
	_, err := s.db.Exec("UPDATE telemetry SET value=? WHERE device_id=?", `{"power":42}`, id)
	if err != nil {
		t.Fatal(err)
	}
	if num(obj(obj(read(id, now)["summary"])["power"])["avg"]) != 42 {
		t.Fatal("corrected data served from stale cache")
	}
	_, err = s.db.Exec("DELETE FROM telemetry WHERE device_id=?", id)
	if err != nil || read(id, now)["samples"] != 0 {
		t.Fatal("deleted data served from cache", err)
	}
	for i := 0; i < historyCacheLimit+10; i++ {
		read(fmt.Sprint(i), now)
	}
	if len(s.charts.entries) != historyCacheLimit {
		t.Fatal("cache grew without bound", len(s.charts.entries))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.dataHistory(ctx, "0", "TC1", "24h", now); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled cache request not rejected", err)
	}
}

func TestHourlySummariesInvalidationRestartAndRetention(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	now := time.Now().Truncate(time.Hour).UnixMilli()
	seedHistory(t, s, "fixture", now, 180, false)
	check := func() {
		t.Helper()
		want, err := s.referenceDataHistory(context.Background(), "fixture", "S30-V1B", "30d", now)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.dataHistory(context.Background(), "fixture", "S30-V1B", "30d", now)
		if err != nil {
			t.Fatal(err)
		}
		sameHistory(t, got, want)
	}
	check()
	var summaries int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM telemetry_hours WHERE value IS NOT NULL").Scan(&summaries); err != nil || summaries == 0 {
		t.Fatal("no persisted summaries", err)
	}
	s.Close()
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	check()
	// Late readings, resets, corrections and deletes dirty existing summaries.
	for _, query := range []string{
		"UPDATE telemetry SET value='{" + `"power":900,"meter":0,"costTotal":0` + "}' WHERE minute=(SELECT MIN(minute) FROM telemetry)",
		"DELETE FROM telemetry WHERE minute%3=0",
	} {
		if _, err = s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
		check()
	}
	at := now - 4*historyHour
	s.recordSample(Obj{"id": "fixture", "model": "S30-V1B", "state": Obj{"power": 0.0, "energy": 0.0}}, at)
	check()
	// A stale snapshot must not overwrite a revision dirtied by a new reading.
	var revision int64
	s.db.QueryRow("SELECT revision FROM telemetry_hours WHERE device_id=? AND hour=?", "fixture", at).Scan(&revision)
	s.recordSample(Obj{"id": "fixture", "model": "S30-V1B", "state": Obj{"power": 8.0}}, at+sampleInterval)
	if err = s.saveHistoryHours(context.Background(), "fixture", []hourPart{{hour: at, revision: revision, data: hourAggregate{Count: 999}}}); err != nil {
		t.Fatal(err)
	}
	check()
	s.pruneSamples(now + sampleRetention + historyHour)
	var records, hours int
	s.db.QueryRow("SELECT COUNT(*) FROM telemetry").Scan(&records)
	s.db.QueryRow("SELECT COUNT(*) FROM telemetry_hours").Scan(&hours)
	if records != 0 || hours != 0 {
		t.Fatal("retention left stale raw/summary rows", records, hours)
	}
	check()
}

func TestConcurrentHistoryQueriesAndCanceledWaiter(t *testing.T) {
	s := historyTestStore(t)
	now := time.Now().Truncate(time.Minute).UnixMilli()
	seedHistory(t, s, "fixture", now, 300, true)
	const clients = 12
	start := make(chan struct{})
	results := make(chan Obj, clients)
	errorsFound := make(chan error, clients)
	for i := 0; i < clients; i++ {
		go func() {
			<-start
			data, err := s.dataHistory(context.Background(), "fixture", "S30-V1B", "30d", now)
			results <- data
			errorsFound <- err
		}()
	}
	close(start)
	want, err := s.referenceDataHistory(context.Background(), "fixture", "S30-V1B", "30d", now)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < clients; i++ {
		select {
		case data := <-results:
			if err = <-errorsFound; err != nil {
				t.Fatal(err)
			}
			sameHistory(t, data, want)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent history requests stalled")
		}
	}
	if len(s.charts.loading) != 0 || len(s.charts.entries) != 1 {
		t.Fatal("duplicate chart queries did not share a cache entry")
	}
	// A waiting client's cancellation must not cancel or strand the active job.
	key := historyKey{"waiting", "M1", "24h", now / sampleInterval, 0}
	done := make(chan struct{})
	s.charts.loading[key] = done
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = s.dataHistory(ctx, "waiting", "M1", "24h", now)
	if !errors.Is(err, context.DeadlineExceeded) || s.charts.loading[key] != done {
		t.Fatal("canceled follower disturbed active query", err)
	}
	delete(s.charts.loading, key)
	close(done)
	if _, err = s.dataHistory(context.Background(), "waiting", "M1", "24h", now); err != nil {
		t.Fatal("cache stuck after canceled follower", err)
	}
}

type pausedCSVWriter struct {
	once    sync.Once
	started chan struct{}
	release chan struct{}
}

func (w *pausedCSVWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started); <-w.release })
	return len(p), nil
}

func TestHistoryAndSamplingContinueDuringCSVExport(t *testing.T) {
	s := historyTestStore(t)
	now := time.Now().Truncate(time.Minute).UnixMilli()
	seedHistory(t, s, "fixture", now, 60, false)
	for _, db := range []*sql.DB{s.readDB, s.exportDB} {
		if _, err := db.Exec("DELETE FROM telemetry"); err == nil {
			t.Fatal("read pool allowed a write")
		}
	}
	w := &pausedCSVWriter{started: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- s.writeDataCSV(context.Background(), w) }()
	defer func() {
		close(w.release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-w.started:
	case <-time.After(5 * time.Second):
		t.Fatal("export did not start")
	}
	written := make(chan struct{})
	go func() {
		s.recordSample(Obj{"id": "fixture", "model": "TC1", "state": Obj{"power": 12.0}}, now)
		close(written)
	}()
	select {
	case <-written:
	case <-time.After(2 * time.Second):
		t.Fatal("export blocked sampling")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, err := s.dataHistory(ctx, "fixture", "TC1", "24h", now)
	if err != nil || data["samples"] != 61 {
		t.Fatal("export blocked chart query or fresh data", err)
	}
}

func TestLegacyHistoryBuildsSummariesOnDemand(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "manager.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Hour).UnixMilli()
	_, err = db.Exec(`CREATE TABLE telemetry(device_id TEXT NOT NULL,minute INTEGER NOT NULL,at INTEGER NOT NULL,value TEXT NOT NULL,PRIMARY KEY(device_id,minute)) WITHOUT ROWID;
INSERT INTO telemetry VALUES('old',?,?, '{"power":123,"meter":2}');`, (now-historyHour)/sampleInterval, now-historyHour)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	s.db.QueryRow("SELECT COUNT(*) FROM telemetry_hours").Scan(&count)
	if count != 0 {
		t.Fatal("startup scanned and backfilled history")
	}
	data, err := s.dataHistory(context.Background(), "old", "TC1", "30d", now)
	if err != nil || data["samples"] != 1 || num(obj(obj(data["summary"])["power"])["avg"]) != 123 {
		t.Fatal("legacy history lost", data, err)
	}
}

func BenchmarkHistory30Days(b *testing.B) {
	s := historyTestStore(b)
	now := time.Now().Truncate(time.Minute).UnixMilli()
	// Ten devices, 432,000 minute records, in an isolated temporary database.
	for i := 0; i < 10; i++ {
		seedHistory(b, s, fmt.Sprint(i), now, 30*24*60, false)
	}
	ctx := context.Background()
	if _, err := s.hourlyDataHistory(ctx, "0", "S30-V1B", "30d", now); err != nil {
		b.Fatal(err)
	}
	for _, mode := range []string{"original", "hourly", "cached"} {
		b.Run(mode, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var err error
				switch mode {
				case "original":
					_, err = s.referenceDataHistory(ctx, "0", "S30-V1B", "30d", now)
				case "hourly":
					_, err = s.hourlyDataHistory(ctx, "0", "S30-V1B", "30d", now)
				case "cached":
					_, err = s.dataHistory(ctx, "0", "S30-V1B", "30d", now)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	if err := s.writeDataCSV(ctx, io.Discard); err != nil {
		b.Fatal(err)
	}
}
