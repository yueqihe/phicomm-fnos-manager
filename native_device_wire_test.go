package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/vmihailenco/msgpack/v5"
)

func writeMsgpack(t *testing.T, w http.ResponseWriter, status int, value Obj) {
	t.Helper()
	raw, err := msgpack.Marshal(value)
	if err != nil {
		t.Error(err)
		return
	}
	w.Header().Set("Content-Type", "application/msgpack")
	w.WriteHeader(status)
	w.Write(raw)
}

func TestDeviceWireNumericTypesBoundsAndDeterministicCommands(t *testing.T) {
	value := Obj{"signed": int16(-20), "unsigned": uint32(4294967295), "float": float32(24.5), "zero": uint8(0), "missing": nil, "sensors": Obj{"pm25": Obj{"valid": true, "value": uint16(12)}}}
	raw, _ := msgpack.Marshal(value)
	decoded, binary, err := decodeDeviceWire(raw)
	if err != nil || !binary || decoded["signed"] != -20.0 || decoded["unsigned"] != 4294967295.0 || decoded["float"] != 24.5 || decoded["zero"] != 0.0 || decoded["missing"] != nil || obj(obj(decoded["sensors"])["pm25"])["value"] != 12.0 {
		t.Fatal(decoded, binary, err)
	}
	invalid := [][]byte{{0x81, 0xa1, 'x'}, {0x81, 0xa1, 'x', 0xdf, 0xff, 0xff, 0xff, 0xff}, {0x81, 0xa1, 'x', 0xdd, 0xff, 0xff, 0xff, 0xff}, {0x82, 0xa1, 'x', 1, 0xa1, 'x', 2}, {0x81, 0x01, 0x01}, append(raw, 0xc0)}
	nested := []byte{0x81, 0xa1, 'x'}
	invalid = append(invalid, append(append(nested, bytes.Repeat([]byte{0x91}, 40)...), 0xc0))
	for _, nonfinite := range []any{math.NaN(), math.Inf(1), math.Inf(-1), float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		data, err := msgpack.Marshal(Obj{"sensors": Obj{"temperature": Obj{"valid": true, "value": nonfinite}}})
		if err != nil {
			t.Fatal(err)
		}
		invalid = append(invalid, data)
	}
	for _, data := range invalid {
		if _, _, err := decodeDeviceWire(data); err == nil {
			t.Fatalf("invalid MessagePack accepted: %x", data)
		}
	}
	command := envelope("config.set", Obj{"section": "mqtt", "values": Obj{"port": 1883.0, "enabled": true}})
	first, err := encodeDeviceWire(command, true)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		next, _ := encodeDeviceWire(command, true)
		if !bytes.Equal(first, next) {
			t.Fatal("request bytes changed for the same request ID")
		}
	}
	var untyped Obj
	if err := msgpack.Unmarshal(first, &untyped); err != nil {
		t.Fatal(err)
	}
	if _, floating := obj(obj(untyped["params"])["values"])["port"].(float64); floating {
		t.Fatal("integer command parameter encoded as a float")
	}
}

