package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/miekg/dns"
	"github.com/vmihailenco/msgpack/v5"
)

func TestS30SingleOutletAndIndependentMeterValidity(t *testing.T) {
	f := firmwareFixture(t, "S30-V1B", "aabbcc000701")
	meter := obj(f.state["metering"])
	meter["active_power"], meter["current"] = 0.0, 0.0
	for _, fields := range []float64{7, 1, 4, 0} {
		meter["valid_fields"] = fields
		s := normalize("S30-V1B", f.state, nil, nil)
		if (s["voltage"] != nil) != (int(fields)&1 != 0) || (s["current"] != nil) != (int(fields)&2 != 0) || (s["power"] != nil) != (int(fields)&4 != 0) || s["energy"] != 1.23 || len(arr(s["outlets"])) != 1 {
			t.Fatal("independent metering validity or outlet count", fields, s)
		}
	}
	meter["valid_fields"] = nil
	if s := normalize("S30-V1B", f.state, nil, nil); s["power"] != nil || s["voltage"] != nil {
		t.Fatal("invalid mask accepted", s)
	}
	for _, index := range []float64{-1, 1, 5, 0.5} {
		if validateControl("S30-V1B", "outlet.set", Obj{"index": index, "on": true}) == nil || validateControl("S30-V1B", "config.set", Obj{"section": "socket", "values": Obj{"index": index, "name": "插座"}}) == nil {
			t.Fatal("nonexistent outlet accepted", index)
		}
	}
	if matches(Obj{"outlets": []bool{true}}, "outlet.set", Obj{"index": 1.0, "on": true}) {
		t.Fatal("out of bounds confirmation")
	}
}

func TestS30LANControlBillingHistoryAndCSV(t *testing.T) {
	m, a, server := testManager(t)
	f := firmwareFixture(t, "S30-V1B", "aabbcc000702")
	discoveryFixture(a.discovery, f.server.URL)
	a.discovery.start(Obj{"mode": "mdns"})
	a.discovery.wg.Wait()
	found := a.discovery.snapshot()["devices"].([]Obj)
	if len(found) != 1 || found[0]["model"] != "S30-V1B" {
		t.Fatal("discovery failed", found)
	}
	_, err := m.saveTariff(Obj{"mode": "flat", "flatRate": 0.6})
	if err != nil {
		t.Fatal(err)
	}
	d, err := m.add(context.Background(), Obj{"model": "S30-V1B", "address": f.server.URL, "method": "hybrid", "room": "书房"})
	if err != nil || !boolean(d["online"]) || obj(d["state"])["voltage"] != 230.5 || d["chartMetric"] != "power" {
		t.Fatal(d, err)
	}
	id := str(d["id"])
	for _, command := range []struct {
		action string
		params Obj
	}{
		{"outlet.set", Obj{"on": true}}, {"outlet.set", Obj{"index": 0.0, "on": false}},
		{"indicator.set", Obj{"enabled": false}}, {"child_lock.set", Obj{"enabled": true}},
		{"config.set", Obj{"section": "socket", "values": Obj{"index": 0.0, "name": "咖啡机"}}},
	} {
		status, result := requestJSON(t, server, "devices/"+id+"/control", "POST", Obj{"action": command.action, "params": command.params})
		if status != 200 || !boolean(result["confirmed"]) {
			t.Fatal(command, status, result)
		}
	}
	f.mu.Lock()
	f.state["socket_names"] = []string{"设备网页改名"}
	obj(f.state["metering"])["energy_kwh"] = 2.23
	f.mu.Unlock()
	d, _ = m.refresh(context.Background(), id)
	if arr(obj(d["state"])["outletNames"])[0] != "设备网页改名" {
		t.Fatal("name did not synchronize")
	}
	near(t, num(obj(d["billing"])["cost"]), 0.6)
	now := time.Now().Truncate(time.Minute).UnixMilli()
	// Exercise interval energy/cost differences and raw electrical measurements.
	e, _ := m.entry(id)
	m.mu.RLock()
	saved := clone(e.d)
	m.mu.RUnlock()
	obj(saved["state"])["energy"] = 1.23
	obj(saved["billing"])["cost"] = 0.0
	m.store.recordSample(saved, now-2*sampleInterval)
	obj(saved["state"])["energy"] = 2.23
	obj(saved["billing"])["cost"] = 0.6
	m.store.recordSample(saved, now-sampleInterval)
	h, err := m.dataHistory(context.Background(), id, "24h")
	if err != nil || len(h["metrics"].([]dataMetric)) != 5 {
		t.Fatal(h, err)
	}
	summary := obj(h["summary"])
	near(t, num(obj(summary["energy"])["total"]), 1)
	near(t, num(obj(summary["cost"])["total"]), 0.6)
	near(t, num(obj(summary["voltage"])["avg"]), 230.5)
	f.mu.Lock()
	f.password = "fixture-password"
	f.mu.Unlock()
	if _, err := readIdentity(context.Background(), f.server.URL, "bad-password", time.Second); codeOf(err) != "auth_failed" {
		t.Fatal(err)
	}
	if _, err := readIdentity(context.Background(), f.server.URL, "fixture-password", time.Second); err != nil {
		t.Fatal("Basic authentication failed", err)
	}
	// A removed hyphenated model must still be identified correctly in exports.
	m.store.remove(id)
	var output bytes.Buffer
	if err := m.store.writeDataCSV(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(output.String(), "\ufeff"))).ReadAll()
	if err != nil || rows[1][2] != "S30-V1B" || rows[1][3] != "aabbcc000702" || rows[0][18] != "电压（V）" || rows[1][18] != "230.5" || rows[1][19] != "0.125" {
		t.Fatal(rows, err)
	}
}

