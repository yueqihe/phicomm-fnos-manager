package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func activityRows(m *Manager, action string) []Obj {
	rows := []Obj{}
	for _, row := range m.store.history(math.MaxInt64) {
		if row["action"] == action {
			rows = append(rows, row)
		}
	}
	return rows
}

func TestActivityLANChangesAndCommands(t *testing.T) {
	m, _, _ := testManager(t)
	f := firmwareFixture(t, "TC1", "aabbcc000081")
	f.state["ip"], f.state["ssid"], f.state["gateway"] = "192.168.1.20", "Old Wi-Fi", "192.168.1.1"
	d, err := m.add(context.Background(), Obj{"model": "TC1", "method": "lan", "address": f.server.URL, "name": "书房排插"})
	if err != nil {
		t.Fatal(err)
	}
	id := str(d["id"])
	if len(activityRows(m, "device.online")) != 1 || len(activityRows(m, "network.ip")) != 0 {
		t.Fatal("initial read must record one online event and establish network baseline")
	}
	// A command and its confirming read produce one descriptive operation.
	if _, err = m.control(context.Background(), id, "outlet.set", Obj{"index": 2.0, "on": true}); err != nil {
		t.Fatal(err)
	}
	rows := activityRows(m, "outlet.set")
	if len(rows) != 1 || !strings.Contains(str(rows[0]["message"]), "插口 3（插口 3）：开启") || rows[0]["outcome"] != "confirmed" || len(activityRows(m, "outlet.change")) != 0 {
		t.Fatal("command was ambiguous or duplicated", rows)
	}
	// Simulate a physical button / firmware web page operation.
	f.mu.Lock()
	f.state["ip"], f.state["ssid"], f.state["gateway"] = "192.168.9.20", "New Wi-Fi", "192.168.9.1"
	f.state["child_lock"], f.state["led_enabled"] = true, false
	arr(f.state["socket_states"])[4] = float64(1)
	f.mu.Unlock()
	if _, err = m.refresh(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"network.ip", "network.wifi", "network.gateway", "outlet.change", "indicator.change", "child_lock.change"} {
		rows = activityRows(m, action)
		if len(rows) != 1 || rows[0]["outcome"] != "observed" || !strings.Contains(str(rows[0]["message"]), " → ") {
			t.Fatal("missing before/after event", action, rows)
		}
	}
	count := len(m.store.history(math.MaxInt64))
	for i := 0; i < 3; i++ {
		f.mu.Lock()
		f.state["rssi"] = float64(-40 - i)
		f.power["power_watts"] = float64(80 + i)
		if i == 0 {
			delete(f.state, "ssid")
			delete(f.state, "ip")
		} else {
			f.state["ssid"], f.state["ip"] = "New Wi-Fi", "192.168.9.20"
		}
		f.mu.Unlock()
		m.refresh(context.Background(), id)
	}
	if len(m.store.history(math.MaxInt64)) != count {
		t.Fatal("polling, missing fields, or sensor fluctuations repeated events")
	}
}

func TestActivityOfflineGraceRecoveryAndRestart(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := newManager(s, 100*time.Millisecond, time.Hour)
	f, e := recoveryFixture(t, m, "S30-V1B", "lan")
	id := str(e.d["id"])
	moved := httptest.NewServer(f.server.Config.Handler)
	defer moved.Close()
	f.server.Close()
	m.mu.Lock()
	e.r.lanLast = time.Now().Add(-31 * time.Second)
	m.mu.Unlock()
	for i := 0; i < 2; i++ {
		m.refresh(context.Background(), id)
		if len(activityRows(m, "device.offline")) != 0 {
			t.Fatal("short collection failures must use existing online grace")
		}
	}
	for i := 0; i < 3; i++ {
		m.refresh(context.Background(), id)
	}
	if len(activityRows(m, "device.offline")) != 1 {
		t.Fatal("offline should be recorded once")
	}
	if _, err = m.update(id, Obj{"address": moved.URL}); err != nil {
		t.Fatal(err)
	}
	m.refresh(context.Background(), id)
	if len(activityRows(m, "device.online")) != 2 || len(activityRows(m, "outlet.change")) != 0 {
		t.Fatal("reconnection lost baseline or did not record online")
	}
	count := len(m.store.history(math.MaxInt64))
	m.close()
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	m, _ = newManager(s, 100*time.Millisecond, time.Hour)
	defer m.close()
	m.refresh(context.Background(), id)
	if len(m.store.history(math.MaxInt64)) != count {
		t.Fatal("manager restart fabricated state changes")
	}
}

