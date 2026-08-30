package main

import (
	"embed"
	"io/fs"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

//go:embed web/*
var webFS embed.FS

type AdminServer struct {
	cfg          *Config
	tokenMgr     *TokenManager
	logger       *RequestLogger
	prices       *PriceCatalog
	subUsage     *SubscriptionUsageClient
	authResolver *ClaudeAuthResolver
	startAt      time.Time
}

func NewAdminServer(cfg *Config, tokenMgr *TokenManager, logger *RequestLogger, prices *PriceCatalog, authResolver *ClaudeAuthResolver, subUsage *SubscriptionUsageClient) *AdminServer {
	return &AdminServer{
		cfg:          cfg,
		tokenMgr:     tokenMgr,
		logger:       logger,
		prices:       prices,
		subUsage:     subUsage,
		authResolver: authResolver,
		startAt:      time.Now(),
	}
}

func (s *AdminServer) Start(addr string) {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/overview", s.handleOverview)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/config/source", s.handleSetSource)
	mux.HandleFunc("/api/stats", s.handleStats)
	mux.HandleFunc("/api/stats/daily", s.handleStatsByDay)
	mux.HandleFunc("/api/stats/hourly", s.handleStatsByHour)
	mux.HandleFunc("/api/stats/routes", s.handleStatsByRoute)
	mux.HandleFunc("/api/stats/tokens", s.handleTokenTotals)
	mux.HandleFunc("/api/usage", s.handleUsage)
	mux.HandleFunc("/api/subscription/usage", s.handleSubscriptionUsage)
	mux.HandleFunc("/api/prices", s.handlePrices)
	mux.HandleFunc("/api/prices/refresh", s.handleRefreshPrices)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/logs/errors", s.handleErrors)
	mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("/api/token/refresh", s.handleTokenRefresh)
	mux.HandleFunc("/api/keys", s.handleAPIKeys)
	mux.HandleFunc("/api/keys/add", s.handleAddAPIKey)
	mux.HandleFunc("/api/keys/update", s.handleUpdateAPIKey)
	mux.HandleFunc("/api/keys/remove", s.handleRemoveAPIKey)
	mux.HandleFunc("/api/keys/test", s.handleTestCredential)
	mux.HandleFunc("/api/models", s.handleModels)
	mux.HandleFunc("/api/models/add", s.handleAddModel)
	mux.HandleFunc("/api/models/remove", s.handleRemoveModel)
	mux.HandleFunc("/api/models/route", s.handleModelRoute)
	mux.HandleFunc("/api/models/discover", s.handleDiscoverModels)
	mux.HandleFunc("/api/redirects", s.handleRedirects)
	mux.HandleFunc("/api/redirects/set", s.handleSetRedirect)
	mux.HandleFunc("/api/version", s.handleVersion)

	webContent, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("failed to load embedded web files: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(webContent)))

	server := &http.Server{Addr: addr, Handler: s.requireAdminToken(mux)}
	log.Infof("admin dashboard on http://localhost%s", addr)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Errorf("admin server error: %v", err)
	}
}

func (s *AdminServer) requireAdminToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" && !requestTokenMatches(r, s.cfg.AdminToken()) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("WWW-Authenticate", "Bearer")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"invalid admin token"}` + "\n"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
