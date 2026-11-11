package main

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestDeviceNetworkNormalization(t *testing.T) {
	status := Obj{"ssid": "测试 Wi-Fi", "rssi": -54.0, "ip": "192.168.50.10", "mask": "255.255.255.0", "gateway": "192.168.50.1", "wifi_connected": true, "wifi_password": "private-fixture-secret"}
	for _, model := range []string{"TC1", "M1", "A1", "DAIKIN", "S30-V1B"} {
		network := obj(normalize(model, status, nil, nil)["network"])
		if network["ssid"] != status["ssid"] || network["rssi"] != -54.0 || network["ip"] != status["ip"] || network["mask"] != status["mask"] || network["gateway"] != status["gateway"] || network["connected"] != true {
			t.Fatal(model, network)
		}
		raw, err := json.Marshal(network)
		if err != nil || strings.Contains(string(raw), "private-fixture-secret") {
			t.Fatal("credential escaped into network details", err)
		}
	}
	for _, value := range []any{nil, "-54", true, 12.0, -200.0, math.NaN(), math.Inf(1)} {
		if networkState(Obj{"rssi": value})["rssi"] != nil {
			t.Fatal("invalid signal was accepted", value)
		}
	}
	if networkState(Obj{"rssi": 0.0})["rssi"] != 0.0 || networkState(Obj{"wifi_rssi": -70.0})["rssi"] != -70.0 {
		t.Fatal("zero or legacy RSSI was lost")
	}
	network := networkState(Obj{"ssid": 12.0, "ip": "javascript:alert(1)", "mask": "invalid", "gateway": "user@host", "wifi_connected": "true"})
	for key, value := range network {
		if value != nil {
			t.Fatal("invalid metadata was not left empty", key, value)
		}
	}
	if networkState(Obj{"ip": "fd00::10"})["ip"] != "fd00::10" {
		t.Fatal("IPv6 address was lost")
	}
}

func TestDeviceNetworkLANAndMQTTUpdates(t *testing.T) {
	m, _, _ := testManager(t)
	f := firmwareFixture(t, "S30-V1B", "aabbcc000731")
	f.state["ssid"], f.state["rssi"], f.state["ip"] = "first-network", -42.0, "192.168.50.10"
	f.state["wifi_password"], f.state["mqtt_password"] = "private-fixture-secret", "private-fixture-secret"
	d, err := m.add(context.Background(), Obj{"model": "S30-V1B", "address": f.server.URL, "method": "hybrid"})
	if err != nil || obj(obj(d["state"])["network"])["ssid"] != "first-network" {
		t.Fatal(d, err)
	}
	id := str(d["id"])
	f.mu.Lock()
	f.state["ssid"], f.state["rssi"] = "second-network", -67.0
	live := clone(f.state)
	f.mu.Unlock()
	d, err = m.refresh(context.Background(), id)
	if err != nil || obj(obj(d["state"])["network"])["ssid"] != "second-network" {
		t.Fatal("LAN network metadata did not refresh", d, err)
	}
	m.mu.Lock()
	m.devices[id].r.availability = true
	m.mu.Unlock()
	live["ssid"], live["rssi"], live["ip"] = "mqtt-network", -60.0, "192.168.60.10"
	packet, _ := json.Marshal(live)
	m.onPacket("s30-v1b/aabbcc000731/state", packet, true)
	if obj(obj(m.list()[0]["state"])["network"])["ssid"] == "mqtt-network" {
		t.Fatal("retained network metadata was treated as fresh")
	}
	m.onPacket("s30-v1b/aabbcc000731/state", packet, false)
	d = m.list()[0]
	network := obj(obj(d["state"])["network"])
	if d["id"] != id || network["ssid"] != "mqtt-network" || network["rssi"] != -60.0 || network["ip"] != "192.168.60.10" {
		t.Fatal("MQTT network metadata did not refresh", d)
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), "private-fixture-secret") {
		t.Fatal("device credentials escaped into the public response")
	}
}