func TestActivityMQTTAndHybridRoutes(t *testing.T) {
	m, _, _ := testManager(t)
	m.mqtt.setStatus("connected", "fixture")
	d, _ := identity("TC1", "aabbcc000082")
	d["name"], d["method"] = "MQTT 排插", "mqtt"
	e := &deviceEntry{d: d, r: runtimeState{availability: true}}
	m.devices[str(d["id"])], m.order = e, []string{str(d["id"])}
	p := Obj{"model": "tc1", "device_id": d["mac"], "socket_states": []bool{false, false, false, false, false, false}, "ssid": "MQTT Wi-Fi", "ip": "192.168.1.20"}
	publish := func(retained bool) {
		raw, _ := json.Marshal(p)
		m.onPacket("tc1/"+str(d["mac"])+"/state", raw, retained)
	}
	publish(true)
	if len(activityRows(m, "device.online")) != 0 {
		t.Fatal("retained state created an online event")
	}
	publish(false)
	e.r.activityTargets = Obj{"outlet:0": true}
	p["socket_states"] = []bool{true, true, false, false, false, false}
	p["ssid"], p["ip"] = "Changed Wi-Fi", "192.168.9.20"
	publish(false)
	if rows := activityRows(m, "outlet.change"); len(rows) != 1 || !strings.Contains(str(rows[0]["message"]), "插口 2") {
		t.Fatal("a pending command suppressed an unrelated outlet change", rows)
	}
	if len(activityRows(m, "network.ip")) != 1 || len(activityRows(m, "network.wifi")) != 1 {
		t.Fatal("MQTT did not record network changes")
	}
	e.r.activityTargets = nil
	// Losing MQTT alone does not make a hybrid device offline.
	d["method"], d["address"] = "hybrid", "http://192.168.9.20"
	e.r.lanLast = time.Now()
	m.onPacket("tc1/"+str(d["mac"])+"/availability", []byte("offline"), false)
	if len(activityRows(m, "device.offline")) != 0 {
		t.Fatal("lost one of two routes but recorded device offline")
	}
	e.r.lanLast = time.Now().Add(-2 * time.Minute)
	m.onPacket("tc1/"+str(d["mac"])+"/availability", []byte("offline"), false)
	m.mqttOnline(false)
	if len(activityRows(m, "device.offline")) != 1 {
		t.Fatal("duplicate MQTT/broker offline notifications")
	}
}

func TestActivityUpgradeAndOtherModels(t *testing.T) {
	m, _, _ := testManager(t)
	for i, model := range []string{"M1", "A1", "DAIKIN"} {
		f := firmwareFixture(t, model, []string{"aabbcc000083", "aabbcc000084", "aabbcc000085"}[i])
		d, err := m.add(context.Background(), Obj{"model": model, "method": "lan", "address": f.server.URL})
		if err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		switch model {
		case "M1":
			f.state["brightness_requested"] = 3.0
		case "A1":
			f.state["fan_on"], f.state["fan_mode"] = true, "auto"
		case "DAIKIN":
			f.state["indicator_enabled"] = false
		}
		f.mu.Unlock()
		m.refresh(context.Background(), str(d["id"]))
	}
	if len(activityRows(m, "display.change")) != 1 || len(activityRows(m, "fan.change")) != 2 || len(activityRows(m, "indicator.change")) != 1 {
		t.Fatal("non-plug controls were not observed")
	}
	// Old installations have no activity snapshot. Seed from their saved state
	// so a genuine first-read change can still be detected after upgrading.
	d := clone(m.devices[m.order[0]].d)
	delete(d, "activity")
	m.store.save(d)
	dir := t.TempDir()
	s, _ := openStore(dir)
	s.save(d)
	upgraded, _ := newManager(s, time.Second, time.Hour)
	defer upgraded.close()
	e := upgraded.devices[str(d["id"])]
	if obj(e.d["activity"])["brightness"] != obj(d["state"])["brightness"] {
		t.Fatal("upgrade did not seed previous readings")
	}
}
