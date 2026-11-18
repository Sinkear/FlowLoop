package daemon

import (
	"context"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/logging"
)

// onInferShapeRecorder (Phase C §12.4 涌现固化): goal:finished hook. Only
// status=done + unbound (infer) goals carry an emergent signal — the recorder
// itself re-checks, so this hook can subscribe unconditionally. Errors never
// break the terminal flow (logged only).
func (d *Daemon) onInferShapeRecorder(ctx context.Context, e events.Event) {
	payload, _ := e.Payload.(map[string]any)
	goalID, _ := payload["goal_id"].(string)
	if goalID == "" {
		return
	}
	if d.rsi == nil {
		return
	}
	if err := d.rsi.RecordInferShape(ctx, goalID); err != nil {
		logging.Errorf("infer-shape recorder: goal %s: %v", goalID, err)
	}
}
