package session

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/zanfiel/synapse/internal/types"
)

// Branch creates a new session that copies messages up to a point from the source.
func (s *Store) Branch(sourceID string, atMessage int) (string, error) {
	msgs, err := s.LoadMessages(sourceID)
	if err != nil {
		return "", err
	}

	if atMessage <= 0 || atMessage > len(msgs) {
		atMessage = len(msgs)
	}

	newID := uuid.New().String()[:8]

	source, err := s.Get(sourceID)
	if err != nil {
		return "", err
	}

	_, err = s.db.Exec(
		`INSERT INTO sessions (id, title, model, work_dir, system_prompt) VALUES (?, ?, ?, ?, ?)`,
		newID, fmt.Sprintf("Branch of %s", sourceID), source.Model, source.WorkDir, "",
	)
	if err != nil {
		return "", err
	}

	for i := 0; i < atMessage; i++ {
		s.SaveMessage(newID, msgs[i])
	}

	return newID, nil
}

// Export returns all messages as a JSON-serializable structure.
func (s *Store) Export(sessionID string) (*SessionExport, error) {
	sess, err := s.Get(sessionID)
	if err != nil {
		return nil, err
	}

	msgs, err := s.LoadMessages(sessionID)
	if err != nil {
		return nil, err
	}

	return &SessionExport{
		ID:        sess.ID,
		Title:     sess.Title,
		Model:     sess.Model,
		WorkDir:   sess.WorkDir,
		CreatedAt: sess.CreatedAt,
		Messages:  msgs,
	}, nil
}

type SessionExport struct {
	ID        string            `json:"id"`
	Title     string            `json:"title"`
	Model     string            `json:"model"`
	WorkDir   string            `json:"work_dir"`
	CreatedAt time.Time         `json:"created_at"`
	Messages  []types.Message `json:"messages"`
}

// Search finds sessions containing the query text in their messages.
func (s *Store) Search(query string, limit int) ([]Session, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT s.id, s.title, s.model, s.work_dir, s.created_at, s.updated_at,
			   (SELECT COUNT(*) FROM messages m2 WHERE m2.session_id = s.id) as msg_count
		FROM sessions s
		JOIN messages m ON m.session_id = s.id
		WHERE m.content LIKE ?
		ORDER BY s.updated_at DESC
		LIMIT ?
	`, "%"+query+"%", limit)
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
