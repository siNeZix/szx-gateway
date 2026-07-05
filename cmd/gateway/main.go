package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"openrouter-gateway/internal/config"
	"openrouter-gateway/internal/keys"
	"openrouter-gateway/internal/models"
	"openrouter-gateway/internal/proxies"
	"openrouter-gateway/internal/proxy"
	"openrouter-gateway/internal/store"
	"openrouter-gateway/internal/web"
)

func main() {
	log.Println("Starting LLM Gateway (OpenRouter + AIHubMix)...")

	cfg := config.Load()
	log.Printf("OpenRouter on %s, AIHubMix on %s", cfg.ListenAddr, cfg.AIHubMixListenAddr)

	dbStore, err := store.New(cfg.DbPath)
	if err != nil {
		log.Fatalf("Database initialization failed: %v", err)
	}
	defer dbStore.Close()
	log.Printf("SQLite database initialized at %s", cfg.DbPath)

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

	openRouterProxy := proxy.NewProxyHandler(cfg, dbStore, openRouterPool, rankingMgr, proxyPool)
	aihubmixProxy := proxy.NewAihubmixHandler(cfg, dbStore, aihubmixPool, rankingMgr, proxyPool)

	pools := map[string]*keys.KeyPool{
		"openrouter": openRouterPool,
		"aihubmix":   aihubmixPool,
	}
	webServer := web.NewWebServer(cfg, dbStore, rankingMgr, pools, proxyPool)

	// OpenRouter mux: /v1/* → OpenRouter proxy, / → admin
	orMux := http.NewServeMux()
	webServer.Start(orMux)
	orMux.Handle("/v1/", openRouterProxy)

	// AIHubMix mux: /v1/* → AIHubMix proxy, / → same admin
	amMux := http.NewServeMux()
	webServer.Start(amMux)
	amMux.Handle("/v1/", aihubmixProxy)

	orServer := &http.Server{Addr: cfg.ListenAddr, Handler: orMux}
	amServer := &http.Server{Addr: cfg.AIHubMixListenAddr, Handler: amMux}

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

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down gracefully...")
	keyChecker.Stop()
	aihubmixChecker.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := orServer.Shutdown(ctx); err != nil {
		log.Printf("OpenRouter shutdown error: %v", err)
	}
	if err := amServer.Shutdown(ctx); err != nil {
		log.Printf("AIHubMix shutdown error: %v", err)
	}

	log.Println("LLM Gateway stopped.")
}
