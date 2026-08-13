package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrForbidden       = errors.New("forbidden")
	ErrInviteExpired   = errors.New("invite expired or exhausted")
	ErrAmbiguousEmail  = errors.New("ambiguous email")
	ErrAlreadyMember   = errors.New("already a member")
	ErrMemberLimit     = errors.New("member limit reached")
	ErrInvalidSequence = errors.New("invalid sequence")
	ErrConflict        = errors.New("conflict")
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type Store struct {
	db *sql.DB
}

func OpenStore(ctx context.Context, databaseURL string) (*Store, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) CleanupExpired(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		WITH deleted_attempts AS (
			DELETE FROM oidc_login_attempts WHERE expires_at <= now()
		), deleted_sessions AS (
			DELETE FROM auth_sessions WHERE expires_at <= now()
		)
		DELETE FROM conversation_invites
		WHERE expires_at <= now() OR revoked_at IS NOT NULL`)
	return err
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return err
	}
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		var applied bool
		if err := s.db.QueryRowContext(ctx, `SELECT EXISTS(
			SELECT 1 FROM schema_migrations WHERE version = $1
		)`, entry.Name()).Scan(&applied); err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err == nil {
			_, err = tx.ExecContext(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, entry.Name())
		}
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", entry.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) SaveLoginAttempt(ctx context.Context, state string, attempt LoginAttempt, expiresAt time.Time) error {
	hash := sha256.Sum256([]byte(state))
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO oidc_login_attempts(state_hash, nonce, code_verifier, return_to, expires_at)
		VALUES($1,$2,$3,$4,$5)`, hash[:], attempt.Nonce, attempt.CodeVerifier, attempt.ReturnTo, expiresAt)
	return err
}

func (s *Store) ConsumeLoginAttempt(ctx context.Context, state string) (LoginAttempt, error) {
	hash := sha256.Sum256([]byte(state))
	var attempt LoginAttempt
	err := s.db.QueryRowContext(ctx, `
		DELETE FROM oidc_login_attempts
		WHERE state_hash=$1 AND expires_at > now()
		RETURNING nonce, code_verifier, return_to`, hash[:]).Scan(&attempt.Nonce, &attempt.CodeVerifier, &attempt.ReturnTo)
	if errors.Is(err, sql.ErrNoRows) {
		return LoginAttempt{}, ErrNotFound
	}
	return attempt, err
}

func (s *Store) UpsertOIDCUser(ctx context.Context, issuer string, claims OIDCClaims) (User, error) {
	email, ok := normalizeEmail(claims.Email)
	if !ok {
		return User{}, ErrConflict
	}
	claims.Email = email
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	user := User{ID: uuid.New()}
	err = tx.QueryRowContext(ctx, `
		UPDATE users SET username=NULLIF($3,''),display_name=NULLIF($4,''),email=NULLIF($5,''),
			email_verified=$6,picture_url=NULLIF($7,''),last_login_at=now(),updated_at=now()
		WHERE oidc_issuer=$1 AND oidc_subject=$2
		RETURNING id,oidc_subject,COALESCE(username,''),COALESCE(display_name,''),
			COALESCE(email,''),email_verified,COALESCE(picture_url,''),status`,
		issuer, claims.Subject, claims.Username, claims.Name, claims.Email,
		claims.EmailVerified, claims.Picture).Scan(&user.ID, &user.Subject, &user.Username,
		&user.DisplayName, &user.Email, &user.EmailVerified, &user.PictureURL, &user.Status)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return User{}, err
		}
		return user, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return User{}, err
	}

	// OIDC identities converge on the same account by normalized verified email.
	// Only an unambiguous match is linked to a new identity.
	if email, ok := normalizeEmail(claims.Email); ok {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE lower(email)=lower($1)`, email).Scan(&count); err != nil {
			return User{}, err
		}
		if count > 1 {
			return User{}, ErrAmbiguousEmail
		}
		if count == 1 {
			err = tx.QueryRowContext(ctx, `UPDATE users SET oidc_issuer=$1,oidc_subject=$2,
				username=NULLIF($3,''),display_name=NULLIF($4,''),email=$5,email_verified=$6,
				picture_url=NULLIF($7,''),last_login_at=now(),updated_at=now()
				WHERE lower(email)=lower($5)
				RETURNING id,oidc_subject,COALESCE(username,''),COALESCE(display_name,''),COALESCE(email,''),
				email_verified,COALESCE(picture_url,''),status`, issuer, claims.Subject, claims.Username, claims.Name, email,
				claims.EmailVerified, claims.Picture).Scan(&user.ID, &user.Subject, &user.Username, &user.DisplayName, &user.Email, &user.EmailVerified, &user.PictureURL, &user.Status)
			if err != nil {
				return User{}, err
			}
			if err := tx.Commit(); err != nil {
				return User{}, err
			}
			return user, nil
		}
	}

	err = tx.QueryRowContext(ctx, `
		INSERT INTO users(id, oidc_issuer, oidc_subject, username, display_name, email,
			email_verified, picture_url, last_login_at)
		VALUES($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),$7,NULLIF($8,''),now())
		RETURNING id, oidc_subject, COALESCE(username,''), COALESCE(display_name,''),
			COALESCE(email,''), email_verified, COALESCE(picture_url,''), status`,
		user.ID, issuer, claims.Subject, claims.Username, claims.Name, claims.Email,
		claims.EmailVerified, claims.Picture).Scan(&user.ID, &user.Subject, &user.Username,
		&user.DisplayName, &user.Email, &user.EmailVerified, &user.PictureURL, &user.Status)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(); err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) CreateSession(ctx context.Context, userID uuid.UUID, rawToken, userAgent, ip string, expiresAt time.Time) error {
	hash := sha256.Sum256([]byte(rawToken))
	var ipValue any
	if ip != "" {
		ipValue = ip
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO auth_sessions(id,user_id,token_hash,expires_at,user_agent,ip)
		VALUES($1,$2,$3,$4,NULLIF($5,''),$6)`, uuid.New(), userID, hash[:], expiresAt, userAgent, ipValue)
	return err
}

