package main

import (
	"context"
	"reflect"
	"testing"
)

func TestPowerOnNormalizationAndValidation(t *testing.T) {
	caps := Obj{"power_on_modes": []string{"off", "on", "restore", "invalid", "restore"}}
	for _, mode := range []string{"off", "on", "restore"} {
		s := normalize("S30-V1B", Obj{"power_on_mode": mode, "power_on": false}, nil, caps)
		if s["powerOnMode"] != mode || boolean(s["powerOnLegacy"]) || !reflect.DeepEqual(s["powerOnModes"], []string{"off", "on", "restore"}) {
			t.Fatal("modern policy not normalized", s)
		}
	}
	for _, enabled := range []bool{false, true} {
		s := normalize("S30-V1B", Obj{"power_on": enabled}, nil, nil)
		want := requestedPowerOnMode(Obj{"power_on": enabled})
		if s["powerOnMode"] != want || !boolean(s["powerOnLegacy"]) || !reflect.DeepEqual(s["powerOnModes"], []string{"off", "on"}) {
			t.Fatal("legacy policy not normalized", s)
		}
	}
	for _, raw := range []Obj{{}, {"power_on": "false"}, {"power_on_mode": "unknown", "power_on": false}} {
		if state := normalize("TC1", raw, nil, caps); state["powerOnMode"] != nil {
			t.Fatal("missing/invalid policy became a setting", raw, state)
		}
	}
	for _, values := range []Obj{
		{"power_on_mode": "restore"}, {"power_on_mode": "on"}, {"power_on_mode": "off"}, {"power_on": true}, {"power_on": false},
	} {
		if err := validateControl("S30-V1B", "config.set", Obj{"section": "device", "values": values}); err != nil {
			t.Fatal(values, err)
		}
	}
	for _, values := range []Obj{
		{}, {"power_on_mode": "invalid"}, {"power_on_mode": true}, {"power_on": "false"}, {"power_on": 1.0},
		{"power_on_mode": "on", "power_on": true}, {"power_on_mode": "off", "name": "unexpected"}, {"password": "unexpected"},
	} {
		if err := validateControl("S30-V1B", "config.set", Obj{"section": "device", "values": values}); err == nil {
			t.Fatal("unsafe device config accepted", values)
		}
	}
	if validateControl("M1", "config.set", Obj{"section": "device", "values": Obj{"power_on": true}}) == nil {
		t.Fatal("non-plug accepted a power policy")
	}
}

func TestPowerOnLANRoundTripAndLegacy(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "modern"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			m, _, server := testManager(t)
			f := firmwareFixture(t, "S30-V1B", "aabbcc000721")
			if legacy {
				delete(f.state, "power_on_mode")
				f.bootModes = nil
			}
			d, err := m.add(context.Background(), Obj{"model": "S30-V1B", "address": f.server.URL, "method": "lan"})
			if err != nil {
				t.Fatal(err)
			}
			id := str(d["id"])
			for _, mode := range []string{"on", "off", "restore"} {
				values := Obj{"power_on_mode": mode}
				if legacy && mode != "restore" {
					values = Obj{"power_on": mode == "on"}
				}
				status, result := requestJSON(t, server, "devices/"+id+"/control", "POST", Obj{"action": "config.set", "params": Obj{"section": "device", "values": values}})
				if legacy && mode == "restore" {
					if status != 422 || result["code"] != "unsupported" {
						t.Fatal("legacy restore must be rejected", status, result)
					}
					continue
				}
				state := obj(obj(result["device"])["state"])
				if status != 200 || !boolean(result["confirmed"]) || state["powerOnMode"] != mode || boolean(state["on"]) {
					t.Fatal("policy was not confirmed or changed current output", mode, status, result)
				}
				if result["message"] != "上电输出设置已同步到设备" || arr(state["outletNames"])[0] != "桌面插座" {
					t.Fatal("confirmation or unrelated setting changed", result)
				}
			}
			// A change from the firmware's own web page is read back on the next poll.
			f.mu.Lock()
			f.state["power_on"] = true
			if !legacy {
				f.state["power_on_mode"] = "on"
			}
			f.mu.Unlock()
			d, err = m.refresh(context.Background(), id)
			if err != nil || obj(d["state"])["powerOnMode"] != "on" {
				t.Fatal("device-side change did not synchronize", d, err)
			}
			f.mu.Lock()
			f.actions = []string{"status.get", "capabilities.get", "outlet.set"}
			f.mu.Unlock()
			m.refresh(context.Background(), id)
			if _, err := m.control(context.Background(), id, "config.set", Obj{"section": "device", "values": Obj{"power_on": false}}); codeOf(err) != "unsupported" {
				t.Fatal("missing config capability was bypassed", err)
			}
		})
	}
}
