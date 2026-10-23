package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type AppError struct {
	Message string
	Status  int
	Code    string
}

func (e *AppError) Error() string                           { return e.Message }
func bad(message string) error                              { return &AppError{message, 400, "invalid_params"} }
func failure(message string, status int, code string) error { return &AppError{message, status, code} }
func codeOf(err error) string {
	if e, ok := err.(*AppError); ok {
		return e.Code
	}
	return "internal_error"
}

var macPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)

func macOf(v any) string {
	return strings.ToLower(strings.NewReplacer(":", "", "-", "").Replace(str(v)))
}
func identity(model, mac string) (Obj, error) {
	model = strings.ToUpper(model)
	mac = macOf(mac)
	if (model != "TC1" && model != "M1" && model != "A1") || !macPattern.MatchString(mac) {
		return nil, bad("型号或 MAC 地址无效")
	}
	return Obj{"id": strings.ToLower(model) + "-" + mac, "model": model, "mac": mac}, nil
}
func userKey(user string) string {
	if user == "" {
		return "local"
	}
	return "u_" + hex.EncodeToString([]byte(user))
}
func privateIP(ip net.IP) bool {
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast())
}
func lanURL(value string) (string, error) {
	if !strings.Contains(value, "://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "http" && u.Scheme != "https") {
		return "", bad("设备地址仅支持内网 IP 或主机名，可带端口")
	}
	if u.Port() != "" {
		p, e := strconv.Atoi(u.Port())
		if e != nil || p < 1 || p > 65535 {
			return "", bad("设备端口无效")
		}
	}
	return u.Scheme + "://" + u.Host, nil
}
func cleanText(v any, max int, label string) (string, error) {
	s, ok := v.(string)
	if !ok || utf8.RuneCountInString(s) > max || strings.IndexFunc(s, func(r rune) bool { return r < 32 }) >= 0 {
		return "", bad(label + "格式不正确")
	}
	return strings.TrimSpace(s), nil
}
func cleanSecret(v any, max int) (string, error) {
	s, ok := v.(string)
	if !ok || len(s) > max || strings.IndexFunc(s, func(r rune) bool { return r < 32 }) >= 0 {
		return "", bad("密码格式不正确")
	}
	return s, nil
}
func valueOr(v any, fallback any) any {
	if v == nil || v == "" {
		return fallback
	}
	return v
}
func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
func stringsOf(v any) []string {
	out := []string{}
	for _, x := range arr(v) {
		if s, ok := x.(string); ok && len(out) < 100 {
			out = append(out, s)
		}
	}
	return out
}
func finite(v any) any {
	if n, ok := v.(float64); ok && !math.IsNaN(n) && !math.IsInf(n, 0) {
		return n
	}
	return nil
}
func envelope(action string, params Obj) Obj {
	if params == nil {
		params = Obj{}
	}
	return Obj{"request_id": token(), "action": action, "params": params}
}
func result(payload, request Obj) (Obj, error) {
	if str(payload["request_id"]) != str(request["request_id"]) || str(payload["code"]) == "" {
		return nil, failure("设备响应与请求不匹配", 502, "protocol_error")
	}
	code := str(payload["code"])
	if code != "ok" && code != "accepted" {
		messages := map[string]string{"unsupported": "当前固件不支持此操作", "busy": "设备忙，请稍后再试", "invalid_params": "设备拒绝了操作参数", "storage_error": "设备保存失败", "conflict": "设备请求冲突"}
		message := messages[code]
		if message == "" {
			message = "设备拒绝操作：" + code
		}
		return nil, failure(message, 422, code)
	}
	return payload, nil
}
func httpCommand(parent context.Context, address, password string, request Obj, timeout time.Duration) (Obj, error) {
	address, err := lanURL(address)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(address)
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, u.Hostname())
	if err != nil {
		return nil, failure("设备连接超时或网络中断", 502, "transport_error")
	}
	if len(ips) == 0 {
		return nil, failure("局域网直连仅允许内网地址", 400, "invalid_address")
	}
	for _, ip := range ips {
		if !privateIP(ip.IP) {
			return nil, failure("局域网直连仅允许内网地址", 400, "invalid_address")
		}
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	target := net.JoinHostPort(ips[0].IP.String(), port)
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", target)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", address+"/api/v1/command", bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if password != "" {
		req.SetBasicAuth("admin", password)
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, failure("设备连接超时或网络中断", 502, "transport_error")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, failure("设备网页密码不正确", 422, "auth_failed")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 262145))
	if err != nil {
		return nil, failure("设备响应中断", 502, "transport_error")
	}
	if len(data) > 262144 {
		return nil, failure("设备响应过大", 502, "protocol_error")
	}
	var payload Obj
	if json.Unmarshal(data, &payload) != nil || payload == nil {
		return nil, failure("设备返回的不是有效 JSON，请确认社区固件版本", 502, "protocol_error")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, failure(fmt.Sprintf("设备返回 HTTP %d", res.StatusCode), 502, "transport_error")
	}
	return result(payload, request)
}

type Query func(string, Obj) (Obj, error)

