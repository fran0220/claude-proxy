package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseOAuthUsage(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "oauth_usage.json"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseOAuthUsage(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Session == nil || got.Session.Utilization == nil || *got.Session.Utilization != 6 {
		t.Fatalf("session=%+v", got.Session)
	}
	if got.Weekly == nil || got.Weekly.Utilization == nil || *got.Weekly.Utilization != 97 {
		t.Fatalf("weekly=%+v", got.Weekly)
	}
	if len(got.Limits) != 3 {
		t.Fatalf("limits=%d", len(got.Limits))
	}
	var fable *OAuthLimit
	for i := range got.Limits {
		if got.Limits[i].Kind == "weekly_scoped" {
			fable = &got.Limits[i]
		}
	}
	if fable == nil || fable.Scope == nil || fable.Scope.Model == nil || fable.Scope.Model.DisplayName != "Fable" || fable.Percent != 100 {
		t.Fatalf("fable limit=%+v", fable)
	}
	if got.ExtraUsage == nil || got.ExtraUsage.IsEnabled {
		t.Fatalf("extra=%+v", got.ExtraUsage)
	}
}