func TestS30MQTTOpenIoTDiscoveryAndState(t *testing.T) {
	m, _, server := testManager(t)
	port := freePort(t)
	status, data := requestJSON(t, server, "settings/mqtt", "PUT", Obj{"mode": "embedded", "host": "192.168.1.10", "port": float64(port), "username": "s30-tests", "password": "fixture-secret"})
	if status != 200 {
		t.Fatal(data)
	}
	waitUntil(t, func() bool { return obj(m.settings()["mqttStatus"])["state"] == "connected" })
	f := firmwareFixture(t, "S30-V1B", "aabbcc000703")
	base := "phicomm/v2/" + userKey("s30-tests")
	topic := base + "/s30-v1b/aabbcc000703"
	c := connectTest(t, port, "s30-tests", "fixture-secret", "s30-fixture")
	sub := c.Subscribe(topic+"/command", 1, func(_ paho.Client, msg paho.Message) {
		var req Obj
		if msgpack.Unmarshal(msg.Payload(), &req) != nil {
			return
		}
		raw, _ := json.Marshal(req)
		json.Unmarshal(raw, &req)
		f.mu.Lock()
		raw, _ = msgpack.Marshal(f.command(req))
		f.mu.Unlock()
		c.Publish(topic+"/result", 1, false, raw)
	})
	if !sub.WaitTimeout(time.Second) || sub.Error() != nil {
		t.Fatal("subscribe failed")
	}
	publication := Obj{"schema": 2, "protocol": "openiot", "command_protocol": "tmcp", "protocol_version": 2, "model": "s30-v1b", "device_id": "aabbcc000703", "wire_formats": []string{"msgpack"}, "default_wire_format": "msgpack"}
	raw, _ := msgpack.Marshal(publication)
	pub := c.Publish(base+"/discovery/aabbcc000703", 1, true, raw)
	pub.WaitTimeout(time.Second)
	waitUntil(t, func() bool { return len(m.list()) == 1 })
	publishTest(t, c, topic+"/availability", "online", true)
	waitUntil(t, func() bool { return boolean(m.list()[0]["online"]) })
	id := "s30-v1b-aabbcc000703"
	boot, err := m.control(context.Background(), id, "config.set", Obj{"section": "device", "values": Obj{"power_on_mode": "restore"}})
	if err != nil || !boolean(boot["confirmed"]) || obj(obj(boot["device"])["state"])["powerOnMode"] != "restore" {
		t.Fatal("MQTT power policy was not confirmed", boot, err)
	}
	result, err := m.control(context.Background(), id, "outlet.set", Obj{"index": 0.0, "on": true})
	if err != nil || !boolean(result["confirmed"]) {
		t.Fatal(result, err)
	}
	f.mu.Lock()
	live := clone(f.state)
	f.mu.Unlock()
	obj(live["metering"])["active_power"] = 0.0
	obj(live["metering"])["voltage"] = 225.0
	obj(live["metering"])["valid_fields"] = 5.0
	raw, _ = msgpack.Marshal(live)
	m.onPacket("s30-v1b/aabbcc000703/state", raw, true)
	if obj(m.list()[0]["state"])["voltage"] == 225.0 {
		t.Fatal("retained telemetry accepted as fresh")
	}
	m.onPacket("s30-v1b/aabbcc000703/state", raw, false)
	s := obj(m.list()[0]["state"])
	if s["voltage"] != 225.0 || s["power"] != 0.0 || s["current"] != nil || s["powerOnMode"] != "restore" || len(stringsOf(s["powerOnModes"])) != 3 {
		t.Fatal(s)
	}
	d, err := m.add(context.Background(), Obj{"model": "S30-V1B", "address": f.server.URL, "method": "hybrid"})
	if err != nil || len(m.list()) != 1 || d["id"] != id || !boolean(d["lan"]) || !boolean(d["mqtt"]) {
		t.Fatal("identity merge failed", d, err)
	}
}

