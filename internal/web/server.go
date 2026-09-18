package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
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
	if err := ws.store.CreateWebSession(hashSessionToken(token), expires); err != nil {
		return err
	}
	ws.setSessionCookie(w, token, expires)
	return nil
}

func (ws *WebServer) refreshSession(r *http.Request, w http.ResponseWriter) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	now := time.Now()
	expires := now.Add(sessionTTL)
	ok, err := ws.store.RefreshWebSession(hashSessionToken(cookie.Value), now, expires)
	if err != nil || !ok {
		return false
	}
	ws.setSessionCookie(w, cookie.Value, expires)
	return true
}

func (ws *WebServer) deleteSession(r *http.Request, w http.ResponseWriter) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = ws.store.DeleteWebSession(hashSessionToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil})
}

func hashSessionToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(hash[:])
}

func (ws *WebServer) setSessionCookie(w http.ResponseWriter, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Value: value, Path: "/", Expires: expires, MaxAge: int(sessionTTL.Seconds()), HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
