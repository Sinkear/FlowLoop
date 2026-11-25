package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/Sinkear/flowloop/internal/logging"
)

// PlanAlignment is the read-only join between the compiled spec (blueprint_node)
// and what actually ran (sub_goal / run / consult_request). It is the anchor the
// incident lacked: before it, no code compared "计划图" with "实际执行图".
type PlanAlignment struct {
	GoalID              string             `json:"goal_id"`
	BlueprintVersionID  string             `json:"blueprint_version_id"`
	BlueprintName       string             `json:"blueprint_name"`
	Pinned              bool               `json:"pinned"`
	Materialized        bool               `json:"materialized"`
	Nodes               []AlignmentNode    `json:"nodes"`
	SubGoals            []AlignmentSubGoal `json:"sub_goals"`
	Planned             int                `json:"planned"`
	Settled             int                `json:"settled"`
	Mapped              int                `json:"mapped"`   // nodes with a sub_goal_id
	Executed            int                `json:"executed"` // mapped nodes whose sub_goal exists
	UnplannedSubGoals   int                `json:"unplanned_sub_goals"`
	ConsultCount        int                `json:"consult_count"`
	Writes              int                `json:"writes"`              // delivered commits recorded at deliver time
	AttributedWrites    int                `json:"attributed_writes"`   // commits carrying a known Blueprint-Node trailer
	UnattributedWrites  int                `json:"unattributed_writes"` // 计划外写入: commits with no (known) trailer
	UnattributedCommits []string           `json:"unattributed_commits"`
	Deviations          []string           `json:"deviations"`
}

type AlignmentNode struct {
	NodeKey       string         `json:"node_key"`
	NodeType      string         `json:"node_type"`
	State         string         `json:"state"`
	SubGoalID     string         `json:"sub_goal_id"`
	SubGoalStatus string         `json:"sub_goal_status"`
	Title         string         `json:"title"`
	Runs          []AlignmentRun `json:"runs"`
}

type AlignmentRun struct {
	ID      string `json:"id"`
	Role    string `json:"role"`
	Status  string `json:"status"`
	Summary string `json:"summary"`
}

type AlignmentSubGoal struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Status   string `json:"status"`
	Assignee string `json:"assignee_id"`
	Planned  bool   `json:"planned"`
}

