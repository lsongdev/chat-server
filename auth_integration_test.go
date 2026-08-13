package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"html"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
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
		OIDCRedirectURL: "http://chat.example/auth/callback", MobileAuthCallback: "flame://auth/callback", SessionTTL: time.Hour,
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

	verifier := strings.Repeat("a", 43)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	mobileLoginResponse := httptest.NewRecorder()
	auth.MobileLogin(mobileLoginResponse, httptest.NewRequest(
		http.MethodGet, "http://chat.example/auth/mobile/login?code_challenge="+challenge, nil))
	if mobileLoginResponse.Code != http.StatusFound {
		t.Fatalf("mobile login returned %d: %s", mobileLoginResponse.Code, mobileLoginResponse.Body.String())
	}
	mobileAuthorizeURL, err := url.Parse(mobileLoginResponse.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	loginNonce = mobileAuthorizeURL.Query().Get("nonce")
	mobileCallbackResponse := httptest.NewRecorder()
	auth.Callback(mobileCallbackResponse, httptest.NewRequest(
		http.MethodGet,
		"http://chat.example/auth/callback?code=valid-code&state="+url.QueryEscape(mobileAuthorizeURL.Query().Get("state")),
		nil,
	))
	if mobileCallbackResponse.Code != http.StatusOK || mobileCallbackResponse.Header().Get("Location") != "" {
		t.Fatalf("mobile callback did not end the OIDC redirect chain: status=%d location=%q",
			mobileCallbackResponse.Code, mobileCallbackResponse.Header().Get("Location"))
	}
	mobileCallbackBody := mobileCallbackResponse.Body.String()
	start := strings.Index(mobileCallbackBody, "flame://auth/callback?code=")
	if start < 0 {
		t.Fatalf("mobile callback does not contain the app handoff: %s", mobileCallbackBody)
	}
	end := strings.IndexAny(mobileCallbackBody[start:], "\"<")
	if end < 0 {
		t.Fatalf("mobile callback target is malformed: %s", mobileCallbackBody)
	}
	callbackURL, err := url.Parse(html.UnescapeString(mobileCallbackBody[start : start+end]))
	if err != nil || callbackURL.Scheme != "flame" || callbackURL.Query().Get("code") == "" {
		t.Fatalf("invalid mobile callback: %q %v", callbackURL, err)
	}
	mobileTokenResponse := httptest.NewRecorder()
	auth.MobileToken(mobileTokenResponse, httptest.NewRequest(
		http.MethodPost, "http://chat.example/auth/mobile/token",
		strings.NewReader(`{"code":"`+callbackURL.Query().Get("code")+`","code_verifier":"`+verifier+`"}`),
	))
	if mobileTokenResponse.Code != http.StatusOK {
		t.Fatalf("mobile token returned %d: %s", mobileTokenResponse.Code, mobileTokenResponse.Body.String())
	}
	if len(mobileTokenResponse.Result().Cookies()) == 0 {
		t.Fatal("mobile token exchange did not create a session cookie")
	}
}

func TestMobileHandoffEndsHTTPRedirectChain(t *testing.T) {
	response := httptest.NewRecorder()
	new(Auth).writeMobileHandoff(response, `flame://auth/callback?code=test-code`)

	if response.Code != http.StatusOK {
		t.Fatalf("handoff returned %d", response.Code)
	}
	if location := response.Header().Get("Location"); location != "" {
		t.Fatalf("handoff continued the HTTP redirect chain to %q", location)
	}
	body := response.Body.String()
	if !strings.Contains(body, `window.location.replace("flame://auth/callback?code=test-code")`) ||
		!strings.Contains(body, `href="flame://auth/callback?code=test-code"`) {
		t.Fatalf("handoff page is missing its automatic or manual app link: %s", body)
	}
}
