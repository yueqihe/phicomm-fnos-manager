package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHTTPShutdownDrainsHandlersBeforeReturning(t *testing.T) {
	for _, forced := range []bool{false, true} {
		name := "graceful"
		if forced {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				if forced {
					<-r.Context().Done()
				}
				<-release
				close(finished)
				w.WriteHeader(204)
			})}
			grace := 3 * time.Second
			if forced {
				grace = 50 * time.Millisecond
			}
			result := make(chan error, 1)
			go func() { result <- serveUntilStopped(ctx, server, listener, grace) }()
			clientDone := make(chan struct{})
			go func() {
				defer close(clientDone)
				client := &http.Client{Timeout: 4 * time.Second}
				res, _ := client.Get("http://" + listener.Addr().String())
				if res != nil {
					res.Body.Close()
				}
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				close(release)
				t.Fatal("handler never started")
			}
			cancel()
			select {
			case err := <-result:
				close(release)
				t.Fatalf("server returned before handler drained: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			select {
			case err := <-result:
				if forced && !errors.Is(err, context.DeadlineExceeded) || !forced && err != nil {
					t.Fatal(err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("shutdown did not complete")
			}
			select {
			case <-finished:
			default:
				t.Fatal("handler still active")
			}
			<-clientDone
		})
	}
}

func TestRealUnixSocketGatewayAndReadiness(t *testing.T) {
	// Keep the path short enough for AF_UNIX on Windows and Linux.
	directory, err := os.MkdirTemp("", "pm-unix-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "app.sock")
	if !abandonedSocket(path) || socketReady(path) {
		t.Fatal("missing socket probe incorrect")
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s, err := openStore(filepath.Join(directory, "data"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := newManager(s, time.Second, time.Hour)
	if err != nil {
		s.db.Close()
		t.Fatal(err)
	}
	defer m.close()
	a := &App{manager: m, base: "/app/phicomm-manager"}
	a.discovery = newDiscovery(m)
	defer a.discovery.close()
	server := &http.Server{Handler: a, ConnContext: gatewayConnectionContext}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- serveUntilStopped(ctx, server, listener, time.Second) }()
	defer func() {
		cancel()
		if err := <-result; err != nil {
			t.Error(err)
		}
	}()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Second}
	if !socketReady(path) || abandonedSocket(path) {
		t.Fatal("live socket probe incorrect")
	}
	for _, value := range []struct {
		user, admin string
		status      int
	}{{"", "", 401}, {"1000", "false", 401}, {"1000", "true", 200}} {
		req, _ := http.NewRequest("GET", "http://nas.example/app/phicomm-manager/api/devices", nil)
		req.Header.Set("X-Trim-Userid", value.user)
		req.Header.Set("X-Trim-Isadmin", value.admin)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, res.Body)
		res.Body.Close()
		if res.StatusCode != value.status {
			t.Fatal(value, res.StatusCode)
		}
	}
}
