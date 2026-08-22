package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"szx-gateway/internal/config"
	"szx-gateway/internal/keys"
	"szx-gateway/internal/logging"
	"szx-gateway/internal/models"
	"szx-gateway/internal/proxies"
	"szx-gateway/internal/proxy"
	"szx-gateway/internal/store"
	"szx-gateway/internal/web"
)

func main() {
	cfg := config.Load()
	logWriter, err := logging.NewRotatingWriter(cfg.LogDir, int64(cfg.LogMaxSizeMB)*1024*1024, cfg.LogMaxBackups)
	if err != nil {
		log.Fatalf("Log initialization failed: %v", err)
	}
	defer logWriter.Close()
	log.SetOutput(io.MultiWriter(os.Stdout, logWriter))
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.LUTC)

	log.Println("Starting SZX Gateway (OpenRouter + AIHubMix + Google)...")
	log.Printf("OpenRouter on %s, AIHubMix on %s, Google on %s", cfg.ListenAddr, cfg.AIHubMixListenAddr, cfg.GoogleListenAddr)

	dbStore, err := store.Open(cfg.DBDriver, cfg.DbPath, cfg.DBDSN, cfg.DBMaxOpenConns, cfg.DBMaxIdleConns)
	if err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}
	defer dbStore.Close()
	log.Printf("%s database initialized", cfg.DBDriver)

	openRouterPool, err := keys.NewKeyPool(dbStore, "openrouter")
	if err != nil {
		log.Fatalf("OpenRouter key pool init failed: %v", err)
	}
	log.Println("OpenRouter key pool loaded.")

	aihubmixPool, err := keys.NewKeyPool(dbStore, "aihubmix")
	if err != nil {
		log.Fatalf("AIHubMix key pool init failed: %v", err)
	}
	log.Println("AIHubMix key pool loaded.")

	googlePool, err := keys.NewKeyPool(dbStore, "google")
	if err != nil {
		log.Fatalf("Google key pool init failed: %v", err)
	}
	log.Println("Google key pool loaded.")

	rankingMgr := models.NewRankingManager(dbStore, cfg.RankingRefresh)
	rankingMgr.Start()
	log.Println("Model ranking manager started.")
	proxyPool := proxies.NewPool(dbStore)

	keyChecker := keys.NewKeyChecker(
		openRouterPool,
		cfg.KeyCheckTTL,
		cfg.KeyCheckRate,
		cfg.KeyCheckRateInterval,
		cfg.KeyCheckConcurrency,
		"https://openrouter.ai/api/v1/key",
		"openrouter",
		proxyPool,
		dbStore,
	)
	keyChecker.Start()
	log.Println("Background key checker started (OpenRouter).")

	aihubmixChecker := keys.NewKeyChecker(
		aihubmixPool,
		cfg.KeyCheckTTL,
		cfg.KeyCheckRate,
		cfg.KeyCheckRateInterval,
		cfg.KeyCheckConcurrency,
		"https://aihubmix.com/v1/models",
		"aihubmix",
		proxyPool,
		dbStore,
	)
	aihubmixChecker.Start()
	log.Println("Background key checker started (AIHubMix).")

	googleChecker := keys.NewKeyChecker(
		googlePool,
		cfg.KeyCheckTTL,
		cfg.KeyCheckRate,
		cfg.KeyCheckRateInterval,
		cfg.KeyCheckConcurrency,
		"https://generativelanguage.googleapis.com/v1beta/models",
		"google",
		proxyPool,
		dbStore,
	)
	googleChecker.Start()
	log.Println("Background key checker started (Google).")

	// ponytail: фоновый сброс дневных счётчиков раз в минуту. Решает баг
	// "использовано за сегодня не сбросилось после UTC+0" — без этого сброс
	// происходит только лениво, при первом запросе через ключ.
	dailyResetCtx, dailyResetCancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-dailyResetCtx.Done():
				return
			case <-ticker.C:
				if n := openRouterPool.ResetExpiredDailyUsage(); n > 0 {
					log.Printf("Daily reset: %d openrouter keys cleared", n)
				}
				if n := aihubmixPool.ResetExpiredDailyUsage(); n > 0 {
					log.Printf("Daily reset: %d aihubmix keys cleared", n)
				}
				if n := googlePool.ResetExpiredDailyUsage(); n > 0 {
					log.Printf("Daily reset: %d google keys cleared", n)
				}
			}
		}
	}()
	log.Println("Daily usage reset worker started (1m interval).")

	openRouterProxy := proxy.NewProxyHandler(cfg, dbStore, openRouterPool, rankingMgr, proxyPool)
	aihubmixProxy := proxy.NewAihubmixHandler(cfg, dbStore, aihubmixPool, rankingMgr, proxyPool)
	googleProxy := proxy.NewGoogleHandler(cfg, dbStore, googlePool, rankingMgr, proxyPool)

	pools := map[string]*keys.KeyPool{
		"openrouter": openRouterPool,
		"aihubmix":   aihubmixPool,
		"google":     googlePool,
	}
	modelChecker := models.NewModelChecker(dbStore, cfg.GatewayToken, map[string]string{
		"openrouter": "http://127.0.0.1" + cfg.ListenAddr,
		"aihubmix":   "http://127.0.0.1" + cfg.AIHubMixListenAddr,
		"google":     "http://127.0.0.1" + cfg.GoogleListenAddr,
	})
	keyChecks := keys.NewCheckService(aihubmixPool, aihubmixChecker, func() string {
		free := rankingMgr.GetAihubmixFreeModels()
		if len(free) == 0 {
			return ""
		}
		return free[0].ID
	})
	webServer := web.NewWebServer(cfg, dbStore, rankingMgr, modelChecker, pools, keyChecks, proxyPool)

	// OpenRouter mux: /v1/* → OpenRouter proxy, / → admin
	orMux := http.NewServeMux()
	webServer.Start(orMux)
	orMux.Handle("/v1/", openRouterProxy)

	// AIHubMix mux: /v1/* → AIHubMix proxy, / → same admin
	amMux := http.NewServeMux()
	webServer.Start(amMux)
	amMux.Handle("/v1/", aihubmixProxy)

	// Google mux: /v1/* → Google proxy, / → same admin
	gMux := http.NewServeMux()
	webServer.Start(gMux)
	gMux.Handle("/v1/", googleProxy)

	orServer := &http.Server{Addr: cfg.ListenAddr, Handler: orMux}
	amServer := &http.Server{Addr: cfg.AIHubMixListenAddr, Handler: amMux}
	gServer := &http.Server{Addr: cfg.GoogleListenAddr, Handler: gMux}

	go func() {
		log.Printf("OpenRouter server on %s", cfg.ListenAddr)
		if err := orServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("OpenRouter server failure: %v", err)
		}
	}()
	go func() {
		log.Printf("AIHubMix server on %s", cfg.AIHubMixListenAddr)
		if err := amServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("AIHubMix server failure: %v", err)
		}
	}()
	modelChecker.Start()
	log.Println("Background model availability checker started.")
	go func() {
		log.Printf("Google server on %s", cfg.GoogleListenAddr)
		if err := gServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Google server failure: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	dailyResetCancel()
	modelChecker.Stop()
	keyChecker.Stop()
	aihubmixChecker.Stop()
	googleChecker.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := orServer.Shutdown(ctx); err != nil {
		log.Printf("OpenRouter shutdown error: %v", err)
	}
	if err := amServer.Shutdown(ctx); err != nil {
		log.Printf("AIHubMix shutdown error: %v", err)
	}
	if err := gServer.Shutdown(ctx); err != nil {
		log.Printf("Google shutdown error: %v", err)
	}

	log.Println("SZX Gateway stopped.")
}