func (s *Store) UserBySession(ctx context.Context, rawToken string) (User, error) {
	hash := sha256.Sum256([]byte(rawToken))
	var user User
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id,u.oidc_subject,COALESCE(u.username,''),COALESCE(u.display_name,''),
			COALESCE(u.email,''),u.email_verified,COALESCE(u.picture_url,''),u.status
		FROM auth_sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.expires_at > now() AND u.status='active'`, hash[:]).Scan(
		&user.ID, &user.Subject, &user.Username, &user.DisplayName, &user.Email,
		&user.EmailVerified, &user.PictureURL, &user.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *Store) DeleteSession(ctx context.Context, rawToken string) error {
	hash := sha256.Sum256([]byte(rawToken))
	_, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE token_hash=$1`, hash[:])
	return err
}

func (s *Store) CreateConversation(ctx context.Context, creator, conversationID uuid.UUID, title string) (Conversation, Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Conversation{}, Event{}, err
	}
	defer tx.Rollback()

	var existing Conversation
	err = tx.QueryRowContext(ctx, `
		SELECT c.id,COALESCE(c.title,''),c.last_seq,m.last_read_seq,0,m.joined_seq,m.role,m.status,c.updated_at
		FROM conversations c JOIN conversation_members m ON m.conversation_id=c.id
		WHERE c.id=$1 AND m.user_id=$2`, conversationID, creator).Scan(
		&existing.ID, &existing.Title, &existing.LastSeq, &existing.LastReadSeq, &existing.UnreadCount,
		&existing.JoinedSeq, &existing.Role, &existing.Status, &existing.UpdatedAt)
	if err == nil {
		if existing.Status != "active" {
			return Conversation{}, Event{}, ErrConflict
		}
		return existing, Event{}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Conversation{}, Event{}, err
	}
	var occupied bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversations WHERE id=$1)`, conversationID).Scan(&occupied); err != nil {
		return Conversation{}, Event{}, err
	}
	if occupied {
		return Conversation{}, Event{}, ErrConflict
	}

	conversation := Conversation{ID: conversationID, Title: title, LastSeq: 1, LastReadSeq: 1, UnreadCount: 0, JoinedSeq: 1, Role: "owner", Status: "active"}
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO conversations(id,title,created_by,last_seq)
		VALUES($1,NULLIF($2,''),$3,1)
		RETURNING updated_at`, conversation.ID, title, creator).Scan(&conversation.UpdatedAt); err != nil {
		return Conversation{}, Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_members(conversation_id,user_id,role,joined_seq,last_read_seq)
		VALUES($1,$2,'owner',1,1)`, conversation.ID, creator); err != nil {
		return Conversation{}, Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_member_periods(id,conversation_id,user_id,joined_seq)
		VALUES($1,$2,$3,1)`, uuid.New(), conversation.ID, creator); err != nil {
		return Conversation{}, Event{}, err
	}
	payload, _ := json.Marshal(map[string]any{"title": title, "created_by": creator})
	event := Event{ConversationID: conversation.ID, Seq: 1, ID: uuid.New(), SenderID: &creator, Type: "conversation.created", Payload: payload}
	if err := insertEvent(ctx, tx, &event, nil); err != nil {
		return Conversation{}, Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Conversation{}, Event{}, err
	}
	return conversation, event, nil
}

