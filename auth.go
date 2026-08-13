package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type contextKey string

const userContextKey contextKey = "user"

type Auth struct {
	store       *Store
	config      Config
	provider    *oidc.Provider
	verifier    *oidc.IDTokenVerifier
	oauthConfig oauth2.Config
}

func NewAuth(ctx context.Context, store *Store, cfg Config) (*Auth, error) {
	provider, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	return &Auth{
		store:    store,
		config:   cfg,
		provider: provider,
		verifier: provider.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}),
		oauthConfig: oauth2.Config{
			ClientID:     cfg.OIDCClientID,
			ClientSecret: cfg.OIDCClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.OIDCRedirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
		},
	}, nil
}

func (a *Auth) Login(w http.ResponseWriter, r *http.Request) {
	returnTo := safeReturnTo(r.URL.Query().Get("return_to"))
	state, err := randomToken(32)
	if err != nil {
		serverError(w, r, err)
		return
	}
	nonce, err := randomToken(32)
	if err != nil {
		serverError(w, r, err)
		return
	}
	verifier := oauth2.GenerateVerifier()
	if err := a.store.SaveLoginAttempt(r.Context(), state, LoginAttempt{
		Nonce: nonce, CodeVerifier: verifier, ReturnTo: returnTo,
	}, time.Now().Add(10*time.Minute)); err != nil {
		serverError(w, r, err)
		return
	}
	redirect := a.oauthConfig.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.S256ChallengeOption(verifier),
	)
	http.Redirect(w, r, redirect, http.StatusFound)
}

func (a *Auth) Callback(w http.ResponseWriter, r *http.Request) {
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		http.Error(w, "login was not completed", http.StatusUnauthorized)
		return
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		http.Error(w, "invalid login callback", http.StatusBadRequest)
		return
	}
	attempt, err := a.store.ConsumeLoginAttempt(r.Context(), state)
	if err != nil {
		http.Error(w, "login attempt expired; please try again", http.StatusBadRequest)
		return
	}
	token, err := a.oauthConfig.Exchange(r.Context(), code, oauth2.VerifierOption(attempt.CodeVerifier))
	if err != nil {
		http.Error(w, "could not complete login", http.StatusUnauthorized)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		http.Error(w, "identity provider did not return an ID token", http.StatusUnauthorized)
		return
	}
	idToken, err := a.verifier.Verify(r.Context(), rawIDToken)
	if err != nil {
		http.Error(w, "invalid identity token", http.StatusUnauthorized)
		return
	}
	var claims OIDCClaims
	if err := idToken.Claims(&claims); err != nil || claims.Subject == "" {
		http.Error(w, "invalid identity claims", http.StatusUnauthorized)
		return
	}
	if subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(attempt.Nonce)) != 1 {
		http.Error(w, "invalid login nonce", http.StatusUnauthorized)
		return
	}
	if claims.Name == "" && claims.Username == "" && claims.Email == "" {
		userInfo, userInfoErr := a.provider.UserInfo(r.Context(), oauth2.StaticTokenSource(token))
		if userInfoErr == nil && userInfo.Subject == claims.Subject {
			var profile OIDCClaims
			if userInfo.Claims(&profile) == nil {
				profile.Subject = claims.Subject
				profile.Nonce = claims.Nonce
				claims = profile
			}
		}
	}
	email, ok := normalizeEmail(claims.Email)
	if !ok || !claims.EmailVerified {
		http.Error(w, "identity provider did not return a valid email", http.StatusUnauthorized)
		return
	}
	claims.Email = email
	user, err := a.store.UpsertOIDCUser(r.Context(), a.config.OIDCIssuer, claims)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if user.Status != "active" {
		http.Error(w, "account is unavailable", http.StatusForbidden)
		return
	}
	sessionToken, err := randomToken(32)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if err := a.store.CreateSession(r.Context(), user.ID, sessionToken, r.UserAgent(), clientIP(r, a.config.TrustProxyHeaders), time.Now().Add(a.config.SessionTTL)); err != nil {
		serverError(w, r, err)
		return
	}
	a.setSessionCookie(w, sessionToken, time.Now().Add(a.config.SessionTTL))
	http.Redirect(w, r, attempt.ReturnTo, http.StatusFound)
}

func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(a.sessionCookieName()); err == nil {
		_ = a.store.DeleteSession(r.Context(), cookie.Value)
	}
	a.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (a *Auth) Optional(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(a.sessionCookieName())
		if err == nil && cookie.Value != "" {
			user, lookupErr := a.store.UserBySession(r.Context(), cookie.Value)
			if lookupErr == nil {
				r = r.WithContext(context.WithValue(r.Context(), userContextKey, user))
			} else if !errors.Is(lookupErr, ErrNotFound) {
				serverError(w, r, lookupErr)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (a *Auth) Required(next http.Handler) http.Handler {
	return a.Optional(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := currentUser(r.Context()); !ok {
			writeProblem(w, http.StatusUnauthorized, "authentication_required", "sign in to continue")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

func (a *Auth) RequireSameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if _, ok := a.config.AllowedOrigins[origin]; !ok {
			writeProblem(w, http.StatusForbidden, "invalid_origin", "request origin is not allowed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func currentUser(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userContextKey).(User)
	return user, ok
}

func (a *Auth) setSessionCookie(w http.ResponseWriter, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name: a.sessionCookieName(), Value: token, Path: "/", Expires: expires,
		MaxAge: int(time.Until(expires).Seconds()), HttpOnly: true, Secure: a.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: a.sessionCookieName(), Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: a.config.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) sessionCookieName() string {
	if a.config.CookieSecure {
		return "__Host-chat_session"
	}
	return "chat_session"
}

func safeReturnTo(value string) string {
	if value == "" {
		return "/"
	}
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return u.RequestURI()
}

func randomToken(bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func clientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0]); net.ParseIP(forwarded) != nil {
			return forwarded
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return ""
}
