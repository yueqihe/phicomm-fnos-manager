package main

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestUpgradeFromNodeSQLiteAndAESWithoutNodeRuntime(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile("test/data/legacy-node/manager.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "manager.sqlite"), data, 0600); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("test/data/legacy-node/key.hex")
	if err != nil {
		t.Fatal(err)
	}
	key, err := hex.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "secrets.key"), key, 0600)
	s, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	initialize(s)
	if s.get("admin", nil) != nil {
		t.Fatal("obsolete app password retained")
	}
	m, err := newManager(s, time.Second, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.list()) != 1 || m.list()[0]["name"] != "旧版测试设备" {
		t.Fatal(m.list())
	}
	m.mu.RLock()
	d := clone(m.devices["tc1-aabbcc000098"].d)
	m.mu.RUnlock()
	password, err := s.decrypt(str(d["password"]))
	if err != nil || password != " legacy-device-secret " {
		t.Fatal("legacy AES credential failed", err)
	}
	password, err = s.decrypt(str(m.mqttConfig["password"]))
	if err != nil || password != "legacy-mqtt-secret" {
		t.Fatal("MQTT credential failed", err)
	}
	if !m.ignored["m1-aabbcc000099"] {
		t.Fatal("ignored devices lost")
	}
	near(t, num(obj(m.list()[0]["billing"])["cost"]), 4.32)
	if len(s.history(9007199254740991)) != 1 {
		t.Fatal("history lost")
	}
	d["room"] = "书房"
	d["password"] = s.encrypt("go-written-secret")
	s.save(d)
	m.close()
	s, err = openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	if s.devices()[0]["room"] != "书房" {
		t.Fatal("restart lost configuration")
	}
	password, err = s.decrypt(str(s.devices()[0]["password"]))
	if err != nil || password != "go-written-secret" {
		t.Fatal("Go credential roundtrip failed")
	}
	actual, _ := os.ReadFile(filepath.Join(dir, "secrets.key"))
	if hex.EncodeToString(actual) != hex.EncodeToString(key) {
		t.Fatal("upgrade replaced encryption key")
	}
}
