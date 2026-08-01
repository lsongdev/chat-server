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
