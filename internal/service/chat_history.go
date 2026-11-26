package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Sinkear/flowloop/internal/store"
)

// ChatHistoryService persists the web chat workbench's conversations.
//
// The workbench used to keep its transcript in localStorage only, so the
// history disappeared whenever the page was opened from another origin
// (localhost vs 127.0.0.1) or another browser. Since the app is single-user
// and the daemon is local, the canonical store is the daemon DB: every
// browser then sees the same conversations, and clearing site data no longer
// destroys them.
//
// The message rows are an opaque JSON array owned by the web client (the
// same ChatMsg shape it used to write to localStorage). The service only
// validates the envelope: id, title, participants, timestamps and count.
type ChatHistoryService struct{ st *store.Store }

func NewChatHistoryService(st *store.Store) *ChatHistoryService {
	return &ChatHistoryService{st: st}
}

// ChatConversationMeta is the sidebar/list shape — the full transcript is
// only fetched for the conversation the user opens.
type ChatConversationMeta struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Participants []string `json:"participants"`
	MessageCount int      `json:"message_count"`
	CreatedAt    int64    `json:"created_at"` // unix ms (the web client's clock)
	UpdatedAt    int64    `json:"updated_at"`
}

// ChatConversation is one full conversation: metadata + the transcript.
type ChatConversation struct {
	ChatConversationMeta
	Messages json.RawMessage `json:"messages"`
}

// chatHistoryMaxMessages caps the transcript the client may persist. The web
// client already trims to 300; a hard server cap keeps a bad client from
// filling the DB.
const chatHistoryMaxMessages = 500

// chatHistoryMaxBytes is a defensive payload cap (8 MiB). A normal 300-message
// transcript is well under it even with tool outputs.
const chatHistoryMaxBytes = 8 << 20

func (s *ChatHistoryService) List(ctx context.Context, limit int) ([]ChatConversationMeta, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,title,participants,message_count,created_at,updated_at
		   FROM chat_conversation ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list chat conversations: %w", err)
	}
	defer rows.Close()
	out := []ChatConversationMeta{}
	for rows.Next() {
		var m ChatConversationMeta
		var participants string
		if err := rows.Scan(&m.ID, &m.Title, &participants, &m.MessageCount, &m.CreatedAt, &m.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chat conversation: %w", err)
		}
		m.Participants = []string{}
		_ = json.Unmarshal([]byte(participants), &m.Participants)
		if m.Participants == nil {
			m.Participants = []string{}
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate chat conversations: %w", err)
	}
	return out, nil
}

func (s *ChatHistoryService) Get(ctx context.Context, id string) (*ChatConversation, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrNotFound
	}
	var c ChatConversation
	var participants, messages string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,title,participants,message_count,created_at,updated_at,messages
		   FROM chat_conversation WHERE id=?`, id).
		Scan(&c.ID, &c.Title, &participants, &c.MessageCount, &c.CreatedAt, &c.UpdatedAt, &messages)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get chat conversation: %w", err)
	}
	c.Participants = []string{}
	_ = json.Unmarshal([]byte(participants), &c.Participants)
	if c.Participants == nil {
		c.Participants = []string{}
	}
	if strings.TrimSpace(messages) == "" {
		messages = "[]"
	}
	c.Messages = json.RawMessage(messages)
	return &c, nil
}

// Save upserts one conversation. The web client owns id/created_at so a
// retry or a second tab cannot fork the same conversation into two rows;
// updated_at defaults to now.
func (s *ChatHistoryService) Save(ctx context.Context, c ChatConversation) (*ChatConversationMeta, error) {
	if strings.TrimSpace(c.ID) == "" {
		c.ID = newID()
	}
	if len(c.Messages) == 0 {
		c.Messages = json.RawMessage("[]")
	}
	if len(c.Messages) > chatHistoryMaxBytes {
		return nil, fmt.Errorf("chat conversation %q is too large (%d bytes)", c.ID, len(c.Messages))
	}
	var msgs []json.RawMessage
	if err := json.Unmarshal(c.Messages, &msgs); err != nil {
		return nil, fmt.Errorf("messages must be a JSON array: %w", err)
	}
	if len(msgs) > chatHistoryMaxMessages {
		msgs = msgs[len(msgs)-chatHistoryMaxMessages:]
		trimmed, err := json.Marshal(msgs)
		if err != nil {
			return nil, fmt.Errorf("trim messages: %w", err)
		}
		c.Messages = trimmed
	}
	if c.Participants == nil {
		c.Participants = []string{}
	}
	participants, err := json.Marshal(c.Participants)
	if err != nil {
		return nil, fmt.Errorf("marshal participants: %w", err)
	}
	now := time.Now().UnixMilli()
	if c.CreatedAt <= 0 {
		c.CreatedAt = now
	}
	if c.UpdatedAt <= 0 || c.UpdatedAt < c.CreatedAt {
		c.UpdatedAt = now
	}
	if c.UpdatedAt < c.CreatedAt {
		c.UpdatedAt = c.CreatedAt
	}
	c.MessageCount = len(msgs)
	if strings.TrimSpace(c.Title) == "" {
		c.Title = "新对话"
	}
	_, err = s.st.DB().ExecContext(ctx,
		`INSERT INTO chat_conversation (id,title,participants,messages,message_count,created_at,updated_at)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(id) DO UPDATE SET
		   title=excluded.title,
		   participants=excluded.participants,
		   messages=excluded.messages,
		   message_count=excluded.message_count,
		   updated_at=excluded.updated_at`,
		c.ID, c.Title, string(participants), string(c.Messages), c.MessageCount, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("save chat conversation: %w", err)
	}
	return &c.ChatConversationMeta, nil
}

func (s *ChatHistoryService) Delete(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return ErrNotFound
	}
	res, err := s.st.DB().ExecContext(ctx, `DELETE FROM chat_conversation WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete chat conversation: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
