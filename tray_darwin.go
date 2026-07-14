//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"fyne.io/systray"
	log "github.com/sirupsen/logrus"
)

func runStatusApp(ctx context.Context, cfg *Config, tokenMgr *TokenManager, logger *RequestLogger, authResolver *ClaudeAuthResolver) {
	if os.Getenv("CLAUDE_PROXY_NO_TRAY") == "1" {
		<-ctx.Done()
		return
	}

	go func() {
		<-ctx.Done()
		systray.Quit()
	}()

	systray.Run(onTrayReady(ctx, cfg, tokenMgr, logger, authResolver), onTrayExit)
}

func onTrayReady(ctx context.Context, cfg *Config, tokenMgr *TokenManager, logger *RequestLogger, authResolver *ClaudeAuthResolver) func() {
	return func() {
		systray.SetIcon(iconGreen)
		systray.SetTitle("Claude")
		systray.SetTooltip("Claude Proxy")

		mStatus := systray.AddMenuItem(fmt.Sprintf("Claude Proxy %s - Running", version), "")
		mStatus.Disable()
		mProxy := systray.AddMenuItem("Proxy: "+localURL(cfg.Listen), "")
		mProxy.Disable()
		mAdmin := systray.AddMenuItem("Dashboard: "+localURL(cfg.AdminListen), "")
		mAdmin.Disable()

		systray.AddSeparator()

		mAuth := systray.AddMenuItem("Auth: checking...", "")
		mAuth.Disable()
		mModels := systray.AddMenuItem("Models: checking...", "")
		mModels.Disable()
		mStats := systray.AddMenuItem("Stats: checking...", "")
		mStats.Disable()
		mLast := systray.AddMenuItem("Last request: none", "")
		mLast.Disable()

		systray.AddSeparator()

		mOpenDashboard := systray.AddMenuItem("Open Dashboard", "")
		mOpenHealth := systray.AddMenuItem("Open Proxy Health", "")
		mReloadToken := systray.AddMenuItem("Reload Claude Token", "")
		mDiscover := systray.AddMenuItem("Discover Models", "")

		systray.AddSeparator()

		mQuit := systray.AddMenuItem("Quit", "")

		items := trayItems{mAuth: mAuth, mModels: mModels, mStats: mStats, mLast: mLast}
		refreshTray(cfg, authResolver, logger, items)

		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					systray.Quit()
					return
				case <-ticker.C:
					refreshTray(cfg, authResolver, logger, items)
				case <-mOpenDashboard.ClickedCh:
					_ = exec.Command("open", localURL(cfg.AdminListen)).Start()
				case <-mOpenHealth.ClickedCh:
					_ = exec.Command("open", localURL(cfg.Listen)+"/healthz").Start()
				case <-mReloadToken.ClickedCh:
					go reloadToken(ctx, tokenMgr, cfg, authResolver, logger, items, mReloadToken)
				case <-mDiscover.ClickedCh:
					go discoverModelsFromTray(ctx, cfg, authResolver, logger, items, mDiscover)
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				}
			}
		}()
	}
}

func onTrayExit() {
	log.Info("status bar app exited")
}

type trayItems struct {
	mAuth   *systray.MenuItem
	mModels *systray.MenuItem
	mStats  *systray.MenuItem
	mLast   *systray.MenuItem
}

func refreshTray(cfg *Config, authResolver *ClaudeAuthResolver, logger *RequestLogger, items trayItems) {
	authStatus := authResolver.AuthStatus()
	localOK, _ := authStatus["local_available"].(bool)
	apikeyOK, _ := authStatus["apikey_available"].(bool)
	if localOK || apikeyOK {
		systray.SetIcon(iconGreen)
	} else {
		systray.SetIcon(iconRed)
	}

	items.mAuth.SetTitle(formatAuthStatus(authStatus))
	items.mModels.SetTitle(formatModelsStatus(cfg))

	stats := logger.GetStatsFiltered(StatsFilter{Provider: "anthropic"})
	items.mStats.SetTitle(fmt.Sprintf("Stats: %d reqs | %d errors | %s tokens",
		stats.TotalRequests, stats.TotalErrors, fmtTokensTray(stats.TotalTokens)))

	logs := logger.GetLogsFiltered(1, 0, "anthropic", "", 0)
	if len(logs) == 0 {
		items.mLast.SetTitle("Last request: none")
		return
	}
	last := logs[0]
	items.mLast.SetTitle(fmt.Sprintf("Last: %s | %d | %s", last.Model, last.Status, last.Timestamp.Local().Format("15:04:05")))
}

