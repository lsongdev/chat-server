package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
)

type API struct {
	store  *Store
	hub    *Hub
	config Config
}

func NewAPI(store *Store, hub *Hub, cfg Config) *API {
	return &API{store: store, hub: hub, config: cfg}
}

func (a *API) Me(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	writeJSON(w, http.StatusOK, addUserAvatar(user))
}

func (a *API) ListConversations(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversations, err := a.store.ListConversations(r.Context(), user.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if conversations == nil {
		conversations = []Conversation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversations": conversations})
}

func (a *API) CreateConversation(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	var input struct {
		ID    uuid.UUID `json:"id"`
		Title string    `json:"title"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.ID == uuid.Nil || input.Title == "" || len([]rune(input.Title)) > 100 {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation", "conversation id and a title of at most 100 characters are required")
		return
	}
	conversation, event, err := a.store.CreateConversation(r.Context(), user.ID, input.ID, input.Title)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	a.hub.AddUserConversation(user.ID, conversation.ID)
	if event.ID != uuid.Nil {
		a.hub.BroadcastChanged(conversation.ID, event.Seq)
	}
	writeJSON(w, http.StatusCreated, conversation)
}

func (a *API) ListMembers(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	members, err := a.store.ListMembers(r.Context(), user.ID, conversationID)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	if members == nil {
		members = []Member{}
	}
	for index := range members {
		members[index] = addMemberAvatar(members[index])
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (a *API) AddMember(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	var input struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(w, r, &input, 8<<10); err != nil {
		return
	}
	email, ok := normalizeEmail(input.Email)
	if !ok {
		writeProblem(w, http.StatusBadRequest, "invalid_email", "enter a complete email address")
		return
	}
	member, event, err := a.store.AddMemberByEmail(r.Context(), user.ID, conversationID, email, a.config.MaxConversationMembers)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	a.hub.AddUserConversation(member.UserID, conversationID)
	a.hub.BroadcastChanged(conversationID, event.Seq)
	writeJSON(w, http.StatusCreated, addMemberAvatar(member))
}

func (a *API) RenameConversation(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	var input struct {
		Title string `json:"title"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len([]rune(input.Title)) > 100 {
		writeProblem(w, http.StatusBadRequest, "invalid_title", "conversation title is required and must not exceed 100 characters")
		return
	}
	event, err := a.store.RenameConversation(r.Context(), user.ID, conversationID, input.Title)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	a.hub.BroadcastChanged(conversationID, event.Seq)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) DeleteConversation(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	members, err := a.store.DeleteConversation(r.Context(), user.ID, conversationID)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	a.hub.BroadcastDeleted(conversationID)
	for _, memberID := range members {
		a.hub.RemoveUserConversation(memberID, conversationID)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) LeaveConversation(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	event, _, err := a.store.LeaveConversation(r.Context(), user.ID, conversationID)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	a.hub.BroadcastChanged(conversationID, event.Seq)
	a.hub.RemoveUserConversation(user.ID, conversationID)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) RemoveMember(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	targetID, err := uuid.Parse(mux.Vars(r)["userID"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_user_id", "user id is invalid")
		return
	}
	event, err := a.store.RemoveMember(r.Context(), user.ID, conversationID, targetID)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	a.hub.BroadcastChanged(conversationID, event.Seq)
	a.hub.RemoveUserConversation(targetID, conversationID)
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) UpdateMemberRole(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	targetID, err := uuid.Parse(mux.Vars(r)["userID"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_user_id", "user id is invalid")
		return
	}
	var input struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(w, r, &input, 4<<10); err != nil {
		return
	}
	member, event, err := a.store.UpdateMemberRole(r.Context(), user.ID, conversationID, targetID, input.Role)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	a.hub.BroadcastChanged(conversationID, event.Seq)
	writeJSON(w, http.StatusOK, addMemberAvatar(member))
}

func (a *API) ListContacts(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	contacts, err := a.store.ListContacts(r.Context(), user.ID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if contacts == nil {
		contacts = []Contact{}
	}
	for i := range contacts {
		contacts[i].AvatarURL = gravatarURL(contacts[i].Email, defaultAvatarSize)
	}
	writeJSON(w, http.StatusOK, map[string]any{"contacts": contacts})
}

func (a *API) SaveContact(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	var input struct {
		Name  string `json:"name"`
		Email string `json:"email"`
		Note  string `json:"note"`
	}
	if err := decodeJSON(w, r, &input, 16<<10); err != nil {
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Note = strings.TrimSpace(input.Note)
	email, ok := normalizeEmail(input.Email)
	if !ok || input.Name == "" || len([]rune(input.Name)) > 80 || len([]rune(input.Note)) > 1000 {
		writeProblem(w, http.StatusBadRequest, "invalid_contact", "contact name or email is invalid")
		return
	}
	contactID := uuid.New()
	if raw := mux.Vars(r)["contactID"]; raw != "" {
		var err error
		contactID, err = uuid.Parse(raw)
		if err != nil {
			writeProblem(w, http.StatusBadRequest, "invalid_contact_id", "contact id is invalid")
			return
		}
	}
	contact, err := a.store.SaveContact(r.Context(), user.ID, contactID, input.Name, email, input.Note)
	if err != nil {
		serverError(w, r, err)
		return
	}
	contact.AvatarURL = gravatarURL(contact.Email, defaultAvatarSize)
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	writeJSON(w, status, contact)
}

func (a *API) DeleteContact(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	contactID, err := uuid.Parse(mux.Vars(r)["contactID"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_contact_id", "contact id is invalid")
		return
	}
	if err := a.store.DeleteContact(r.Context(), user.ID, contactID); err != nil {
		handleStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) ListEvents(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	afterSeq, err := strconv.ParseInt(defaultString(r.URL.Query().Get("after_seq"), "0"), 10, 64)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_sequence", "after_seq is invalid")
		return
	}
	limit, _ := strconv.Atoi(defaultString(r.URL.Query().Get("limit"), "100"))
	events, err := a.store.ListEvents(r.Context(), user.ID, conversationID, afterSeq, limit)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	if events == nil {
		events = []Event{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (a *API) SendMessage(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	var input struct {
		ClientMessageID uuid.UUID       `json:"client_message_id"`
		Content         json.RawMessage `json:"content"`
	}
	if err := decodeJSON(w, r, &input, int64(a.config.MaxMessageBytes+4096)); err != nil {
		return
	}
	if input.ClientMessageID == uuid.Nil || !validMessageContent(input.Content, a.config.MaxMessageBytes) {
		writeProblem(w, http.StatusBadRequest, "invalid_message", "message is empty or too large")
		return
	}
	event, err := a.store.AppendMessage(r.Context(), user.ID, conversationID, input.ClientMessageID, input.Content)
	if err != nil {
		handleStoreError(w, r, err)
		return
	}
	a.hub.BroadcastChanged(conversationID, event.Seq)
	writeJSON(w, http.StatusCreated, event)
}

func (a *API) UpdateRead(w http.ResponseWriter, r *http.Request) {
	user, _ := currentUser(r.Context())
	conversationID, err := uuid.Parse(mux.Vars(r)["id"])
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_conversation_id", "conversation id is invalid")
		return
	}
	var input struct {
		Seq int64 `json:"seq"`
	}
	if err := decodeJSON(w, r, &input, 4<<10); err != nil {
		return
	}
	if err := a.store.UpdateRead(r.Context(), user.ID, conversationID, input.Seq); err != nil {
		handleStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func validMessageContent(raw json.RawMessage, maxBytes int) bool {
	if len(raw) == 0 || len(raw) > maxBytes {
		return false
	}
	var content struct {
		Type string          `json:"type"`
		Text string          `json:"text"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &content) != nil || len(content.Type) == 0 || len(content.Type) > 32 {
		return false
	}
	if content.Type == "text" {
		return strings.TrimSpace(content.Text) != ""
	}
	return len(content.Data) > 0 && string(content.Data) != "null"
}

func writeProblem(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, maxBytes int64) error {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeProblem(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Content-Type must be application/json")
		return errors.New("invalid content type")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body is invalid")
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeProblem(w, http.StatusBadRequest, "invalid_json", "request body must contain one JSON value")
		return errors.New("multiple JSON values")
	}
	return nil
}

func handleStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		writeProblem(w, http.StatusForbidden, "forbidden", "你没有执行此操作的权限")
	case errors.Is(err, ErrNotFound):
		writeProblem(w, http.StatusNotFound, "not_found", "未找到对应用户或资源")
	case errors.Is(err, ErrAmbiguousEmail):
		writeProblem(w, http.StatusConflict, "ambiguous_email", "该邮件地址对应多个用户，暂时无法添加")
	case errors.Is(err, ErrAlreadyMember):
		writeProblem(w, http.StatusConflict, "already_member", "该用户已经在会话中")
	case errors.Is(err, ErrMemberLimit):
		writeProblem(w, http.StatusConflict, "member_limit", "会话成员数量已达到上限")
	case errors.Is(err, ErrInvalidSequence):
		writeProblem(w, http.StatusBadRequest, "invalid_sequence", "消息序号无效")
	case errors.Is(err, ErrConflict):
		writeProblem(w, http.StatusConflict, "conflict", "当前状态不允许此操作")
	default:
		serverError(w, r, err)
	}
}

func normalizeEmail(value string) (string, bool) {
	email := strings.ToLower(strings.TrimSpace(value))
	if len(email) < 3 || len(email) > 254 || strings.IndexFunc(email, func(r rune) bool {
		return r <= 0x20 || r == 0x7f
	}) >= 0 {
		return "", false
	}
	at := strings.LastIndexByte(email, '@')
	if at < 1 || at > 64 || at == len(email)-1 || strings.Contains(email[:at], "@") {
		return "", false
	}
	local, domain := email[:at], email[at+1:]
	if strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") || strings.Contains(local, "..") ||
		!strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") || strings.Contains(domain, "..") {
		return "", false
	}
	return email, true
}

func serverError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeProblem(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "duration", time.Since(started).String())
	})
}

func securityHeaders(cfg Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws: wss:; img-src 'self' https: data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Request-ID", uuid.NewString())
		if cfg.CookieSecure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("panic in request", "method", r.Method, "path", r.URL.Path, "panic", fmt.Sprint(recovered))
				writeProblem(w, http.StatusInternalServerError, "internal_error", "request could not be completed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
