package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Sinkear/ouroflow/internal/events"
)

func newPracticeImportForTest(c *templateTestCluster) *PracticeImportService {
	return NewPracticeImportService(c.apply, NewBlueprintService(c.st, events.NewBus()))
}

func practiceJSON(t *testing.T, body string) json.RawMessage {
	t.Helper()
	if !json.Valid([]byte(body)) {
		t.Fatalf("test fixture is not valid JSON: %s", body)
	}
	return json.RawMessage(body)
}

const practiceSquadJSON = `{
  "api_version": "ouroflow/v1",
  "kind": "squad",
  "metadata": {"name": "调研小队", "description": "收集资料", "version": "1.0.0"},
  "spec": {
    "agents": [
      {"name": "scout-lead", "description": "负责人", "system_prompt": "你是负责人。", "runtime": "test-rt"},
      {"name": "scout-worker", "description": "成员", "system_prompt": "你是成员。", "runtime": "test-rt"}
    ],
    "squad": {
      "name": "调研小队",
      "description": "测试小队",
      "leader": "scout-lead",
      "instructions": "拆解并委派。",
      "members": [{"name": "scout-worker", "role": "member"}]
    }
  }
}`

const practiceProjectJSON = `{
  "api_version": "ouroflow/v1",
  "kind": "project",
  "metadata": {"name": "数据平台", "description": "测试项目", "version": "1.0.0"},
  "spec": {
    "domain": {"type": "scratch", "default_branch": "main"},
    "team": {
      "agents": [
        {"name": "data-owner", "description": "负责人", "system_prompt": "你是负责人。", "runtime": "test-rt"},
        {"name": "data-dev", "description": "开发", "system_prompt": "你是开发。", "runtime": "test-rt"}
      ],
      "squad": {
        "name": "数据小队",
        "leader": "data-owner",
        "instructions": "拆解。",
        "members": [{"name": "data-dev", "role": "member"}]
      }
    },
    "goals": [
      {"title": "搭骨架", "assignee": "data-owner", "assignee_type": "agent", "start": true},
      {"title": "写采集", "assignee": "数据小队", "assignee_type": "squad"}
    ],
    "schedules": [
      {"name": "每日巡检", "title": "巡检", "cron": "0 9 * * *", "assignee": "data-owner", "assignee_type": "agent"}
    ]
  }
}`

func TestPracticeImportSquadCreatesCopies(t *testing.T) {
	c := newTemplateCluster(t)
	svc := newPracticeImportForTest(c)
	ctx := context.Background()
	req := PracticeImportRequest{Package: practiceJSON(t, practiceSquadJSON)}

	preview, err := svc.Preview(ctx, req)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Kind != practiceKindSquad || preview.Name != "调研小队" || len(preview.Agents) != 2 {
		t.Fatalf("unexpected preview: %+v", preview)
	}
	if preview.Source != "unverified" || preview.SourceLabel != "未验证" {
		t.Fatalf("local import must be unverified: %+v", preview)
	}

	res, err := svc.Import(ctx, req)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Squad == nil || res.Squad.Name != "调研小队" {
		t.Fatalf("squad missing from result: %+v", res)
	}
	if len(res.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %+v", res.Agents)
	}
	var scratchCount int
	if err := c.st.DB().QueryRow(`SELECT COUNT(1) FROM domain WHERE name = ? AND type = 'scratch'`, "调研小队").Scan(&scratchCount); err != nil {
		t.Fatalf("query scratch domain: %v", err)
	}
	if scratchCount != 1 {
		t.Fatalf("squad import should create its scratch workspace, got %d", scratchCount)
	}

	second, err := svc.Import(ctx, req)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	if second.Squad == nil || second.Squad.Name != "调研小队（副本）" {
		t.Fatalf("second import must suffix the copy, got %+v", second.Squad)
	}
	if second.Squad.ID == res.Squad.ID {
		t.Fatalf("second import reused the squad instead of copying it")
	}
	names := map[string]bool{}
	for _, a := range second.Agents {
		names[a.Name] = true
	}
	if !names["scout-lead（副本）"] || !names["scout-worker（副本）"] {
		t.Fatalf("agents should be copied independently, got %+v", second.Agents)
	}
}

