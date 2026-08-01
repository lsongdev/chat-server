package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSON(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"chat"}`))
		request.Header.Set("Content-Type", "application/json")
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(recorder, request, &body, 1024); err != nil || body.Name != "chat" {
			t.Fatalf("decode failed: %v, %#v", err, body)
		}
	})

	t.Run("unknown field", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/", strings.NewReader(`{"unknown":true}`))
		request.Header.Set("Content-Type", "application/json")
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(recorder, request, &body, 1024); err == nil {
			t.Fatal("expected unknown field to be rejected")
		}
	})
}

func TestNormalizeEmail(t *testing.T) {
	valid := map[string]string{
		"User@Example.COM":   "user@example.com",
		" person@lsong.org ": "person@lsong.org",
	}
	for input, expected := range valid {
		actual, ok := normalizeEmail(input)
		if !ok || actual != expected {
			t.Fatalf("normalizeEmail(%q) = %q, %v", input, actual, ok)
		}
	}
	for _, input := range []string{"", "name", "@example.com", "name@localhost", "a..b@example.com", "Name <name@example.com>"} {
		if _, ok := normalizeEmail(input); ok {
			t.Fatalf("normalizeEmail accepted %q", input)
		}
	}
}
