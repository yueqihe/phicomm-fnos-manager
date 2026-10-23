package main

import (
	"encoding/json"
	"fmt"
	paho "github.com/eclipse/paho.mqtt.golang"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func waitUntil(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("condition timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}
func publishTest(t *testing.T, c paho.Client, topic string, payload any, retained bool) {
	t.Helper()
	var data []byte
	if s, ok := payload.(string); ok {
		data = []byte(s)
	} else {
		data, _ = json.Marshal(payload)
	}
	token := c.Publish(topic, 1, retained, data)
	if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		t.Fatalf("publish failed %v", token.Error())
	}
}
func connectTest(t *testing.T, port int, user, password, id string) paho.Client {
	t.Helper()
	options := paho.NewClientOptions().AddBroker(fmt.Sprintf("tcp://127.0.0.1:%d", port)).SetClientID(id).SetUsername(user).SetPassword(password).SetAutoReconnect(false).SetConnectTimeout(time.Second)
	c := paho.NewClient(options)
	token := c.Connect()
	if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		t.Fatalf("MQTT connect: %v", token.Error())
	}
	t.Cleanup(func() { c.Disconnect(0) })
	return c
}
func TestMQTTEmbeddedRESTDiscoveryControlAndACL(t *testing.T) {
	m, _, server := testManager(t)
	port := freePort(t)
	status, data := requestJSON(t, server, "settings/mqtt", "PUT", Obj{"mode": "embedded", "host": "192.168.1.10", "port": float64(port), "username": "fixtures", "password": "fixture-secret"})
	if status != 200 {
		t.Fatal(status, data)
	}
	waitUntil(t, func() bool { return obj(m.settings()["mqttStatus"])["state"] == "connected" })
	mac := "aabbcc000090"
	base := "phicomm/v2/" + userKey("fixtures") + "/tc1/" + mac
	c := connectTest(t, port, "fixtures", "fixture-secret", "device-fixture")
	state := Obj{"model": "tc1", "device_id": mac, "socket_states": []any{float64(0), float64(0), float64(0), float64(0), float64(0), float64(0)}}
	var mu sync.Mutex
	writes := 0
	badRetain := false
	token := c.Subscribe(base+"/command", 1, func(_ paho.Client, msg paho.Message) {
		mu.Lock()
		defer mu.Unlock()
		var request Obj
		json.Unmarshal(msg.Payload(), &request)
		action := str(request["action"])
		code := "ok"
		var resultData any
		switch action {
		case "status.get":
			resultData = state
		case "capabilities.get":
			resultData = Obj{"model": "tc1", "actions": []string{"outlet.set", "power.get"}}
		case "power.get":
			resultData = Obj{"power_watts": 20.0, "energy_kwh": 1.23}
		case "outlet.set":
			writes++
			badRetain = badRetain || msg.Retained()
			p := obj(request["params"])
			arr(state["socket_states"])[int(num(p["index"]))] = float64(1)
			code = "accepted"
		}
		raw, _ := json.Marshal(Obj{"request_id": request["request_id"], "code": code, "data": resultData})
		c.Publish(base+"/result", 1, false, raw)
	})
	if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		t.Fatal("subscribe failed")
	}
	publishTest(t, c, "phicomm/v2/"+userKey("fixtures")+"/discovery/"+mac, Obj{"schema": 1.0, "model": "tc1", "protocol": "tmcp", "protocol_version": 2.0}, true)
	publishTest(t, c, base+"/state", state, true)
	waitUntil(t, func() bool { return len(m.list()) == 1 })
	if boolean(m.list()[0]["online"]) {
		t.Fatal("retained state alone marked online")
	}
	publishTest(t, c, base+"/availability", "online", true)
	waitUntil(t, func() bool { return boolean(m.list()[0]["online"]) })
	status, data = requestJSON(t, server, "devices/tc1-"+mac+"/control", "POST", Obj{"action": "outlet.set", "params": Obj{"index": 0.0, "on": true}})
	if status != 200 || !boolean(data["confirmed"]) {
		t.Fatal(status, data)
	}
	mu.Lock()
	if writes != 1 || badRetain {
		t.Fatal("MQTT write repeated or retained")
	}
	mu.Unlock()
	requestJSON(t, server, "settings/tariff", "PUT", Obj{"mode": "flat", "flatRate": 0.6})
	requestJSON(t, server, "devices/tc1-"+mac+"/refresh", "POST", Obj{})
	near(t, num(obj(m.list()[0]["billing"])["cost"]), 0)
	denied := paho.NewClient(paho.NewClientOptions().AddBroker(fmt.Sprintf("tcp://127.0.0.1:%d", port)).SetClientID("wrong-auth").SetUsername("fixtures").SetPassword("wrong").SetConnectTimeout(time.Second))
	token = denied.Connect()
	if !token.WaitTimeout(2*time.Second) || token.Error() == nil {
		t.Fatal("bad credentials accepted")
	}
	denied.Disconnect(0)
	command := envelope("outlet.set", Obj{"index": 0.0, "on": true})
	raw, _ := json.Marshal(command)
	c.Publish(base+"/command", 0, false, raw).WaitTimeout(time.Second)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if writes != 1 {
		t.Fatal("device could publish management command")
	}
	mu.Unlock()
	blocked := c.Subscribe("phicomm/v2/other/#", 1, nil)
	if !blocked.WaitTimeout(time.Second) || blocked.(*paho.SubscribeToken).Result()["phicomm/v2/other/#"] != 128 {
		t.Fatal("cross-user subscription accepted")
	}
	status, data = requestJSON(t, server, "devices/tc1-"+mac, "DELETE", nil)
	if status != 200 {
		t.Fatal(status, data)
	}
	publishTest(t, c, "phicomm/v2/"+userKey("fixtures")+"/discovery/"+mac, Obj{"schema": 1.0, "model": "tc1", "protocol": "tmcp", "protocol_version": 2.0}, true)
	time.Sleep(30 * time.Millisecond)
	if len(m.list()) != 0 {
		t.Fatal("removed device rediscovered")
	}
	c.Disconnect(0)
	occupied, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	busy := occupied.Addr().(*net.TCPAddr).Port
	status, data = requestJSON(t, server, "settings/mqtt", "PUT", Obj{"mode": "embedded", "host": "192.168.1.10", "port": float64(busy), "username": "fixtures"})
	if status != 422 {
		t.Fatal(status, data)
	}
	waitUntil(t, func() bool { return obj(m.settings()["mqttStatus"])["state"] == "connected" })
	if num(obj(m.settings()["mqtt"])["port"]) != float64(port) {
		t.Fatal("failed config replaced working port")
	}
	other, _, otherServer := testManager(t)
	status, data = requestJSON(t, otherServer, "settings/mqtt", "PUT", Obj{"mode": "external", "host": "127.0.0.1", "port": float64(port), "username": "fixtures", "password": "fixture-secret"})
	if status != 200 {
		t.Fatal(status, data)
	}
	waitUntil(t, func() bool { return obj(other.settings()["mqttStatus"])["state"] == "connected" })
	if strings.Contains(fmt.Sprint(other.settings()), "fixture-secret") {
		t.Fatal("MQTT secret exposed")
	}
}
