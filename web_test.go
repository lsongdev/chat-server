package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHomeRendersEmailLoginForAnonymousVisitor(t *testing.T) {
	web, err := NewWeb()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?return_to=%2Finvite%2Fexample", nil)
	web.Home(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("home returned %d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{"id=\"login-form\"", "id=\"name\"", "id=\"email\"", "使用 MyCenter 获取邮箱", "/invite/example"} {
		if !strings.Contains(body, expected) {
			t.Errorf("login page is missing %q", expected)
		}
	}
}

func TestHomeRendersChatForSignedInVisitor(t *testing.T) {
	web, err := NewWeb()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(context.WithValue(request.Context(), userContextKey, User{}))
	web.Home(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "class=\"app-shell\"") {
		t.Fatalf("signed-in home did not render the chat application")
	}
}

func TestInviteRedirectsAnonymousVisitorToEmailLogin(t *testing.T) {
	web, err := NewWeb()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	web.Invite(response, httptest.NewRequest(http.MethodGet, "/invite/example", nil))

	if response.Code != http.StatusFound || response.Header().Get("Location") != "/?return_to=%2Finvite%2Fexample" {
		t.Fatalf("unexpected invite redirect: %d %q", response.Code, response.Header().Get("Location"))
	}
}
