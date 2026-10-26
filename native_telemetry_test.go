package main

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

func TestDataHistoryCountersSamplingAndRetention(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Minute).UnixMilli()
	id := "tc1-aabbcc000070"
	write := func(at int64, power, energy, cost any) {
		s.recordSample(Obj{"id": id, "model": "TC1", "password": "private", "state": Obj{"power": power, "energy": energy}, "billing": Obj{"since": 1.0, "lastAt": float64(at), "cost": cost}}, at)
	}
	// Values before the selected period must not affect its baseline or summary.
	write(now-2*24*60*sampleInterval, 999.0, 50.0, 0.0)
	write(now-10*sampleInterval, 10.0, 100.0, 5.0)
	write(now-10*sampleInterval+1000, 999.0, 999.0, 999.0)
	write(now-8*sampleInterval, 20.0, 101.0, 5.6)
	write(now-7*sampleInterval, nil, nil, nil)
	write(now-6*sampleInterval, 30.0, 0.0, 5.6)
	write(now-5*sampleInterval, 40.0, 2.0, 6.8)
	write(now-4*sampleInterval, math.NaN(), nil, 6.8)
	write(now-3*sampleInterval, 50.0, 3.0, 7.4)
	data, err := s.dataHistory(context.Background(), id, "TC1", "24h", now)
	if err != nil {
		t.Fatal(err)
	}
	if data["samples"] != 7 {
		t.Fatal("duplicate minute not suppressed", data["samples"])
	}
	summary := obj(data["summary"])
	near(t, num(obj(summary["power"])["avg"]), 30)
	near(t, num(obj(summary["energy"])["total"]), 4)
	near(t, num(obj(summary["cost"])["total"]), 2.4)
	for _, point := range data["points"].([]Obj) {
		if num(point["time"]) < float64(now-15*sampleInterval) && obj(point["values"])["power"] != nil {
			t.Fatal("missing period filled with invented values", point)
		}
	}
	for _, span := range []string{"7d", "30d"} {
		result, err := s.dataHistory(context.Background(), id, "TC1", span, now)
		if err != nil || len(result["points"].([]Obj)) > 300 {
			t.Fatal("history query unbounded", err)
		}
	}
	if _, err := s.dataHistory(context.Background(), id, "TC1", "999d", now); err == nil {
		t.Fatal("unsupported range accepted")
	}
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "password") {
		t.Fatal("credentials included in telemetry")
	}
	// Retention pruning and reconnects must preserve recent data and the minute key.
	write(now-sampleRetention-sampleInterval, 1000.0, 1.0, 0.0)
	s.pruneSamples(now)
	var old int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM telemetry WHERE at<?", now-sampleRetention).Scan(&old); err != nil || old != 0 {
		t.Fatal("retention failed", old, err)
	}
	s.db.Close()
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	write(now-3*sampleInterval+5000, 999.0, 999.0, 999.0)
	data, err = s.dataHistory(context.Background(), id, "TC1", "24h", now)
	if err != nil || data["samples"] != 7 {
		t.Fatal("restart lost history or created duplicate", err, data["samples"])
	}
	near(t, num(obj(obj(data["summary"])["power"])["avg"]), 30)
}

func TestDataHistoryModelsInvalidReadingsAndREST(t *testing.T) {
	m, _, server := testManager(t)
	now := time.Now().UnixMilli()
	for i, model := range []string{"TC1", "M1", "A1"} {
		f := firmwareFixture(t, model, []string{"aabbcc000071", "aabbcc000072", "aabbcc000073"}[i])
		_, added := requestJSON(t, server, "devices", "POST", Obj{"model": model, "method": "lan", "address": f.server.URL})
		id := str(obj(added["device"])["id"])
		status, data := requestJSON(t, server, "devices/"+id+"/data-history", "GET", nil)
		if status != 200 || num(data["samples"]) != 1 || len(arr(data["metrics"])) != len(dataMetrics(model)) {
			t.Fatal("successful collection not sampled", status, data)
		}
		if model == "TC1" && obj(obj(data["summary"])["cost"])["total"] != nil {
			t.Fatal("unconfigured tariff invented a cost")
		}
		_, data = requestJSON(t, server, "devices/"+id+"/data-history?range=30d", "GET", nil)
		if len(arr(data["points"])) > 300 {
			t.Fatal("too many points")
		}
		status, _ = requestJSON(t, server, "devices/"+id+"/data-history?range=invalid", "GET", nil)
		if status != 400 {
			t.Fatal(status)
		}
	}
	id := "m1-aabbcc000074"
	m.store.recordSample(Obj{"id": id, "model": "M1", "state": Obj{"sensorValid": false, "pm25": 1000.0, "temp": 1000.0, "co2": nil}}, now)
	data, err := m.store.dataHistory(context.Background(), id, "M1", "24h", now+1)
	if err != nil || obj(obj(data["summary"])["pm25"])["avg"] != nil || obj(obj(data["summary"])["temp"])["avg"] != nil {
		t.Fatal("invalid sensor values accepted", err, data)
	}
	status, _ := requestJSON(t, server, "devices/unknown/data-history", "GET", nil)
	if status != 404 {
		t.Fatal(status)
	}
	// Failed polls must not record cached readings in later minute buckets.
	m.mu.Lock()
	for _, e := range m.devices {
		e.d["address"] = "http://127.0.0.1:1"
	}
	m.mu.Unlock()
	before := len(m.store.devices())
	mustExec := func(query string, args ...any) {
		_, err := m.store.db.Exec(query, args...)
		if err != nil {
			t.Fatal(err)
		}
	}
	mustExec("INSERT INTO telemetry VALUES (?,?,?,?)", "removed-device", 0, now-sampleRetention-sampleInterval, "{}")
	m.store.lastPrune = now - int64(2*time.Hour/time.Millisecond)
	m.pollAll()
	var count int
	must(m.store.db.QueryRow("SELECT COUNT(*) FROM telemetry").Scan(&count))
	if count != before+1 {
		t.Fatal("failed collection recorded stale readings", count)
	}
}

func TestMQTTDataHistoryRejectsRetainedAndMergesSameMinute(t *testing.T) {
	m, _, server := testManager(t)
	id := "m1-aabbcc000075"
	requestJSON(t, server, "devices", "POST", Obj{"model": "M1", "method": "mqtt", "mac": "aabbcc000075"})
	m.mu.Lock()
	m.devices[id].r.availability = true
	m.mu.Unlock()
	state := Obj{"model": "m1", "device_id": "aabbcc000075", "sensor_valid": true, "pm25": 12.0, "temperature": 24.5, "humidity": 51.0, "formaldehyde": 0.012}
	raw, _ := json.Marshal(state)
	m.onPacket("m1/aabbcc000075/state", raw, true)
	var count int
	must(m.store.db.QueryRow("SELECT COUNT(*) FROM telemetry WHERE device_id=?", id).Scan(&count))
	if count != 0 {
		t.Fatal("retained MQTT snapshot entered history")
	}
	m.onPacket("m1/aabbcc000075/state", raw, false)
	m.onPacket("m1/aabbcc000075/state", raw, false)
	data, err := m.dataHistory(context.Background(), id, "24h")
	if err != nil || data["samples"] != 1 {
		t.Fatal("MQTT snapshot not recorded or duplicated", err, data)
	}
	near(t, num(obj(obj(data["summary"])["pm25"])["avg"]), 12)
}