func TestPracticeImportPreviewDoesNotWrite(t *testing.T) {
	c := newTemplateCluster(t)
	svc := newPracticeImportForTest(c)
	ctx := context.Background()
	before := tableCount(t, c, "squad")

	preview, err := svc.Preview(ctx, PracticeImportRequest{Package: practiceJSON(t, practiceSquadJSON)})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Name == "" {
		t.Fatalf("preview should name the planned copy")
	}
	if got := tableCount(t, c, "squad"); got != before {
		t.Fatalf("preview wrote to the database: before=%d after=%d", before, got)
	}
	if got := tableCount(t, c, "agent"); got != 0 {
		t.Fatalf("preview created agents: %d", got)
	}
}

func TestPracticeImportProjectCreatesScratchProjectAndTeam(t *testing.T) {
	c := newTemplateCluster(t)
	svc := newPracticeImportForTest(c)
	ctx := context.Background()
	res, err := svc.Import(ctx, PracticeImportRequest{Package: practiceJSON(t, practiceProjectJSON)})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Project == nil || res.Project.Name != "数据平台" {
		t.Fatalf("project missing from result: %+v", res.Project)
	}
	if res.Squad == nil || res.Squad.Name != "数据小队" {
		t.Fatalf("team squad missing from result: %+v", res.Squad)
	}
	if len(res.Goals) != 2 {
		t.Fatalf("expected 2 goals, got %+v", res.Goals)
	}
	if len(res.Schedules) != 1 {
		t.Fatalf("expected 1 schedule, got %+v", res.Schedules)
	}
	for _, item := range res.Items {
		if item.Error != "" {
			t.Fatalf("unexpected failed item %+v", item)
		}
	}
	var assignees int
	if err := c.st.DB().QueryRow(`SELECT COUNT(1) FROM goal WHERE domain_id = ? AND assignee_id <> ''`, res.Project.ID).Scan(&assignees); err != nil {
		t.Fatalf("query goals: %v", err)
	}
	if assignees != 2 {
		t.Fatalf("goals should resolve assignee ids, got %d", assignees)
	}
	var schedDomain string
	if err := c.st.DB().QueryRow(`SELECT domain_id FROM schedule WHERE name = ?`, "每日巡检").Scan(&schedDomain); err != nil {
		t.Fatalf("query schedule: %v", err)
	}
	if schedDomain != res.Project.ID {
		t.Fatalf("schedule not attached to the imported project: %q", schedDomain)
	}
}

func TestPracticeImportWorkflowRequiresOwnerAndRewritesAssignees(t *testing.T) {
	c := newTemplateCluster(t)
	svc := newPracticeImportForTest(c)
	ctx := context.Background()
	lead, err := c.agentSvc.Create(ctx, Agent{Name: "wf-lead", RuntimeID: "rt-1"})
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	owner, err := c.squadSvc.Create(ctx, Squad{Name: "工作流小队", LeaderID: lead.ID})
	if err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	pkg := practiceJSON(t, `{
      "api_version": "ouroflow/v1",
      "kind": "workflow",
      "metadata": {"name": "发布流程", "description": "测试工作流"},
      "spec": {
        "squad": "工作流小队",
        "graph": {
          "name": "发布流程",
          "nodes": [
            {"id": "plan", "type": "task", "title": "计划", "task": {"assignee": "agent:wf-lead", "brief": "拆解发布"}},
            {"id": "review", "type": "task", "title": "复核", "task": {"assignee": "squad:owner", "brief": "复核结果"}},
            {"id": "done", "type": "end", "title": "完成", "end": {"outcome": "success"}}
          ],
          "edges": [
            {"source": "plan", "source_handle": "done", "target": "review"},
            {"source": "review", "source_handle": "done", "target": "done"}
          ]
        }
      }
    }`)

	preview, err := svc.Preview(ctx, PracticeImportRequest{Package: pkg, SquadID: owner.ID})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.OwnerSquadID != owner.ID || preview.WorkflowNodeCount != 3 {
		t.Fatalf("unexpected workflow preview: %+v", preview)
	}

	res, err := svc.Import(ctx, PracticeImportRequest{Package: pkg, SquadID: owner.ID})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Workflow == nil {
		t.Fatalf("workflow missing from result: %+v", res)
	}
	bp, versions, err := svc.blueprints.Get(ctx, res.Workflow.ID)
	if err != nil {
		t.Fatalf("get blueprint: %v", err)
	}
	if bp.SquadID != owner.ID {
		t.Fatalf("workflow must belong to the selected squad: %q", bp.SquadID)
	}
	if len(versions) != 1 || versions[0].Status != "draft" {
		t.Fatalf("workflow must be imported as a draft, got %+v", versions)
	}
	if !strings.Contains(versions[0].Content, "agent:"+lead.ID) {
		t.Fatalf("agent assignee was not rewritten to its id: %s", versions[0].Content)
	}
	if !strings.Contains(versions[0].Content, "squad:"+owner.ID) {
		t.Fatalf("squad:owner was not rewritten to the owner id: %s", versions[0].Content)
	}
}

