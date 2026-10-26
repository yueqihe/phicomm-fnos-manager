package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayLANRequestSources(t *testing.T) {
	_, a, _ := testManager(t)
	a.dev = false
	serve := func(method, path, origin, token, user, admin, site, forwarded string, local bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://internal-gateway"+path, strings.NewReader(`{"mode":"disabled"}`))
		r = r.WithContext(context.WithValue(r.Context(), localSocketKey{}, local))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Trim-Userid", user)
		r.Header.Set("X-Trim-Isadmin", admin)
		r.Header.Set("Origin", origin)
		r.Header.Set("X-Manager-CSRF", token)
		r.Header.Set("Sec-Fetch-Site", site)
		r.Header.Set("X-Forwarded-Host", forwarded)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		return w
	}
	w := serve("GET", "/api/session", "", "", "1000", "true", "", "", true)
	var session Obj
	if err := json.Unmarshal(w.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	token := str(session["csrfToken"])
	if w.Code != 200 || !boolean(session["authenticated"]) || len(token) != 64 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authenticated session did not issue an uncached request token")
	}
	for _, value := range []struct {
		name, origin, token, user, admin, site, forwarded string
		local                                             bool
		status                                            int
	}{
		{"LAN host rewritten", "http://10.0.0.9:5666", token, "1000", "true", "same-origin", "", true, 200},
		{"LAN HTTP without fetch metadata", "http://10.0.0.9:5666", token, "1000", "true", "", "", true, 200},
		{"nested proxies", "http://192.168.1.9:5666", token, "1000", "true", "same-origin", "localhost, 192.168.1.9:5666", true, 200},
		{"HTTPS gateway", "https://nas.example", token, "1000", "true", "same-origin", "", true, 200},
		{"normal matching host", "http://internal-gateway", "", "1000", "true", "same-origin", "", true, 200},
		{"normal forwarded host", "http://10.0.0.9:5666", "", "1000", "true", "", "10.0.0.9:5666", true, 200},
		{"missing token", "http://10.0.0.9:5666", "", "1000", "true", "same-origin", "", true, 403},
		{"wrong token", "http://10.0.0.9:5666", "invalid", "1000", "true", "", "", true, 403},
		{"different user", "http://10.0.0.9:5666", token, "1001", "true", "", "", true, 403},
		{"nonadmin", "http://10.0.0.9:5666", token, "1000", "false", "", "", true, 403},
		{"TCP identity forgery", "http://10.0.0.9:5666", token, "1000", "true", "", "", false, 403},
		{"TCP matching host still needs authentication", "http://internal-gateway", token, "1000", "true", "", "", false, 401},
		{"cross-site with token", "http://10.0.0.9:5666", token, "1000", "true", "cross-site", "", true, 403},
		{"outside origin without token", "https://outside.example", "", "1000", "true", "", "", true, 403},
		{"opaque origin", "null", token, "1000", "true", "", "", true, 403},
		{"non-http origin", "file://internal-gateway", token, "1000", "true", "", "", true, 403},
		{"origin with credentials", "http://user@internal-gateway", token, "1000", "true", "", "", true, 403},
	} {
		t.Run(value.name, func(t *testing.T) {
			w := serve("PUT", "/api/settings/tariff", value.origin, value.token, value.user, value.admin, value.site, value.forwarded, value.local)
			if w.Code != value.status {
				t.Fatalf("status %d, expected %d", w.Code, value.status)
			}
			if value.status == 403 {
				var data Obj
				json.Unmarshal(w.Body.Bytes(), &data)
				if data["code"] != "invalid_origin" {
					t.Fatal("source error was reported as a parameter error")
				}
			}
		})
	}
	for _, identity := range [][3]string{{"", "", "true"}, {"1000", "false", "true"}, {"1000", "true", "false"}} {
		w := serve("GET", "/api/session", "", "", identity[0], identity[1], "", "", identity[2] == "true")
		var data Obj
		json.Unmarshal(w.Body.Bytes(), &data)
		if boolean(data["authenticated"]) || data["csrfToken"] != "" {
			t.Fatal("unauthenticated session received a token")
		}
	}
	other := &App{}
	r := httptest.NewRequest("GET", "http://internal-gateway/api/session", nil)
	r.Header.Set("X-Trim-Userid", "1000")
	if other.requestToken(r) == token {
		t.Fatal("request token survived application restart")
	}
}
