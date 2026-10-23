package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const version = "0.2.3"

//go:embed public/index.html public/app.js public/styles.css
var frontend embed.FS

type localSocketKey struct{}

func gatewayConnectionContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, localSocketKey{}, c.RemoteAddr().Network() == "unix")
}

type App struct {
	manager   *Manager
	discovery *Discovery
	base      string
	dev       bool
}

func (a *App) authenticated(r *http.Request) bool {
	if a.dev {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		return net.ParseIP(host).IsLoopback()
	}
	local, _ := r.Context().Value(localSocketKey{}).(bool)
	return local && strings.TrimSpace(r.Header.Get("X-Trim-Userid")) != "" && r.Header.Get("X-Trim-Isadmin") == "true"
}
func jsonBody(r *http.Request) (Obj, error) {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		return nil, failure("请求必须使用 JSON", 415, "invalid_params")
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 16385))
	if err != nil {
		return nil, bad("请求内容无效")
	}
	if len(raw) > 16384 {
		return nil, failure("请求过大", 413, "invalid_params")
	}
	var input Obj
	if jsonDecode(raw, &input) != nil || input == nil {
		return nil, bad("JSON 格式不正确")
	}
	return input, nil
}
func sendJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	raw, err := marshalJSON(data)
	if err == nil {
		w.Write(raw)
	}
}
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'self'; base-uri 'self'; form-action 'self'")
	defer func() {
		if value := recover(); value != nil {
			log.Print("请求处理异常：", value)
			sendJSON(w, 500, Obj{"error": "服务处理失败，请查看服务日志", "code": "internal_error"})
		}
	}()
	status, data, err := a.handle(w, r)
	if status == 0 && err == nil {
		return
	}
	if err != nil {
		status = 500
		code := "internal_error"
		message := "服务处理失败，请查看服务日志"
		if e, ok := err.(*AppError); ok {
			status = e.Status
			code = e.Code
			message = e.Message
		} else {
			log.Print("请求处理异常：", err)
		}
		data = Obj{"error": message, "code": code}
	}
	sendJSON(w, status, data)
}
func (a *App) handle(w http.ResponseWriter, r *http.Request) (int, any, error) {
	path := r.URL.Path
	if a.base != "" {
		if path == a.base && r.Method == "GET" {
			http.Redirect(w, r, a.base+"/", 302)
			return 0, nil, nil
		}
		if !strings.HasPrefix(path, a.base+"/") {
			return 0, nil, failure("页面不存在", 404, "not_found")
		}
		path = strings.TrimPrefix(path, a.base)
	}
	if !strings.HasPrefix(path, "/api/") {
		files := map[string][2]string{"/": {"public/index.html", "text/html; charset=utf-8"}, "/app.js": {"public/app.js", "text/javascript; charset=utf-8"}, "/styles.css": {"public/styles.css", "text/css; charset=utf-8"}}
		file, ok := files[path]
		if !ok || (r.Method != "GET" && r.Method != "HEAD") {
			return 0, nil, failure("页面不存在", 404, "not_found")
		}
		raw, err := frontend.ReadFile(file[0])
		if err != nil {
			return 0, nil, err
		}
		w.Header().Set("Content-Type", file[1])
		w.Header().Set("Cache-Control", "no-cache")
		if r.Method != "HEAD" {
			w.Write(raw)
		}
		return 0, nil, nil
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			return 0, nil, failure("请求来源不正确", 403, "invalid_params")
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			local, _ := r.Context().Value(localSocketKey{}).(bool)
			forwarded := ""
			if local {
				forwarded = strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0])
			}
			if err != nil || u.Host == "" || (u.Host != r.Host && (forwarded == "" || u.Host != forwarded)) {
				return 0, nil, failure("请求来源不正确", 403, "invalid_params")
			}
		}
	}
	if path == "/api/session" {
		if r.Method == "GET" {
			return 200, Obj{"authenticated": a.authenticated(r), "authMode": "fnos"}, nil
		}
		return 0, nil, failure("请使用飞牛 NAS 管理员账号访问应用", 403, "fnos_auth_required")
	}
	if !a.authenticated(r) {
		return 0, nil, failure("请使用飞牛 NAS 管理员账号重新打开应用", 401, "fnos_auth_required")
	}
	var input Obj
	if r.Method != "GET" && r.Method != "HEAD" && r.Method != "DELETE" {
		var err error
		input, err = jsonBody(r)
		if err != nil {
			return 0, nil, err
		}
	}
	switch path {
	case "/api/devices":
		if r.Method == "GET" {
			return 200, Obj{"devices": a.manager.list(), "service": a.manager.settings()["mqttStatus"]}, nil
		}
		if r.Method == "POST" {
			device, err := a.manager.add(r.Context(), input)
			return 201, Obj{"device": device}, err
		}
	case "/api/settings":
		if r.Method == "GET" {
			return 200, a.manager.settings(), nil
		}
	case "/api/settings/tariff":
		if r.Method == "PUT" {
			data, err := a.manager.saveTariff(input)
			return 200, data, err
		}
	case "/api/settings/mqtt":
		if r.Method == "PUT" {
			data, err := a.manager.saveMQTT(input)
			return 200, data, err
		}
	case "/api/settings/password":
		return 0, nil, failure("应用使用飞牛账号认证，无需管理密码", 403, "fnos_auth_required")
	case "/api/history":
		if r.Method == "GET" {
			before := int64(9007199254740991)
			if value := r.URL.Query().Get("before"); value != "" {
				var err error
				before, err = strconv.ParseInt(value, 10, 64)
				if err != nil || before < 1 {
					return 0, nil, bad("分页参数无效")
				}
			}
			records := a.manager.store.history(before)
			var next any
			if len(records) == 100 {
				next = records[len(records)-1]["id"]
			}
			return 200, Obj{"records": records, "next": next}, nil
		}
	case "/api/discovery":
		switch r.Method {
		case "GET":
			return 200, a.discovery.snapshot(), nil
		case "POST":
			data, err := a.discovery.start(input)
			return 202, data, err
		case "DELETE":
			return 200, a.discovery.cancel(), nil
		}
	}
	if strings.HasPrefix(path, "/api/devices/") {
		parts := strings.Split(strings.TrimPrefix(path, "/api/devices/"), "/")
		id := parts[0]
		operation := ""
		if len(parts) == 2 {
			operation = parts[1]
		}
		if len(parts) > 2 {
			return 0, nil, failure("接口不存在", 404, "not_found")
		}
		if operation == "" {
			if r.Method == "PUT" {
				device, err := a.manager.update(id, input)
				if err == nil {
					a.manager.refreshAsync(id)
				}
				return 200, Obj{"device": device}, err
			}
			if r.Method == "DELETE" {
				err := a.manager.remove(id)
				return 200, Obj{"ok": true}, err
			}
		}
		if r.Method == "POST" {
			switch operation {
			case "refresh":
				device, err := a.manager.refresh(r.Context(), id)
				return 200, Obj{"device": device}, err
			case "control":
				params, ok := input["params"].(map[string]any)
				if !ok {
					return 0, nil, bad("控制参数无效")
				}
				data, err := a.manager.control(r.Context(), id, str(input["action"]), params)
				return 200, data, err
			case "mqtt-config":
				data, err := a.manager.provision(r.Context(), id)
				return 200, data, err
			}
		}
	}
	return 0, nil, failure("接口不存在", 404, "not_found")
}
func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
func socketReady(path string) bool {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 800 * time.Millisecond}
	res, err := client.Get("http://localhost/app/phicomm-manager/api/session")
	if err != nil {
		return false
	}
	defer res.Body.Close()
	return res.StatusCode == 200
}
func abandonedSocket(path string) bool {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return false
	}
	conn, err := net.DialTimeout("unix", path, 800*time.Millisecond)
	if err == nil {
		conn.Close()
		return false
	}
	return errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, os.ErrNotExist)
}
func initialize(s *Store) {
	_, err := s.db.Exec("DELETE FROM settings WHERE key='admin'")
	must(err)
	s.set("fnosGatewayAuth", true)
	s.set("nativeGoVersion", version)
}

