package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func releaseFixture(tag string) githubRelease {
	raw := fmt.Sprintf(`{"tag_name":%q,"published_at":"2026-11-05T00:00:00Z","body":"性能优化\n<script>window.unsafe=true</script>","assets":[
{"name":"phicomm-manager-%s-arm.fpk","browser_download_url":"%s/releases/download/%s/phicomm-manager-%s-arm.fpk","size":100,"state":"uploaded"},
{"name":"phicomm-manager-%s-x86.fpk","browser_download_url":"%s/releases/download/%s/phicomm-manager-%s-x86.fpk","size":100,"state":"uploaded"}]}`,
		tag, strings.TrimPrefix(tag, "v"), projectURL, tag, strings.TrimPrefix(tag, "v"), strings.TrimPrefix(tag, "v"), projectURL, tag, strings.TrimPrefix(tag, "v"))
	var result githubRelease
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		panic(err)
	}
	return result
}

func TestReleaseVersionAndArchitecture(t *testing.T) {
	for _, test := range []struct{ tag, current, status string }{
		{"v0.2.12", "0.2.9", "update"}, {"v0.10.0", "0.9.99", "update"},
		{"v1.0.0", "0.99.99", "update"}, {"v0.2.12", "0.2.12", "current"}, {"v0.2.11", "0.2.12", "ahead"},
	} {
		for _, arch := range []string{"amd64", "arm64", "386"} {
			got, err := releaseResult(releaseFixture(test.tag), test.current, arch)
			if err != nil || got.Status != test.status {
				t.Fatal(test, got, err)
			}
			platform, _ := packagePlatform(arch)
			if platform == "" {
				if got.DownloadURL != "" {
					t.Fatal("unsupported architecture received a package")
				}
			} else if !strings.HasSuffix(got.DownloadURL, "-"+platform+".fpk") {
				t.Fatal("wrong architecture", arch, got.DownloadURL)
			}
		}
	}
	for _, tag := range []string{"v0.2.13-rc.1", "v0.2", "v01.2.13", "v0.2.13/x", "v999999999999999999999.2.0", "latest"} {
		if _, err := releaseResult(releaseFixture(tag), version, "amd64"); err == nil {
			t.Fatal("invalid/non-stable release accepted", tag)
		}
	}
	for _, modify := range []func(*githubRelease){func(r *githubRelease) { r.Draft = true }, func(r *githubRelease) { r.Prerelease = true }, func(r *githubRelease) { r.Published = time.Time{} }} {
		r := releaseFixture("v99.0.0")
		modify(&r)
		if _, err := releaseResult(r, version, "arm64"); err == nil {
			t.Fatal("unpublished release accepted")
		}
	}
	for _, modify := range []func(*githubRelease){
		func(r *githubRelease) { r.Assets[1].URL = "https://example.com/untrusted.fpk" },
		func(r *githubRelease) { r.Assets[1].Name = "wrong-x86.fpk" },
		func(r *githubRelease) { r.Assets[1].State = "new" },
		func(r *githubRelease) { r.Assets[1].Size = 0 },
		func(r *githubRelease) { r.Assets = nil },
	} {
		r := releaseFixture("v99.0.0")
		modify(&r)
		got, err := releaseResult(r, version, "amd64")
		if err != nil || got.Status != "update" || got.DownloadURL != "" || got.ReleaseURL != projectURL+"/releases/tag/v99.0.0" {
			t.Fatal("missing/unsafe package should retain only release link", got, err)
		}
	}
}

func updateResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func TestUpdateCacheAndFailureRecovery(t *testing.T) {
	now := time.Now()
	var calls int
	status := 200
	body, _ := json.Marshal(releaseFixture("v99.0.0"))
	c := updateChecker{now: func() time.Time { return now }, client: &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != latestReleaseAPI {
			return updateResponse(503, ""), nil
		}
		calls++
		if r.URL.String() != latestReleaseAPI || r.Method != "GET" || r.Header.Get("User-Agent") != "IoT-fnOS-Manager/"+version || r.Header.Get("Authorization") != "" {
			t.Error("unexpected update request", r.Method, r.URL)
		}
		return updateResponse(status, string(body)), nil
	})}}
	check := func(force bool, expected string, expectedCalls int) {
		t.Helper()
		result, err := c.check(context.Background(), force)
		if err != nil || result.Status != expected || calls != expectedCalls {
			t.Fatal("cache/failure state incorrect", result, err, calls, expectedCalls)
		}
	}
	check(false, "update", 1)
	check(true, "update", 1)
	now = now.Add(2 * time.Minute)
	check(false, "update", 1)
	status = 403
	check(true, "error", 2)
	check(true, "error", 2)
	status = 200
	now = now.Add(31 * time.Second)
	check(true, "update", 3)
	now = now.Add(16 * time.Minute)
	check(false, "update", 4)
	for _, bad := range []struct {
		code int
		body string
	}{{500, "secret error"}, {404, ""}, {429, ""}, {200, "not json"}, {200, strings.Repeat("x", (2<<20)+1)}} {
		now = now.Add(16 * time.Minute)
		status, body = bad.code, []byte(bad.body)
		check(false, "error", calls+1)
		if c.result.LatestVersion != "" || c.result.DownloadURL != "" || strings.Contains(c.result.Message, "secret") {
			t.Fatal("failed checks exposed stale version or upstream error text", c.result)
		}
	}
}