func reloadToken(ctx context.Context, tokenMgr *TokenManager, cfg *Config, authResolver *ClaudeAuthResolver, logger *RequestLogger, items trayItems, item *systray.MenuItem) {
	item.SetTitle("Reloading token...")
	item.Disable()
	defer item.Enable()

	if err := tokenMgr.loadFromKeychain(); err != nil {
		log.Errorf("keychain reload failed: %v", err)
		item.SetTitle("Reload failed")
		time.AfterFunc(4*time.Second, func() { item.SetTitle("Reload Claude Token") })
		refreshTray(cfg, authResolver, logger, items)
		return
	}
	if _, err := tokenMgr.GetAccessToken(ctx); err != nil {
		log.Errorf("token refresh failed: %v", err)
		item.SetTitle("Token refresh failed")
		time.AfterFunc(4*time.Second, func() { item.SetTitle("Reload Claude Token") })
		refreshTray(cfg, authResolver, logger, items)
		return
	}
	item.SetTitle("Token reloaded")
	time.AfterFunc(3*time.Second, func() { item.SetTitle("Reload Claude Token") })
	refreshTray(cfg, authResolver, logger, items)
}

func discoverModelsFromTray(ctx context.Context, cfg *Config, authResolver *ClaudeAuthResolver, logger *RequestLogger, items trayItems, item *systray.MenuItem) {
	item.SetTitle("Discovering models...")
	item.Disable()
	defer item.Enable()

	auth, route := authResolver.ResolveDiscoveryAuth(ctx, "")
	models, err := discoverClaudeModels(ctx, auth)
	if err != nil && route == RouteLocal {
		fallback := authResolver.ResolveRoute(ctx, RouteAPIKey)
		if fallback.Valid() {
			if fallbackModels, fallbackErr := discoverClaudeModels(ctx, fallback); fallbackErr == nil {
				auth, route, models, err = fallback, RouteAPIKey, fallbackModels, nil
			}
		}
	}
	if err != nil {
		log.Errorf("model discovery failed: %v", err)
		item.SetTitle("Discovery failed")
		time.AfterFunc(4*time.Second, func() { item.SetTitle("Discover Models") })
		refreshTray(cfg, authResolver, logger, items)
		return
	}

	added, updated := cfg.MergeDiscoveredModels(models, route, time.Now())
	if err := cfg.Save(); err != nil {
		log.Errorf("saving discovered models failed: %v", err)
		item.SetTitle("Save failed")
		time.AfterFunc(4*time.Second, func() { item.SetTitle("Discover Models") })
		refreshTray(cfg, authResolver, logger, items)
		return
	}

	source := ""
	if auth != nil {
		source = auth.Source
	}
	item.SetTitle(fmt.Sprintf("Discovered %d (%d new, %d updated) via %s/%s", len(models), added, updated, route, source))
	time.AfterFunc(5*time.Second, func() { item.SetTitle("Discover Models") })
	refreshTray(cfg, authResolver, logger, items)
}

func formatAuthStatus(auth map[string]any) string {
	localOK, _ := auth["local_available"].(bool)
	apikeyOK, _ := auth["apikey_available"].(bool)
	if localOK {
		expires, _ := auth["local_expires_in"].(string)
		if expires != "" {
			return "Auth: Keychain valid (" + expires + ")"
		}
		return "Auth: Keychain valid"
	}
	if apikeyOK {
		return "Auth: API key configured"
	}
	if errText, _ := auth["local_error"].(string); errText != "" {
		return "Auth: unavailable - " + truncateTray(errText, 42)
	}
	return "Auth: unavailable"
}

func formatModelsStatus(cfg *Config) string {
	cfg.mu.RLock()
	models := append([]ModelEntry(nil), cfg.Claude.Models...)
	cfg.mu.RUnlock()

	local, apikey, total := countRoutes(models)
	discovered := 0
	for _, m := range models {
		if m.Discovered {
			discovered++
		}
	}
	return fmt.Sprintf("Models: %d total | %d local | %d API key | %d discovered", total, local, apikey, discovered)
}

func localURL(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "http://localhost"
	}
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return strings.TrimRight(addr, "/")
	}
	if strings.HasPrefix(addr, ":") {
		return "http://localhost" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "http://localhost:" + strings.TrimPrefix(addr, "0.0.0.0:")
	}
	if strings.HasPrefix(addr, "[::]:") {
		return "http://localhost:" + strings.TrimPrefix(addr, "[::]:")
	}
	return "http://" + addr
}

func fmtTokensTray(n int64) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1e3)
	}
	return fmt.Sprintf("%d", n)
}

func truncateTray(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
