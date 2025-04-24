# 轻量级 Go 聊天服务器技术设计文档

## 1. 项目概述

本项目旨在开发一个基于 Golang 的极度轻量级聊天服务器，采用会话为中心的消息传递模型，使用 SQLite 作为数据存储引擎。系统设计为易于部署和维护，同时保持良好的可扩展性。

## 2. 系统架构

### 2.1 核心组件

- **HTTP API 服务**：提供 RESTful 接口
- **消息分发系统**：处理消息的实时推送
- **数据存储层**：基于 SQLite 的持久化存储
- **认证授权模块**：管理用户会话和权限

### 2.2 消息流转模型

```
用户 → 发送消息 → 会话 → 分发给会话成员 → 接收用户
```

消息总是通过会话进行传递，不存在点对点的直接消息。每个会话可以包含多个成员，发送到会话的消息会被分发给所有会话成员。

## 3. 数据模型

### 3.1 数据库表结构

#### users 表
```sql
CREATE TABLE users (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    email TEXT UNIQUE NOT NULL,
    password TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

#### sessions 表
```sql
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL,
    token TEXT UNIQUE NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
```

#### contacts 表
```sql
CREATE TABLE contacts (
    id INTEGER PRIMARY KEY, 
    uuid TEXT NOT NULL,
    user_id INTEGER NOT NULL,
    name TEXT NOT NULL,
    email TEXT NOT NULL,
    notes TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    FOREIGN KEY (contact_user_id) REFERENCES users(id) ON DELETE CASCADE,
    UNIQUE (user_id, contact_user_id)
);
```

Notes: contacts.id 这里有个问题，Swift UI 中使用的是 UUID，自增 id 可能会被遍历攻击，也不方便做分布式部署

#### conversations 表
```sql
CREATE TABLE conversations (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

#### conversation_members 表
```sql
CREATE TABLE conversation_members (
    conversation_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (conversation_id, user_id),
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
```

#### messages 表
```sql
CREATE TABLE messages (
    id INTEGER PRIMARY KEY,
    conversation_id INTEGER NOT NULL,
    user_id INTEGER NOT NULL,
    content TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (conversation_id) REFERENCES conversations(id) ON DELETE CASCADE,
    -- FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE SET NULL
);
```

### 3.2 索引策略

```sql
-- 用户查询优化
CREATE INDEX idx_users_email ON users(email);

-- 会话成员查询优化
CREATE INDEX idx_conversation_members_user ON conversation_members(user_id);

-- 消息查询优化
CREATE INDEX idx_messages_conversation ON messages(conversation_id);
```

## 4. API 设计

### 4.1 认证相关

```
POST /users          # 注册新用户
POST /sessions       # 用户登录，创建会话
DELETE /sessions     # 用户登出，销毁会话
```

### 4.2 联系人管理

```
GET /contacts        # 获取联系人列表
POST /contacts       # 添加联系人
PUT /contacts/:id    # 更新联系人信息
DELETE /contacts/:id # 删除联系人
```

### 4.3 会话管理

```
GET /conversations   # 获取用户参与的会话列表
POST /conversations  # 创建新会话
PUT /conversations/:id  # 更新会话信息
DELETE /conversations/:id  # 删除会话
```

### 4.4 消息管理

```
GET /messages  # 获取会话消息（支持长轮询）
POST /messages # 发送消息到会话
```

## 5. 关键技术实现

### 5.2 长轮询消息获取

```go

type MessageBroker struct {
}

func (b *MessageBroker) Publish(conversationID int64, msg *Message) {
    // find members by conversationID
    members := getMembersFrom(conversationID)
    for member := range members {
        member.messages <- msg
    }
}

func (h *Handler) GetMessages(w http.ResponseWriter, r *http.Request) {
    userId = r.Context().Get("user_id")
    conversationIds := getUserConversations(userId)
    userID, err := h.getUserIDFromToken(r)
    lastMsgID := r.URL.Query().Get("offset")
    
    var out: []Messages
    for conversationId := range conversationIds {
        messages, err := h.store.GetMessages(conversationID, lastMsgID, limit)
        if err != nil {
            http.Error(w, err.Error(), http.StatusInternalServerError)
            return
        }
    }
    
    // 如果找到消息，立即返回
    if len(out) > 0 {
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(messages)
        return
    }
    
    // 如果没有找到消息，设置长轮询
    timeout := time.After(30 * time.Second)
    messageCh, unsubscribe := h.broker.Wait(userId)
    defer unsubscribe()
    
    select {
    case <-r.Context().Done():
        // 客户端关闭连接
        return
    case <-timeout:
        // 超时，返回空消息列表
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode([]Message{})
        return
    case msg := <-messageCh:
        // 收到新消息，返回
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode([]Message{*msg})
        return
    }
}
```

## 6. 性能优化策略

### 6.1 SQLite 优化

- 启用 WAL（Write-Ahead Logging）模式减少写操作阻塞
- 适当设置 journal_mode 和 synchronous 参数平衡性能与数据安全
- 批量操作使用事务包装，减少磁盘 I/O

```go
db.Exec("PRAGMA journal_mode = WAL")
db.Exec("PRAGMA synchronous = NORMAL")
db.Exec("PRAGMA cache_size = 1000")
```

### 6.2 连接池配置

```go
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(5)
db.SetConnMaxLifetime(5 * time.Minute)
```

### 6.3 查询优化

- 使用 prepared statements 减少解析开销
- 对消息表实施时间范围查询限制
- 分页获取大量数据

## 7. 安全措施

### 7.1 认证与授权

- 使用 bcrypt 进行密码哈希
- 所有 API 端点（除登录注册外）需要验证令牌

### 7.2 输入验证

- 严格验证所有用户输入
- 使用参数化查询防止 SQL 注入
- 设置内容大小限制防止资源耗尽攻击

## 8. 扩展性考虑

如果需要扩展系统以支持更高负载，可以考虑以下升级路径：

1. **数据库迁移**：从 SQLite 迁移到 PostgreSQL
2. **消息分区**：按时间或会话 ID 对消息表进行分区
3. **缓存层**：引入 Redis 缓存热门会话和最近消息
4. **微服务拆分**：将消息处理和用户管理拆分为独立服务



// func (s *Server) registerUser(w http.ResponseWriter, r *http.Request) {
// 	if r.Method == "GET" {
// 		s.Render(w, "signup", nil)
// 		return
// 	}
// 	var user User
// 	if r.Header.Get("Content-Type") == "application/json" {
// 		if err := json.NewDecoder(r.Body).Decode(&user); err != nil {
// 			http.Error(w, err.Error(), http.StatusBadRequest)
// 			return
// 		}
// 	} else {
// 		user.Name = r.FormValue("name")
// 		user.Email = r.FormValue("email")
// 		user.Password = r.FormValue("password")
// 	}

// 	result, err := s.db.Exec("INSERT INTO users (name, email, password) VALUES (?, ?, ?)",
// 		user.Name, user.Email, user.Password)
// 	if err != nil {
// 		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
// 			http.Error(w, "Email already exists", http.StatusConflict)
// 		} else {
// 			http.Error(w, err.Error(), http.StatusInternalServerError)
// 		}
// 		return
// 	}

// 	id, err := result.LastInsertId()
// 	if err != nil {
// 		http.Error(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}
// 	user.ID = id
// 	user.Password = "" // 不返回密码

// 	w.WriteHeader(http.StatusCreated)
// 	json.NewEncoder(w).Encode(user)
// }

// func (s *Server) loginUser(w http.ResponseWriter, r *http.Request) {
// 	if r.Method == "GET" {
// 		s.Render(w, "login", nil)
// 		return
// 	}

// 	var credentials User
// 	if r.Header.Get("Content-Type") == "application/json" {
// 		if err := json.NewDecoder(r.Body).Decode(&credentials); err != nil {
// 			http.Error(w, err.Error(), http.StatusBadRequest)
// 			return
// 		}
// 	} else {
// 		credentials.Email = r.FormValue("email")
// 		credentials.Password = r.FormValue("password")
// 	}

// 	var user User
// 	err := s.db.QueryRow("SELECT id, name, email, password FROM users WHERE email = ?",
// 		credentials.Email).Scan(&user.ID, &user.Name, &user.Email, &user.Password)
// 	if err != nil {
// 		if err == sql.ErrNoRows {
// 			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
// 		} else {
// 			http.Error(w, err.Error(), http.StatusInternalServerError)
// 		}
// 		return
// 	}
// 	// TODO: 验证密码哈希
// 	if user.Password != credentials.Password {
// 		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
// 		return
// 	}

// 	// 生成会话token
// 	token := uuid.New().String()
// 	session := Session{
// 		UserID: user.ID,
// 		Token:  token,
// 	}
// 	result, err := s.db.Exec("INSERT INTO sessions (user_id, token) VALUES (?, ?)", session.UserID, session.Token)
// 	if err != nil {
// 		http.Error(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}

// 	id, err := result.LastInsertId()
// 	if err != nil {
// 		http.Error(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}
// 	session.ID = id

// 	w.Header().Set("Content-Type", "application/json")
// 	w.Header().Set("Set-Cookie", fmt.Sprintf("token=%s; Path=/; HttpOnly; SameSite=Lax", token))
// 	w.WriteHeader(http.StatusOK)
// 	json.NewEncoder(w).Encode(session)
// }

// func (s *Server) logoutUser(w http.ResponseWriter, r *http.Request) {
// 	token := r.Header.Get("Authorization")
// 	if token == "" {
// 		http.Error(w, "Missing authorization token", http.StatusUnauthorized)
// 		return
// 	}

// 	_, err := s.db.Exec("DELETE FROM sessions WHERE token = ?", token)
// 	if err != nil {
// 		http.Error(w, err.Error(), http.StatusInternalServerError)
// 		return
// 	}

// 	w.WriteHeader(http.StatusNoContent)
// }