func TestMessagePackHTTPBearerAndOldJSONFirmware(t *testing.T) {
	for _, protected := range []bool{false, true} {
		t.Run(map[bool]string{false: "no-password", true: "bearer"}[protected], func(t *testing.T) {
			m, a, _ := testManager(t)
			f := firmwareFixture(t, "DAIKIN", "aabbcc000601")
			obj(f.state["sensors"])["co2"] = Obj{"valid": true, "value": uint16(2534), "age_ms": uint32(40)}
			obj(f.state["sensors"])["temperature"] = Obj{"valid": true, "value": float32(27.25), "age_ms": uint32(40)}
			var logins, writes atomic.Int32
			var revoked atomic.Bool
			device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/info" {
					writeMsgpack(t, w, 200, Obj{"code": "ok", "data": Obj{"model": "daikin", "device_id": "aabbcc000601", "auth": "bearer", "auth_required": protected, "default_wire_format": "msgpack"}})
					return
				}
				raw, _ := io.ReadAll(r.Body)
				var request Obj
				if r.Header.Get("Content-Type") != "application/msgpack" || msgpack.Unmarshal(raw, &request) != nil {
					writeMsgpack(t, w, 415, Obj{"code": "invalid_params"})
					return
				}
				if r.URL.Path == "/api/v1/auth/login" {
					logins.Add(1)
					if request["password"] != "fixture-password" {
						writeMsgpack(t, w, 401, Obj{"code": "unauthorized"})
						return
					}
					revoked.Store(false)
					writeMsgpack(t, w, 200, Obj{"code": "ok", "data": Obj{"token": "fixture-session", "expires_in": uint16(1800), "password_required": true}})
					return
				}
				if protected && (revoked.Load() || r.Header.Get("Authorization") != "Bearer fixture-session") {
					writeMsgpack(t, w, 401, Obj{"code": "unauthorized"})
					return
				}
				if !strings.HasSuffix(str(request["action"]), ".get") {
					writes.Add(1)
				}
				f.mu.Lock()
				response := f.command(request)
				writeMsgpack(t, w, 200, response)
				f.mu.Unlock()
			}))
			defer device.Close()
			password := ""
			if protected {
				password = "fixture-password"
				if _, err := readIdentity(context.Background(), device.URL, "", time.Second); codeOf(err) != "auth_failed" {
					t.Fatal("missing password accepted", err)
				}
			}
			added, err := m.add(context.Background(), Obj{"model": "DAIKIN", "method": "lan", "address": device.URL, "password": password})
			if err != nil || !boolean(added["online"]) || obj(added["state"])["co2"] != 2534.0 || obj(added["state"])["temp"] != 27.25 {
				t.Fatal(added, err)
			}
			if protected {
				if logins.Load() != 1 {
					t.Fatal("did not reuse bearer session", logins.Load())
				}
				revoked.Store(true)
				if _, err := m.refresh(context.Background(), str(added["id"])); err != nil || logins.Load() != 2 {
					t.Fatal("revoked session did not renew", err, logins.Load())
				}
				if _, err := readIdentity(context.Background(), device.URL, "wrong-password", time.Second); codeOf(err) != "auth_failed" {
					t.Fatal("wrong password borrowed cached credentials", err)
				}
				public, _ := json.Marshal(m.list())
				stored, _ := json.Marshal(m.store.devices())
				if bytes.Contains(public, []byte("fixture-session")) || bytes.Contains(stored, []byte("fixture-session")) || bytes.Contains(public, []byte(password)) {
					t.Fatal("session token persisted or credentials exposed")
				}
			}
			controlled, err := m.control(context.Background(), str(added["id"]), "indicator.set", Obj{"enabled": false})
			if err != nil || !boolean(controlled["confirmed"]) || writes.Load() != 1 {
				t.Fatal("binary control not confirmed exactly once", controlled, err, writes.Load())
			}
			history, err := m.dataHistory(context.Background(), str(added["id"]), "24h")
			if err != nil || obj(obj(history["summary"])["co2"])["latest"] != 2534.0 {
				t.Fatal("integer readings missing from history", err, history)
			}
			var csv bytes.Buffer
			if err := m.store.writeDataCSV(context.Background(), &csv); err != nil || !strings.Contains(csv.String(), "2534") {
				t.Fatal("binary readings missing from CSV", err)
			}
			if !protected {
				discoveryFixture(a.discovery, device.URL)
				a.discovery.start(Obj{"mode": "mdns"})
				a.discovery.wg.Wait()
				if len(a.discovery.snapshot()["devices"].([]Obj)) != 1 {
					t.Fatal("binary device missing from scan")
				}
			}
		})
	}
}

func TestHTTPFormatRejectionDoesNotReplayUncertainWrites(t *testing.T) {
	for _, outcome := range []string{"ok", "disconnect", "wrong-id", "busy"} {
		t.Run(outcome, func(t *testing.T) {
			var writes atomic.Int32
			device := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/v1/info" {
					w.WriteHeader(404)
					return
				}
				if r.Header.Get("Content-Type") != "application/msgpack" {
					writeMsgpack(t, w, 415, Obj{"code": "invalid_params"})
					return
				}
				data, _ := io.ReadAll(r.Body)
				var request Obj
				msgpack.Unmarshal(data, &request)
				writes.Add(1)
				if outcome == "disconnect" {
					conn, _, _ := w.(http.Hijacker).Hijack()
					conn.Close()
					return
				}
				id := request["request_id"]
				code := "accepted"
				if outcome == "wrong-id" {
					id = "wrong"
				}
				if outcome == "busy" {
					code = "busy"
				}
				writeMsgpack(t, w, 200, Obj{"request_id": id, "code": code})
			}))
			defer device.Close()
			_, err := httpCommand(context.Background(), device.URL, "", envelope("indicator.set", Obj{"enabled": false}), time.Second)
			if writes.Load() != 1 || (err == nil) != (outcome == "ok") {
				t.Fatal("uncertain write replayed or accepted", outcome, writes.Load(), err)
			}
		})
	}
}

