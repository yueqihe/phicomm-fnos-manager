package main

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type runtimeState struct {
	lanLast, mqttLast  time.Time
	failures           int
	availability, busy bool
	actions            []string
	err, lanErr        string
}
type deviceEntry struct {
	op sync.Mutex
	d  Obj
	r  runtimeState
}
type Manager struct {
	mu                 sync.RWMutex
	configMu           sync.Mutex
	store              *Store
	devices            map[string]*deviceEntry
	order              []string
	ignored            map[string]bool
	tariff, mqttConfig Obj
	mqtt               *MQTT
	ctx                context.Context
	cancel             context.CancelFunc
	wg                 sync.WaitGroup
	timeout, interval  time.Duration
	closed             bool
}

func defaultMQTT() Obj {
	return Obj{"mode": "disabled", "host": "", "port": float64(1883), "username": "phicomm", "password": "", "tls": false}
}
func newManager(s *Store, timeout, interval time.Duration) (*Manager, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{store: s, devices: map[string]*deviceEntry{}, ignored: map[string]bool{}, ctx: ctx, cancel: cancel, timeout: timeout, interval: interval}
	for _, d := range s.devices() {
		id := str(d["id"])
		m.devices[id] = &deviceEntry{d: d, r: runtimeState{failures: 3, actions: []string{}}}
		m.order = append(m.order, id)
	}
	for _, v := range arr(s.get("ignored", []any{})) {
		m.ignored[str(v)] = true
	}
	m.tariff = obj(s.get("tariff", defaultTariff()))
	m.mqttConfig = obj(s.get("mqtt", defaultMQTT()))
	m.mqtt = newMQTT(timeout, m.onPacket, m.mqttOnline)
	if err := m.mqtt.start(m.mqttConfig, s); err != nil {
		log.Print("MQTT 初始化：", err)
	}
	return m, nil
}
func (m *Manager) start() {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.pollAll()
		timer := time.NewTicker(m.interval)
		defer timer.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-timer.C:
				m.pollAll()
			}
		}
	}()
}
func (m *Manager) close() {
	m.mu.Lock()
	m.closed = true
	m.cancel()
	transport := m.mqtt
	m.mu.Unlock()
	transport.stop()
	m.wg.Wait()
	m.store.db.Close()
}
func (m *Manager) refreshAsync(id string) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	e := m.devices[id]
	if e == nil {
		m.mu.Unlock()
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		if e.op.TryLock() {
			defer e.op.Unlock()
			m.read(m.ctx, e)
		}
	}()
}
func (m *Manager) entry(id string) (*deviceEntry, error) {
	m.mu.RLock()
	e := m.devices[id]
	m.mu.RUnlock()
	if e == nil {
		return nil, failure("设备不存在", 404, "not_found")
	}
	return e, nil
}
func (m *Manager) alive(e *deviceEntry, route string) bool {
	now := time.Now()
	if route == "lan" {
		return e.d["method"] != "mqtt" && str(e.d["address"]) != "" && !e.r.lanLast.IsZero() && now.Sub(e.r.lanLast) < 90*time.Second && !(e.r.failures >= 3 && now.Sub(e.r.lanLast) >= 30*time.Second)
	}
	return e.d["method"] != "lan" && m.mqtt.status()["state"] == "connected" && e.r.availability && !e.r.mqttLast.IsZero() && now.Sub(e.r.mqttLast) < 90*time.Second
}
func (m *Manager) public(e *deviceEntry) Obj {
	d := e.d
	lan, mqtt := m.alive(e, "lan"), m.alive(e, "mqtt")
	err := e.r.err
	if err == "" {
		err = e.r.lanErr
	}
	var bill any
	if d["model"] == "TC1" {
		bill = billingSummary(obj(d["billing"]), m.tariff, float64(time.Now().UnixMilli()))
	}
	return clone(Obj{"id": d["id"], "model": d["model"], "mac": d["mac"], "name": d["name"], "room": d["room"], "method": d["method"], "address": str(d["address"]), "passwordSet": str(d["password"]) != "", "online": lan || mqtt, "lan": lan, "mqtt": mqtt, "lastSeen": d["lastSeen"], "state": d["state"], "billing": bill, "actions": e.r.actions, "error": err, "busy": e.r.busy})
}
func (m *Manager) list() []Obj {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := []Obj{}
	for _, id := range m.order {
		if e := m.devices[id]; e != nil {
			out = append(out, m.public(e))
		}
	}
	return out
}
func (m *Manager) settings() Obj {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := clone(m.mqttConfig)
	c["passwordSet"] = str(c["password"]) != ""
	delete(c, "password")
	return Obj{"mqtt": c, "mqttStatus": m.mqtt.status(), "tariff": clone(m.tariff), "pollSeconds": m.interval.Seconds()}
}
func (m *Manager) saveTariff(input Obj) (Obj, error) {
	m.mu.Lock()
	next, err := validateTariff(input, m.tariff)
	if err == nil {
		m.store.set("tariff", next)
		m.tariff = next
		m.store.log(nil, "tariff.configure", "ok", "电价配置已保存")
	}
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return m.settings(), nil
}
func (m *Manager) pollAll() {
	m.mu.RLock()
	entries := []*deviceEntry{}
	for _, id := range m.order {
		if e := m.devices[id]; e != nil {
			entries = append(entries, e)
		}
	}
	m.mu.RUnlock()
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 4)
	for _, e := range entries {
		if m.ctx.Err() != nil {
			break
		}
		semaphore <- struct{}{}
		wg.Add(1)
		go func(e *deviceEntry) {
			defer wg.Done()
			defer func() { <-semaphore }()
			if e.op.TryLock() {
				defer e.op.Unlock()
				m.read(m.ctx, e)
			}
		}(e)
	}
	wg.Wait()
}
func (m *Manager) query(ctx context.Context, d Obj, route, action string, params Obj) (Obj, error) {
	request := envelope(action, params)
	if route == "lan" {
		password, err := m.store.decrypt(str(d["password"]))
		if err != nil {
			return nil, err
		}
		return httpCommand(ctx, str(d["address"]), password, request, m.timeout)
	}
	m.mu.RLock()
	transport := m.mqtt
	m.mu.RUnlock()
	return transport.command(ctx, d, request)
}
func (m *Manager) checkLAN(ctx context.Context, d Obj) error {
	password, err := m.store.decrypt(str(d["password"]))
	if err != nil {
		return err
	}
	info, err := readIdentity(ctx, str(d["address"]), password, m.timeout)
	if err != nil {
		return err
	}
	return verifyIdentity(info, d)
}
func (m *Manager) readRoute(ctx context.Context, e *deviceEntry, route string) error {
	m.mu.RLock()
	d := clone(e.d)
	m.mu.RUnlock()
	if route == "lan" {
		if err := m.checkLAN(ctx, d); err != nil {
			return err
		}
	}
	query := func(action string, p Obj) (Obj, error) { return m.query(ctx, d, route, action, p) }
	res, err := query("status.get", nil)
	if err != nil {
		return err
	}
	info, err := resolveIdentity(obj(res["data"]), query)
	if err != nil {
		return err
	}
	if err = verifyIdentity(info, d); err != nil {
		return err
	}
	caps, err := query("capabilities.get", nil)
	if err != nil {
		return err
	}
	actions := stringsOf(obj(caps["data"])["actions"])
	power := Obj{}
	if d["model"] == "TC1" && contains(actions, "power.get") {
		if reading, e := query("power.get", nil); e == nil {
			power = obj(reading["data"])
		}
	}
	now := time.Now()
	state := normalize(str(d["model"]), info, power)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.devices[str(d["id"])] != e {
		return failure("设备已移除", 404, "not_found")
	}
	e.d["state"] = state
	e.d["lastSeen"] = now.UTC().Format(time.RFC3339Nano)
	if d["model"] == "TC1" {
		var prev Obj
		if e.d["billing"] != nil {
			prev = obj(e.d["billing"])
		}
		bill := recordEnergy(prev, state["energy"], m.tariff, float64(now.UnixMilli()))
		if bill != nil {
			e.d["billing"] = bill
		}
	}
	e.r.actions = actions
	if route == "lan" {
		e.r.lanLast = now
		e.r.failures = 0
		e.r.lanErr = ""
	} else {
		e.r.mqttLast = now
		e.r.availability = true
	}
	e.r.err = ""
	m.store.save(e.d)
	return nil
}
func (m *Manager) read(ctx context.Context, e *deviceEntry) Obj {
	m.mu.RLock()
	d := clone(e.d)
	m.mu.RUnlock()
	var last error
	if d["method"] != "mqtt" && str(d["address"]) != "" {
		last = m.readRoute(ctx, e, "lan")
		if last == nil {
			m.mu.RLock()
			defer m.mu.RUnlock()
			return m.public(e)
		}
		m.mu.Lock()
		e.r.failures++
		e.r.lanErr = last.Error()
		if codeOf(last) == "identity_mismatch" || codeOf(last) == "auth_failed" {
			e.r.lanLast = time.Time{}
		}
		m.mu.Unlock()
	}
	m.mu.RLock()
	tryMQTT := d["method"] != "lan" && m.mqtt.status()["state"] == "connected"
	m.mu.RUnlock()
	if tryMQTT {
		last = m.readRoute(ctx, e, "mqtt")
		if last == nil {
			m.mu.RLock()
			defer m.mu.RUnlock()
			return m.public(e)
		}
		m.mu.Lock()
		e.r.mqttLast = time.Time{}
		m.mu.Unlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e.r.err = "等待设备接入"
	if last != nil {
		e.r.err = last.Error()
	}
	return m.public(e)
}
func (m *Manager) refresh(ctx context.Context, id string) (Obj, error) {
	e, err := m.entry(id)
	if err != nil {
		return nil, err
	}
	e.op.Lock()
	defer e.op.Unlock()
	m.mu.RLock()
	valid := m.devices[id] == e
	m.mu.RUnlock()
	if !valid {
		return nil, failure("设备不存在", 404, "not_found")
	}
	return m.read(ctx, e), nil
}
func (m *Manager) add(ctx context.Context, input Obj) (Obj, error) {
	model := strings.ToUpper(str(input["model"]))
	if !contains([]string{"TC1", "M1", "A1"}, model) {
		return nil, bad("请选择设备型号")
	}
	method := str(valueOr(input["method"], "lan"))
	if !contains([]string{"lan", "mqtt", "hybrid"}, method) {
		return nil, bad("接入方式无效")
	}
	var address, password, mac string
	var err error
	if method != "mqtt" {
		address, err = lanURL(str(input["address"]))
		if err != nil {
			return nil, err
		}
		password, err = cleanSecret(valueOr(input["password"], ""), 32)
		if err != nil {
			return nil, err
		}
		info, err := readIdentity(ctx, address, password, m.timeout)
		if err != nil {
			return nil, err
		}
		if info["model"] != model {
			return nil, failure("设备返回的型号与所选型号不一致", 409, "identity_mismatch")
		}
		mac = macOf(info["device_id"])
	} else {
		mac = macOf(input["mac"])
	}
	key, err := identity(model, mac)
	if err != nil {
		return nil, err
	}
	id := str(key["id"])
	// Reserve one stable entry so concurrent LAN/MQTT registration shares its lock.
	m.mu.Lock()
	e := m.devices[id]
	existing := e != nil
	if e == nil {
		e = &deviceEntry{d: Obj{}, r: runtimeState{failures: 3, actions: []string{}}}
	}
	m.mu.Unlock()
	e.op.Lock()
	defer e.op.Unlock()
	m.mu.Lock()
	if actual := m.devices[id]; actual != nil && actual != e {
		m.mu.Unlock()
		return nil, failure("设备配置正在更新，请稍后重试", 409, "busy")
	}
	old := e.d
	name, err := cleanText(valueOr(input["name"], valueOr(old["name"], model+"-"+mac[8:])), 48, "设备名称")
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	room, err := cleanText(valueOr(input["room"], valueOr(old["room"], "未分组")), 32, "空间")
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	d := clone(old)
	for k, v := range key {
		d[k] = v
	}
	d["name"] = name
	d["room"] = room
	d["method"] = method
	if method == "lan" && old["method"] == "mqtt" {
		d["method"] = "hybrid"
	}
	if address == "" {
		address = str(old["address"])
	}
	d["address"] = address
	d["password"] = str(old["password"])
	if password != "" {
		d["password"] = m.store.encrypt(password)
	}
	e.d = d
	delete(m.ignored, id)
	m.persistIgnored()
	if !existing {
		m.order = append(m.order, id)
	}
	m.devices[id] = e
	m.store.save(d)
	m.store.log(d, "device.add", "ok", "设备已添加或合并连接")
	m.mu.Unlock()
	return m.read(ctx, e), nil
}
func (m *Manager) persistIgnored() {
	ids := []string{}
	for id := range m.ignored {
		ids = append(ids, id)
	}
	m.store.set("ignored", ids)
}
func (m *Manager) update(id string, input Obj) (Obj, error) {
	e, err := m.entry(id)
	if err != nil {
		return nil, err
	}
	e.op.Lock()
	defer e.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.devices[id] != e {
		return nil, failure("设备不存在", 404, "not_found")
	}
	next := clone(e.d)
	for _, field := range []string{"name", "room"} {
		if raw, exists := input[field]; exists {
			max := 48
			if field == "room" {
				max = 32
			}
			value, err := cleanText(raw, max, field)
			if err != nil {
				return nil, err
			}
			if value == "" {
				if field == "name" {
					return nil, bad("设备名称不能为空")
				}
				value = "未分组"
			}
			next[field] = value
		}
	}
	if method, present := input["method"]; present {
		if !contains([]string{"lan", "mqtt", "hybrid"}, str(method)) {
			return nil, bad("接入方式无效")
		}
		next["method"] = method
	}
	if raw, present := input["address"]; present {
		value := str(raw)
		if value != "" {
			value, err = lanURL(value)
			if err != nil {
				return nil, err
			}
		}
		next["address"] = value
	}
	if next["method"] != "mqtt" && str(next["address"]) == "" {
		return nil, bad("请填写设备内网地址")
	}
	if boolean(input["clearPassword"]) {
		next["password"] = ""
	} else if raw := str(input["password"]); raw != "" {
		password, err := cleanSecret(raw, 32)
		if err != nil {
			return nil, err
		}
		next["password"] = m.store.encrypt(password)
	}
	if next["address"] != e.d["address"] || next["method"] != e.d["method"] || next["password"] != e.d["password"] {
		e.r = runtimeState{failures: 3, actions: []string{}}
	}
	e.d = next
	m.store.save(next)
	m.store.log(next, "connection.update", "ok", "连接配置已保存")
	return m.public(e), nil
}
func (m *Manager) remove(id string) error {
	e, err := m.entry(id)
	if err != nil {
		return err
	}
	e.op.Lock()
	defer e.op.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ignored[id] = true
	m.persistIgnored()
	m.store.remove(id)
	delete(m.devices, id)
	for i, v := range m.order {
		if v == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	m.store.log(e.d, "device.remove", "ok", "已从管理端移除")
	return nil
}
func matches(s Obj, action string, p Obj) bool {
	switch action {
	case "outlet.set":
		outlets := arr(s["outlets"])
		if len(outlets) != 6 {
			return false
		}
		if index, exists := p["index"]; exists {
			return outlets[int(num(index))] == p["on"]
		}
		for _, v := range outlets {
			if v != p["on"] {
				return false
			}
		}
		return true
	case "display.set":
		return s["brightness"] == p["brightness"]
	case "fan.mode.set":
		return s["mode"] == p["mode"]
	case "fan.set":
		for k, v := range p {
			field := k
			if k == "speed" {
				field = "requestedSpeed"
				if s["mode"] == "auto" {
					field = "autoMaxSpeed"
				}
			}
			if s[field] != v {
				return false
			}
		}
		return true
	case "light.set", "indicator.set":
		return s["light"] == p["enabled"]
	case "child_lock.set":
		return s["childLock"] == p["enabled"]
	}
	return false
}
func (m *Manager) write(ctx context.Context, e *deviceEntry, action string, p Obj) (Obj, error) {
	m.mu.Lock()
	route := ""
	if m.alive(e, "lan") {
		route = "lan"
	} else if m.alive(e, "mqtt") {
		route = "mqtt"
	}
	d := clone(e.d)
	if route == "" {
		m.mu.Unlock()
		return nil, failure("设备暂时离线，无法控制", 409, "offline")
	}
	if action != "config.set" && !contains(e.r.actions, action) {
		m.mu.Unlock()
		return nil, failure("当前固件未声明支持此操作", 422, "unsupported")
	}
	e.r.busy = true
	m.mu.Unlock()
	defer func() { m.mu.Lock(); e.r.busy = false; m.mu.Unlock() }()
	var err error
	if route == "lan" {
		err = m.checkLAN(ctx, d)
	}
	var response Obj
	if err == nil {
		response, err = m.query(ctx, d, route, action, p)
	}
	if err != nil {
		m.mu.Lock()
		if codeOf(err) == "identity_mismatch" {
			e.r.lanLast = time.Time{}
		}
		outcome := "failed"
		if codeOf(err) == "transport_error" {
			outcome = "unconfirmed"
		}
		m.store.log(d, action, outcome, err.Error())
		m.mu.Unlock()
		return nil, err
	}
	confirmed := false
	for i := 0; i < 2 && !confirmed; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				break
			case <-time.After(350 * time.Millisecond):
			}
		}
		if m.readRoute(ctx, e, route) != nil {
			break
		}
		m.mu.RLock()
		confirmed = matches(obj(e.d["state"]), action, p)
		m.mu.RUnlock()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	outcome, message := "accepted", "设备已接收，状态等待确认"
	if confirmed {
		outcome = "confirmed"
		message = "设备状态已确认"
	}
	m.store.log(e.d, action, outcome, message)
	return Obj{"code": response["code"], "confirmed": confirmed, "device": m.public(e), "message": message}, nil
}
func (m *Manager) control(ctx context.Context, id, action string, p Obj) (Obj, error) {
	e, err := m.entry(id)
	if err != nil {
		return nil, err
	}
	e.op.Lock()
	defer e.op.Unlock()
	m.mu.RLock()
	model := str(e.d["model"])
	valid := m.devices[id] == e
	m.mu.RUnlock()
	if !valid {
		return nil, failure("设备不存在", 404, "not_found")
	}
	if err = validateControl(model, action, p); err != nil {
		return nil, err
	}
	return m.write(ctx, e, action, p)
}
func (m *Manager) provision(ctx context.Context, id string) (Obj, error) {
	e, err := m.entry(id)
	if err != nil {
		return nil, err
	}
	e.op.Lock()
	defer e.op.Unlock()
	m.mu.RLock()
	c := clone(m.mqttConfig)
	model := str(e.d["model"])
	m.mu.RUnlock()
	if c["mode"] == "disabled" || str(c["host"]) == "" || str(c["username"]) == "" {
		return nil, bad("请先在服务设置填写设备可访问的 Broker 地址和账号")
	}
	if boolean(c["tls"]) {
		return nil, bad("设备固件目前使用普通 MQTT TCP，请使用兼容的 Broker 端口")
	}
	password, err := m.store.decrypt(str(c["password"]))
	if err != nil {
		return nil, err
	}
	values := Obj{"host": c["host"], "port": c["port"], "user": c["username"], "password": password}
	if model == "TC1" {
		values["protocol"] = "tmcp"
	}
	response, err := m.write(ctx, e, "config.set", Obj{"section": "mqtt", "values": values})
	if err == nil {
		m.mu.Lock()
		if e.d["method"] == "lan" {
			e.d["method"] = "hybrid"
			m.store.save(e.d)
		}
		m.mu.Unlock()
	}
	return response, err
}

