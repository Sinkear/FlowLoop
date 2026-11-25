package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"

	"github.com/Sinkear/flowloop/internal/logging"
)

// StartNextRoundFromProposal is the explicit link between "a proposal was
// approved" and "a new training round runs": RSI dead-ended because approval
// only produced a draft version, and neither publishing nor creating the next
// training goal happened anywhere. This is the deliberate human action that
// closes the loop (still human-triggered — RSI never starts work by itself):
//
//	approved proposal → publish its applied version (if still draft) →
//	clone the origin training goal (domain/squad/acceptance/budget) →
//	pin the new version → optionally start it immediately.
//
// Idempotent: the new goal carries source_ref="rsi-proposal:<proposalID>", so
// hitting the button twice returns the existing goal instead of spawning a
// duplicate round.
// nextRoundInFlight holds source_ref values whose next round is being created
// in this process. Sync.Map is process-wide on purpose: the handler builds a
// GoalService per request, so a per-service lock would not serialize anything.
var nextRoundInFlight sync.Map

func (s *GoalService) StartNextRoundFromProposal(ctx context.Context, proposalID, createdByType, createdByID string, start bool) (*Goal, bool, error) {
	if proposalID == "" {
		return nil, false, NewFieldRequiredError("proposal_id")
	}
	bp := NewBlueprintService(s.st, s.bus)
	p, err := bp.GetProposal(ctx, proposalID)
	if err != nil {
		return nil, false, fmt.Errorf("next round: proposal: %w", err)
	}
	if p.Status != "approved" || p.AppliedVersionID == "" {
		return nil, false, NewValidationError(fmt.Sprintf("proposal is %s — approve it first (approval opens the draft that the next round pins)", p.Status))
	}

	// Idempotency before publishing: a second click must not conflict on the
	// already-published version nor create a second goal.
	sourceRef := "rsi-proposal:" + p.ID
	// Single-flight: the check-then-create is not atomic in SQLite, so two
	// concurrent calls (the dialog's two buttons, a double click, two clients)
	// could both pass the existence check in the window between the publish
	// commit and the goal insert, and each would create a training round with
	// the same source_ref — double cost, duplicated rounds. Keyed by sourceRef,
	// which is unique per proposal.
	if _, busy := nextRoundInFlight.LoadOrStore(sourceRef, struct{}{}); busy {
		if existing, err := s.getBySourceRef(ctx, sourceRef); err == nil && existing != nil {
			return existing, false, nil
		}
		return nil, false, NewConflictError("a next round for this proposal is already being created — retry in a moment")
	}
	defer nextRoundInFlight.Delete(sourceRef)
	if existing, err := s.getBySourceRef(ctx, sourceRef); err != nil {
		return nil, false, fmt.Errorf("next round: check existing: %w", err)
	} else if existing != nil {
		return existing, false, nil
	}

	v, err := bp.GetVersion(ctx, p.AppliedVersionID)
	if err != nil {
		return nil, false, fmt.Errorf("next round: applied version: %w", err)
	}
	switch v.Status {
	case "draft":
		// Approval already re-validated the patched content; publishing here is
		// the explicit confirmation that this exact version becomes authority.
		if _, err := bp.Publish(ctx, v.ID, "RSI proposal: "+p.Title); err != nil {
			return nil, false, fmt.Errorf("next round: publish applied version: %w", err)
		}
		v.Status = "published"
	case "published":
		// already authority — nothing to do
	default:
		return nil, false, NewValidationError(fmt.Sprintf("applied version is %s — only a draft or published version can start a round", v.Status))
	}

	origin, err := s.nextRoundOrigin(ctx, p.FindingID)
	if err != nil {
		return nil, false, err
	}

	status := "backlog"
	if start {
		status = "active"
	}
	g := Goal{
		Title:              "下一轮：" + p.Title,
		Description:        nextRoundDescription(p, origin, v),
		DomainID:           origin.DomainID,
		AssigneeType:       origin.AssigneeType,
		AssigneeID:         origin.AssigneeID,
		Status:             status,
		Category:           origin.Category,
		CreatedByType:      createdByType,
		CreatedByID:        createdByID,
		ConnectionID:       origin.ConnectionID,
		ExecutionMode:      "train",
		ContentCapture:     origin.ContentCapture,
		BudgetUSD:          origin.BudgetUSD,
		SampleRate:         origin.SampleRate,
		BlueprintVersionID: v.ID,
		SourceRef:          sourceRef,
	}
	if g.CreatedByType == "" {
		g.CreatedByType = "human"
	}
	created, err := s.Create(ctx, g)
	if err != nil {
		// A second process (or a racing call that slipped past the single-flight)
		// may have created the round already; report that one instead of an error
		// that would invite another retry.
		if existing, e := s.getBySourceRef(ctx, sourceRef); e == nil && existing != nil {
			return existing, false, nil
		}
		return nil, false, fmt.Errorf("next round: create goal: %w", err)
	}
	logging.Infof("rsi: next round for proposal %q → goal %s (pin %s, %s)", p.Title, created.ID, v.ID[:8], status)
	return created, true, nil
}