// Wait for HTTP handlers before the caller closes the manager's database.
type drainingHandler struct {
	handler http.Handler
	mu      sync.Mutex
	closing bool
	active  sync.WaitGroup
}

func (h *drainingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.closing {
		h.mu.Unlock()
		http.Error(w, "服务正在停止", http.StatusServiceUnavailable)
		return
	}
	h.active.Add(1)
	h.mu.Unlock()
	defer h.active.Done()
	h.handler.ServeHTTP(w, r)
}

func (h *drainingHandler) drain() {
	h.mu.Lock()
	h.closing = true
	h.mu.Unlock()
	h.active.Wait()
}

func serveUntilStopped(ctx context.Context, server *http.Server, listener net.Listener, grace time.Duration) error {
	requests, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	handler := &drainingHandler{handler: server.Handler}
	server.Handler = handler
	server.BaseContext = func(net.Listener) context.Context { return requests }
	serveDone := make(chan struct{})
	shutdownDone := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
		case <-serveDone:
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		err := server.Shutdown(shutdownCtx)
		if err != nil {
			cancelRequests()
			server.Close()
		}
		handler.drain()
		shutdownDone <- err
	}()
	err := server.Serve(listener)
	close(serveDone)
	shutdownErr := <-shutdownDone
	if errors.Is(err, http.ErrServerClosed) {
		return shutdownErr
	}
	return errors.Join(err, shutdownErr)
}