func TestDAIKINMessagePackMQTTDiscoveryStateAndControl(t *testing.T) {
	m, _, server := testManager(t)
	port := freePort(t)
	status, data := requestJSON(t, server, "settings/mqtt", "PUT", Obj{"mode": "embedded", "host": "192.168.1.10", "port": float64(port), "username": "binary-tests", "password": "fixture-secret"})
	if status != 200 {
		t.Fatal(status, data)
	}
	waitUntil(t, func() bool { return obj(m.settings()["mqttStatus"])["state"] == "connected" })
	f := firmwareFixture(t, "DAIKIN", "aabbcc000602")
	base := "phicomm/v2/" + userKey("binary-tests")
	topic := base + "/daikin/aabbcc000602"
	c := connectTest(t, port, "binary-tests", "fixture-secret", "binary-fixture")
	var writes atomic.Int32
	sub := c.Subscribe(topic+"/command", 1, func(_ paho.Client, msg paho.Message) {
		if !messagePackMap(msg.Payload()) {
			return
		}
		var request Obj
		if msgpack.Unmarshal(msg.Payload(), &request) != nil {
			return
		}
		if !strings.HasSuffix(str(request["action"]), ".get") {
			writes.Add(1)
		}
		f.mu.Lock()
		raw, _ := msgpack.Marshal(f.command(request))
		f.mu.Unlock()
		c.Publish(topic+"/result", 1, false, raw)
	})
	if !sub.WaitTimeout(time.Second) || sub.Error() != nil {
		t.Fatal("subscribe failed")
	}
	publish := func(topic string, value Obj, retained bool) {
		raw, _ := msgpack.Marshal(value)
		result := c.Publish(topic, 1, retained, raw)
		if !result.WaitTimeout(time.Second) || result.Error() != nil {
			t.Fatal("publish failed")
		}
	}
	publication := Obj{"schema": uint8(2), "model": "daikin", "device_id": "aabbcc000602", "protocol": "tmcp", "protocol_version": uint8(2), "wire_formats": []string{"msgpack"}, "default_wire_format": "msgpack"}
	publish(base+"/discovery/aabbcc000602", publication, true)
	waitUntil(t, func() bool { return len(m.list()) == 1 })
	publishTest(t, c, topic+"/availability", "online", true)
	waitUntil(t, func() bool { return boolean(m.list()[0]["online"]) })
	status, data = requestJSON(t, server, "devices/daikin-aabbcc000602/control", "POST", Obj{"action": "indicator.set", "params": Obj{"enabled": false}})
	if status != 200 || !boolean(data["confirmed"]) || writes.Load() != 1 {
		t.Fatal(status, data, writes.Load())
	}
	f.mu.Lock()
	live := clone(f.state)
	f.mu.Unlock()
	obj(live["sensors"])["co2"] = Obj{"valid": true, "value": uint16(2550), "age_ms": uint32(100)}
	obj(live["sensors"])["tvoc"] = Obj{"valid": false, "value": uint16(700), "age_ms": uint32(100)}
	publish(topic+"/state", live, false)
	waitUntil(t, func() bool { return obj(m.list()[0]["state"])["co2"] == 2550.0 })
	if obj(m.list()[0]["state"])["tvoc"] != nil {
		t.Fatal("invalid reading became valid")
	}
	e, _ := m.entry("daikin-aabbcc000602")
	m.mu.RLock()
	wire := e.d["mqttWire"]
	m.mu.RUnlock()
	if wire != "msgpack" {
		t.Fatal("wire format not retained")
	}
	// Publishing a mismatched discovery identity must not alter the device.
	publication["device_id"] = "aabbcc999999"
	raw, _ := msgpack.Marshal(publication)
	m.onPacket("discovery/aabbcc000602", raw, true)
	if len(m.list()) != 1 {
		t.Fatal("mismatched announcement accepted")
	}
}

// Opt-in integration check: reads only; uses a temporary manager database and
// never changes device settings, Wi-Fi, MQTT, indicators or firmware.
func TestDAIKINLiveReadOnly(t *testing.T) {
	address := os.Getenv("PHICOMM_TEST_DAIKIN_URL")
	if address == "" {
		t.Skip("set PHICOMM_TEST_DAIKIN_URL for a read-only real-device check")
	}
	m, a, _ := testManager(t)
	m.timeout = 5 * time.Second
	discoveryFixture(a.discovery, address)
	if _, err := a.discovery.start(Obj{"mode": "mdns"}); err != nil {
		t.Fatal(err)
	}
	a.discovery.wg.Wait()
	if found := a.discovery.snapshot()["devices"].([]Obj); len(found) != 1 || found[0]["model"] != "DAIKIN" {
		t.Fatal("live identity not discovered", found)
	}
	d, err := m.add(context.Background(), Obj{"model": "DAIKIN", "method": "lan", "address": address})
	if err != nil || !boolean(d["online"]) {
		t.Fatal("live read failed", err)
	}
	s := obj(d["state"])
	valid := 0
	for _, key := range []string{"pm25", "pm10", "co2", "temp", "humidity", "tvoc"} {
		if finite(s[key]) != nil {
			valid++
		}
	}
	if valid == 0 {
		t.Fatal("no valid live measurements")
	}
	history, err := m.dataHistory(context.Background(), str(d["id"]), "24h")
	if err != nil || num(history["samples"]) < 1 {
		t.Fatal("live sample was not recorded", err)
	}
	var csv bytes.Buffer
	if err := m.store.writeDataCSV(context.Background(), &csv); err != nil {
		t.Fatal(err)
	}
	readings, _ := json.Marshal(s)
	t.Logf("Read-only live DAIKIN verified; readings=%s; history samples=%v; CSV bytes=%d", readings, history["samples"], csv.Len())
}
