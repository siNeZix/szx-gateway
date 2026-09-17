package store

import (
	"crypto/subtle"
	"database/sql"
	"time"
)

type OneMinAIConversation struct {
	ID, OwnerHash, KeyHash, UpstreamID, Model string
	CreatedAt, UpdatedAt, ExpiresAt           time.Time
}
type OneMinAIAsset struct {
	ID, ConversationID, OwnerHash, KeyHash, UpstreamID, Kind, ContentType, Filename, SHA256 string
	SizeBytes                                                                               int64
	CreatedAt, ExpiresAt                                                                    time.Time
}

func (s *Store) CreateOneMinAIConversation(v OneMinAIConversation) error {
	_, err := s.db.Exec(`INSERT INTO oneminai_conversations (id, owner_hash, key_hash, upstream_id, model, created_at, updated_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, v.ID, v.OwnerHash, v.KeyHash, v.UpstreamID, v.Model, v.CreatedAt.UTC(), v.UpdatedAt.UTC(), v.ExpiresAt.UTC())
	return err
}
func (s *Store) GetOneMinAIConversation(id, ownerHash string, now time.Time) (OneMinAIConversation, bool, error) {
	var v OneMinAIConversation
	err := s.db.QueryRow(`SELECT id, owner_hash, key_hash, upstream_id, model, created_at, updated_at, expires_at FROM oneminai_conversations WHERE id = ? AND expires_at > ?`, id, now.UTC()).Scan(&v.ID, &v.OwnerHash, &v.KeyHash, &v.UpstreamID, &v.Model, &v.CreatedAt, &v.UpdatedAt, &v.ExpiresAt)
	if err == sql.ErrNoRows {
		return v, false, nil
	}
	if err != nil {
		return v, false, err
	}
	if subtle.ConstantTimeCompare([]byte(v.OwnerHash), []byte(ownerHash)) != 1 {
		return v, false, nil
	}
	return v, true, nil
}
func (s *Store) TouchOneMinAIConversation(id string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE oneminai_conversations SET updated_at = ? WHERE id = ?`, now.UTC(), id)
	return err
}
func (s *Store) CleanupExpiredOneMinAI(now time.Time, batch int) (int64, error) {
	if batch <= 0 {
		batch = 100
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM oneminai_conversations WHERE expires_at <= ? ORDER BY expires_at LIMIT ?`, now.UTC(), batch)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if _, err = tx.Exec(`DELETE FROM oneminai_assets WHERE conversation_id = ?`, id); err != nil {
			return 0, err
		}
		if _, err = tx.Exec(`DELETE FROM oneminai_conversations WHERE id = ?`, id); err != nil {
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}
func (s *Store) CreateOneMinAIAsset(v OneMinAIAsset) error {
	_, err := s.db.Exec(`INSERT INTO oneminai_assets (id, conversation_id, owner_hash, key_hash, upstream_id, kind, content_type, filename, size_bytes, sha256, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, v.ID, v.ConversationID, v.OwnerHash, v.KeyHash, v.UpstreamID, v.Kind, v.ContentType, v.Filename, v.SizeBytes, v.SHA256, v.CreatedAt.UTC(), v.ExpiresAt.UTC())
	return err
}