func (s *Store) ListConversations(ctx context.Context, userID uuid.UUID) ([]Conversation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id,COALESCE(c.title,''),c.last_seq,m.last_read_seq,
			(SELECT count(*) FROM conversation_events e
			 WHERE e.conversation_id=c.id AND e.seq>m.last_read_seq AND e.event_type='message.created'),
			m.joined_seq,m.role,m.status,c.updated_at
		FROM conversation_members m JOIN conversations c ON c.id=m.conversation_id
		WHERE m.user_id=$1 AND m.status='active'
		ORDER BY c.updated_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var conversations []Conversation
	for rows.Next() {
		var conversation Conversation
		if err := rows.Scan(&conversation.ID, &conversation.Title,
			&conversation.LastSeq, &conversation.LastReadSeq, &conversation.UnreadCount, &conversation.JoinedSeq, &conversation.Role,
			&conversation.Status, &conversation.UpdatedAt); err != nil {
			return nil, err
		}
		conversations = append(conversations, conversation)
	}
	return conversations, rows.Err()
}

func (s *Store) ListMembers(ctx context.Context, userID, conversationID uuid.UUID) ([]Member, error) {
	var allowed bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT status IN ('active','left') FROM conversation_members
		WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID).Scan(&allowed); errors.Is(err, sql.ErrNoRows) || !allowed {
		return nil, ErrForbidden
	} else if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT m.user_id,COALESCE(u.username,''),COALESCE(u.display_name,''),
			COALESCE(u.email,''),u.email_verified,COALESCE(u.picture_url,''),m.role,m.status,m.joined_seq
		FROM conversation_members m JOIN users u ON u.id=m.user_id
		WHERE m.conversation_id=$1
		ORDER BY (m.status='active') DESC,m.joined_at`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var members []Member
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.UserID, &member.Username, &member.DisplayName,
			&member.Email, &member.EmailVerified, &member.PictureURL, &member.Role, &member.Status, &member.JoinedSeq); err != nil {
			return nil, err
		}
		members = append(members, member)
	}
	return members, rows.Err()
}

func (s *Store) AddMemberByEmail(ctx context.Context, actorID, conversationID uuid.UUID, email string, maxMembers int) (Member, Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, Event{}, err
	}
	defer tx.Rollback()

	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT last_seq FROM conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&seq); errors.Is(err, sql.ErrNoRows) {
		return Member{}, Event{}, ErrNotFound
	} else if err != nil {
		return Member{}, Event{}, err
	}
	var allowed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT status='active' AND role IN ('owner','admin') FROM conversation_members
		WHERE conversation_id=$1 AND user_id=$2`, conversationID, actorID).Scan(&allowed); errors.Is(err, sql.ErrNoRows) || !allowed {
		return Member{}, Event{}, ErrForbidden
	} else if err != nil {
		return Member{}, Event{}, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id,COALESCE(username,''),COALESCE(display_name,''),email,email_verified,COALESCE(picture_url,'')
		FROM users WHERE lower(email)=lower($1) AND status='active' ORDER BY id LIMIT 2`, email)
	if err != nil {
		return Member{}, Event{}, err
	}
	var matches []Member
	for rows.Next() {
		var member Member
		if err := rows.Scan(&member.UserID, &member.Username, &member.DisplayName, &member.Email,
			&member.EmailVerified, &member.PictureURL); err != nil {
			rows.Close()
			return Member{}, Event{}, err
		}
		matches = append(matches, member)
	}
	if err := rows.Close(); err != nil {
		return Member{}, Event{}, err
	}
	if len(matches) == 0 {
		return Member{}, Event{}, ErrNotFound
	}
	if len(matches) > 1 {
		return Member{}, Event{}, ErrAmbiguousEmail
	}
	member := matches[0]
	var existingStatus string
	err = tx.QueryRowContext(ctx, `SELECT status FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, conversationID, member.UserID).Scan(&existingStatus)
	if err == nil && existingStatus == "active" {
		return Member{}, Event{}, ErrAlreadyMember
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Member{}, Event{}, err
	}
	var activeCount int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM conversation_members WHERE conversation_id=$1 AND status='active'`, conversationID).Scan(&activeCount); err != nil {
		return Member{}, Event{}, err
	}
	if activeCount >= maxMembers {
		return Member{}, Event{}, ErrMemberLimit
	}

	seq++
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET last_seq=$2,updated_at=now() WHERE id=$1`, conversationID, seq); err != nil {
		return Member{}, Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_members(conversation_id,user_id,role,joined_seq,last_read_seq,status,left_seq)
		VALUES($1,$2,'member',$3,$3,'active',NULL)
		ON CONFLICT (conversation_id,user_id) DO UPDATE SET
			role='member',joined_seq=EXCLUDED.joined_seq,last_read_seq=EXCLUDED.last_read_seq,
			status='active',left_seq=NULL,joined_at=now(),updated_at=now()`, conversationID, member.UserID, seq); err != nil {
		return Member{}, Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_member_periods(id,conversation_id,user_id,joined_seq)
		VALUES($1,$2,$3,$4)`, uuid.New(), conversationID, member.UserID, seq); err != nil {
		return Member{}, Event{}, err
	}
	member.Role, member.Status, member.JoinedSeq = "member", "active", seq
	payload, _ := json.Marshal(map[string]any{"user_id": member.UserID, "email": member.Email, "role": member.Role})
	event := Event{ConversationID: conversationID, Seq: seq, ID: uuid.New(), SenderID: &actorID, Type: "member.joined", Payload: payload}
	if err := insertEvent(ctx, tx, &event, nil); err != nil {
		return Member{}, Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, Event{}, err
	}
	return member, event, nil
}

