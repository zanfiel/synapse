package session

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zanfiel/synapse/internal/types"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

type Session struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Model     string    `json:"model"`
	WorkDir   string    `json:"work_dir"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Messages  int       `json:"messages"`
}

func NewStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dataDir, "sessions.db")
	db, err := sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			title TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			work_dir TEXT NOT NULL DEFAULT '',
			system_prompt TEXT NOT NULL DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS messages (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL REFERENCES sessions(id),
			role TEXT NOT NULL,
			content TEXT,
			content_json INTEGER NOT NULL DEFAULT 0,
			tool_calls TEXT,
			tool_call_id TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE INDEX IF NOT EXISTS idx_messages_session ON messages(session_id);
	`)
	if err != nil {
		return err
	}

	if _, err := db.Exec(`ALTER TABLE messages ADD COLUMN content_json INTEGER NOT NULL DEFAULT 0`); err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
		return err
	}

	return nil
}

func (s *Store) Create(model, workDir, systemPrompt string) (string, error) {
	id := uuid.New().String()[:8]
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, model, work_dir, system_prompt) VALUES (?, ?, ?, ?)`,
		id, model, workDir, systemPrompt,
	)
	return id, err
}

func (s *Store) SaveMessage(sessionID string, msg types.Message) error {
	var content string
	contentJSON := 0
	switch c := msg.Content.(type) {
	case string:
		content = c
	default:
		if c != nil {
			data, _ := json.Marshal(c)
			content = string(data)
			contentJSON = 1
		}
	}

	var toolCallsJSON string
	if len(msg.ToolCalls) > 0 {
		data, _ := json.Marshal(msg.ToolCalls)
		toolCallsJSON = string(data)
	}

	_, err := s.db.Exec(
		`INSERT INTO messages (session_id, role, content, content_json, tool_calls, tool_call_id) VALUES (?, ?, ?, ?, ?, ?)`,
		sessionID, msg.Role, content, contentJSON, toolCallsJSON, msg.ToolCallID,
	)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(`UPDATE sessions SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, sessionID)
	return err
}

func (s *Store) SetTitle(sessionID, title string) error {
	_, err := s.db.Exec(`UPDATE sessions SET title = ? WHERE id = ?`, title, sessionID)
	return err
}

func (s *Store) LoadMessages(sessionID string) ([]types.Message, error) {
	rows, err := s.db.Query(
		`SELECT role, content, content_json, tool_calls, tool_call_id FROM messages WHERE session_id = ? ORDER BY id`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []types.Message
	for rows.Next() {
		var role, content, toolCallsJSON, toolCallID sql.NullString
		var contentJSON sql.NullInt64
		if err := rows.Scan(&role, &content, &contentJSON, &toolCallsJSON, &toolCallID); err != nil {
			return nil, err
		}

		msg := types.Message{
			Role:       role.String,
			ToolCallID: toolCallID.String,
		}

		if content.Valid && content.String != "" {
			if contentJSON.Valid && contentJSON.Int64 != 0 {
				var parsed interface{}
				if err := json.Unmarshal([]byte(content.String), &parsed); err != nil {
					fmt.Fprintf(os.Stderr, "warn: corrupt json content in session %s: %v\n", sessionID, err)
					msg.Content = content.String
				} else {
					msg.Content = parsed
				}
			} else {
				msg.Content = content.String
			}
		}

		if toolCallsJSON.Valid && toolCallsJSON.String != "" {
			var tcs []types.ToolCall
			if err := json.Unmarshal([]byte(toolCallsJSON.String), &tcs); err != nil {
				fmt.Fprintf(os.Stderr, "warn: corrupt tool_calls in session %s: %v\n", sessionID, err)
			}
			msg.ToolCalls = tcs
		}

		msgs = append(msgs, msg)
	}

	return msgs, nil
}

func (s *Store) List(limit int) ([]Session, error) {
	rows, err := s.db.Query(`
		SELECT s.id, s.title, s.model, s.work_dir, s.created_at, s.updated_at,
			   COUNT(m.id) as msg_count
		FROM sessions s
		LEFT JOIN messages m ON m.session_id = s.id
		GROUP BY s.id
		ORDER BY s.updated_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var created, updated string
		if err := rows.Scan(&sess.ID, &sess.Title, &sess.Model, &sess.WorkDir, &created, &updated, &sess.Messages); err != nil {
			continue
		}
		sess.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
		sess.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
		sessions = append(sessions, sess)
	}
	return sessions, nil
}

func (s *Store) Delete(sessionID string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	tx.Exec(`DELETE FROM messages WHERE session_id = ?`, sessionID)
	tx.Exec(`DELETE FROM sessions WHERE id = ?`, sessionID)
	return tx.Commit()
}

func (s *Store) Get(sessionID string) (*Session, error) {
	var sess Session
	var created, updated string
	err := s.db.QueryRow(`
		SELECT s.id, s.title, s.model, s.work_dir, s.created_at, s.updated_at,
			   COUNT(m.id) as msg_count
		FROM sessions s
		LEFT JOIN messages m ON m.session_id = s.id
		WHERE s.id = ?
		GROUP BY s.id
	`, sessionID).Scan(&sess.ID, &sess.Title, &sess.Model, &sess.WorkDir, &created, &updated, &sess.Messages)
	if err != nil {
		return nil, fmt.Errorf("session %s not found", sessionID)
	}
	sess.CreatedAt, _ = time.Parse("2006-01-02 15:04:05", created)
	sess.UpdatedAt, _ = time.Parse("2006-01-02 15:04:05", updated)
	return &sess, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

// SearchMessages searches message content across all sessions.
func (s *Store) SearchMessages(query string, limit int) ([]MessageResult, error) {
	rows, err := s.db.Query(`
		SELECT m.session_id, m.role, m.content, m.created_at
		FROM messages m
		WHERE m.content LIKE ? AND m.role IN ('user', 'assistant')
		ORDER BY m.created_at DESC
		LIMIT ?
	`, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []MessageResult
	for rows.Next() {
		var r MessageResult
		var created sql.NullString
		if err := rows.Scan(&r.SessionID, &r.Role, &r.Content, &created); err != nil {
			continue
		}
		if created.Valid {
			r.CreatedAt = created.String
		}
		results = append(results, r)
	}
	return results, nil
}

// MessageResult holds a search hit from past sessions.
type MessageResult struct {
	SessionID string
	Role      string
	Content   string
	CreatedAt string
}
