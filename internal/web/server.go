package web

import (
	"crypto/subtle"
	"net/http"

	"openrouter-gateway/internal/config"
	"openrouter-gateway/internal/keys"
	"openrouter-gateway/internal/models"
	"openrouter-gateway/internal/store"
)

type WebServer struct {
	cfg        *config.Config
	store      *store.Store
	rankingMgr *models.RankingManager
	pools      map[string]*keys.KeyPool
}

func NewWebServer(cfg *config.Config, s *store.Store, rm *models.RankingManager, pools map[string]*keys.KeyPool) *WebServer {
	return &WebServer{
		cfg:        cfg,
		store:      s,
		rankingMgr: rm,
		pools:      pools,
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
			w.Header().Set("WWW-Authenticate", `Basic realm="OpenRouter Gateway"`)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("Unauthorized\n"))
			return
		}

		next.ServeHTTP(w, r)
	}
}
