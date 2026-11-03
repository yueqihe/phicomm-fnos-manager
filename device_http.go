package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type deviceHTTPSession struct {
	mu                          sync.Mutex
	initialized, binary, bearer bool
	token                       string
	expires                     time.Time
}

var deviceHTTPSessions = struct {
	sync.Mutex
	values map[[32]byte]*deviceHTTPSession
}{values: map[[32]byte]*deviceHTTPSession{}}

func httpSession(address, password string) *deviceHTTPSession {
	key := sha256.Sum256([]byte(address + "\x00" + password))
	deviceHTTPSessions.Lock()
	defer deviceHTTPSessions.Unlock()
	if s := deviceHTTPSessions.values[key]; s != nil {
		return s
	}
	// Discovery visits arbitrary hosts. Keep the in-memory cache bounded.
	if len(deviceHTTPSessions.values) >= 256 {
		for old := range deviceHTTPSessions.values {
			delete(deviceHTTPSessions.values, old)
			break
		}
	}
	s := &deviceHTTPSession{}
	deviceHTTPSessions.values[key] = s
	return s
}

type deviceHTTPReply struct {
	status  int
	payload Obj
	binary  bool
}

func deviceHTTPRequest(ctx context.Context, client *http.Client, method, url, password, bearer string, value Obj, binary bool) (deviceHTTPReply, error) {
	var raw []byte
	var err error
	if value != nil {
		raw, err = encodeDeviceWire(value, binary)
		if err != nil {
			return deviceHTTPReply{}, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(raw))
	if err != nil {
		return deviceHTTPReply{}, err
	}
	if binary {
		req.Header.Set("Content-Type", "application/msgpack")
		req.Header.Set("Accept", "application/msgpack")
	} else {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	} else if password != "" {
		req.SetBasicAuth("admin", password)
	}
	res, err := client.Do(req)
	if err != nil {
		return deviceHTTPReply{}, failure("设备连接超时或网络中断", 502, "transport_error")
	}
	defer res.Body.Close()
	reply := deviceHTTPReply{status: res.StatusCode}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxDevicePayload+1))
	if err != nil {
		return reply, failure("设备响应中断", 502, "transport_error")
	}
	if len(data) > maxDevicePayload {
		return reply, failure("设备响应过大", 502, "protocol_error")
	}
	reply.payload, reply.binary, err = decodeDeviceWire(data)
	if err != nil {
		return reply, failure("设备返回的不是有效 JSON 或 MessagePack，请确认社区固件版本", 502, "protocol_error")
	}
	return reply, nil
}

func (s *deviceHTTPSession) negotiate(ctx context.Context, client *http.Client, address string) error {
	reply, err := deviceHTTPRequest(ctx, client, "GET", address+"/api/v1/info", "", "", nil, true)
	if reply.status == http.StatusNotAcceptable {
		// Old JSON-only firmware can reject a MessagePack response preference.
		reply, err = deviceHTTPRequest(ctx, client, "GET", address+"/api/v1/info", "", "", nil, false)
	}
	if err != nil && reply.status == 0 {
		return err
	}
	// Older firmware may not implement public info. Its command API stays JSON/Basic.
	if err == nil && reply.status == http.StatusOK && reply.payload["code"] == "ok" {
		info := obj(reply.payload["data"])
		if supportedModel(strings.ToUpper(str(info["model"]))) && macPattern.MatchString(macOf(info["device_id"])) {
			s.binary = reply.binary || info["default_wire_format"] == "msgpack"
			s.bearer = info["auth"] == "bearer"
		}
	}
	s.initialized = true
	return nil
}

func (s *deviceHTTPSession) login(ctx context.Context, client *http.Client, address, password string) error {
	if password == "" {
		return failure("设备需要网页密码，请在连接设置中填写", 422, "auth_failed")
	}
	reply, err := deviceHTTPRequest(ctx, client, "POST", address+"/api/v1/auth/login", "", "", Obj{"username": "admin", "password": password}, s.binary)
	if reply.status == 401 || reply.status == 403 {
		return failure("设备网页密码不正确", 422, "auth_failed")
	}
	if err != nil {
		return err
	}
	if reply.status < 200 || reply.status >= 300 || reply.payload["code"] != "ok" {
		return failure("设备登录失败，请稍后重试", 502, "transport_error")
	}
	data := obj(reply.payload["data"])
	credential := str(data["token"])
	if (credential == "" && data["password_required"] != false) || len(credential) > 256 || strings.ContainsAny(credential, "\r\n ") {
		return failure("设备未返回有效登录凭据", 502, "protocol_error")
	}
	s.token = credential
	lifetime := num(data["expires_in"])
	if lifetime <= 0 || lifetime > 1800 {
		lifetime = 1800
	}
	s.expires = time.Now().Add(time.Duration(lifetime * 0.9 * float64(time.Second)))
	return nil
}

func (s *deviceHTTPSession) command(ctx context.Context, client *http.Client, address, password string, request Obj) (Obj, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		if err := s.negotiate(ctx, client, address); err != nil {
			return nil, err
		}
	}
	if s.bearer && password != "" && !time.Now().Before(s.expires) {
		if err := s.login(ctx, client, address, password); err != nil {
			return nil, err
		}
	}
	// Only explicit rejection before execution (401/415) may negotiate and retry.
	// Timeouts, malformed successful replies and device errors never replay a write.
	for attempt := 0; attempt < 3; attempt++ {
		basic := password
		if s.bearer {
			basic = ""
		}
		reply, err := deviceHTTPRequest(ctx, client, "POST", address+"/api/v1/command", basic, s.token, request, s.binary)
		if reply.status == http.StatusUnsupportedMediaType && !s.binary && reply.binary && err == nil {
			s.binary = true
			continue
		}
		if reply.status == 401 && attempt == 0 {
			s.token = ""
			if err := s.negotiate(ctx, client, address); err != nil {
				return nil, err
			}
			if s.bearer && password != "" {
				if err := s.login(ctx, client, address, password); err != nil {
					return nil, err
				}
				continue
			}
		}
		if reply.status == 401 || reply.status == 403 {
			return nil, failure("设备网页密码不正确", 422, "auth_failed")
		}
		if err != nil {
			return nil, err
		}
		if reply.status < 200 || reply.status >= 300 {
			return nil, failure(fmt.Sprintf("设备返回 HTTP %d", reply.status), 502, "transport_error")
		}
		return result(reply.payload, request)
	}
	return nil, failure("设备通信格式协商失败", 502, "protocol_error")
}
