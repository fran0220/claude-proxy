package main

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

const (
	anthropicAPIBase     = "https://api.anthropic.com"
	defaultAnthropicBeta = "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,thinking-token-count-2026-05-13,context-management-2025-06-27,prompt-caching-scope-2026-01-05,effort-2025-11-24,extended-cache-ttl-2025-04-11"
)

var claudeCodeForwardHeaders = []string{
	"X-Claude-Code-Session-Id",
	"X-Claude-Code-Request-Class",
	"X-Claude-Code-Agent-Type",
	"X-Claude-Code-Prev-Tool-Durations",
	"X-Claude-Code-Compaction",
	"X-Claude-Code-Context-Compacted",
}

type ClaudeHandler struct {
	cfg     *Config
	retryer *Retryer
	client  *http.Client
	logger  *RequestLogger
}

func NewClaudeHandler(cfg *Config, retryer *Retryer, logger *RequestLogger) *ClaudeHandler {
	return &ClaudeHandler{
		cfg:     cfg,
		retryer: retryer,
		client:  &http.Client{},
		logger:  logger,
	}
}

func (h *ClaudeHandler) Handle(w http.ResponseWriter, r *http.Request, body []byte, auth *ProviderAuth, logID string) {
	base := anthropicAPIBase
	if auth.BaseURL != "" {
		base = strings.TrimRight(auth.BaseURL, "/")
	}
	upstreamURL := buildAnthropicURL(base, r.URL.Path) + "?beta=true"
	if r.URL.RawQuery != "" {
		upstreamURL += "&" + r.URL.RawQuery
	}

	model := gjson.GetBytes(body, "model").String()
	isStream := isStreamingRequest(r, body)
	body, renameTools := renameConflictingTools(body)
	body = injectClaudeCodeIdentity(body, h.cfg.UserID)

	systemPreview := gjson.GetBytes(body, "system.0.text").String()
	userID := gjson.GetBytes(body, "metadata.user_id").String()
	log.Debugf("[CLAUDE] model=%s system[0]=%q user_id=%s body_len=%d stream=%v",
		model, truncateStr(systemPreview, 60), truncateStr(userID, 30), len(body), isStream)
	if h.cfg.Debug.DumpLastRequest {
		_ = os.WriteFile(h.cfg.Debug.DumpPath, body, 0644)
	}

	resp, err := h.retryer.Do(r.Context(), h.client, func() (*http.Request, error) {
		req, reqErr := http.NewRequest(r.Method, upstreamURL, bytes.NewReader(body))
		if reqErr != nil {
			return nil, reqErr
		}
		applyDirectClaudeHeaders(req, r, auth, isStream)
		return req, nil
	})
	if err != nil {
		log.Errorf("claude request failed: %v", err)
		writeProxyError(w, http.StatusBadGateway, "proxy_error", err.Error())
		return
	}
	defer resp.Body.Close()

	for key, values := range resp.Header {
		if renameTools && strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	if isStream && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		usage, streamErr := h.streamResponsePassthrough(w, resp.Body, renameTools)
		errMsg := ""
		if streamErr != nil {
			errMsg = streamErr.Error()
			log.Warnf("SSE stream passthrough error: %v", streamErr)
		}
		h.logger.RecordResultID(logID, model, resp.StatusCode, usage, 0, errMsg, "", "")
		return
	}

	respBody, _ := io.ReadAll(resp.Body)
	if renameTools {
		respBody = renameToolsInResponse(respBody)
	}
	_, _ = w.Write(respBody)
	usage := ParseClaudeUsage(respBody)
	errMsg := ""
	if resp.StatusCode >= 400 {
		errMsg = gjson.GetBytes(respBody, "error.message").String()
	}
	h.logger.RecordResultID(logID, model, resp.StatusCode, usage, 0, errMsg, "", string(respBody))
}

