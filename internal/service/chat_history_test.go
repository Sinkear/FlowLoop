package service

import (
	"context"
	"encoding/json"
	"testing"
)

func TestChatHistorySaveListGetDelete(t *testing.T) {
	st := newTestStore(t)
	svc := NewChatHistoryService(st)
	ctx := context.Background()

	first, err := svc.Save(ctx, ChatConversation{
		ChatConversationMeta: ChatConversationMeta{
			ID: "c-1", Title: "第一次对话", Participants: []string{"a1"}, CreatedAt: 1000,
		},
		Messages: json.RawMessage(`[{"id":1,"kind":"user","text":"hello"}]`),
	})
	if err != nil {
		t.Fatalf("save first: %v", err)
	}
	if first.MessageCount != 1 {
		t.Fatalf("message_count = %d, want 1", first.MessageCount)
	}
	if first.CreatedAt != 1000 || first.UpdatedAt < 1000 {
		t.Fatalf("timestamps = %d/%d, want created 1000 and updated >= 1000", first.CreatedAt, first.UpdatedAt)
	}

	// A later save upserts (does not duplicate) and moves the row to the top
	// of the list.
	if _, err := svc.Save(ctx, ChatConversation{
		ChatConversationMeta: ChatConversationMeta{
			ID: "c-1", Title: "改名了", Participants: []string{"a1", "a2"}, CreatedAt: 1000, UpdatedAt: 5000,
		},
		Messages: json.RawMessage(`[
			{"id":1,"kind":"user","text":"hello"},
			{"id":2,"kind":"agent","text":"hi"}
		]`),
	}); err != nil {
		t.Fatalf("save second: %v", err)
	}
	if _, err := svc.Save(ctx, ChatConversation{
		ChatConversationMeta: ChatConversationMeta{ID: "c-2", Title: "更旧", CreatedAt: 500, UpdatedAt: 2000},
	}); err != nil {
		t.Fatalf("save c-2: %v", err)
	}

	list, err := svc.List(ctx, 10)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 || list[0].ID != "c-1" {
		t.Fatalf("list order = %+v, want c-1 first (updated 5000 > 2000)", list)
	}
	if list[0].MessageCount != 2 || list[0].Title != "改名了" || len(list[0].Participants) != 2 {
		t.Fatalf("list meta = %+v, want updated title/count/participants", list[0])
	}

	got, err := svc.Get(ctx, "c-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var msgs []map[string]any
	if err := json.Unmarshal(got.Messages, &msgs); err != nil || len(msgs) != 2 {
		t.Fatalf("messages = %s (err %v), want 2 entries", got.Messages, err)
	}

	if _, err := svc.Get(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("get missing err = %v, want ErrNotFound", err)
	}

	if err := svc.Delete(ctx, "c-2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := svc.Delete(ctx, "c-2"); err != ErrNotFound {
		t.Fatalf("delete again err = %v, want ErrNotFound", err)
	}
	list, _ = svc.List(ctx, 10)
	if len(list) != 1 || list[0].ID != "c-1" {
		t.Fatalf("after delete list = %+v", list)
	}
}

func TestChatHistoryRejectsNonArrayMessages(t *testing.T) {
	st := newTestStore(t)
	svc := NewChatHistoryService(st)
	_, err := svc.Save(context.Background(), ChatConversation{
		ChatConversationMeta: ChatConversationMeta{ID: "bad"},
		Messages:             json.RawMessage(`{"not":"an array"}`),
	})
	if err == nil {
		t.Fatal("Save accepted a non-array messages payload")
	}
}
