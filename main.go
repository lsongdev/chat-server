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

type sessionId string

const userIDKey sessionId = "user_id"

type H map[string]interface{}

type Server struct {
	db  *sql.DB
	mux *http.ServeMux
}

func NewServer() (*Server, error) {
	s := &Server{
		mux: http.NewServeMux(),
	}
	if err := s.initDB(); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %v", err)
	}
	s.initRouter()
	return s, nil
}

func (s *Server) initDB() error {
	var err error
	s.db, err = sql.Open("sqlite3", "./chat.db")
	if err != nil {
		return err
	}
	// 创建表
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		email TEXT UNIQUE NOT NULL,
		password TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		UNIQUE (email)
	)`)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		token TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS conversations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		CREATE TABLE IF NOT EXISTS conversation_members (
			conversation_id INTEGER NOT NULL,
			user_id INTEGER NOT NULL,
			PRIMARY KEY (conversation_id, user_id),
			FOREIGN KEY (conversation_id) REFERENCES conversations(id),
			FOREIGN KEY (user_id) REFERENCES users(id)
		)
	`)
	if err != nil {
		log.Fatal(err)
	}
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		conversation_id INTEGER NOT NULL,
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

func (s *Server) initRouter() {
	s.mux.HandleFunc("/", s.homeView)
	s.mux.HandleFunc("/signup", s.registerUser)
	s.mux.HandleFunc("/login", s.loginUser)
	s.mux.HandleFunc("/logout", s.authMiddleware(s.logoutUser))
	s.mux.HandleFunc("/messages", s.authMiddleware(s.handleMessages))
	s.mux.HandleFunc("/conversations", s.authMiddleware(s.handleConversations))
}

func (s *Server) Start() error {
	server := &http.Server{
		Addr:         ":8080",
		Handler:      s.mux,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	log.Println("Starting server on :8080")
	return server.ListenAndServe()
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

type Conversation struct {
	ID      int64   `json:"id"`
	Name    string  `json:"name"`
	Members []int64 `json:"members"` // 会话成员的用户ID列表
}

type Message struct {
	ID             int64     `json:"id"`
	FromUserID     int64     `json:"from_user_id"`
	ConversationID int64     `json:"conversation_id"`
	Content        string    `json:"content"`
	CreatedAt      time.Time `json:"created_at"`
}

type User struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"` // 密码不会在JSON中返回
}

type Session struct {
	ID        int64     `json:"id"`
	Token     string    `json:"token"`
	UserID    int64     `json:"user_id"`
	CreatedAt time.Time `json:"created_at"`
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
		if r.Header.Get("Content-Type") == "application/json" {
			s.getConversations(w, r)
		} else {
			s.Render(w, "conversations", nil)
		}
	case http.MethodPost:
		s.createConversation(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	// userID := r.Context().Value(userIDKey).(int64)
	var conversation Conversation
	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&conversation); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	result, err := s.db.Exec("INSERT INTO conversations (name) VALUES (?)", conversation.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	conversation.ID, err = result.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, member := range conversation.Members {
		s.db.Exec("INSERT INTO conversation_members (conversation_id, user_id) VALUES (?, ?)", conversation.ID, member)
	}
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(conversation)
}

// 消息API处理函数
func (s *Server) getMessages(w http.ResponseWriter, r *http.Request) {
	// userID := r.Context().Value(userIDKey).(int64)
}

func (s *Server) createMessage(w http.ResponseWriter, r *http.Request) {
	// userID := r.Context().Value(userIDKey).(int64)
	var message Message
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
}

// 用户认证中间件
func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// get token from cookie and header
		var token string
		cookie, err := r.Cookie("token")
		if err != nil {
			authorization := r.Header.Get("Authorization")
			token = strings.TrimPrefix(authorization, "Bearer ")
		} else {
			token = cookie.Value
		}
		if token == "" {
			http.Error(w, "Missing authorization token", http.StatusUnauthorized)
			return
		}

		// 从token中获取用户信息
		var userID int64
		err = s.db.QueryRow("SELECT user_id FROM sessions WHERE token = ?", token).Scan(&userID)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		// 将用户ID添加到请求上下文
		ctx := context.WithValue(r.Context(), userIDKey, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// 用户API处理函数
func (s *Server) registerUser(w http.ResponseWriter, r *http.Request) {

	if r.Method == "GET" {
		s.Render(w, "signup", nil)
		return
	}

	var user User

	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		user.Name = r.FormValue("name")
		user.Email = r.FormValue("email")
		user.Password = r.FormValue("password")
	}

	// TODO: 对密码进行哈希处理
	result, err := s.db.Exec("INSERT INTO users (name, email, password) VALUES (?, ?, ?)",
		user.Name, user.Email, user.Password)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "Email already exists", http.StatusConflict)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	user.ID = id
	user.Password = "" // 不返回密码

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(user)
}

func (s *Server) loginUser(w http.ResponseWriter, r *http.Request) {

	if r.Method == "GET" {
		s.Render(w, "login", nil)
		return
	}

	var credentials User

	if r.Header.Get("Content-Type") == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		credentials.Email = r.FormValue("email")
		credentials.Password = r.FormValue("password")
	}

	var user User
	err := s.db.QueryRow("SELECT id, name, email, password FROM users WHERE email = ?",
		credentials.Email).Scan(&user.ID, &user.Name, &user.Email, &user.Password)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	// TODO: 验证密码哈希
	if user.Password != credentials.Password {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}

	// 生成会话token
	token := uuid.New().String()
	session := Session{
		UserID:    user.ID,
		Token:     token,
		CreatedAt: time.Now(),
	}

	result, err := s.db.Exec("INSERT INTO sessions (user_id, token, created_at) VALUES (?, ?, ?)",
		session.UserID, session.Token, session.CreatedAt.Format("2006-01-02 15:04:05"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	session.ID = id

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Set-Cookie", fmt.Sprintf("token=%s; Path=/; HttpOnly; SameSite=Lax", token))
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(session)
}

func (s *Server) logoutUser(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token == "" {
		http.Error(w, "Missing authorization token", http.StatusUnauthorized)
		return
	}

	_, err := s.db.Exec("DELETE FROM sessions WHERE token = ?", token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) homeView(w http.ResponseWriter, r *http.Request) {
	s.Render(w, "home", nil)
}

func (s *Server) getConversations(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(userIDKey).(int64)
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
			var userID int64
			if err := rows.Scan(&userID); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			conversation.Members = append(conversation.Members, userID)
		}
		conversations = append(conversations, conversation)
	}
	json.NewEncoder(w).Encode(conversations)
}

func main() {
	s, err := NewServer()
	if err != nil {
		log.Fatalf("Failed to initialize server: %v", err)
	}
	defer s.db.Close()

	// 启动服务器
	if err := s.Start(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