func run() error {
	dataDir := flag.String("data", env("MANAGER_DATA_DIR", "data"), "data directory")
	socket := flag.String("socket", env("MANAGER_SOCKET", ""), "fnOS gateway socket")
	base := flag.String("base", env("MANAGER_BASE_PATH", "/app/phicomm-manager"), "gateway prefix")
	listen := flag.String("listen", "127.0.0.1:8800", "development listen address")
	dev := flag.Bool("dev", false, "enable loopback-only development access")
	initOnly := flag.Bool("initialize", false, "initialize or migrate persistent data")
	ready := flag.String("socket-ready", "", "check gateway socket readiness")
	abandoned := flag.String("socket-abandoned", "", "check abandoned gateway socket")
	showVersion := flag.Bool("version", false, "print version")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	if *ready != "" {
		if !socketReady(*ready) {
			return fmt.Errorf("socket not ready")
		}
		return nil
	}
	if *abandoned != "" {
		if !abandonedSocket(*abandoned) {
			return fmt.Errorf("socket is live or inaccessible")
		}
		return nil
	}
	s, err := openStore(*dataDir)
	if err != nil {
		return err
	}
	initialize(s)
	if *initOnly {
		return s.db.Close()
	}
	*base = strings.TrimSuffix(*base, "/")
	if *base != "" && (!strings.HasPrefix(*base, "/") || strings.Contains(*base, "..") || strings.Contains(*base, "//")) {
		s.db.Close()
		return fmt.Errorf("无效网关路径")
	}
	if !*dev && *socket == "" {
		s.db.Close()
		return fmt.Errorf("飞牛认证模式必须使用本地 Socket")
	}
	var listener net.Listener
	if *socket != "" {
		if !abandonedSocket(*socket) {
			s.db.Close()
			return fmt.Errorf("Socket 仍在使用或路径不可访问")
		}
		if info, err := os.Lstat(*socket); err == nil && info.Mode()&os.ModeSocket != 0 {
			if err = os.Remove(*socket); err != nil {
				s.db.Close()
				return err
			}
		}
		listener, err = net.Listen("unix", *socket)
		if err == nil {
			err = os.Chmod(*socket, 0660)
		}
	} else {
		host, _, e := net.SplitHostPort(*listen)
		if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			s.db.Close()
			return fmt.Errorf("开发模式仅允许监听回环地址")
		}
		listener, err = net.Listen("tcp", *listen)
	}
	if err != nil {
		if listener != nil {
			listener.Close()
		}
		s.db.Close()
		return err
	}
	defer listener.Close()
	m, err := newManager(s, 5*time.Second, 15*time.Second)
	if err != nil {
		s.db.Close()
		return err
	}
	app := &App{manager: m, base: *base, dev: *dev}
	app.discovery = newDiscovery(m)
	defer func() { app.discovery.close(); m.close() }()
	server := &http.Server{Handler: app, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 40 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384, ConnContext: gatewayConnectionContext}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m.start()
	log.Printf("斐讯设备管理 %s (Go)，监听 %s，数据 %s", version, listener.Addr(), filepath.Clean(*dataDir))
	return serveUntilStopped(ctx, server, listener, 10*time.Second)
}
func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func marshalJSON(value any) ([]byte, error) { return json.Marshal(value) }
