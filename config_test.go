package main

import "testing"

func TestLoadConfig(t *testing.T) {
	t.Setenv("CHAT_BASE_URL", "https://chat.example.com")
	t.Setenv("DATABASE_URL", "postgres://example")
	t.Setenv("OIDC_CLIENT_ID", "client")
	t.Setenv("OIDC_CLIENT_SECRET", "secret")
	t.Setenv("ALLOWED_ORIGINS", "https://chat.example.com, https://admin.example.com")

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CookieSecure {
		t.Fatal("expected secure cookies for HTTPS base URL")
	}
	if cfg.OIDCRedirectURL != "https://chat.example.com/auth/callback" {
		t.Fatalf("unexpected redirect URL: %s", cfg.OIDCRedirectURL)
	}
	if _, ok := cfg.AllowedOrigins["https://admin.example.com"]; !ok {
		t.Fatal("second allowed origin was not parsed")
	}
}

func TestSafeReturnTo(t *testing.T) {
	tests := map[string]string{
		"":                         "/",
		"/chat/conversation-id?from=email": "/chat/conversation-id?from=email",
		"https://evil.example/":    "/",
		"//evil.example/path":      "/",
		"relative":                 "/",
	}
	for input, expected := range tests {
		if actual := safeReturnTo(input); actual != expected {
			t.Errorf("safeReturnTo(%q)=%q, want %q", input, actual, expected)
		}
	}
}
