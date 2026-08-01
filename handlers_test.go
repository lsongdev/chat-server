package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
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

func TestConversationTitleIsRequired(t *testing.T) {
	api := &API{}
	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		path    string
		vars    map[string]string
	}{
		{name: "create", handler: api.CreateConversation, path: "/api/conversations"},
		{name: "rename", handler: api.RenameConversation, path: "/api/conversations/00000000-0000-0000-0000-000000000001", vars: map[string]string{"id": "00000000-0000-0000-0000-000000000001"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.path, strings.NewReader(`{"title":"   "}`))
			request.Header.Set("Content-Type", "application/json")
			request = request.WithContext(context.WithValue(request.Context(), userContextKey, User{}))
			if test.vars != nil {
				request = mux.SetURLVars(request, test.vars)
			}
			response := httptest.NewRecorder()

			test.handler(response, request)

			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "required") {
				t.Fatalf("empty title returned %d: %s", response.Code, response.Body.String())
			}
		})
	}
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