// nextRoundOrigin is the training goal a proposal's finding came from — it
// donates the execution context (domain, squad/agent, acceptance level,
// capture/sampling/budget) so the next round is comparable to the baseline.
type nextRoundOrigin struct {
	GoalID         string
	Title          string
	DomainID       string
	AssigneeType   string
	AssigneeID     string
	Category       string
	ConnectionID   string
	ContentCapture string
	BudgetUSD      float64
	SampleRate     float64
}

func (s *GoalService) nextRoundOrigin(ctx context.Context, findingID string) (nextRoundOrigin, error) {
	var o nextRoundOrigin
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT g.id, g.title, COALESCE(g.domain_id,''), g.assignee_type, g.assignee_id,
		        COALESCE(g.category,'hitl'), COALESCE(g.connection_id,''),
		        COALESCE(g.content_capture,'redacted'), COALESCE(g.budget_usd,2), COALESCE(g.sample_rate,1)
		   FROM rsi_finding f
		   JOIN rsi_report rp ON rp.id=f.report_id
		   JOIN training_session ts ON ts.id=rp.session_id
		   JOIN goal g ON g.id=ts.goal_id
		  WHERE f.id=?`, findingID).
		Scan(&o.GoalID, &o.Title, &o.DomainID, &o.AssigneeType, &o.AssigneeID,
			&o.Category, &o.ConnectionID, &o.ContentCapture, &o.BudgetUSD, &o.SampleRate)
	if errors.Is(err, sql.ErrNoRows) {
		return o, NewValidationError("proposal has no linked training finding/goal — cannot infer the next round's domain and assignee")
	}
	if err != nil {
		return o, fmt.Errorf("next round: origin goal: %w", err)
	}
	// The origin goal's blueprint pin is irrelevant (the new round pins the
	// applied version), but the assignee must be executable.
	if o.AssigneeType != "agent" && o.AssigneeType != "squad" {
		return o, NewValidationError(fmt.Sprintf("origin goal assignee_type is %q — a training round needs an agent or squad", o.AssigneeType))
	}
	return o, nil
}

func nextRoundDescription(p *BlueprintProposal, o nextRoundOrigin, v *BlueprintVersion) string {
	dir := "↑"
	if p.ExpectedDirection == "down" {
		dir = "↓"
	}
	out := fmt.Sprintf("RSI 提案《%s》已批准，本目标用其应用版本 v%d 跑下一轮训练，验证提案效果。\n\n", p.Title, v.Version)
	out += fmt.Sprintf("- 提案：%s\n", p.ID)
	out += fmt.Sprintf("- 基线版本：%s\n", p.BaseVersionID)
	out += fmt.Sprintf("- 应用版本：v%d（%s）\n", v.Version, v.ID)
	if p.ExpectedMetric != "" {
		out += fmt.Sprintf("- 预期：%s %s\n", p.ExpectedMetric, dir)
	}
	if p.Hypothesis != "" {
		out += fmt.Sprintf("- 假设：%s\n", p.Hypothesis)
	}
	out += fmt.Sprintf("- 原始目标：%s（%s）\n", o.Title, o.GoalID)
	out += "\n域、小队/执行者、验收级别、采样率与预算继承自原始训练目标；执行模式为 train（终态会冻结实际图并做训练分析）。"
	return out
}

func (s *GoalService) getBySourceRef(ctx context.Context, sourceRef string) (*Goal, error) {
	var id string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id FROM goal WHERE source_ref=? LIMIT 1`, sourceRef).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}
