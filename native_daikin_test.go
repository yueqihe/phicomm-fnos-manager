package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
)

func TestDAIKINSensorValidityAndOptionalModules(t *testing.T) {
	f := firmwareFixture(t, "DAIKIN", "aabbcc000501")
	sensors := obj(f.state["sensors"])
	// Preserve a real zero, reject stale and invalid values independently.
	sensors["pm25"] = Obj{"valid": true, "value": 0.0, "age_ms": 1000.0}
	sensors["pm10"] = Obj{"valid": true, "value": 25.0, "age_ms": 20001.0}
	sensors["co2"] = Obj{"valid": false, "value": 550.0, "age_ms": 1000.0}
	f.state["co2_warmup_seconds_remaining"] = 120.0
	n := normalize("DAIKIN", f.state, nil)
	if !boolean(n["sensorValid"]) || n["pm25"] != 0.0 || n["tvoc"] != 0.0 || n["temp"] != 24.5 || n["pm10"] != nil || n["co2"] != nil || n["hcho"] != nil || boolean(n["hchoPresent"]) || !boolean(n["pm10Present"]) || n["co2Warmup"] != 120.0 {
		t.Fatal("independent validity, zero or optional sensor incorrect", n)
	}
	f.state["formaldehyde_present"] = true
	sensors["formaldehyde"] = Obj{"valid": false, "value": 0.123}
	if n = normalize("DAIKIN", f.state, nil); !boolean(n["hchoPresent"]) || n["hcho"] != nil {
		t.Fatal("lost detected but invalid module", n)
	}
	sensors["formaldehyde"] = Obj{"valid": true, "value": 0.012, "age_ms": 500.0}
	sensors["pm25"] = Obj{"valid": false, "value": 99.0}
	n = normalize("DAIKIN", f.state, nil)
	if n["hcho"] != 0.012 || boolean(n["sensorValid"]) || n["pm25"] != nil || n["humidity"] != 51.0 {
		t.Fatal("one invalid sensor hid other measurements", n)
	}
	for _, age := range []any{nil, -1.0, "100"} {
		sensors["tvoc"] = Obj{"valid": true, "value": 22.0, "age_ms": age}
		if normalize("DAIKIN", f.state, nil)["tvoc"] != nil {
			t.Fatal("invalid data age accepted", age)
		}
	}
	for _, action := range []string{"fan.set", "display.set", "child_lock.set"} {
		if validateControl("DAIKIN", action, Obj{"enabled": true}) == nil {
			t.Fatal("unrelated control accepted", action)
		}
	}
}

func TestDAIKINLANDiscoveryControlHistoryAndExport(t *testing.T) {
	m, app, server := testManager(t)
	f := firmwareFixture(t, "DAIKIN", "aabbcc000502")
	d := app.discovery
	d.interfaces = func() []Adapter { return []Adapter{{ID: "test", Address: "127.0.0.2", CIDR: "127.0.0.2/30"}} }
	d.browse = func(context.Context, Adapter) ([]string, string) { return []string{f.server.URL}, "complete" }
	if _, err := d.start(Obj{"mode": "mdns"}); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, func() bool { return d.snapshot()["state"] == "complete" })
	found := d.snapshot()["devices"].([]Obj)
	if len(found) != 1 || found[0]["model"] != "DAIKIN" || found[0]["id"] != "daikin-aabbcc000502" {
		t.Fatal("mDNS candidate did not pass Daikin identity validation", found)
	}
	status, added := requestJSON(t, server, "devices", "POST", Obj{"model": "M1", "method": "lan", "address": f.server.URL})
	if status != 409 {
		t.Fatal("wrong device model accepted", status, added)
	}
	status, added = requestJSON(t, server, "devices", "POST", Obj{"model": "DAIKIN", "method": "hybrid", "address": f.server.URL, "name": "大金检测仪"})
	device := obj(added["device"])
	if status != 201 || !boolean(device["online"]) || obj(device["state"])["pm10"] != 18.0 {
		t.Fatal("Daikin LAN status failed", status, added)
	}
	status, controlled := requestJSON(t, server, "devices/daikin-aabbcc000502/control", "POST", Obj{"action": "indicator.set", "params": Obj{"enabled": false}})
	if status != 200 || !boolean(controlled["confirmed"]) || boolean(obj(obj(controlled["device"])["state"])["light"]) {
		t.Fatal("indicator control failed", status, controlled)
	}
	_, history := requestJSON(t, server, "devices/daikin-aabbcc000502/data-history", "GET", nil)
	summary := obj(history["summary"])
	if num(history["samples"]) != 1 || len(arr(history["metrics"])) != 7 || obj(summary["tvoc"])["latest"] != 0.0 || obj(summary["co2"])["latest"] != 650.0 || obj(summary["hcho"])["latest"] != nil {
		t.Fatal("Daikin history changed readings", history)
	}
	var output bytes.Buffer
	if err := m.store.writeDataCSV(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(output.String(), "\ufeff"))).ReadAll()
	if err != nil || len(records) != 2 || records[1][2] != "DAIKIN" || records[0][16] != "PM10（μg/m³）" || records[0][17] != "TVOC（ppb）" || records[1][16] != "18" || records[1][17] != "0" || records[1][10] != "" {
		t.Fatal("Daikin raw export failed", err, records)
	}
	f.mu.Lock()
	f.password = "protected-device"
	f.mu.Unlock()
	if _, err := readIdentity(context.Background(), f.server.URL, "", time.Second); codeOf(err) != "auth_failed" {
		t.Fatal("Daikin password not enforced", err)
	}
	if _, err := readIdentity(context.Background(), f.server.URL, "protected-device", time.Second); err != nil {
		t.Fatal("password-protected Daikin identity failed", err)
	}
}