func applyDirectClaudeHeaders(req *http.Request, original *http.Request, auth *ProviderAuth, stream bool) {
	if auth.AuthType == AuthXAPIKey {
		req.Header.Set("x-api-key", auth.Token)
	} else {
		req.Header.Set("Authorization", "Bearer "+auth.Token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", headerOrDefault(original, "Anthropic-Version", "2023-06-01"))
	req.Header.Set("Anthropic-Dangerous-Direct-Browser-Access", "true")

	beta := original.Header.Get("Anthropic-Beta")
	if beta == "" {
		beta = defaultAnthropicBeta
	} else {
		beta = ensureAnthropicBeta(beta, "claude-code-20250219")
	}
	beta = ensureAnthropicBeta(beta, "oauth-2025-04-20")
	req.Header.Set("Anthropic-Beta", beta)

	req.Header.Set("X-App", "cli")
	req.Header.Set("User-Agent", "claude-cli/"+claudeCodeVersion+" (external, cli)")
	req.Header.Set("X-Stainless-Lang", "js")
	req.Header.Set("X-Stainless-Runtime", "node")
	req.Header.Set("X-Stainless-Runtime-Version", "v26.3.0")
	req.Header.Set("X-Stainless-Package-Version", "0.112.1")
	req.Header.Set("X-Stainless-Os", "MacOS")
	req.Header.Set("X-Stainless-Arch", "arm64")
	req.Header.Set("X-Stainless-Retry-Count", "0")
	req.Header.Set("X-Stainless-Timeout", "600")
	req.Header.Set("Connection", "keep-alive")
	for _, header := range claudeCodeForwardHeaders {
		if value := original.Header.Get(header); value != "" {
			req.Header.Set(header, value)
		}
	}

	if stream {
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("Accept-Encoding", "identity")
	} else {
		req.Header.Set("Accept", "application/json")
	}
}

// streamResponsePassthrough copies SSE stream without modification, capturing usage.
// Claude SSE streams emit usage in the "message_delta" event's data line, not the last data line.
// We scan every data line for usage and keep the best (non-zero) result.
func (h *ClaudeHandler) streamResponsePassthrough(w http.ResponseWriter, body io.Reader, renameTools bool) (TokenUsage, error) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		data, err := io.ReadAll(body)
		if renameTools {
			data = renameToolsInResponse(data)
		}
		if _, writeErr := w.Write(data); writeErr != nil {
			return ParseClaudeUsage(data), writeErr
		}
		return ParseClaudeUsage(data), err
	}

	var usage TokenUsage
	reader := bufio.NewReader(body)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			ending := line[len(line):]
			payload := line
			if bytes.HasSuffix(payload, []byte("\r\n")) {
				payload = payload[:len(payload)-2]
				ending = []byte("\r\n")
			} else if bytes.HasSuffix(payload, []byte("\n")) {
				payload = payload[:len(payload)-1]
				ending = []byte("\n")
			}
			if bytes.HasPrefix(payload, []byte("data: ")) {
				if u := ParseClaudeUsage(payload[len("data: "):]); u.InputTokens > 0 || u.OutputTokens > 0 || u.CacheReadTokens > 0 || u.CacheCreateTokens > 0 {
					usage = u
				}
			}
			if renameTools {
				payload = renameToolsInSSELine(payload)
			}
			if _, err := w.Write(append(payload, ending...)); err != nil {
				return usage, err
			}
			flusher.Flush()
		}
		if readErr != nil {
			if readErr == io.EOF {
				return usage, nil
			}
			return usage, readErr
		}
	}
}

func isStreamingRequest(r *http.Request, body []byte) bool {
	if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
		return true
	}
	if strings.Contains(r.Header.Get("X-Stainless-Helper-Method"), "stream") {
		return true
	}
	return bytes.Contains(body, []byte("\"stream\":true")) || bytes.Contains(body, []byte("\"stream\": true"))
}

func headerOrDefault(r *http.Request, key, fallback string) string {
	if v := r.Header.Get(key); v != "" {
		return v
	}
	return fallback
}

func ensureAnthropicBeta(beta, required string) string {
	for _, part := range strings.Split(beta, ",") {
		if strings.TrimSpace(part) == required {
			return beta
		}
	}
	if strings.TrimSpace(beta) == "" {
		return required
	}
	return beta + "," + required
}