func (s *Store) RenameConversation(ctx context.Context, actorID, conversationID uuid.UUID, title string) (Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT last_seq FROM conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&seq); errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	} else if err != nil {
		return Event{}, err
	}
	var allowed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT status='active' AND role IN ('owner','admin') FROM conversation_members
		WHERE conversation_id=$1 AND user_id=$2`, conversationID, actorID).Scan(&allowed); errors.Is(err, sql.ErrNoRows) || !allowed {
		return Event{}, ErrForbidden
	} else if err != nil {
		return Event{}, err
	}
	seq++
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET title=NULLIF($2,''),last_seq=$3,updated_at=now() WHERE id=$1`, conversationID, title, seq); err != nil {
		return Event{}, err
	}
	payload, _ := json.Marshal(map[string]string{"title": title})
	event := Event{ConversationID: conversationID, Seq: seq, ID: uuid.New(), SenderID: &actorID, Type: "conversation.renamed", Payload: payload}
	if err := insertEvent(ctx, tx, &event, nil); err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (s *Store) DeleteConversation(ctx context.Context, actorID, conversationID uuid.UUID) ([]uuid.UUID, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var allowed bool
	if err := tx.QueryRowContext(ctx, `
		SELECT role='owner' AND status='active' FROM conversation_members
		WHERE conversation_id=$1 AND user_id=$2`, conversationID, actorID).Scan(&allowed); errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrForbidden
	}
	rows, err := tx.QueryContext(ctx, `SELECT user_id FROM conversation_members WHERE conversation_id=$1`, conversationID)
	if err != nil {
		return nil, err
	}
	var memberIDs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		memberIDs = append(memberIDs, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM conversations WHERE id=$1`, conversationID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return memberIDs, nil
}

func (s *Store) UpdateMemberRole(ctx context.Context, actorID, conversationID, targetID uuid.UUID, role string) (Member, Event, error) {
	if role != "admin" && role != "member" {
		return Member{}, Event{}, ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Member{}, Event{}, err
	}
	defer tx.Rollback()
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT last_seq FROM conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&seq); errors.Is(err, sql.ErrNoRows) {
		return Member{}, Event{}, ErrNotFound
	} else if err != nil {
		return Member{}, Event{}, err
	}
	var actorRole, actorStatus string
	if err := tx.QueryRowContext(ctx, `SELECT role,status FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, conversationID, actorID).Scan(&actorRole, &actorStatus); err != nil || actorRole != "owner" || actorStatus != "active" {
		return Member{}, Event{}, ErrForbidden
	}
	var member Member
	if err := tx.QueryRowContext(ctx, `
		UPDATE conversation_members m SET role=$3,updated_at=now()
		FROM users u WHERE m.conversation_id=$1 AND m.user_id=$2 AND m.user_id=u.id
			AND m.status='active' AND m.role<>'owner'
		RETURNING m.user_id,COALESCE(u.username,''),COALESCE(u.display_name,''),COALESCE(u.email,''),
			u.email_verified,COALESCE(u.picture_url,''),m.role,m.status,m.joined_seq`, conversationID, targetID, role).Scan(
		&member.UserID, &member.Username, &member.DisplayName, &member.Email, &member.EmailVerified, &member.PictureURL, &member.Role, &member.Status, &member.JoinedSeq); errors.Is(err, sql.ErrNoRows) {
		return Member{}, Event{}, ErrForbidden
	} else if err != nil {
		return Member{}, Event{}, err
	}
	seq++
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET last_seq=$2,updated_at=now() WHERE id=$1`, conversationID, seq); err != nil {
		return Member{}, Event{}, err
	}
	payload, _ := json.Marshal(map[string]any{"user_id": targetID, "role": role})
	event := Event{ConversationID: conversationID, Seq: seq, ID: uuid.New(), SenderID: &actorID, Type: "member.role_changed", Payload: payload}
	if err := insertEvent(ctx, tx, &event, nil); err != nil {
		return Member{}, Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Member{}, Event{}, err
	}
	return member, event, nil
}

func (s *Store) ListContacts(ctx context.Context, ownerID uuid.UUID) ([]Contact, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id,c.name,c.email,COALESCE(c.note,''),u.id,c.created_at,c.updated_at
		FROM contacts c LEFT JOIN users u ON lower(u.email)=lower(c.email) AND u.status='active'
		WHERE c.owner_id=$1 ORDER BY lower(c.name),lower(c.email)`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var contacts []Contact
	for rows.Next() {
		var contact Contact
		var linked uuid.NullUUID
		if err := rows.Scan(&contact.ID, &contact.Name, &contact.Email, &contact.Note, &linked, &contact.CreatedAt, &contact.UpdatedAt); err != nil {
			return nil, err
		}
		if linked.Valid {
			contact.LinkedUser = &linked.UUID
		}
		contacts = append(contacts, contact)
	}
	return contacts, rows.Err()
}