var brokerHost = regexp.MustCompile(`^(?:[a-zA-Z0-9][a-zA-Z0-9.-]*|[a-fA-F0-9:]+)$`)

func (m *Manager) saveMQTT(input Obj) (Obj, error) {
	if !m.configMu.TryLock() {
		return nil, failure("MQTT 配置正在更新", 409, "busy")
	}
	defer m.configMu.Unlock()
	m.mu.RLock()
	next := clone(m.mqttConfig)
	previous := m.mqtt
	m.mu.RUnlock()
	mode := str(input["mode"])
	if !contains([]string{"disabled", "embedded", "external"}, mode) {
		return nil, bad("MQTT 服务模式无效")
	}
	next["mode"] = mode
	host, err := cleanText(valueOr(input["host"], ""), 253, "Broker 地址")
	if err != nil {
		return nil, err
	}
	if host != "" && !brokerHost.MatchString(host) {
		return nil, bad("Broker 地址格式不正确")
	}
	next["host"] = host
	port := num(input["port"])
	if raw, ok := input["port"].(string); ok {
		port, _ = strconv.ParseFloat(raw, 64)
	}
	if port < 1 || port > 65535 || port != float64(int(port)) {
		return nil, bad("端口范围为 1–65535")
	}
	next["port"] = port
	user, err := cleanText(valueOr(input["username"], ""), 63, "接入账号")
	if err != nil || len(user) > 63 {
		return nil, bad("MQTT 账号最多 63 字节")
	}
	next["username"] = user
	next["tls"] = boolean(input["tls"])
	if boolean(input["clearPassword"]) {
		next["password"] = ""
	} else if password := str(input["password"]); password != "" {
		value, err := cleanSecret(password, 256)
		if err != nil {
			return nil, err
		}
		next["password"] = m.store.encrypt(value)
	}
	if mode == "embedded" && (user == "" || str(next["password"]) == "") {
		return nil, bad("内置 MQTT 服务需要设置接入账号和密码")
	}
	if mode == "external" && host == "" {
		return nil, bad("请填写外部 Broker 地址")
	}
	if mode == "embedded" && boolean(next["tls"]) {
		return nil, bad("内置服务使用 MQTT TCP，请关闭 TLS")
	}
	if reflect.DeepEqual(next, m.mqttConfig) {
		return m.settings(), nil
	}
	m.mqttOnline(false)
	previous.stop()
	transport := newMQTT(m.timeout, m.onPacket, m.mqttOnline)
	if err := transport.start(next, m.store); err != nil {
		m.mu.RLock()
		old := clone(m.mqttConfig)
		m.mu.RUnlock()
		restored := newMQTT(m.timeout, m.onPacket, m.mqttOnline)
		if restoreErr := restored.start(old, m.store); restoreErr != nil {
			log.Print("恢复 MQTT 服务：", restoreErr)
		}
		m.mu.Lock()
		m.mqtt = restored
		m.mu.Unlock()
		return nil, err
	}
	m.mu.Lock()
	m.mqtt = transport
	m.mqttConfig = next
	m.store.set("mqtt", next)
	m.store.log(nil, "mqtt.configure", "ok", "MQTT 服务配置已保存")
	m.mu.Unlock()
	return m.settings(), nil
}
func (m *Manager) mqttOnline(online bool) {
	m.mu.Lock()
	for _, e := range m.devices {
		e.r.mqttLast = time.Time{}
		e.r.availability = false
	}
	m.mu.Unlock()
}
func (m *Manager) onPacket(tail string, payload []byte, retained bool) {
	parts := strings.Split(tail, "/")
	var p Obj
	if len(parts) == 2 && parts[0] == "discovery" {
		if jsonDecode(payload, &p) != nil || num(p["schema"]) != 1 || p["protocol"] != "tmcp" || num(p["protocol_version"]) != 2 {
			return
		}
		key, err := identity(str(p["model"]), parts[1])
		if err != nil {
			return
		}
		id := str(key["id"])
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.ignored[id] || m.devices[id] != nil {
			return
		}
		key["name"] = fmt.Sprintf("%s-%s", key["model"], str(key["mac"])[8:])
		key["room"] = "未分组"
		key["method"] = "mqtt"
		key["address"] = ""
		key["password"] = ""
		m.devices[id] = &deviceEntry{d: key, r: runtimeState{failures: 3, actions: []string{}}}
		m.order = append(m.order, id)
		m.store.save(key)
		m.store.log(key, "mqtt.discovery", "ok", "MQTT 自动发现设备")
		return
	}
	if len(parts) != 3 {
		return
	}
	key, err := identity(parts[0], parts[1])
	if err != nil {
		return
	}
	id := str(key["id"])
	m.mu.Lock()
	defer m.mu.Unlock()
	e := m.devices[id]
	if e == nil || e.d["method"] == "lan" {
		return
	}
	switch parts[2] {
	case "availability":
		e.r.availability = string(payload) == "online"
		e.r.mqttLast = time.Time{}
		if e.r.availability {
			go m.refreshAsync(id)
		}
	case "capabilities":
		if jsonDecode(payload, &p) == nil {
			e.r.actions = stringsOf(p["actions"])
		}
	case "state":
		if retained || !e.r.availability || jsonDecode(payload, &p) != nil || verifyIdentity(p, e.d) != nil {
			return
		}
		power := Obj{}
		if e.d["model"] == "TC1" {
			s := obj(e.d["state"])
			power = Obj{"power_watts": s["power"], "energy_kwh": s["energy"]}
		}
		e.d["state"] = normalize(str(e.d["model"]), p, power)
		e.d["lastSeen"] = time.Now().UTC().Format(time.RFC3339Nano)
		e.r.mqttLast = time.Now()
		m.store.save(e.d)
	}
}
