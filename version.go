package main

var version = "dev"

// claudeCodeVersion is the Claude Code protocol version this proxy emulates.
// Anthropic uses it in both HTTP and billing identity headers to gate models.
const claudeCodeVersion = "2.1.257"
