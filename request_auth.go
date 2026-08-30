package main

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

func bearerToken(r *http.Request) string {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(auth) > 7 && strings.EqualFold(auth[:7], "Bearer ") {
		return strings.TrimSpace(auth[7:])
	}
	return ""
}

func requestTokenMatches(r *http.Request, expected string) bool {
	if tokenMatches(strings.TrimSpace(r.Header.Get("x-api-key")), expected) {
		return true
	}
	return tokenMatches(bearerToken(r), expected)
}

func tokenMatches(actual, expected string) bool {
	if actual == "" || expected == "" || len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
