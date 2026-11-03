package main

import (
	"testing"
	"time"
)

func TestRefreshSettingsValidationAndPropagation(t *testing.T) {
	m, _, server := testManager(t)
	status, data := requestJSON(t, server, "settings/refresh", "PUT", Obj{"pageSeconds": "2", "pollSeconds": float64(60)})
	if status != 200 || num(obj(data["refresh"])["pageSeconds"]) != 2 || m.pollInterval() != time.Minute {
		t.Fatal("refresh settings were not applied", status, data)
	}
	for _, field := range []string{"pageSeconds", "pollSeconds"} {
		for _, value := range []any{nil, true, "", "NaN", "Inf", float64(0), float64(1.5), float64(301)} {
			status, data := requestJSON(t, server, "settings/refresh", "PUT", Obj{field: value})
			if status != 400 {
				t.Fatalf("accepted invalid %s=%v: %d %v", field, value, status, data)
			}
		}
	}
	for _, field := range []string{"pageSeconds", "pollSeconds"} {
		value := float64(1)
		if field == "pollSeconds" {
			value = 61
		}
		if status, _ := requestJSON(t, server, "settings/refresh", "PUT", Obj{field: value}); status != 400 {
			t.Fatal("range limit ignored", field, status)
		}
	}
	_, data = requestJSON(t, server, "devices", "GET", nil)
	if !reflectEqual(data["refresh"], Obj{"pageSeconds": float64(2), "pollSeconds": float64(60)}) {
		t.Fatal("other browsers cannot learn the saved intervals, or invalid writes changed them", data)
	}
	if status, data = requestJSON(t, server, "settings/refresh", "PUT", Obj{"pageSeconds": float64(300), "pollSeconds": "5"}); status != 200 {
		t.Fatal("valid range boundaries rejected", status, data)
	}
}

func TestRefreshSettingsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	for attempt := 0; attempt < 2; attempt++ {
		s, err := openStore(dir)
		if err != nil {
			t.Fatal(err)
		}
		m, err := newManager(s, time.Second, 15*time.Second)
		if err != nil {
			s.Close()
			t.Fatal(err)
		}
		if attempt == 0 {
			if !reflectEqual(m.settings()["refresh"], Obj{"pageSeconds": float64(5), "pollSeconds": float64(15)}) {
				m.close()
				t.Fatal("upgrade defaults changed")
			}
			_, err = m.saveRefresh(Obj{"pageSeconds": float64(10), "pollSeconds": float64(30)})
		} else if !reflectEqual(m.settings()["refresh"], Obj{"pageSeconds": float64(10), "pollSeconds": float64(30)}) {
			m.close()
			t.Fatal("saved intervals were lost after restarting")
		}
		m.close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRefreshSettingsResetActivePollTimer(t *testing.T) {
	m, _, server := testManager(t)
	f := firmwareFixture(t, "TC1", "aabbcc000991")
	status, data := requestJSON(t, server, "devices", "POST", Obj{"model": "TC1", "method": "lan", "address": f.server.URL})
	if status != 201 {
		t.Fatal(status, data)
	}
	id := str(obj(data["device"])["id"])
	if _, err := m.saveRefresh(Obj{"pollSeconds": float64(60)}); err != nil {
		t.Fatal(err)
	}
	m.start()
	count := func() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }
	before := count()
	deadline := time.Now().Add(time.Second)
	for count() <= before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	e, _ := m.entry(id)
	// Wait for the initial read to finish before changing the fixture.
	e.op.Lock()
	f.mu.Lock()
	f.power["power_watts"] = float64(321)
	f.mu.Unlock()
	e.op.Unlock()
	if _, err := m.saveRefresh(Obj{"pollSeconds": float64(5)}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.RLock()
		power := num(obj(e.d["state"])["power"])
		m.mu.RUnlock()
		if power == 321 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("saving a shorter interval did not reset the running NAS polling timer")
}
