package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Sinkear/flowloop/internal/events"
)

// TestFailedGoalCardExplainsItself (决策 6-19, user m00313): the failure card
// must lead with the human cause and the next step, with the raw executor
// error demoted to a 原始报错 tail — not 200 bytes of opaque black-box text.
func TestFailedGoalCardExplainsItself(t *testing.T) {
	sent := make(chan string, 4)
	n := New("app", "secret", "chat_id", "oc_mock")
	n.send = func(text string) error { sent <- text; return nil }
	n.SetQueryStore(&fakeQS{})
	bus := events.NewBus()
	n.Subscribe(bus)
	bus.Publish(context.Background(), events.Event{
		Topic: "goal:finished",
		Payload: map[string]any{
			"goal_id": "abc123456789",
			"status":  "failed",
			"summary": "prompt: acp: session/prompt: Internal error: ECONNREFUSED: Unable to connect. Is the computer able to access the url?",
			"cause":   "模型端点连接被拒——执行器配置的模型服务（本机网关或供应商 API）不可达",
			"advice":  "检查模型端点是否在运行、端口与网络是否可达。恢复后「重开任务」即可继续。",
		},
	})
	select {
	case card := <-sent:
		for _, want := range []string{
			"失败",
			"原因：模型端点连接被拒",
			"下一步：检查模型端点",
			"原始报错：",
			"ECONNREFUSED",
		} {
			if !strings.Contains(card, want) {
				t.Fatalf("failure card missing %q:\n%s", want, card)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no failure card sent")
	}
}

// TestFailedGoalCardWithoutCauseDegrades: events published elsewhere without
// cause/advice (older publishers, tests) must degrade to the legacy body —
// the raw summary alone, never an empty card.
func TestFailedGoalCardWithoutCauseDegrades(t *testing.T) {
	sent := make(chan string, 4)
	n := New("app", "secret", "chat_id", "oc_mock")
	n.send = func(text string) error { sent <- text; return nil }
	n.SetQueryStore(&fakeQS{})
	bus := events.NewBus()
	n.Subscribe(bus)
	bus.Publish(context.Background(), events.Event{
		Topic: "goal:finished",
		Payload: map[string]any{
			"goal_id": "abc123456789",
			"status":  "failed",
			"summary": "boom: gateway went away",
		},
	})
	select {
	case card := <-sent:
		if !strings.Contains(card, "boom: gateway went away") {
			t.Fatalf("legacy failure card must carry the raw summary:\n%s", card)
		}
		if strings.Contains(card, "原因：") || strings.Contains(card, "下一步：") {
			t.Fatalf("no cause/advice in the payload means no such sections:\n%s", card)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no failure card sent")
	}
}