// PlanAlignment assembles the plan-vs-actual table. A pinned goal with no
// ledger rows is itself the deviation (materialized=false), not an error.
func (s *GoalService) PlanAlignment(ctx context.Context, goalID string) (*PlanAlignment, error) {
	out := &PlanAlignment{GoalID: goalID, Nodes: []AlignmentNode{}, SubGoals: []AlignmentSubGoal{}, UnattributedCommits: []string{}, Deviations: []string{}}
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(g.blueprint_version_id,''), COALESCE(b.name,'')
		   FROM goal g
		   LEFT JOIN blueprint_version v ON v.id=g.blueprint_version_id
		   LEFT JOIN blueprint b ON b.id=v.blueprint_id
		  WHERE g.id=?`, goalID).Scan(&out.BlueprintVersionID, &out.BlueprintName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("plan alignment: goal: %w", err)
	}
	out.Pinned = out.BlueprintVersionID != ""

	nodes := map[string]*AlignmentNode{}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT node_key, node_type, COALESCE(sub_goal_id,''), state
		   FROM blueprint_node WHERE goal_id=? ORDER BY node_key`, goalID)
	if err != nil {
		return nil, fmt.Errorf("plan alignment: nodes: %w", err)
	}
	for rows.Next() {
		n := &AlignmentNode{Runs: []AlignmentRun{}}
		if err := rows.Scan(&n.NodeKey, &n.NodeType, &n.SubGoalID, &n.State); err != nil {
			rows.Close()
			return nil, err
		}
		nodes[n.NodeKey] = n
		out.Nodes = append(out.Nodes, *n)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	out.Planned = len(out.Nodes)
	out.Materialized = out.Planned > 0

	mapped := map[string]*AlignmentNode{}
	for i := range out.Nodes {
		if out.Nodes[i].SubGoalID != "" {
			out.Mapped++
			mapped[out.Nodes[i].SubGoalID] = &out.Nodes[i]
		}
	}

	sgRows, err := s.st.DB().QueryContext(ctx,
		`SELECT id, title, status, assignee_id FROM sub_goal WHERE goal_id=? ORDER BY created_at`, goalID)
	if err != nil {
		return nil, fmt.Errorf("plan alignment: sub_goals: %w", err)
	}
	for sgRows.Next() {
		var sg AlignmentSubGoal
		if err := sgRows.Scan(&sg.ID, &sg.Title, &sg.Status, &sg.Assignee); err != nil {
			sgRows.Close()
			return nil, err
		}
		sg.Planned = mapped[sg.ID] != nil
		if sg.Planned {
			out.Executed++
		} else {
			out.UnplannedSubGoals++
			out.Deviations = append(out.Deviations,
				fmt.Sprintf("计划外子任务 %s（%s），不属于任何 blueprint_node", sg.ID, sg.Title))
		}
		if n := mapped[sg.ID]; n != nil {
			n.SubGoalStatus = sg.Status
			if n.Title == "" {
				n.Title = sg.Title
			}
		}
		out.SubGoals = append(out.SubGoals, sg)
	}
	if err := sgRows.Err(); err != nil {
		sgRows.Close()
		return nil, err
	}
	sgRows.Close()

	for i := range out.Nodes {
		if out.Nodes[i].State == "settled" || out.Nodes[i].State == "skipped" {
			out.Settled++
		}
	}

	// Runs per sub_goal (the actual execution attached to a node).
	runRows, err := s.st.DB().QueryContext(ctx,
		`SELECT id, role, status, COALESCE(sub_goal_id,''), COALESCE(result_summary,'')
		   FROM run WHERE goal_id=? ORDER BY COALESCE(started_at, created_at)`, goalID)
	if err != nil {
		return nil, fmt.Errorf("plan alignment: runs: %w", err)
	}
	for runRows.Next() {
		var r AlignmentRun
		var subGoalID string
		if err := runRows.Scan(&r.ID, &r.Role, &r.Status, &subGoalID, &r.Summary); err != nil {
			runRows.Close()
			return nil, err
		}
		if n := mapped[subGoalID]; n != nil {
			n.Runs = append(n.Runs, r)
		}
	}
	if err := runRows.Err(); err != nil {
		runRows.Close()
		return nil, err
	}
	runRows.Close()

	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM consult_request WHERE goal_id=?`, goalID).Scan(&out.ConsultCount); err != nil {
		return nil, fmt.Errorf("plan alignment: consults: %w", err)
	}

	// Delivered-commit attribution (BUG-4): the owner-write ledger recorded at
	// deliver time. Read-only here — an unreadable ledger must not turn the
	// whole alignment report into an error, so it only warns.
	if writes, err := s.ListBlueprintWrites(ctx, goalID); err != nil {
		logging.Warnf("plan-alignment: goal %s: blueprint writes: %v", goalID, err)
	} else {
		out.Writes = len(writes)
		for _, w := range writes {
			if w.Attributed {
				out.AttributedWrites++
			} else {
				out.UnattributedWrites++
				out.UnattributedCommits = append(out.UnattributedCommits, w.CommitSHA+" "+w.Subject)
			}
		}
	}

	// Deviation verdicts — the signals the incident had no way to surface.
	if out.Pinned && !out.Materialized {
		out.Deviations = append(out.Deviations,
			fmt.Sprintf("计划未物化：绑定蓝本 %s，但 blueprint_node=0（执行从未按规范图编译）", out.BlueprintVersionID))
	}
	if out.Pinned && out.Materialized {
		for _, n := range out.Nodes {
			// spawn 是 holder（child 才是执行载体），没有自己的 sub_goal 不是偏离。
			if n.SubGoalID == "" && n.NodeType != "end" && n.NodeType != "gate" && n.NodeType != "condition" && n.NodeType != "router" && n.NodeType != "spawn" {
				out.Deviations = append(out.Deviations,
					fmt.Sprintf("节点 %s（%s/%s）没有对应的执行子任务", n.NodeKey, n.NodeType, n.State))
			}
		}
		if out.ConsultCount > 0 && out.Executed < out.Planned {
			out.Deviations = append(out.Deviations,
				fmt.Sprintf("执行覆盖率不足：%d/%d 节点有执行子任务，同时存在 %d 次咨询派发（咨询不是执行）",
					out.Executed, out.Planned, out.ConsultCount))
		}
	}
	// A pinned goal that ran entirely through consults: the incident's exact
	// signature (0 sub_goals, >0 consult dispatches).
	if out.Pinned && out.ConsultCount > 0 && out.Executed == 0 {
		out.Deviations = append(out.Deviations,
			fmt.Sprintf("咨询化执行：0 个计划内执行子任务，却有 %d 次 consult 派发——工作没有落在计划节点上", out.ConsultCount))
	}
	// 计划外写入: delivered commits that belong to no spec node. Attribution
	// never blocks a delivery; it surfaces here (and in the RSI finding).
	if out.Pinned && out.UnattributedWrites > 0 {
		out.Deviations = append(out.Deviations,
			fmt.Sprintf("计划外写入：%d 个交付提交没有 Blueprint-Node 归户（owner 直接落盘）", out.UnattributedWrites))
	}
	if len(out.Deviations) > 0 {
		logging.Warnf("plan-alignment: goal %s deviates from its spec (%d deviation(s)): %v", goalID, len(out.Deviations), out.Deviations)
	}
	return out, nil
}

// planAlignmentFindings turns the alignment table into an RSI finding so a
// deviating run enters the training loop instead of being invisible.
func (s *RSIService) planAlignmentFindings(ctx context.Context, goalID string) []RsiFinding {
	gs := NewGoalService(s.st, s.bus)
	al, err := gs.PlanAlignment(ctx, goalID)
	if err != nil || al == nil || !al.Pinned {
		return nil
	}
	if len(al.Deviations) == 0 {
		return nil
	}
	sev := "warn"
	score := scoreBaseWarn
	if !al.Materialized || (al.ConsultCount > 0 && al.Executed == 0) {
		sev = "critical"
		score = scoreBaseCritical
	}
	return []RsiFinding{{
		Category:   "plan_deviation",
		Severity:   sev,
		Confidence: "L1",
		Title:      fmt.Sprintf("计划与执行不一致：%d 处偏离（计划 %d 节点 / 已落地 %d / 咨询 %d 次）", len(al.Deviations), al.Planned, al.Executed, al.ConsultCount),
		Explanation: fmt.Sprintf("绑定蓝本 %s（%s），但实际执行与编译图对不上：%v",
			al.BlueprintVersionID, al.BlueprintName, al.Deviations),
		Suggestion: "把执行拉回编译图：未物化则先激活/编译；计划外子任务与 consult 派发应改为节点执行，或显式登记为 plan_mode=manual。",
		Evidence: []string{
			fmt.Sprintf("planned=%d settled=%d mapped=%d executed=%d unplanned_sub_goals=%d consult_count=%d materialized=%v",
				al.Planned, al.Settled, al.Mapped, al.Executed, al.UnplannedSubGoals, al.ConsultCount, al.Materialized),
			"deviations: " + strings.Join(al.Deviations, " | "),
		},
		Score: score,
	}}
}