func (s *Store) SaveContact(ctx context.Context, ownerID, contactID uuid.UUID, name, email, note string) (Contact, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Contact{}, err
	}
	defer tx.Rollback()
	var contact Contact
	err = tx.QueryRowContext(ctx, `UPDATE contacts SET name=$3,email=$4,note=NULLIF($5,''),updated_at=now()
		WHERE id=$1 AND owner_id=$2 RETURNING id,name,email,COALESCE(note,''),created_at,updated_at`,
		contactID, ownerID, name, email, note).Scan(&contact.ID, &contact.Name, &contact.Email, &contact.Note, &contact.CreatedAt, &contact.UpdatedAt)
	if err == nil {
		if err := tx.Commit(); err != nil {
			return Contact{}, err
		}
		return contact, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Contact{}, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO contacts(id,owner_id,name,email,note) VALUES($1,$2,$3,$4,NULLIF($5,''))
		ON CONFLICT(owner_id,email) DO UPDATE SET name=EXCLUDED.name,note=EXCLUDED.note,updated_at=now()
		RETURNING id,name,email,COALESCE(note,''),created_at,updated_at`, contactID, ownerID, name, email, note).Scan(
		&contact.ID, &contact.Name, &contact.Email, &contact.Note, &contact.CreatedAt, &contact.UpdatedAt)
	if err != nil {
		return Contact{}, err
	}
	if err := tx.Commit(); err != nil {
		return Contact{}, err
	}
	return contact, nil
}

func (s *Store) DeleteContact(ctx context.Context, ownerID, contactID uuid.UUID) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM contacts WHERE id=$1 AND owner_id=$2`, contactID, ownerID)
	if err != nil {
		return err
	}
	if count, _ := result.RowsAffected(); count == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) LeaveConversation(ctx context.Context, userID, conversationID uuid.UUID) (Event, *uuid.UUID, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, nil, err
	}
	defer tx.Rollback()
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT last_seq FROM conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&seq); errors.Is(err, sql.ErrNoRows) {
		return Event{}, nil, ErrNotFound
	} else if err != nil {
		return Event{}, nil, err
	}
	var role, status string
	if err := tx.QueryRowContext(ctx, `SELECT role,status FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID).Scan(&role, &status); errors.Is(err, sql.ErrNoRows) || status != "active" {
		return Event{}, nil, ErrForbidden
	} else if err != nil {
		return Event{}, nil, err
	}
	var newOwner *uuid.UUID
	if role == "owner" {
		var next uuid.NullUUID
		err := tx.QueryRowContext(ctx, `
			SELECT user_id FROM conversation_members
			WHERE conversation_id=$1 AND user_id<>$2 AND status='active'
			ORDER BY CASE role WHEN 'admin' THEN 0 ELSE 1 END,joined_at LIMIT 1`, conversationID, userID).Scan(&next)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Event{}, nil, err
		}
		if next.Valid {
			if _, err := tx.ExecContext(ctx, `UPDATE conversation_members SET role='owner',updated_at=now() WHERE conversation_id=$1 AND user_id=$2`, conversationID, next.UUID); err != nil {
				return Event{}, nil, err
			}
			newOwner = &next.UUID
		}
	}
	seq++
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET last_seq=$2,updated_at=now() WHERE id=$1`, conversationID, seq); err != nil {
		return Event{}, nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversation_members SET status='left',left_seq=$3,updated_at=now() WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID, seq); err != nil {
		return Event{}, nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversation_member_periods SET left_seq=$3,left_at=now(),leave_reason='left'
		WHERE conversation_id=$1 AND user_id=$2 AND left_seq IS NULL`, conversationID, userID, seq); err != nil {
		return Event{}, nil, err
	}
	payload, _ := json.Marshal(map[string]any{"user_id": userID, "new_owner_id": newOwner})
	event := Event{ConversationID: conversationID, Seq: seq, ID: uuid.New(), SenderID: &userID, Type: "member.left", Payload: payload}
	if err := insertEvent(ctx, tx, &event, nil); err != nil {
		return Event{}, nil, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, nil, err
	}
	return event, newOwner, nil
}

func (s *Store) RemoveMember(ctx context.Context, actorID, conversationID, targetID uuid.UUID) (Event, error) {
	if actorID == targetID {
		return Event{}, ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT last_seq FROM conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&seq); errors.Is(err, sql.ErrNoRows) {
		return Event{}, ErrNotFound
	} else if err != nil {
		return Event{}, err
	}
	var actorRole, actorStatus, targetRole, targetStatus string
	if err := tx.QueryRowContext(ctx, `SELECT role,status FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, conversationID, actorID).Scan(&actorRole, &actorStatus); err != nil {
		return Event{}, ErrForbidden
	}
	if err := tx.QueryRowContext(ctx, `SELECT role,status FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, conversationID, targetID).Scan(&targetRole, &targetStatus); err != nil {
		return Event{}, ErrNotFound
	}
	allowed := actorStatus == "active" && targetStatus == "active" && (actorRole == "owner" || (actorRole == "admin" && targetRole == "member"))
	if !allowed || targetRole == "owner" {
		return Event{}, ErrForbidden
	}
	seq++
	if _, err := tx.ExecContext(ctx, `UPDATE conversations SET last_seq=$2,updated_at=now() WHERE id=$1`, conversationID, seq); err != nil {
		return Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversation_members SET status='removed',left_seq=$3,updated_at=now() WHERE conversation_id=$1 AND user_id=$2`, conversationID, targetID, seq); err != nil {
		return Event{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE conversation_member_periods SET left_seq=$3,left_at=now(),leave_reason='removed'
		WHERE conversation_id=$1 AND user_id=$2 AND left_seq IS NULL`, conversationID, targetID, seq); err != nil {
		return Event{}, err
	}
	payload, _ := json.Marshal(map[string]any{"user_id": targetID, "removed_by": actorID})
	event := Event{ConversationID: conversationID, Seq: seq, ID: uuid.New(), SenderID: &actorID, Type: "member.removed", Payload: payload}
	if err := insertEvent(ctx, tx, &event, nil); err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func (s *Store) ActiveConversationIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT conversation_id FROM conversation_members WHERE user_id=$1 AND status='active'`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (s *Store) ListEvents(ctx context.Context, userID, conversationID uuid.UUID, afterSeq int64, limit int) ([]Event, error) {
	if afterSeq < 0 {
		return nil, ErrInvalidSequence
	}
	if limit < 1 || limit > 200 {
		limit = 100
	}
	var status string
	var joinedSeq int64
	var leftSeq sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT status,joined_seq,left_seq FROM conversation_members
		WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID).Scan(&status, &joinedSeq, &leftSeq)
	if errors.Is(err, sql.ErrNoRows) || status == "removed" {
		return nil, ErrForbidden
	}
	if err != nil {
		return nil, err
	}
	upper := int64(1<<63 - 1)
	if status == "left" && leftSeq.Valid {
		upper = leftSeq.Int64
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.conversation_id,e.seq,e.id,e.sender_id,e.client_event_id,COALESCE(u.email,''),
			COALESCE(u.display_name,u.username,u.email,''),e.event_type,e.payload,e.created_at
		FROM conversation_events e LEFT JOIN users u ON u.id=e.sender_id
		WHERE conversation_id=$1 AND seq>$2 AND seq>=$3 AND seq<=$4
		ORDER BY seq LIMIT $5`, conversationID, afterSeq, joinedSeq, upper, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var event Event
		var senderID uuid.NullUUID
		var clientMessageID uuid.NullUUID
		if err := rows.Scan(&event.ConversationID, &event.Seq, &event.ID, &senderID, &clientMessageID, &event.SenderEmail, &event.SenderName,
			&event.Type, &event.Payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		if senderID.Valid {
			event.SenderID = &senderID.UUID
		}
		if clientMessageID.Valid {
			event.ClientMessageID = &clientMessageID.UUID
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) AppendMessage(ctx context.Context, userID, conversationID, clientEventID uuid.UUID, content json.RawMessage) (Event, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()

	var active bool
	if err := tx.QueryRowContext(ctx, `SELECT status='active' FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID).Scan(&active); errors.Is(err, sql.ErrNoRows) || !active {
		return Event{}, ErrForbidden
	} else if err != nil {
		return Event{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT true FROM conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&active); err != nil {
		return Event{}, err
	}

	var existing Event
	var senderID uuid.NullUUID
	err = tx.QueryRowContext(ctx, `
		SELECT e.conversation_id,e.seq,e.id,e.sender_id,e.event_type,e.payload,e.created_at,
			COALESCE(u.email,''),COALESCE(u.display_name,u.username,u.email,'')
		FROM conversation_events e LEFT JOIN users u ON u.id=e.sender_id
		WHERE e.conversation_id=$1 AND e.sender_id=$2 AND e.client_event_id=$3`,
		conversationID, userID, clientEventID).Scan(&existing.ConversationID, &existing.Seq,
		&existing.ID, &senderID, &existing.Type, &existing.Payload, &existing.CreatedAt,
		&existing.SenderEmail, &existing.SenderName)
	if err == nil {
		existing.SenderID = &senderID.UUID
		existing.ClientMessageID = &clientEventID
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Event{}, err
	}

	var seq int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE conversations SET last_seq=last_seq+1,updated_at=now()
		WHERE id=$1 RETURNING last_seq`, conversationID).Scan(&seq); err != nil {
		return Event{}, err
	}
	event := Event{ConversationID: conversationID, Seq: seq, ID: uuid.New(), SenderID: &userID, ClientMessageID: &clientEventID, Type: "message.created", Payload: content}
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(email,''),COALESCE(display_name,username,email,'') FROM users WHERE id=$1`, userID).Scan(&event.SenderEmail, &event.SenderName); err != nil {
		return Event{}, err
	}
	if err := insertEvent(ctx, tx, &event, &clientEventID); err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func insertEvent(ctx context.Context, tx *sql.Tx, event *Event, clientEventID *uuid.UUID) error {
	return tx.QueryRowContext(ctx, `
		INSERT INTO conversation_events(conversation_id,seq,id,sender_id,client_event_id,event_type,payload)
		VALUES($1,$2,$3,$4,$5,$6,$7)
		RETURNING created_at`, event.ConversationID, event.Seq, event.ID, event.SenderID,
		clientEventID, event.Type, event.Payload).Scan(&event.CreatedAt)
}

func (s *Store) UpdateRead(ctx context.Context, userID, conversationID uuid.UUID, seq int64) error {
	if seq < 0 {
		return ErrInvalidSequence
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE conversation_members m SET last_read_seq=GREATEST(last_read_seq,$3),updated_at=now()
		FROM conversations c
		WHERE m.conversation_id=$1 AND m.user_id=$2 AND m.status='active'
			AND c.id=m.conversation_id AND $3<=c.last_seq`, conversationID, userID, seq)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return ErrInvalidSequence
	}
	return nil
}

func (s *Store) CreateInvite(ctx context.Context, userID, conversationID uuid.UUID, rawToken string, expiresAt time.Time) error {
	hash := sha256.Sum256([]byte(rawToken))
	var allowed bool
	if err := s.db.QueryRowContext(ctx, `
		SELECT role IN ('owner','admin') AND status='active'
		FROM conversation_members WHERE conversation_id=$1 AND user_id=$2`, conversationID, userID).Scan(&allowed); errors.Is(err, sql.ErrNoRows) || !allowed {
		return ErrForbidden
	} else if err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO conversation_invites(id,conversation_id,token_hash,created_by,expires_at)
		VALUES($1,$2,$3,$4,$5)`, uuid.New(), conversationID, hash[:], userID, expiresAt)
	return err
}

func (s *Store) AcceptInvite(ctx context.Context, userID uuid.UUID, rawToken string, maxMembers int) (uuid.UUID, Event, error) {
	hash := sha256.Sum256([]byte(rawToken))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return uuid.Nil, Event{}, err
	}
	defer tx.Rollback()

	var conversationID uuid.UUID
	err = tx.QueryRowContext(ctx, `
		UPDATE conversation_invites SET use_count=use_count+1
		WHERE token_hash=$1 AND revoked_at IS NULL AND expires_at>now() AND use_count<max_uses
		RETURNING conversation_id`, hash[:]).Scan(&conversationID)
	if errors.Is(err, sql.ErrNoRows) {
		return uuid.Nil, Event{}, ErrInviteExpired
	}
	if err != nil {
		return uuid.Nil, Event{}, err
	}

	var lockedID uuid.UUID
	if err := tx.QueryRowContext(ctx, `SELECT id FROM conversations WHERE id=$1 FOR UPDATE`, conversationID).Scan(&lockedID); err != nil {
		return uuid.Nil, Event{}, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT user_id::text FROM conversation_members WHERE conversation_id=$1 AND status='active' ORDER BY user_id::text`, conversationID)
	if err != nil {
		return uuid.Nil, Event{}, err
	}
	var activeMemberIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return uuid.Nil, Event{}, err
		}
		activeMemberIDs = append(activeMemberIDs, id)
	}
	rows.Close()
	if len(activeMemberIDs) >= maxMembers {
		return uuid.Nil, Event{}, ErrMemberLimit
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE conversations SET last_seq=last_seq+1,updated_at=now()
		WHERE id=$1 RETURNING last_seq`, conversationID).Scan(&seq); err != nil {
		return uuid.Nil, Event{}, err
	}
	memberRole := "member"
	if len(activeMemberIDs) == 0 {
		memberRole = "owner"
	}

	result, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_members(conversation_id,user_id,role,joined_seq,last_read_seq,status,left_seq)
		VALUES($1,$2,$4,$3,$3,'active',NULL)
		ON CONFLICT (conversation_id,user_id) DO UPDATE SET
			role=EXCLUDED.role,joined_seq=EXCLUDED.joined_seq,last_read_seq=EXCLUDED.last_read_seq,
			status='active',left_seq=NULL,joined_at=now(),updated_at=now()
		WHERE conversation_members.status<>'active'`, conversationID, userID, seq, memberRole)
	if err != nil {
		return uuid.Nil, Event{}, err
	}
	affected, _ := result.RowsAffected()
	if affected == 0 {
		return uuid.Nil, Event{}, ErrForbidden
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO conversation_member_periods(id,conversation_id,user_id,joined_seq)
		VALUES($1,$2,$3,$4)`, uuid.New(), conversationID, userID, seq); err != nil {
		return uuid.Nil, Event{}, err
	}
	payload, _ := json.Marshal(map[string]any{"user_id": userID, "role": memberRole})
	event := Event{ConversationID: conversationID, Seq: seq, ID: uuid.New(), SenderID: &userID, Type: "member.joined", Payload: payload}
	if err := insertEvent(ctx, tx, &event, nil); err != nil {
		return uuid.Nil, Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return uuid.Nil, Event{}, err
	}
	return conversationID, event, nil
}
