package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"szx-gateway/internal/config"
)

func TestAuthSessionFlow(t *testing.T) {
	ws := NewWebServer(&config.Config{WebUsername: "admin", WebPassword: "secret"}, nil, nil, nil, nil, nil)
	mux := http.NewServeMux()
	ws.registerAPIRoutes(mux)

	request := httptest.NewRequest(http.MethodGet, "/api/v2/auth/check", nil)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("check without session status = %d, want %d", response.Code, http.StatusUnauthorized)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", bytes.NewBufferString(`{"username":"admin","password":"secret"}`))
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, want %d", response.Code, http.StatusOK)
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != sessionCookieName || !cookies[0].HttpOnly || cookies[0].MaxAge != int(sessionTTL.Seconds()) {
		t.Fatalf("unexpected session cookie: %#v", cookies)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v2/auth/check", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("check with session status = %d, want %d", response.Code, http.StatusOK)
	}

	request = httptest.NewRequest(http.MethodPost, "/api/v2/auth/logout", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("logout status = %d, want %d", response.Code, http.StatusOK)
	}
}
