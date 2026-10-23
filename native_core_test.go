package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type testFirmware struct {
	mu           sync.Mutex
	state, power Obj
	actions      []string
	calls        []string
	password     string
	legacy, drop bool
	server       *httptest.Server
}

func firmwareFixture(t *testing.T, model, mac string) *testFirmware {
	t.Helper()
	f := &testFirmware{state: Obj{"model": strings.ToLower(model), "device_id": mac, "version": "fixture", "child_lock": false, "led_enabled": true}, power: Obj{"power_watts": 23.4, "energy_kwh": 1.23}}
	f.actions = []string{"status.get", "capabilities.get", "config.set"}
	switch model {
	case "TC1":
		f.state["socket_states"] = []any{float64(0), float64(0), float64(0), float64(0), float64(0), float64(0)}
		f.actions = append(f.actions, "outlet.set", "power.get", "indicator.set", "child_lock.set")
	case "M1":
		f.state["brightness_requested"] = float64(2)
		f.state["sensor_valid"] = true
		f.state["pm25"] = float64(12)
		f.actions = append(f.actions, "display.set")
	case "A1":
		f.state["fan_on"] = false
		f.state["fan_mode"] = "manual"
		f.state["speed"] = float64(35)
		f.actions = append(f.actions, "fan.set", "fan.mode.set", "light.set", "child_lock.set")
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.password != "" {
			user, password, ok := r.BasicAuth()
			if !ok || user != "admin" || password != f.password {
				w.WriteHeader(401)
				return
			}
		}
		if r.Method != "POST" || r.URL.Path != "/api/v1/command" {
			w.WriteHeader(404)
			return
		}
		var request Obj
		json.NewDecoder(r.Body).Decode(&request)
		response := f.command(request)
		if f.drop && !strings.HasSuffix(str(request["action"]), ".get") {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		sendJSON(w, 200, response)
	}))
	t.Cleanup(f.server.Close)
	return f
}
func (f *testFirmware) command(request Obj) Obj {
	action := str(request["action"])
	f.calls = append(f.calls, action)
	p := obj(request["params"])
	code := "ok"
	var data any
	switch action {
	case "info.get":
		if f.legacy {
			code = "unsupported"
		} else {
			data = f.state
		}
	case "status.get":
		data = f.state
	case "capabilities.get":
		data = Obj{"model": strings.Split(str(f.state["device_id"]), "-")[0], "actions": f.actions}
		data.(Obj)["model"] = "tc1"
		if _, ok := f.state["model"]; ok {
			data.(Obj)["model"] = f.state["model"]
		}
	case "power.get":
		data = f.power
	case "outlet.set":
		states := arr(f.state["socket_states"])
		value := float64(0)
		if boolean(p["on"]) {
			value = 1
		}
		if index, ok := p["index"]; ok {
			states[int(num(index))] = value
		} else {
			for i := range states {
				states[i] = value
			}
		}
		f.state["socket_states"] = states
		code = "accepted"
	case "display.set":
		f.state["brightness_requested"] = num(p["brightness"]) / 25
	case "fan.set":
		for _, key := range []string{"on", "mode", "speed"} {
			if v, ok := p[key]; ok {
				target := key
				if key == "on" || key == "mode" {
					target = "fan_" + key
				}
				f.state[target] = v
			}
		}
	case "fan.mode.set":
		f.state["fan_mode"] = p["mode"]
	case "indicator.set", "light.set":
		f.state["led_enabled"] = p["enabled"]
	case "child_lock.set":
		f.state["child_lock"] = p["enabled"]
	case "config.set":
		code = "accepted"
	default:
		code = "unsupported"
	}
	return Obj{"request_id": request["request_id"], "code": code, "data": data}
}
func testManager(t *testing.T) (*Manager, *App, *httptest.Server) {
	t.Helper()
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := newManager(s, 500*time.Millisecond, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	a := &App{manager: m, dev: true}
	a.discovery = newDiscovery(m)
	server := httptest.NewServer(a)
	t.Cleanup(func() { server.Close(); a.discovery.close(); m.close() })
	return m, a, server
}
func requestJSON(t *testing.T, server *httptest.Server, path, method string, input any) (int, Obj) {
	t.Helper()
	raw, _ := json.Marshal(input)
	req, _ := http.NewRequest(method, server.URL+"/api/"+path, strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var data Obj
	if err = json.NewDecoder(res.Body).Decode(&data); err != nil {
		t.Fatal(err)
	}
	return res.StatusCode, data
}
func near(t *testing.T, actual, expected float64) {
	t.Helper()
	if math.Abs(actual-expected) > 1e-8 {
		t.Fatalf("%v != %v", actual, expected)
	}
}
func TestBilling(t *testing.T) {
	c, err := validateTariff(Obj{"mode": "tou", "peakRate": "1", "valleyRate": "0.3"}, defaultTariff())
	if err != nil {
		t.Fatal(err)
	}
	start := float64(time.Date(2026, 10, 23, 21, 0, 0, 0, time.FixedZone("CN", 8*3600)).UnixMilli())
	b := recordEnergy(nil, float64(100), c, start)
	b = recordEnergy(b, float64(112), c, start+12*3600000)
	near(t, num(b["cost"]), 5)
	near(t, num(b["valleyEnergy"]), 10)
	near(t, num(b["energy"]), 12)
	same := recordEnergy(b, nil, c, start+13*3600000)
	if !reflectEqual(same, b) {
		t.Fatal("missing reading changed bill")
	}
	b = recordEnergy(b, float64(0), c, start+13*3600000)
	near(t, num(b["cost"]), 5)
	near(t, num(b["resets"]), 1)
	next, err := validateTariff(Obj{"mode": "flat", "flatRate": "0.6"}, c)
	if err != nil {
		t.Fatal(err)
	}
	b = recordEnergy(b, float64(20), next, start+14*3600000)
	near(t, num(b["cost"]), 5)
	b = recordEnergy(b, float64(21), next, start+15*3600000)
	near(t, num(b["cost"]), 5.6)
	for _, badRate := range []any{nil, "", true, -1.0, "NaN", "0.12345"} {
		if _, err := validateTariff(Obj{"mode": "flat", "flatRate": badRate}, defaultTariff()); err == nil {
			t.Fatalf("accepted invalid price %v", badRate)
		}
	}
	rate, period := currentPrice(c, start+3600000)
	near(t, num(rate), 0.3)
	if period != "valley" {
		t.Fatal(period)
	}
}
func reflectEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func TestLANAndBillingREST(t *testing.T) {
	m, _, server := testManager(t)
	f := firmwareFixture(t, "TC1", "aabbcc000001")
	f.legacy = true
	delete(f.state, "model")
	raw, err := os.ReadFile("test/data/tc1-0.9.6-ed4474-status.json")
	if err == nil {
		var fixture Obj
		json.Unmarshal(raw, &fixture)
		if obj(fixture["data"])["device_id"] != nil {
			f.state = obj(fixture["data"])
			f.state["device_id"] = "aabbcc000001"
		}
	}
	status, data := requestJSON(t, server, "devices", "POST", Obj{"model": "TC1", "method": "hybrid", "address": f.server.URL})
	if status != 201 {
		t.Fatal(status, data)
	}
	d := obj(data["device"])
	if !boolean(d["online"]) {
		t.Fatal(d)
	}
	id := str(d["id"])
	status, data = requestJSON(t, server, "devices/"+id+"/control", "POST", Obj{"action": "outlet.set", "params": Obj{"index": float64(0), "on": true}})
	if status != 200 || !boolean(data["confirmed"]) {
		t.Fatal(status, data)
	}
	requestJSON(t, server, "settings/tariff", "PUT", Obj{"mode": "flat", "flatRate": "0.6"})
	requestJSON(t, server, "devices/"+id+"/refresh", "POST", Obj{})
	f.mu.Lock()
	f.power["energy_kwh"] = 3.23
	f.mu.Unlock()
	_, data = requestJSON(t, server, "devices/"+id+"/refresh", "POST", Obj{})
	near(t, num(obj(obj(data["device"])["billing"])["cost"]), 1.2)
	f.mu.Lock()
	f.power["energy_kwh"] = nil
	f.mu.Unlock()
	_, data = requestJSON(t, server, "devices/"+id+"/refresh", "POST", Obj{})
	near(t, num(obj(obj(data["device"])["billing"])["cost"]), 1.2)
	f.mu.Lock()
	f.drop = true
	f.mu.Unlock()
	status, data = requestJSON(t, server, "devices/"+id+"/control", "POST", Obj{"action": "outlet.set", "params": Obj{"on": false}})
	if status != 502 {
		t.Fatal(status, data)
	}
	f.mu.Lock()
	writes := 0
	for _, v := range f.calls {
		if v == "outlet.set" {
			writes++
		}
	}
	f.mu.Unlock()
	if writes != 2 {
		t.Fatal("ambiguous write replayed", writes)
	}
	m.mu.RLock()
	if str(m.devices[id].d["password"]) != "" {
		t.Fatal("unexpected password")
	}
	m.mu.RUnlock()
}
func TestThreeModelsAndIdentityGuard(t *testing.T) {
	_, _, server := testManager(t)
	for i, model := range []string{"TC1", "M1", "A1"} {
		t.Run(model, func(t *testing.T) {
			mac := []string{"112233000001", "112233000002", "112233000003"}[i]
			f := firmwareFixture(t, model, mac)
			f.password = " fixture secret "
			status, data := requestJSON(t, server, "devices", "POST", Obj{"model": model, "method": "lan", "address": f.server.URL, "password": f.password})
			if status != 201 {
				t.Fatal(status, data)
			}
			d := obj(data["device"])
			id := str(d["id"])
			if d["password"] != nil || !boolean(d["passwordSet"]) {
				t.Fatal("secret exposed")
			}
			action := "outlet.set"
			p := Obj{"on": true}
			if model == "M1" {
				action = "display.set"
				p = Obj{"brightness": 75.0}
			}
			if model == "A1" {
				action = "fan.set"
				p = Obj{"on": true, "speed": 65.0, "mode": "manual"}
			}
			status, data = requestJSON(t, server, "devices/"+id+"/control", "POST", Obj{"action": action, "params": p})
			if status != 200 || !boolean(data["confirmed"]) {
				t.Fatal(status, data)
			}
			f.mu.Lock()
			f.state["device_id"] = "ffffffffffff"
			count := len(f.calls)
			f.mu.Unlock()
			status, data = requestJSON(t, server, "devices/"+id+"/control", "POST", Obj{"action": action, "params": p})
			if status != 409 {
				t.Fatal(status, data)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			for _, v := range f.calls[count:] {
				if v == action {
					t.Fatal("wrote to a mismatched device")
				}
			}
		})
	}
}
func TestGatewayAuthenticationAndRequestValidation(t *testing.T) {
	_, a, server := testManager(t)
	a.dev = false
	req, _ := http.NewRequest("GET", server.URL+"/api/devices", nil)
	req.Header.Set("X-Trim-Userid", "1000")
	req.Header.Set("X-Trim-Isadmin", "true")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 401 {
		t.Fatal("forged TCP identity accepted")
	}
	serve := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://nas.example"+path, strings.NewReader(body))
		r = r.WithContext(context.WithValue(r.Context(), localSocketKey{}, true))
		r.Header.Set("X-Trim-Userid", "1000")
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	if w := serve("GET", "/api/settings", "", nil); w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	for _, value := range []struct {
		body string
		code int
	}{{"[]", 400}, {"{", 400}, {"null", 400}, {strings.Repeat("x", 16385), 413}} {
		if w := serve("PUT", "/api/settings/tariff", value.body, nil); w.Code != value.code {
			t.Fatal(w.Code, w.Body)
		}
	}
	if w := serve("PUT", "/api/settings/tariff", `{"mode":"disabled"}`, map[string]string{"Origin": "http://outside.example"}); w.Code != 403 {
		t.Fatal(w.Code)
	}
	if w := serve("GET", "/api/devices", "", map[string]string{"X-Trim-Isadmin": "false"}); w.Code != 401 {
		t.Fatal("nonadmin accepted")
	}
	if w := serve("GET", "/app.js", "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "预估消费") {
		t.Fatal("embedded frontend missing")
	}
}
