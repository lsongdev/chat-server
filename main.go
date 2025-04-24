package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

type Conversation struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Members []string `json:"members"`
}

type Message struct {
	ID             string    `json:"id"`
	FromUser       string    `json:"user_id"`
	ConversationID string    `json:"conversation_id"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

type sessionId string

const userIDKey sessionId = "user_id"

type H map[string]any

type Server struct {
	db *sql.DB
}

func NewServer() (*Server, error) {
	s := &Server{}
	if err := s.initDB(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %v", err)
	}
	return s, nil
}

func (s *Server) initDB() error {
	var err error
	s.db, err = sql.Open("sqlite3", "./chat.db")
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS conversations (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS conversation_members (
			conversation_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			PRIMARY KEY (conversation_id, user_id),
			FOREIGN KEY (conversation_id) REFERENCES conversations(id),
			FOREIGN KEY (user_id) REFERENCES users(id)
		)
	`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		conversation_id TEXT NOT NULL,
		content TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (conversation_id) REFERENCES conversations(id),
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`)
	if err != nil {
		return err
	}
	return nil
}

// Render renders an HTML template with the provided data.
func (reader *Server) Render(w http.ResponseWriter, templateName string, data H) {
	tmpl, err := template.ParseFiles("templates/layout.html", "templates/"+templateName+".html")
	// Parse templates from embedded file system
	// tmpl, err := template.New("").ParseFS(templates.Files, "layout.html", templateName+".html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Execute "index.html" within the layout and write to response
	err = tmpl.ExecuteTemplate(w, "layout", data)
	if err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var token string
		cookie, err := r.Cookie("token")
		if err == nil {
			token = cookie.Value
		}
		if token == "" {
			authorization := r.Header.Get("Authorization")
			token = strings.TrimPrefix(authorization, "Bearer ")
		}
		var email string = "song940@gmail.com"
		if r.URL.Query().Has("user_id") {
			email = r.URL.Query().Get("user_id")
		}
		if token == "" && email == "" {
			http.Error(w, "Missing authorization token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userIDKey, email)))
	}
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getMessages(w, r)
	case http.MethodPost:
		s.createMessage(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConversations(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.getConversations(w, r)
	case http.MethodPost:
		s.createConversation(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	var conversation Conversation
	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&conversation); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		r.ParseForm()
		conversation.ID = uuid.New().String()
		conversation.Name = r.FormValue("name")
		conversation.Members = strings.Split(r.FormValue("members"), ",")
	}
	result, err := s.db.Exec("INSERT INTO conversations (id, name) VALUES (?, ?)", conversation.ID, conversation.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Println("createConversation", id)
	for _, member := range conversation.Members {
		s.db.Exec("INSERT INTO conversation_members (conversation_id, user_id) VALUES (?, ?)", conversation.ID, member)
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(conversation)
}

func (s *Server) getMessages(w http.ResponseWriter, r *http.Request) {
	email := r.Context().Value(userIDKey).(string)
	var limit int = 10
	var lastSinceId string
	if r.URL.Query().Has("offset") {
		lastSinceId = r.URL.Query().Get("offset")
	}
	rows, err := s.db.Query(`select * from messages where conversation_id in (select conversation_id from conversation_members where user_id = ?) and created_at > ? order by created_at limit ?`, email, lastSinceId, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	messages := []Message{}
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.ID, &message.FromUser, &message.ConversationID, &message.Content, &message.CreatedAt); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		messages = append(messages, message)
	}
	json.NewEncoder(w).Encode(messages)
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) {
	var message Message
	email := r.Context().Value(userIDKey).(string)
	message.FromUser = email
	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		r.ParseForm()
		message.ID = uuid.New().String()
		message.ConversationID = r.FormValue("conversation_id")
		message.Content = r.FormValue("content")
	}
	result, err := s.db.Exec("INSERT INTO messages (id, user_id, conversation_id, content) VALUES (?, ?, ?, ?)", message.ID, message.FromUser, message.ConversationID, message.Content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	id, err := result.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	log.Println("createMessage", id)
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(message)
}

func (s *Server) homeView(w http.ResponseWriter, r *http.Request) {
	s.Render(w, "home", nil)
}

func (s *Server) getConversations(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(userIDKey).(string)
	rows, err := s.db.Query(`SELECT id, name FROM conversations WHERE id IN (SELECT conversation_id FROM conversation_members WHERE user_id = ?)`, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	conversations := []Conversation{}
	for rows.Next() {
		var conversation Conversation
		if err := rows.Scan(&conversation.ID, &conversation.Name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rows, err := s.db.Query(`SELECT user_id FROM conversation_members WHERE conversation_id = ?`, conversation.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var email string
			if err := rows.Scan(&email); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			conversation.Members = append(conversation.Members, email)
		}
		conversations = append(conversations, conversation)
	}
	if r.Header.Get("Content-Type") == "application/json" {
		json.NewEncoder(w).Encode(conversations)
	} else {
		conversationId := r.FormValue("id")
		s.Render(w, "conversations", H{
			"Conversations":  conversations,
			"ConversationId": conversationId,
		})
	}
}

func main() {
	s, err := NewServer()
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}
	defer s.db.Close()
	http.HandleFunc("/", s.homeView)
	http.HandleFunc("/messages", s.authMiddleware(s.handleMessages))
	http.HandleFunc("/conversations", s.authMiddleware(s.handleConversations))
	http.ListenAndServe(":8080", nil)
}
