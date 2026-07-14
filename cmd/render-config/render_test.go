package main

import "testing"

func TestIsPlaceholderAdmin(t *testing.T) {
	if !isPlaceholderAdmin("change-me") {
		t.Fatal("change-me should be placeholder")
	}
	if isPlaceholderAdmin("local-dev-admin-key-not-for-prod") {
		t.Fatal("local dev key should not be treated as placeholder")
	}
}
