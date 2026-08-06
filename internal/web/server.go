package web

import (
	"crypto/subtle"
	"net/http"

	"szx-gateway/internal/config"
	"szx-gateway/internal/keys"
	"szx-gateway/internal/models"
	"szx-gateway/internal/proxies"
	"szx-gateway/internal/store"
)

type WebServer struct {
	cfg          *config.Config
	store        *store.Store
	rankingMgr   *models.RankingManager
	modelChecker *models.ModelChecker
	pools        map[string]*keys.KeyPool
	proxies      *proxies.Pool
}

func NewWebServer(cfg *config.Config, s *store.Store, rm *models.RankingManager, modelChecker *models.ModelChecker, pools map[string]*keys.KeyPool, proxyPool *proxies.Pool) *WebServer {
	return &WebServer{
		cfg:          cfg,
		store:        s,
		rankingMgr:   rm,
		modelChecker: modelChecker,
		pools:        pools,
		proxies:      proxyPool,
	}
}

func (ws *WebServer) Start(mux *http.ServeMux) {
	ws.registerAPIRoutes(mux)
	mux.HandleFunc("/", ws.basicAuth(ws.handleSPA))
}

func (ws *WebServer) basicAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If credentials are empty, bypass auth
		if ws.cfg.WebUsername == "" && ws.cfg.WebPassword == "" {
			next.ServeHTTP(w, r)
			return
		}

		user, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(ws.cfg.WebUsername)) != 1 ||
			subtle.ConstantTimeCompare([]byte(pass), []byte(ws.cfg.WebPassword)) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="SZX Gateway"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Unauthorized\n"))
			return
		}

		next.ServeHTTP(w, r)
	}
}