func TestOpenIoTMDNSUsesHTTPPort(t *testing.T) {
	cache := newMDNSCache("192.168.13.2/24")
	msg := dns.Msg{MsgHdr: dns.MsgHdr{Response: true}, Answer: []dns.RR{
		&dns.SRV{Hdr: dns.RR_Header{Name: "iotcloud-s30._openiot._udp.local.", Rrtype: dns.TypeSRV, Class: 1, Ttl: 30}, Target: "iotcloud-s30.local.", Port: 11288},
		&dns.A{Hdr: dns.RR_Header{Name: "iotcloud-s30.local.", Rrtype: dns.TypeA, Class: 1, Ttl: 30}, A: net.ParseIP("192.168.13.18")},
	}}
	raw, _ := msg.Pack()
	cache.ingest(raw, net.ParseIP("192.168.13.18"))
	if got := cache.addresses(); len(got) != 1 || got[0] != "http://192.168.13.18" {
		t.Fatal(got)
	}
}

// Explicitly opt in to reading a real device. All persistence is temporary.
func TestS30LiveReadOnly(t *testing.T) {
	address := os.Getenv("PHICOMM_TEST_S30_URL")
	if address == "" {
		t.Skip("set PHICOMM_TEST_S30_URL for live read-only verification")
	}
	m, a, _ := testManager(t)
	m.timeout = 5 * time.Second
	if cidr := os.Getenv("PHICOMM_TEST_S30_CIDR"); cidr != "" {
		if _, err := a.discovery.start(Obj{"mode": "lan", "cidr": cidr}); err != nil {
			t.Fatal(err)
		}
		a.discovery.wg.Wait()
		found := false
		for _, d := range a.discovery.snapshot()["devices"].([]Obj) {
			if d["model"] == "S30-V1B" && d["address"] == address {
				found = true
			}
		}
		if !found {
			t.Fatal("S30 missing from live LAN scan")
		}
	}
	d, err := m.add(context.Background(), Obj{"model": "S30-V1B", "method": "lan", "address": address})
	if err != nil || !boolean(d["online"]) || len(arr(obj(d["state"])["outlets"])) != 1 {
		t.Fatal(d, err)
	}
	h, err := m.dataHistory(context.Background(), str(d["id"]), "24h")
	if err != nil || num(h["samples"]) < 1 {
		t.Fatal(h, err)
	}
	var csv bytes.Buffer
	if err := m.store.writeDataCSV(context.Background(), &csv); err != nil {
		t.Fatal(err)
	}
	state, _ := json.Marshal(d["state"])
	t.Logf("S30 read-only verified: state=%s; history=%v; CSV bytes=%d", state, h["samples"], csv.Len())
}
