//go:build !darwin

package main

import "context"

func runStatusApp(ctx context.Context, cfg *Config, tokenMgr *TokenManager, logger *RequestLogger, authResolver *ClaudeAuthResolver) {
	<-ctx.Done()
}