func TestPracticeImportWorkflowRejectsCrossSquadAndMissingOwner(t *testing.T) {
	c := newTemplateCluster(t)
	svc := newPracticeImportForTest(c)
	ctx := context.Background()
	lead, err := c.agentSvc.Create(ctx, Agent{Name: "wf-lead", RuntimeID: "rt-1"})
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	owner, err := c.squadSvc.Create(ctx, Squad{Name: "工作流小队", LeaderID: lead.ID})
	if err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	other, err := c.squadSvc.Create(ctx, Squad{Name: "其他小队", LeaderID: lead.ID})
	if err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	crossPackage := practiceJSON(t, `{
      "api_version": "ouroflow/v1", "kind": "workflow",
      "metadata": {"name": "跨队流程"},
      "spec": {"graph": {"name": "跨队流程",
        "nodes": [
          {"id": "plan", "type": "task", "title": "计划", "task": {"assignee": "squad:其他小队", "brief": "x"}},
          {"id": "done", "type": "end", "title": "完成", "end": {"outcome": "success"}}
        ],
        "edges": [{"source": "plan", "source_handle": "done", "target": "done"}]
      }}
    }`)
	_, err = svc.Preview(ctx, PracticeImportRequest{Package: crossPackage, SquadID: owner.ID})
	if err == nil || !strings.Contains(err.Error(), "不能跨队") {
		t.Fatalf("expected cross-squad rejection, got %v", err)
	}
	if strings.Contains(err.Error(), other.ID) {
		t.Fatalf("error should name the squads, not dump ids: %v", err)
	}

	noOwnerPkg := practiceJSON(t, `{
      "api_version": "ouroflow/v1", "kind": "workflow",
      "metadata": {"name": "无归属流程"},
      "spec": {"graph": {"name": "无归属流程",
        "nodes": [
          {"id": "plan", "type": "task", "title": "计划", "task": {"assignee": "capability:developer", "brief": "x"}},
          {"id": "done", "type": "end", "title": "完成", "end": {"outcome": "success"}}
        ],
        "edges": [{"source": "plan", "source_handle": "done", "target": "done"}]
      }}
    }`)
	preview, err := svc.Preview(ctx, PracticeImportRequest{Package: noOwnerPkg})
	if err != nil {
		t.Fatalf("preview without owner should not fail: %v", err)
	}
	if !preview.NeedsSquad {
		t.Fatalf("preview must ask for an owner squad: %+v", preview)
	}
	if _, err := svc.Import(ctx, PracticeImportRequest{Package: noOwnerPkg}); err == nil || !strings.Contains(err.Error(), "归属小队") {
		t.Fatalf("import without owner must fail, got %v", err)
	}
}

