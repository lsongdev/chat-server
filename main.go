package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
)

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := LoadConfig()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	store, err := OpenStore(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		return err
	}
	auth, err := NewAuth(ctx, store, cfg)
	if err != nil {
		return err
	}
	web, err := NewWeb()
	if err != nil {
		return err
	}
	hub := NewHub()
	limiter := NewRateLimiter()
	api := NewAPI(store, hub, cfg)
	ws := NewWebSocketHandler(store, hub, cfg)

	router := mux.NewRouter()
	router.Handle("/", auth.Optional(http.HandlerFunc(web.Home))).Methods(http.MethodGet)
	router.Handle("/invite/{token}", auth.Optional(http.HandlerFunc(web.Invite))).Methods(http.MethodGet)
	router.Handle("/auth/login", limiter.LoginMiddleware(cfg, http.HandlerFunc(auth.Login))).Methods(http.MethodGet)
	router.HandleFunc("/auth/callback", auth.Callback).Methods(http.MethodGet)
	router.Handle("/auth/logout", auth.Required(auth.RequireSameOrigin(http.HandlerFunc(auth.Logout)))).Methods(http.MethodPost)

	protected := router.PathPrefix("/api").Subrouter()
	protected.Use(auth.Required, auth.RequireSameOrigin, limiter.MutationMiddleware)
	protected.HandleFunc("/me", api.Me).Methods(http.MethodGet)
	protected.HandleFunc("/conversations", api.ListConversations).Methods(http.MethodGet)
	protected.HandleFunc("/conversations", api.CreateConversation).Methods(http.MethodPost)
	protected.HandleFunc("/conversations/{id}", api.RenameConversation).Methods(http.MethodPatch)
	protected.HandleFunc("/conversations/{id}", api.LeaveConversation).Methods(http.MethodDelete)
	protected.HandleFunc("/conversations/{id}/members", api.ListMembers).Methods(http.MethodGet)
	protected.HandleFunc("/conversations/{id}/members/{userID}", api.RemoveMember).Methods(http.MethodDelete)
	protected.HandleFunc("/conversations/{id}/events", api.ListEvents).Methods(http.MethodGet)
	protected.HandleFunc("/conversations/{id}/messages", api.SendMessage).Methods(http.MethodPost)
	protected.HandleFunc("/conversations/{id}/read", api.UpdateRead).Methods(http.MethodPost)
	protected.HandleFunc("/conversations/{id}/invites", api.CreateInvite).Methods(http.MethodPost)
	protected.HandleFunc("/invites/{token}/accept", api.AcceptInvite).Methods(http.MethodPost)
	router.Handle("/ws", auth.Required(ws)).Methods(http.MethodGet)
	router.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }).Methods(http.MethodGet)
	router.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := store.Ping(ctx); err != nil {
			writeProblem(w, http.StatusServiceUnavailable, "not_ready", "database is unavailable")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}).Methods(http.MethodGet)

	server := &http.Server{
		Addr: cfg.HTTPAddr, Handler: securityHeaders(cfg, recoverer(requestLogger(router))),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 90 * time.Second,
	}
	serverErrors := make(chan error, 1)
	go cleanupLoop(ctx, store)
	go func() {
		slog.Info("chat server listening", "address", cfg.HTTPAddr, "base_url", cfg.BaseURL.String())
		serverErrors <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	case err := <-serverErrors:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func cleanupLoop(ctx context.Context, store *Store) {
	if err := store.CleanupExpired(ctx); err != nil {
		slog.Warn("initial cleanup failed", "error", err)
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := store.CleanupExpired(cleanupCtx)
			cancel()
			if err != nil {
				slog.Warn("expired record cleanup failed", "error", err)
			}
		}
	}
}
