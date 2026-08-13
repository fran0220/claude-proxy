package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
)

func main() {
	log.SetOutput(os.Stderr)
	if strings.EqualFold(os.Getenv("CLAUDE_PROXY_LOG"), "debug") {
		log.SetLevel(log.DebugLevel)
	} else {
		log.SetLevel(log.InfoLevel)
	}

	cfg := loadConfig()
	log.Infof("claude-proxy starting on %s (admin %s)", cfg.Listen, cfg.AdminListen)

	tokenMgr := NewTokenManager()
	prices := NewPriceCatalog(cfg.DataDirPath(), cfg)
	if err := prices.LoadDisk(); err != nil {
		log.Warnf("failed to load cached prices: %v", err)
	}
	logger := NewRequestLoggerWithPrices(cfg.DataDirPath(), prices)
	defer logger.Close()

	authResolver := NewClaudeAuthResolver(cfg, tokenMgr)
	router := NewClaudeRouter(cfg, logger, authResolver)
	admin := NewAdminServer(cfg, tokenMgr, logger, prices, authResolver)

	go admin.Start(cfg.AdminListen)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		if err := prices.Refresh(ctx); err != nil {
			log.Warnf("initial price refresh failed: %v", err)
		}
	}()

	flushStop := make(chan struct{})
	go func() {
		flushTicker := time.NewTicker(30 * time.Second)
		priceTicker := time.NewTicker(6 * time.Hour)
		defer flushTicker.Stop()
		defer priceTicker.Stop()
		for {
			select {
			case <-flushTicker.C:
				logger.FlushPending()
			case <-priceTicker.C:
				ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
				if err := prices.Refresh(ctx); err != nil {
					log.Warnf("scheduled price refresh failed: %v", err)
				}
				cancel()
			case <-flushStop:
				return
			}
		}
	}()

	proxyServer := &http.Server{Addr: cfg.Listen, Handler: router}
	go func() {
		log.Infof("proxy listening on http://localhost%s", cfg.Listen)
		if err := proxyServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("proxy server error: %v", err)
		}
	}()

	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	runStatusApp(ctx, cfg, tokenMgr, logger, authResolver)
	stopSignals()

	log.Info("shutting down")
	close(flushStop)
	logger.FlushPending()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := proxyServer.Shutdown(shutdownCtx); err != nil {
		log.Warnf("proxy shutdown error: %v", err)
	}
}