func TestPracticeImportValidatesPackage(t *testing.T) {
	c := newTemplateCluster(t)
	svc := newPracticeImportForTest(c)
	ctx := context.Background()

	cases := []struct {
		name string
		pkg  string
		want string
	}{
		{"invalid json", `{`, "JSON"},
		{"unknown field", `{"api_version":"ouroflow/v1","kind":"squad","metadata":{"name":"x"},"spec":{"agents":[]},"extra":true}`, "JSON"},
		{"bad kind", `{"api_version":"ouroflow/v1","kind":"agent","metadata":{"name":"x"},"spec":{}}`, "不支持"},
		{"missing metadata name", `{"api_version":"ouroflow/v1","kind":"squad","metadata":{},"spec":{}}`, "metadata.name"},
		{"repo create rejected", `{"api_version":"ouroflow/v1","kind":"project","metadata":{"name":"x"},"spec":{"domain":{"type":"repo"},"repo":{"create":true}}}`, "不会代建仓库"},
		{"empty agents", `{"api_version":"ouroflow/v1","kind":"squad","metadata":{"name":"x"},"spec":{"agents":[],"squad":{"name":"x","leader":"y"}}}`, "agents"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Preview(ctx, PracticeImportRequest{Package: json.RawMessage(tc.pkg)})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}

	var count int
	if err := c.st.DB().QueryRow(`SELECT COUNT(1) FROM domain`).Scan(&count); err != nil {
		t.Fatalf("query domains: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected packages must not write, found %d domains", count)
	}
}

func TestPracticeImportRequiresGitURLForRepoProject(t *testing.T) {
	c := newTemplateCluster(t)
	svc := newPracticeImportForTest(c)
	ctx := context.Background()
	pkg := practiceJSON(t, `{
      "api_version": "ouroflow/v1", "kind": "project",
      "metadata": {"name": "仓库项目"},
      "spec": {"domain": {"type": "repo", "git_url": ""}}
    }`)
	preview, err := svc.Preview(ctx, PracticeImportRequest{Package: pkg})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !preview.NeedsGitURL {
		t.Fatalf("preview must flag the missing repository url: %+v", preview)
	}
	if _, err := svc.Import(ctx, PracticeImportRequest{Package: pkg}); err == nil || !strings.Contains(err.Error(), "Git 仓库地址") {
		t.Fatalf("repo project without url must fail, got %v", err)
	}
}

func TestPracticeImportMissingAgentFailsClearly(t *testing.T) {
	c := newTemplateCluster(t)
	svc := newPracticeImportForTest(c)
	ctx := context.Background()
	lead, err := c.agentSvc.Create(ctx, Agent{Name: "wf-lead", RuntimeID: "rt-1"})
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	owner, err := c.squadSvc.Create(ctx, Squad{Name: "工作流小队", LeaderID: lead.ID})
	if err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	pkg := practiceJSON(t, `{
      "api_version": "ouroflow/v1", "kind": "workflow",
      "metadata": {"name": "缺 Agent"},
      "spec": {"graph": {"name": "缺 Agent",
        "nodes": [
          {"id": "plan", "type": "task", "title": "计划", "task": {"assignee": "agent:missing-dev", "brief": "x"}},
          {"id": "done", "type": "end", "title": "完成", "end": {"outcome": "success"}}
        ],
        "edges": [{"source": "plan", "source_handle": "done", "target": "done"}]
      }}
    }`)
	_, err = svc.Preview(ctx, PracticeImportRequest{Package: pkg, SquadID: owner.ID})
	if err == nil || !strings.Contains(err.Error(), "missing-dev") {
		t.Fatalf("expected a clear missing-agent error, got %v", err)
	}
}

func tableCount(t *testing.T, c *templateTestCluster, table string) int {
	t.Helper()
	var n int
	if err := c.st.DB().QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func TestDecodeStrictJSONRejectsTrailingContent(t *testing.T) {
	var out map[string]any
	if err := decodeStrictJSON([]byte(`{"a":1}`), &out); err != nil {
		t.Fatalf("valid JSON rejected: %v", err)
	}
	if err := decodeStrictJSON([]byte("{\"a\":1}\n{\"b\":2}"), &out); err == nil || !strings.Contains(err.Error(), "多余内容") {
		t.Fatalf("trailing JSON should be rejected, got %v", err)
	}
	if err := decodeStrictJSON([]byte(`{"a":1} trailing`), &out); err == nil {
		t.Fatalf("trailing garbage should be rejected")
	}
}
