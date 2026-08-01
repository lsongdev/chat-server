package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

func TestOIDCLoginFlow(t *testing.T) {
	databaseURL := os.Getenv("CHAT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CHAT_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := OpenStore(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var issuer string
	var loginNonce string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeJSON(w, http.StatusOK, map[string]any{
				"issuer": issuer, "authorization_endpoint": issuer + "/authorize",
				"token_endpoint": issuer + "/token", "jwks_uri": issuer + "/keys",
				"userinfo_endpoint":        issuer + "/userinfo",
				"response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
				"id_token_signing_alg_values_supported": []string{"ES256"},
				"token_endpoint_auth_methods_supported": []string{"client_secret_basic"},
				"code_challenge_methods_supported":      []string{"S256"},
			})
		case "/keys":
			writeJSON(w, http.StatusOK, map[string]any{"keys": []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "test-key", Algorithm: "ES256", Use: "sig"}}})
		case "/token":
			if err := r.ParseForm(); err != nil || r.Form.Get("code") != "valid-code" || r.Form.Get("code_verifier") == "" {
				http.Error(w, "invalid token request", http.StatusBadRequest)
				return
			}
			signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, new(jose.SignerOptions).WithType("JWT").WithHeader("kid", "test-key"))
			if err != nil {
				http.Error(w, "signer failed", http.StatusInternalServerError)
				return
			}
			now := time.Now()
			token, err := jwt.Signed(signer).Claims(jwt.Claims{
				Issuer: issuer, Subject: "oidc-user", Audience: jwt.Audience{"test-client"},
				IssuedAt: jwt.NewNumericDate(now), Expiry: jwt.NewNumericDate(now.Add(time.Hour)),
			}).Claims(map[string]any{
				"nonce": loginNonce, "name": "OIDC User", "preferred_username": "oidc-user",
				"email": "oidc@example.com", "email_verified": true,
			}).Serialize()
			if err != nil {
				http.Error(w, "token failed", http.StatusInternalServerError)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"access_token": "access", "token_type": "Bearer", "expires_in": 3600, "id_token": token})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	issuer = provider.URL

	baseURL, _ := url.Parse("http://chat.example")
	cfg := Config{
		BaseURL: baseURL, OIDCIssuer: issuer, OIDCClientID: "test-client", OIDCClientSecret: "test-secret",
		OIDCRedirectURL: "http://chat.example/auth/callback", SessionTTL: time.Hour,
		AllowedOrigins: map[string]struct{}{"http://chat.example": {}},
	}
	auth, err := NewAuth(ctx, store, cfg)
	if err != nil {
		t.Fatal(err)
	}

	loginResponse := httptest.NewRecorder()
	auth.Login(loginResponse, httptest.NewRequest(http.MethodGet, "http://chat.example/auth/login", nil))
	if loginResponse.Code != http.StatusFound {
		t.Fatalf("login returned %d", loginResponse.Code)
	}
	authorizeURL, err := url.Parse(loginResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	state := authorizeURL.Query().Get("state")
	loginNonce = authorizeURL.Query().Get("nonce")
	if state == "" || loginNonce == "" || authorizeURL.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("login redirect is missing OIDC protections: %s", authorizeURL)
	}

	callbackResponse := httptest.NewRecorder()
	callbackRequest := httptest.NewRequest(http.MethodGet, "http://chat.example/auth/callback?code=valid-code&state="+url.QueryEscape(state), nil)
	auth.Callback(callbackResponse, callbackRequest)
	if callbackResponse.Code != http.StatusFound {
		t.Fatalf("callback returned %d: %s", callbackResponse.Code, callbackResponse.Body.String())
	}
	var sessionValue string
	for _, cookie := range callbackResponse.Result().Cookies() {
		if cookie.Name == "chat_session" {
			sessionValue = cookie.Value
		}
	}
	if sessionValue == "" {
		t.Fatal("callback did not create a session cookie")
	}
	user, err := store.UserBySession(ctx, sessionValue)
	if err != nil || user.Subject != "oidc-user" || user.Email != "oidc@example.com" {
		t.Fatalf("session user mismatch: %#v %v", user, err)
	}
}
