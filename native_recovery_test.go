package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func recoveryFixture(t *testing.T, m *Manager, model, method string) (*testFirmware, *deviceEntry) {
	t.Helper()
	f := firmwareFixture(t, model, "aabbcc000099")
	d, err := m.add(context.Background(), Obj{"model": model, "method": method, "address": f.server.URL, "mac": "aabbcc000099", "name": "原设备", "room": "书房"})
	if err != nil {
		t.Fatal(err)
	}
	e, err := m.entry(str(d["id"]))
	if err != nil {
		t.Fatal(err)
	}
	return f, e
}

func discoveryFixture(d *Discovery, addresses ...string) {
	d.interfaces = func() []Adapter {
		return []Adapter{{ID: "test", Address: "127.0.0.2", CIDR: "127.0.0.2/32"}}
	}
	d.browse = func(context.Context, Adapter) ([]string, string) { return addresses, "complete" }
}

func TestAutomaticLANRecoveryPreservesDataAcrossRestart(t *testing.T) {
	for _, method := range []string{"lan", "hybrid"} {
		t.Run(method, func(t *testing.T) {
			dir := t.TempDir()
			s, err := openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			m, err := newManager(s, 200*time.Millisecond, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			d := newDiscovery(m)
			defer func() { d.close(); m.close() }()
			m.recoverLAN = d.recover
			if _, err := m.saveTariff(Obj{"mode": "flat", "flatRate": "0.6"}); err != nil {
				t.Fatal(err)
			}
			f, e := recoveryFixture(t, m, "TC1", method)
			id := str(e.d["id"])
			now := time.Now().UnixMilli()
			// Historical samples and accumulated charges predate the move.
			e.d["chartMetric"] = "cost"
			e.d["billing"] = recordEnergy(nil, 0.23, m.tariff, float64(now-20*sampleInterval))
			e.d["billing"] = recordEnergy(obj(e.d["billing"]), 1.23, m.tariff, float64(now-10*sampleInterval))
			m.store.save(e.d)
			m.store.recordSample(e.d, now-10*sampleInterval)
			before := clone(e.d)
			moved := httptest.NewServer(f.server.Config.Handler)
			defer moved.Close()
			f.server.Close()
			u, _ := url.Parse(moved.URL)
			fmt.Sscanf(u.Port(), "%d", &d.port)
			var visited atomic.Int32
			d.interfaces = func() []Adapter {
				visited.Add(1)
				return []Adapter{{ID: "new-router", Address: "127.0.0.2", CIDR: "127.0.0.2/30"}}
			}
			d.browse = func(context.Context, Adapter) ([]string, string) { return nil, "unavailable" }
			for i := 0; i < 2; i++ {
				m.pollAll()
				if d.scan != nil {
					t.Fatal("scanned before repeated collection failures")
				}
			}
			m.pollAll()
			d.wg.Wait()
			if visited.Load() == 0 || d.scan == nil || d.scan.state != "complete" || !d.scan.background {
				t.Fatal("automatic scan did not enumerate the current network")
			}
			if len(m.list()) != 1 || e.d["address"] != moved.URL || !m.list()[0]["online"].(bool) {
				t.Fatal("device not rebound in place", m.list())
			}
			for _, field := range []string{"id", "mac", "name", "room", "chartMetric", "method", "password"} {
				if e.d[field] != before[field] {
					t.Fatal("configuration lost", field)
				}
			}
			for _, field := range []string{"since", "cost", "energy", "flatEnergy", "resets", "revision"} {
				if obj(e.d["billing"])[field] != obj(before["billing"])[field] {
					t.Fatal("billing reset", field, e.d["billing"])
				}
			}
			history, err := m.dataHistory(context.Background(), id, "24h")
			if err != nil || num(history["samples"]) < 2 {
				t.Fatal("prior history lost", history, err)
			}
			if !boolean(d.snapshot()["devices"].([]Obj)[0]["recovered"]) {
				t.Fatal("scan did not report recovery")
			}
			d.close()
			m.close()
			s, err = openStore(dir)
			if err != nil {
				t.Fatal(err)
			}
			m, err = newManager(s, 200*time.Millisecond, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if got := m.list(); len(got) != 1 || got[0]["address"] != moved.URL || got[0]["name"] != "原设备" || got[0]["chartMetric"] != "cost" {
				t.Fatal("restart lost recovered configuration", got)
			}
			near(t, num(obj(m.list()[0]["billing"])["cost"]), 0.6)
			reloaded, err := m.dataHistory(context.Background(), id, "24h")
			if err != nil || reloaded["samples"] != history["samples"] {
				t.Fatal("restart lost historical samples", err)
			}
		})
	}
}

func TestManualDiscoveryRecoversAllModelsAndRejectsReusedAddress(t *testing.T) {
	for _, model := range []string{"TC1", "M1", "A1", "DAIKIN"} {
		t.Run(model, func(t *testing.T) {
			m, a, _ := testManager(t)
			f, e := recoveryFixture(t, m, model, "lan")
			moved := httptest.NewServer(f.server.Config.Handler)
			defer moved.Close()
			// The old IP now answers as a different physical device.
			other := firmwareFixture(t, model, "aabbcc000098")
			old := clone(e.d)
			e.d["address"] = other.server.URL
			m.pollAll()
			if m.list()[0]["online"] == true {
				t.Fatal("different MAC at the saved address accepted")
			}
			d := a.discovery
			discoveryFixture(d, other.server.URL, moved.URL)
			if _, err := d.start(Obj{}); err != nil {
				t.Fatal(err)
			}
			d.wg.Wait()
			if len(m.list()) != 1 || e.d["id"] != old["id"] || e.d["address"] != moved.URL || e.r.failures != 0 {
				t.Fatal("manual discovery failed to restore the original device", m.list())
			}
		})
	}
}

func TestRecoverySkipsHealthyMQTTRemovedAndStaleConfigurations(t *testing.T) {
	for _, scenario := range []string{"healthy", "mqtt", "removed", "edited", "busy", "cancelled", "mismatch", "unreadable"} {
		t.Run(scenario, func(t *testing.T) {
			m, _, _ := testManager(t)
			f, e := recoveryFixture(t, m, "M1", "lan")
			e.r.failures = 3
			target := m.lostLAN(1)[str(e.d["id"])]
			moved := httptest.NewServer(f.server.Config.Handler)
			defer moved.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			info := clone(f.state)
			info["model"] = "M1"
			switch scenario {
			case "healthy":
				e.r.failures = 0
			case "mqtt":
				m.update(str(e.d["id"]), Obj{"method": "mqtt"})
			case "removed":
				m.remove(str(e.d["id"]))
			case "edited":
				m.update(str(e.d["id"]), Obj{"address": "192.168.20.10"})
			case "busy":
				e.op.Lock()
				defer e.op.Unlock()
			case "cancelled":
				cancel()
			case "mismatch":
				info["device_id"] = "aabbcc000098"
			case "unreadable":
				f.mu.Lock()
				f.password = "unavailable"
				f.mu.Unlock()
			}
			before := clone(e.d)
			if m.recoverAddress(ctx, target, info, moved.URL) || !reflectEqual(before, e.d) {
				t.Fatal("recovery overwrote protected configuration", scenario, e.d)
			}
			if scenario == "healthy" || scenario == "mqtt" || scenario == "removed" {
				if len(m.lostLAN(3)) != 0 {
					t.Fatal("non-candidate queued for background recovery")
				}
			}
		})
	}
}

func TestRecoveryThrottlesUsesNewInterfacesAndShutsDown(t *testing.T) {
	m, a, _ := testManager(t)
	_, e := recoveryFixture(t, m, "M1", "lan")
	e.r.failures = 3
	d := a.discovery
	discoveryFixture(d)
	var enumerations atomic.Int32
	d.interfaces = func() []Adapter { enumerations.Add(1); return nil }
	d.recover() // Network is temporarily absent during router replacement.
	d.recover()
	if enumerations.Load() != 1 {
		t.Fatal("unavailable network was not throttled")
	}
	discoveryFixture(d)
	started := make(chan struct{})
	d.browse = func(ctx context.Context, _ Adapter) ([]string, string) {
		close(started)
		<-ctx.Done()
		return nil, "unavailable"
	}
	d.nextRecovery = time.Time{}
	d.recover()
	<-started
	if d.cancel()["state"] != "scanning" {
		t.Fatal("closing add dialog cancelled background recovery")
	}
	d.recover() // Running scan must not start another worker.
	start := time.Now()
	d.close()
	if time.Since(start) > time.Second || d.scan.state != "cancelled" {
		t.Fatal("shutdown did not cancel recovery promptly")
	}
	if _, err := d.start(Obj{}); err == nil {
		t.Fatal("closed discovery restarted")
	}
}

func TestProtectedRecoveryDoesNotSendCredentialsAndManualReaddPreservesMetadata(t *testing.T) {
	m, a, _ := testManager(t)
	f, e := recoveryFixture(t, m, "TC1", "hybrid")
	f.mu.Lock()
	f.password = "device-password"
	f.mu.Unlock()
	m.update(str(e.d["id"]), Obj{"password": "device-password", "chartMetric": "energy"})
	var authenticated atomic.Int32
	moved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			authenticated.Add(1)
		}
		f.server.Config.Handler.ServeHTTP(w, r)
	}))
	defer moved.Close()
	d := a.discovery
	discoveryFixture(d, moved.URL)
	before := clone(e.d)
	if _, err := d.start(Obj{}); err != nil {
		t.Fatal(err)
	}
	d.wg.Wait()
	if authenticated.Load() != 0 || len(d.snapshot()["protectedAddresses"].([]string)) != 1 || !reflectEqual(before, e.d) {
		t.Fatal("discovery sent saved credentials to an unidentified address")
	}
	got, err := m.add(context.Background(), Obj{"model": "TC1", "method": "lan", "address": moved.URL, "password": "device-password", "name": "添加默认名", "room": "未分组"})
	if err != nil || len(m.list()) != 1 || got["name"] != "原设备" || got["room"] != "书房" || got["method"] != "hybrid" || got["chartMetric"] != "energy" || got["address"] != moved.URL {
		t.Fatal("manual reconnect reset existing settings", got, err)
	}
}
