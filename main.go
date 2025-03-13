package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
)

// 数据结构定义
type Contact struct {
	ID     int64  `json:"id"`
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Email  string `json:"email"`
	Remark string `json:"remark"`
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
	UserID    int64     `json:"user_id"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

// 全局变量
var (
	db *sql.DB
	// 消息通道映射，key为conversationID
	messageChannels     = make(map[int64][]chan Message)
	messageChannelMutex sync.RWMutex
)

func initDB() error {
	var err error
	// 打开SQLite数据库连接
	db, err = sql.Open("sqlite3", "./chat.db")
	if err != nil {
		return err
	}

	// 创建表
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS contacts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		name TEXT NOT NULL,
		email TEXT UNIQUE NOT NULL,
		remark TEXT,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id),
		UNIQUE (user_id, email)
	)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS conversations (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		members TEXT NOT NULL, -- 以逗号分隔的用户ID
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		from_user_id INTEGER NOT NULL,
		conversation_id INTEGER NOT NULL,
		content TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (conversation_id) REFERENCES conversations(id)
	)`)
	if err != nil {
		return err
	}

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS users (
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

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		token TEXT NOT NULL,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY (user_id) REFERENCES users(id)
	)`)
	if err != nil {
		return err
	}

	return nil
}

// 联系人API处理函数
func getContacts(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)

	rows, err := db.Query("SELECT id, user_id, name, email, remark FROM contacts WHERE user_id = ?", userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	contacts := []Contact{}
	for rows.Next() {
		var contact Contact
		if err := rows.Scan(&contact.ID, &contact.UserID, &contact.Name, &contact.Email, &contact.Remark); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		contacts = append(contacts, contact)
	}

	json.NewEncoder(w).Encode(contacts)
}

func getContact(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid contact ID", http.StatusBadRequest)
		return
	}

	var contact Contact
	err = db.QueryRow("SELECT id, user_id, name, email, remark FROM contacts WHERE id = ? AND user_id = ?", id, userID).
		Scan(&contact.ID, &contact.UserID, &contact.Name, &contact.Email, &contact.Remark)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Contact not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	json.NewEncoder(w).Encode(contact)
}

func createContact(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)
	var contact Contact
	if err := json.NewDecoder(r.Body).Decode(&contact); err != nil {
		log.Println(err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	contact.UserID = userID
	result, err := db.Exec("INSERT INTO contacts (user_id, name, email, remark) VALUES (?, ?, ?, ?)",
		contact.UserID, contact.Name, contact.Email, contact.Remark)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	contact.ID = id
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(contact)
}

func updateContact(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid contact ID", http.StatusBadRequest)
		return
	}

	// 验证联系人是否属于当前用户
	var exists bool
	err = db.QueryRow("SELECT EXISTS(SELECT 1 FROM contacts WHERE id = ? AND user_id = ?)", id, userID).Scan(&exists)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !exists {
		http.Error(w, "Contact not found", http.StatusNotFound)
		return
	}

	var contact Contact
	if err := json.NewDecoder(r.Body).Decode(&contact); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	contact.ID = id
	contact.UserID = userID

	_, err = db.Exec("UPDATE contacts SET name = ?, email = ?, remark = ? WHERE id = ? AND user_id = ?",
		contact.Name, contact.Email, contact.Remark, id, userID)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			http.Error(w, "Email already exists", http.StatusConflict)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	json.NewEncoder(w).Encode(contact)
}

func deleteContact(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid contact ID", http.StatusBadRequest)
		return
	}

	result, err := db.Exec("DELETE FROM contacts WHERE id = ? AND user_id = ?", id, userID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rowsAffected == 0 {
		http.Error(w, "Contact not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// 会话API处理函数
func getConversations(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)

	rows, err := db.Query("SELECT id, name, members FROM conversations")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	conversations := []Conversation{}
	for rows.Next() {
		var conversation Conversation
		var membersStr string
		if err := rows.Scan(&conversation.ID, &conversation.Name, &membersStr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 解析members字符串为用户ID切片
		memberStrs := strings.Split(membersStr, ",")
		conversation.Members = make([]int64, 0, len(memberStrs))
		userInConversation := false
		for _, memberStr := range memberStrs {
			memberID, err := strconv.ParseInt(memberStr, 10, 64)
			if err != nil {
				continue
			}
			conversation.Members = append(conversation.Members, memberID)
			if memberID == userID {
				userInConversation = true
			}
		}

		// 只返回用户所在的会话
		if userInConversation {
			conversations = append(conversations, conversation)
		}
	}

	json.NewEncoder(w).Encode(conversations)
}

func getConversation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid conversation ID", http.StatusBadRequest)
		return
	}

	var conversation Conversation
	var membersStr string
	err = db.QueryRow("SELECT id, name, members FROM conversations WHERE id = ?", id).
		Scan(&conversation.ID, &conversation.Name, &membersStr)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	// 解析members字符串为用户ID切片
	memberStrs := strings.Split(membersStr, ",")
	conversation.Members = make([]int64, 0, len(memberStrs))
	for _, memberStr := range memberStrs {
		memberID, err := strconv.ParseInt(memberStr, 10, 64)
		if err != nil {
			continue
		}
		conversation.Members = append(conversation.Members, memberID)
	}

	json.NewEncoder(w).Encode(conversation)
}

func createConversation(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)

	var conversation Conversation
	if err := json.NewDecoder(r.Body).Decode(&conversation); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 确保创建者在会话成员中
	hasCreator := false
	for _, memberID := range conversation.Members {
		if memberID == userID {
			hasCreator = true
			break
		}
	}
	if !hasCreator {
		conversation.Members = append(conversation.Members, userID)
	}

	if len(conversation.Members) < 2 {
		http.Error(w, "Conversation must have at least two members", http.StatusBadRequest)
		return
	}

	// 将members切片转为逗号分隔的字符串
	memberStrs := make([]string, len(conversation.Members))
	for i, memberID := range conversation.Members {
		memberStrs[i] = strconv.FormatInt(memberID, 10)
	}
	membersStr := strings.Join(memberStrs, ",")

	result, err := db.Exec("INSERT INTO conversations (name, members) VALUES (?, ?)",
		conversation.Name, membersStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	conversation.ID = id

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(conversation)
}

func updateConversation(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid conversation ID", http.StatusBadRequest)
		return
	}

	// 检查用户是否在原会话中
	var oldMembersStr string
	err = db.QueryRow("SELECT members FROM conversations WHERE id = ?", id).Scan(&oldMembersStr)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	oldMembers := strings.Split(oldMembersStr, ",")
	userInConversation := false
	for _, memberStr := range oldMembers {
		memberID, err := strconv.ParseInt(memberStr, 10, 64)
		if err != nil {
			continue
		}
		if memberID == userID {
			userInConversation = true
			break
		}
	}

	if !userInConversation {
		http.Error(w, "You are not a member of this conversation", http.StatusForbidden)
		return
	}

	var conversation Conversation
	if err := json.NewDecoder(r.Body).Decode(&conversation); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	conversation.ID = id

	// 如果没有成员了，删除会话
	if len(conversation.Members) == 0 {
		tx, err := db.Begin()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer tx.Rollback()

		// 删除消息
		_, err = tx.Exec("DELETE FROM messages WHERE conversation_id = ?", id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// 删除会话
		_, err = tx.Exec("DELETE FROM conversations WHERE id = ?", id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if err := tx.Commit(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}

	// 将members切片转为逗号分隔的字符串
	memberStrs := make([]string, len(conversation.Members))
	for i, memberID := range conversation.Members {
		memberStrs[i] = strconv.FormatInt(memberID, 10)
	}
	membersStr := strings.Join(memberStrs, ",")

	_, err = db.Exec("UPDATE conversations SET name = ?, members = ? WHERE id = ?",
		conversation.Name, membersStr, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(conversation)
}

func deleteConversation(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id, err := strconv.ParseInt(vars["id"], 10, 64)
	if err != nil {
		http.Error(w, "Invalid conversation ID", http.StatusBadRequest)
		return
	}

	// 删除会话之前先删除相关的消息通道
	messageChannelMutex.Lock()
	delete(messageChannels, id)
	messageChannelMutex.Unlock()

	// 开始事务
	tx, err := db.Begin()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	// 删除会话相关的消息
	_, err = tx.Exec("DELETE FROM messages WHERE conversation_id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 删除会话
	result, err := tx.Exec("DELETE FROM conversations WHERE id = ?", id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if rowsAffected == 0 {
		http.Error(w, "Conversation not found", http.StatusNotFound)
		return
	}

	// 提交事务
	if err := tx.Commit(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// 消息API处理函数
func getMessages(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)
	conversationID := r.URL.Query().Get("conversation_id")
	if conversationID == "" {
		http.Error(w, "Missing conversation_id parameter", http.StatusBadRequest)
		return
	}

	convID, err := strconv.ParseInt(conversationID, 10, 64)
	if err != nil {
		http.Error(w, "Invalid conversation_id", http.StatusBadRequest)
		return
	}

	// 验证用户是否在会话中
	var membersStr string
	err = db.QueryRow("SELECT members FROM conversations WHERE id = ?", convID).Scan(&membersStr)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	memberStrs := strings.Split(membersStr, ",")
	userInConversation := false
	for _, memberStr := range memberStrs {
		memberID, err := strconv.ParseInt(memberStr, 10, 64)
		if err != nil {
			continue
		}
		if memberID == userID {
			userInConversation = true
			break
		}
	}

	if !userInConversation {
		http.Error(w, "You are not a member of this conversation", http.StatusForbidden)
		return
	}

	lastID := r.URL.Query().Get("last_id")
	var lastIDInt int64 = 0
	if lastID != "" {
		lastIDInt, err = strconv.ParseInt(lastID, 10, 64)
		if err != nil {
			http.Error(w, "Invalid last_id", http.StatusBadRequest)
			return
		}
	}

	// 查询最新消息
	rows, err := db.Query(`
		SELECT id, from_user_id, conversation_id, content, created_at 
		FROM messages 
		WHERE conversation_id = ? AND id > ? 
		ORDER BY id ASC`,
		convID, lastIDInt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	messages := []Message{}
	for rows.Next() {
		var message Message
		var createdAtStr string
		if err := rows.Scan(&message.ID, &message.FromUserID, &message.ConversationID, &message.Content, &createdAtStr); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		message.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", createdAtStr)
		messages = append(messages, message)
	}

	// 如果有消息，直接返回
	if len(messages) > 0 {
		json.NewEncoder(w).Encode(messages)
		return
	}

	// 如果没有消息，开始长轮询
	timeout := 30 * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	msgChan := make(chan Message)

	messageChannelMutex.Lock()
	if _, exists := messageChannels[convID]; !exists {
		messageChannels[convID] = []chan Message{}
	}
	messageChannels[convID] = append(messageChannels[convID], msgChan)
	messageChannelMutex.Unlock()

	defer func() {
		messageChannelMutex.Lock()
		channels := messageChannels[convID]
		for i, ch := range channels {
			if ch == msgChan {
				messageChannels[convID] = append(channels[:i], channels[i+1:]...)
				break
			}
		}
		if len(messageChannels[convID]) == 0 {
			delete(messageChannels, convID)
		}
		messageChannelMutex.Unlock()
		close(msgChan)
	}()

	select {
	case <-ctx.Done():
		json.NewEncoder(w).Encode([]Message{})
	case message := <-msgChan:
		json.NewEncoder(w).Encode([]Message{message})
	}
}

func createMessage(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value("user_id").(int64)

	var message Message
	if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 设置发送者ID
	message.FromUserID = userID

	// 验证会话是否存在且用户是否在会话中
	var membersStr string
	err := db.QueryRow("SELECT members FROM conversations WHERE id = ?", message.ConversationID).Scan(&membersStr)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "Conversation not found", http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}

	memberStrs := strings.Split(membersStr, ",")
	userInConversation := false
	for _, memberStr := range memberStrs {
		memberID, err := strconv.ParseInt(memberStr, 10, 64)
		if err != nil {
			continue
		}
		if memberID == userID {
			userInConversation = true
			break
		}
	}

	if !userInConversation {
		http.Error(w, "You are not a member of this conversation", http.StatusForbidden)
		return
	}

	// 设置创建时间
	message.CreatedAt = time.Now()

	// 将消息插入数据库
	result, err := db.Exec(`
		INSERT INTO messages (from_user_id, conversation_id, content, created_at) 
		VALUES (?, ?, ?, ?)`,
		message.FromUserID, message.ConversationID, message.Content, message.CreatedAt.Format("2006-01-02 15:04:05"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	id, err := result.LastInsertId()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	message.ID = id

	// 向所有等待中的通道发送消息
	messageChannelMutex.RLock()
	if channels, exists := messageChannels[message.ConversationID]; exists {
		for _, ch := range channels {
			select {
			case ch <- message:
			default:
			}
		}
	}
	messageChannelMutex.RUnlock()

	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(message)
}

// 用户认证中间件
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authorization := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authorization, "Bearer ")
		if token == "" {
			http.Error(w, "Missing authorization token", http.StatusUnauthorized)
			return
		}

		// 从token中获取用户信息
		var userID int64
		err := db.QueryRow("SELECT user_id FROM sessions WHERE token = ?", token).Scan(&userID)
		if err != nil {
			if err == sql.ErrNoRows {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
			} else {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
			return
		}
		// 将用户ID添加到请求上下文
		ctx := context.WithValue(r.Context(), "user_id", userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// 用户API处理函数
func registerUser(w http.ResponseWriter, r *http.Request) {
	var user User
	if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// TODO: 对密码进行哈希处理
	result, err := db.Exec("INSERT INTO users (name, email, password) VALUES (?, ?, ?)",
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

func loginUser(w http.ResponseWriter, r *http.Request) {
	var credentials User
	if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var user User
	err := db.QueryRow("SELECT id, name, email, password FROM users WHERE email = ?",
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
	token := fmt.Sprintf("%d-%d", user.ID, time.Now().UnixNano()) // 简单的token生成方式，实际应使用更安全的方法
	session := Session{
		UserID:    user.ID,
		Token:     token,
		CreatedAt: time.Now(),
	}

	result, err := db.Exec("INSERT INTO sessions (user_id, token, created_at) VALUES (?, ?, ?)",
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

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(session)
}

func logoutUser(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if token == "" {
		http.Error(w, "Missing authorization token", http.StatusUnauthorized)
		return
	}

	_, err := db.Exec("DELETE FROM sessions WHERE token = ?", token)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func main() {
	// 初始化数据库
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	// 创建路由器
	r := mux.NewRouter()

	// 用户API
	r.HandleFunc("/users/register", registerUser).Methods("POST")
	r.HandleFunc("/users/login", loginUser).Methods("POST")
	r.HandleFunc("/users/logout", authMiddleware(logoutUser)).Methods("POST")

	// 使用认证中间件保护其他API
	r.HandleFunc("/contacts", authMiddleware(getContacts)).Methods("GET")
	r.HandleFunc("/contacts/{id}", authMiddleware(getContact)).Methods("GET")
	r.HandleFunc("/contacts", authMiddleware(createContact)).Methods("POST")
	r.HandleFunc("/contacts/{id}", authMiddleware(updateContact)).Methods("PUT")
	r.HandleFunc("/contacts/{id}", authMiddleware(deleteContact)).Methods("DELETE")

	r.HandleFunc("/conversations", authMiddleware(getConversations)).Methods("GET")
	r.HandleFunc("/conversations/{id}", authMiddleware(getConversation)).Methods("GET")
	r.HandleFunc("/conversations", authMiddleware(createConversation)).Methods("POST")
	r.HandleFunc("/conversations/{id}", authMiddleware(updateConversation)).Methods("PUT")
	r.HandleFunc("/conversations/{id}", authMiddleware(deleteConversation)).Methods("DELETE")

	r.HandleFunc("/messages", authMiddleware(getMessages)).Methods("GET")
	r.HandleFunc("/messages", authMiddleware(createMessage)).Methods("POST")

	// 设置HTTP服务器
	server := &http.Server{
		Addr:         ":8080",
		Handler:      r,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	// 启动服务器
	log.Println("Starting server on :8080")
	if err := server.ListenAndServe(); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