func resolveIdentity(data Obj, query Query) (Obj, error) {
	if !macPattern.MatchString(macOf(data["device_id"])) {
		return nil, failure("设备未返回有效 MAC，无法确认设备身份", 502, "protocol_error")
	}
	model, present := data["model"]
	if !present {
		res, err := query("capabilities.get", nil)
		if err != nil {
			return nil, err
		}
		model = obj(res["data"])["model"]
	}
	m := strings.ToUpper(str(model))
	if m != "TC1" && m != "M1" && m != "A1" {
		return nil, failure("设备未返回受支持的型号，无法确认设备身份", 502, "protocol_error")
	}
	out := clone(data)
	out["model"] = m
	return out, nil
}
func readIdentity(ctx context.Context, address, password string, timeout time.Duration) (Obj, error) {
	query := func(action string, p Obj) (Obj, error) {
		return httpCommand(ctx, address, password, envelope(action, p), timeout)
	}
	for _, action := range []string{"info.get", "status.get"} {
		res, err := query(action, nil)
		if err != nil {
			if action == "info.get" && codeOf(err) == "unsupported" {
				continue
			}
			return nil, err
		}
		return resolveIdentity(obj(res["data"]), query)
	}
	return nil, failure("无法读取设备身份", 422, "unsupported")
}
func verifyIdentity(info, d Obj) error {
	if strings.ToUpper(str(info["model"])) != str(d["model"]) || macOf(info["device_id"]) != str(d["mac"]) {
		return failure("设备型号或 MAC 与保存记录不一致，请检查连接地址", 409, "identity_mismatch")
	}
	return nil
}
func normalize(model string, s, p Obj) Obj {
	out := Obj{"version": str(s["version"]), "childLock": boolean(s["child_lock"])}
	put := func(v Obj) {
		for k, x := range v {
			out[k] = x
		}
	}
	switch model {
	case "TC1":
		states := arr(s["socket_states"])
		outlets := []bool{}
		on := false
		if len(states) == 6 {
			for _, v := range states {
				b := boolean(v) || v == float64(1)
				outlets = append(outlets, b)
				on = on || b
			}
		}
		names := []string{}
		raw := arr(s["socket_names"])
		for i := 0; i < 6; i++ {
			name := ""
			if i < len(raw) {
				name = str(raw[i])
			}
			if name == "" {
				name = fmt.Sprintf("插口 %d", i+1)
			}
			names = append(names, name)
		}
		put(Obj{"outlets": outlets, "outletNames": names, "on": on, "light": boolean(s["led_enabled"]), "power": finite(p["power_watts"]), "energy": finite(p["energy_kwh"])})
	case "M1":
		var brightness any
		if n := finite(s["brightness_requested"]); n != nil {
			brightness = num(n) * 25
		}
		var co2 any
		if boolean(obj(s["scd40"])["valid"]) {
			co2 = finite(obj(s["scd40"])["co2_ppm"])
		}
		put(Obj{"sensorValid": boolean(s["sensor_valid"]), "pm25": finite(s["pm25"]), "hcho": finite(s["formaldehyde"]), "temp": finite(s["temperature"]), "humidity": finite(s["humidity"]), "brightness": brightness, "co2": co2})
	case "A1":
		mode := "manual"
		if s["fan_mode"] == "auto" {
			mode = "auto"
		}
		put(Obj{"on": boolean(s["fan_on"]), "mode": mode, "speed": finite(s["percentage"]), "requestedSpeed": finite(s["speed"]), "autoMaxSpeed": finite(s["auto_max_speed"]), "appliedSpeed": finite(s["applied_speed"]), "fanState": s["fan_state"], "fanReady": boolean(s["fan_ready"]), "light": boolean(s["led_enabled"]), "filter": finite(obj(s["filter"])["remaining_percent"]), "filterDue": boolean(obj(s["filter"])["replacement_due"])})
	}
	return out
}
func validateControl(model, action string, p Obj) error {
	allowed := func(names ...string) bool {
		if len(p) == 0 {
			return false
		}
		for k := range p {
			if !contains(names, k) {
				return false
			}
		}
		return true
	}
	isBool := func(k string) bool { _, ok := p[k].(bool); return ok }
	integer := func(k string, lo, hi float64) bool {
		n, ok := p[k].(float64)
		return ok && n >= lo && n <= hi && math.Trunc(n) == n
	}
	has := func(k string) bool { _, ok := p[k]; return ok }
	ok := false
	switch {
	case model == "TC1" && action == "outlet.set":
		ok = allowed("index", "on") && isBool("on") && (!has("index") || integer("index", 0, 5))
	case model == "M1" && action == "display.set":
		ok = allowed("brightness") && integer("brightness", 0, 100) && int(num(p["brightness"]))%25 == 0
	case model == "A1" && action == "fan.set":
		ok = allowed("on", "speed", "mode") && (!has("on") || isBool("on")) && (!has("speed") || integer("speed", 0, 100)) && (!has("mode") || p["mode"] == "auto" || p["mode"] == "manual")
	case model == "A1" && action == "fan.mode.set":
		ok = allowed("mode") && (p["mode"] == "auto" || p["mode"] == "manual")
	case (model == "TC1" && action == "indicator.set") || (model == "A1" && action == "light.set") || ((model == "TC1" || model == "A1") && action == "child_lock.set"):
		ok = allowed("enabled") && isBool("enabled")
	}
	if !ok {
		return bad("不支持的操作或参数")
	}
	return nil
}
