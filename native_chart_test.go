package main

import (
	"context"
	"testing"
	"time"
)

func TestChartMetricDefaultsValidationAndPersistence(t *testing.T) {
	m, _, server := testManager(t)
	for i, check := range []struct{ model, mac, initial, selected, invalid string }{
		{"TC1", "aabbcc000601", "power", "energy", "co2"},
		{"M1", "aabbcc000602", "pm25", "temp", "power"},
		{"A1", "aabbcc000603", "speed", "filter", "humidity"},
		{"DAIKIN", "aabbcc000604", "pm25", "tvoc", "speed"},
	} {
		status, response := requestJSON(t, server, "devices", "POST", Obj{"model": check.model, "mac": check.mac, "method": "mqtt"})
		d := obj(response["device"])
		if status != 201 || d["chartMetric"] != check.initial || len(arr(d["chartMetrics"])) != len(dataMetrics(check.model)) {
			t.Fatal("old/new device missing sensible chart defaults", check, status, response)
		}
		path := "devices/" + str(d["id"])
		status, response = requestJSON(t, server, path, "PUT", Obj{"chartMetric": check.selected})
		if status != 200 || obj(response["device"])["chartMetric"] != check.selected {
			t.Fatal("valid chart choice was rejected", check, status, response)
		}
		for _, invalid := range []any{check.invalid, "", nil, 1.0, []string{check.selected}} {
			status, response = requestJSON(t, server, path, "PUT", Obj{"chartMetric": invalid, "name": "must-not-save"})
			if status != 400 || m.list()[i]["chartMetric"] != check.selected || m.list()[i]["name"] == "must-not-save" {
				t.Fatal("invalid selection was saved or changed another field", invalid, status, response)
			}
		}
		status, response = requestJSON(t, server, path, "PUT", Obj{"room": "客厅"})
		if status != 200 || obj(response["device"])["chartMetric"] != check.selected {
			t.Fatal("unrelated configuration reset the chart", response)
		}
		if m.store.devices()[i]["chartMetric"] != check.selected {
			t.Fatal("selection was not written to SQLite")
		}
	}
	f := firmwareFixture(t, "TC1", "aabbcc000605")
	_, response := requestJSON(t, server, "devices", "POST", Obj{"model": "TC1", "method": "lan", "address": f.server.URL})
	path := "devices/" + str(obj(response["device"])["id"])
	f.mu.Lock()
	calls := len(f.calls)
	f.mu.Unlock()
	status, response := requestJSON(t, server, path, "PUT", Obj{"chartMetric": "cost"})
	f.mu.Lock()
	defer f.mu.Unlock()
	if status != 200 || !boolean(obj(response["device"])["online"]) || len(f.calls) != calls {
		t.Fatal("changing chart touched device connection or sent commands", status, response)
	}
}

func TestChartMetricSurvivesManagerRestart(t *testing.T) {
	directory := t.TempDir()
	open := func() *Manager {
		t.Helper()
		s, err := openStore(directory)
		if err != nil {
			t.Fatal(err)
		}
		m, err := newManager(s, time.Second, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	m := open()
	d, err := m.add(context.Background(), Obj{"model": "DAIKIN", "method": "mqtt", "mac": "aabbcc000606"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.update(str(d["id"]), Obj{"chartMetric": "co2"}); err != nil {
		t.Fatal(err)
	}
	m.close()
	m = open()
	defer m.close()
	if m.list()[0]["chartMetric"] != "co2" {
		t.Fatal("saved default lost after restart", m.list())
	}
	if got := chartMetric(Obj{"model": "M1", "chartMetric": "removed-metric"}); got != "pm25" {
		t.Fatal("unknown saved metric did not fall back to model default", got)
	}
}