func TestUpdateReleasePageFallback(t *testing.T) {
	for _, test := range []struct {
		tag, destination, status string
		download                 bool
	}{
		{"v" + version, "https://release-assets.githubusercontent.com/asset", "current", false},
		{"v99.0.0", "https://release-assets.githubusercontent.com/asset", "update", true},
		{"v99.0.0", "https://example.com/asset", "update", false},
		{"v99.0.0-beta.1", "", "error", false},
		{"../../untrusted", "", "error", false},
	} {
		c := updateChecker{client: &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() == latestReleaseAPI {
				return updateResponse(403, ""), nil
			}
			if r.Method != "HEAD" {
				t.Error("fallback must not download package")
			}
			response := updateResponse(302, "")
			if r.URL.String() == projectURL+"/releases/latest" {
				response.Header.Set("Location", projectURL+"/releases/tag/"+test.tag)
			} else {
				response.Header.Set("Location", test.destination)
			}
			return response, nil
		})}}
		got, err := c.check(context.Background(), false)
		if err != nil || got.Status != test.status || (got.DownloadURL != "") != test.download {
			t.Fatal(test, got, err)
		}
		if got.Status != "error" && (got.Source != "release-page" || got.PublishedAt != nil || got.Notes != "") {
			t.Fatal("fallback invented release details", got)
		}
	}
}

func TestUpdateConcurrentChecksAndCancellation(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	body, _ := json.Marshal(releaseFixture("v99.0.0"))
	c := updateChecker{client: &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			return updateResponse(200, string(body)), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})}}
	done := make(chan error, 6)
	for i := 0; i < 6; i++ {
		go func() { _, err := c.check(context.Background(), true); done <- err }()
	}
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.check(ctx, true); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("waiting update request ignored cancellation", err)
	}
	close(release)
	for i := 0; i < 6; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("duplicate GitHub requests", calls.Load())
	}
	canceled := updateChecker{client: &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}}
	ctx2, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err := canceled.check(ctx2, false); !errors.Is(err, context.DeadlineExceeded) || !canceled.result.CheckedAt.IsZero() {
		t.Fatal("canceled request was cached", err)
	}
}

func TestAboutAndUpdatesRequireAuthentication(t *testing.T) {
	_, app, server := testManager(t)
	var calls atomic.Int32
	body, _ := json.Marshal(releaseFixture("v99.0.0"))
	app.updates.client = &http.Client{Transport: updateTransport(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return updateResponse(200, string(body)), nil
	})}
	status, data := requestJSON(t, server, "about", "GET", nil)
	if status != 200 || data["version"] != version || data["author"] != "情深深雨蒙蒙" || calls.Load() != 0 {
		t.Fatal("local about metadata should not contact GitHub", status, data)
	}
	status, data = requestJSON(t, server, "updates", "POST", Obj{})
	if status != 200 || data["status"] != "update" || calls.Load() != 1 {
		t.Fatal(status, data)
	}
	app.dev, app.base = false, "/app/phicomm-manager"
	for _, endpoint := range []string{"about", "updates"} {
		for _, auth := range []bool{false, true} {
			r := httptest.NewRequest("GET", "http://gateway/app/phicomm-manager/api/"+endpoint, nil)
			if auth {
				r = r.WithContext(context.WithValue(r.Context(), localSocketKey{}, true))
				r.Header.Set("X-Trim-Userid", "1000")
				r.Header.Set("X-Trim-Isadmin", "true")
			}
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if (auth && w.Code != 200) || (!auth && w.Code != 401) {
				t.Fatal(endpoint, auth, w.Code)
			}
		}
	}
	r := httptest.NewRequest("POST", "http://gateway/app/phicomm-manager/api/updates", strings.NewReader("{}"))
	r.Header.Set("Origin", "https://untrusted.example")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 403 || calls.Load() != 1 {
		t.Fatal("cross-origin update request accepted", w.Code, calls.Load())
	}
}
