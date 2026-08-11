package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"szx-gateway/internal/config"
	"szx-gateway/internal/keys"
	"szx-gateway/internal/models"
	"szx-gateway/internal/proxies"
	"szx-gateway/internal/store"
)

const (
	sessionCookieName = "szx_session"
	sessionTTL        = 7 * 24 * time.Hour
)

type WebServer struct {
	cfg          *config.Config
	store        *store.Store
	rankingMgr   *models.RankingManager
	modelChecker *models.ModelChecker
	pools        map[string]*keys.KeyPool
	keyChecks    *keys.CheckService
	proxies      *proxies.Pool
	sessions     map[string]time.Time
	sessionsMu   sync.Mutex
}

func NewWebServer(cfg *config.Config, s *store.Store, rm *models.RankingManager, modelChecker *models.ModelChecker, pools map[string]*keys.KeyPool, keyChecks *keys.CheckService, proxyPool *proxies.Pool) *WebServer {
	return &WebServer{
		cfg:          cfg,
		store:        s,
		rankingMgr:   rm,
		modelChecker: modelChecker,
		pools:        pools,
		keyChecks:    keyChecks,
		proxies:      proxyPool,
		sessions:     make(map[string]time.Time),
	}
}

func (ws *WebServer) Start(mux *http.ServeMux) {
	ws.registerAPIRoutes(mux)
	mux.HandleFunc("/", ws.handleSPA)
}

func (ws *WebServer) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if ws.cfg.WebUsername == "" && ws.cfg.WebPassword == "" {
			next.ServeHTTP(w, r)
			return
		}
		if !ws.refreshSession(r, w) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	}
}

func (ws *WebServer) credentialsValid(user, pass string) bool {
	return subtle.ConstantTimeCompare([]byte(user), []byte(ws.cfg.WebUsername)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(ws.cfg.WebPassword)) == 1
}

func (ws *WebServer) createSession(w http.ResponseWriter) error {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(value)
	expires := time.Now().Add(sessionTTL)
	ws.sessionsMu.Lock()
	ws.sessions[token] = expires
	ws.sessionsMu.Unlock()
	ws.setSessionCookie(w, token, expires)
	return nil
}

func (ws *WebServer) refreshSession(r *http.Request, w http.ResponseWriter) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	expires := time.Now().Add(sessionTTL)
	ws.sessionsMu.Lock()
	storedExpiry, ok := ws.sessions[cookie.Value]
	if ok && storedExpiry.After(time.Now()) {
		ws.sessions[cookie.Value] = expires
	} else {
		delete(ws.sessions, cookie.Value)
		ok = false
	}
	ws.sessionsMu.Unlock()
	if !ok {
		return false
	}
	ws.setSessionCookie(w, cookie.Value, expires)
	return true
}

func (ws *WebServer) deleteSession(r *http.Request, w http.ResponseWriter) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		ws.sessionsMu.Lock()
		delete(ws.sessions, cookie.Value)
		ws.sessionsMu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
}

func (ws *WebServer) setSessionCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: value, Path: "/", Expires: expires, MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