func TestDAIKINMQTTDiscoveryControlAndLANMerge(t *testing.T) {
	m, _, server := testManager(t)
	port := freePort(t)
	status, data := requestJSON(t, server, "settings/mqtt", "PUT", Obj{"mode": "embedded", "host": "192.168.1.10", "port": float64(port), "username": "daikin-tests", "password": "fixture-secret"})
	if status != 200 {
		t.Fatal(status, data)
	}
	waitUntil(t, func() bool { return obj(m.settings()["mqttStatus"])["state"] == "connected" })
	f := firmwareFixture(t, "DAIKIN", "aabbcc000503")
	mac := "aabbcc000503"
	prefix := "phicomm/v2/" + userKey("daikin-tests")
	base := prefix + "/daikin/" + mac
	c := connectTest(t, port, "daikin-tests", "fixture-secret", "daikin-fixture")
	subscription := c.Subscribe(base+"/command", 1, func(_ paho.Client, msg paho.Message) {
		var request Obj
		if json.Unmarshal(msg.Payload(), &request) != nil {
			return
		}
		f.mu.Lock()
		response, _ := json.Marshal(f.command(request))
		f.mu.Unlock()
		c.Publish(base+"/result", 1, false, response)
	})
	if !subscription.WaitTimeout(time.Second) || subscription.Error() != nil {
		t.Fatal("Daikin command subscription failed")
	}
	publishTest(t, c, prefix+"/discovery/"+mac, Obj{"schema": 1, "model": "daikin", "protocol": "tmcp", "protocol_version": 2}, true)
	waitUntil(t, func() bool { return len(m.list()) == 1 })
	if boolean(m.list()[0]["online"]) {
		t.Fatal("discovery marked Daikin online without a fresh reading")
	}
	publishTest(t, c, base+"/availability", "online", true)
	waitUntil(t, func() bool { return boolean(m.list()[0]["online"]) })
	status, data = requestJSON(t, server, "devices/daikin-"+mac+"/control", "POST", Obj{"action": "indicator.set", "params": Obj{"enabled": false}})
	if status != 200 || !boolean(data["confirmed"]) {
		t.Fatal("Daikin MQTT control failed", status, data)
	}
	f.mu.Lock()
	obj(f.state["sensors"])["co2"] = Obj{"valid": false, "value": 550.0}
	obj(f.state["sensors"])["pm10"] = Obj{"valid": true, "value": 33.0, "age_ms": 1000.0}
	live := clone(f.state)
	f.mu.Unlock()
	publishTest(t, c, base+"/state", live, false)
	waitUntil(t, func() bool { return obj(m.list()[0]["state"])["pm10"] == 33.0 })
	if obj(m.list()[0]["state"])["co2"] != nil {
		t.Fatal("MQTT invalid CO2 became a measurement")
	}
	status, data = requestJSON(t, server, "devices", "POST", Obj{"model": "DAIKIN", "method": "hybrid", "address": f.server.URL})
	if status != 201 || len(m.list()) != 1 || obj(data["device"])["id"] != "daikin-"+mac || !boolean(obj(data["device"])["lan"]) || !boolean(obj(data["device"])["mqtt"]) {
		t.Fatal("LAN and MQTT failed to merge", status, data)
	}
}
