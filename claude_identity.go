package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"

	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// stableBuildHash is generated once per process to keep billing headers stable
// across requests, which is essential for Anthropic prompt caching.
var (
	stableBuildHash     string
	stableBuildHashOnce sync.Once
)

func getStableBuildHash() string {
	stableBuildHashOnce.Do(func() {
		b := make([]byte, 2)
		_, _ = rand.Read(b)
		stableBuildHash = hex.EncodeToString(b)[:3]
	})
	return stableBuildHash
}

// toolRenames maps tool names that conflict with Claude Code built-in tools.
var toolRenames = map[string]string{
	"glob": "file_glob",
}

// injectClaudeCodeIdentity prepends the Claude Code billing header and agent
// identifier to the system prompt, and injects a user_id into metadata.
// Uses stable buildHash (per process) and stable userID (persisted in config)
// to preserve Anthropic prompt caching across requests.
func injectClaudeCodeIdentity(body []byte, stableUserID string) []byte {
	firstText := gjson.GetBytes(body, "system.0.text").String()
	if strings.HasPrefix(firstText, "x-anthropic-billing-header:") {
		return body
	}

	// Use a fixed cch value — the original sha256(body) was deterministic per-request
	// but the billing header is prepended before the system prompt, so the cch doesn't
	// need to be body-dependent. A stable value preserves cache hits.
	buildHash := getStableBuildHash()
	billingText := fmt.Sprintf("x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=cli; cch=00000;", claudeCodeVersion, buildHash)
	billingBlock := fmt.Sprintf(`{"type":"text","text":"%s"}`, billingText)

	agentBlock := `{"type":"text","text":"You are a Claude agent, built on Anthropic's Claude Agent SDK."}`

	system := gjson.GetBytes(body, "system")
	var newSystem string

	if system.IsArray() {
		newSystem = "[" + billingBlock + "," + agentBlock
		system.ForEach(func(_, part gjson.Result) bool {
			newSystem += "," + part.Raw
			return true
		})
		newSystem += "]"
	} else if system.Type == gjson.String && system.String() != "" {
		textBlock, _ := sjson.Set(`{"type":"text"}`, "text", system.String())
		newSystem = "[" + billingBlock + "," + agentBlock + "," + textBlock + "]"
	} else {
		newSystem = "[" + billingBlock + "," + agentBlock + "]"
	}

	body, _ = sjson.SetRawBytes(body, "system", []byte(newSystem))

	existingUserID := gjson.GetBytes(body, "metadata.user_id").String()
	if existingUserID == "" || !isValidClaudeUserID(existingUserID) {
		body, _ = sjson.SetBytes(body, "metadata.user_id", stableUserID)
	}

	return body
}

func generateClaudeUserID() string {
	hexBytes := make([]byte, 32)
	_, _ = rand.Read(hexBytes)
	hexPart := hex.EncodeToString(hexBytes)
	return "user_" + hexPart + "_account_" + newUUID() + "_session_" + newUUID()
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func isValidClaudeUserID(id string) bool {
	return strings.HasPrefix(id, "user_") && strings.Contains(id, "_account_") && strings.Contains(id, "_session_")
}

// renameConflictingTools renames tools that conflict with Claude Code built-in tools.
func renameConflictingTools(body []byte) ([]byte, bool) {
	tools := gjson.GetBytes(body, "tools")
	if !tools.Exists() || !tools.IsArray() {
		return body, false
	}

	modified := false
	tools.ForEach(func(index, tool gjson.Result) bool {
		name := tool.Get("name").String()
		if newName, ok := toolRenames[name]; ok {
			path := fmt.Sprintf("tools.%d.name", index.Int())
			body, _ = sjson.SetBytes(body, path, newName)
			modified = true
		}
		return true
	})

	if tcName := gjson.GetBytes(body, "tool_choice.name").String(); tcName != "" {
		if newName, ok := toolRenames[tcName]; ok {
			body, _ = sjson.SetBytes(body, "tool_choice.name", newName)
			modified = true
		}
	}

	if modified {
		log.Debugf("renamed conflicting tools in request")
	}
	return body, modified
}

// renameToolsInResponse restores original tool names in responses.
func renameToolsInResponse(data []byte) []byte {
	for original, renamed := range toolRenames {
		content := gjson.GetBytes(data, "content")
		if content.Exists() && content.IsArray() {
			content.ForEach(func(index, block gjson.Result) bool {
				if block.Get("type").String() == "tool_use" && block.Get("name").String() == renamed {
					path := fmt.Sprintf("content.%d.name", index.Int())
					data, _ = sjson.SetBytes(data, path, original)
				}
				return true
			})
		}
	}
	return data
}

// renameToolsInSSELine restores tool names in a single SSE stream line.
func renameToolsInSSELine(line []byte) []byte {
	if !bytes.HasPrefix(line, []byte("data: ")) {
		return line
	}
	payload := line[len("data: "):]
	if bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		return line
	}
	for original, renamed := range toolRenames {
		if gjson.GetBytes(payload, "content_block.type").String() == "tool_use" && gjson.GetBytes(payload, "content_block.name").String() == renamed {
			updated, err := sjson.SetBytes(payload, "content_block.name", original)
			if err == nil {
				return append([]byte("data: "), updated...)
			}
		}
	}
	return line
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
