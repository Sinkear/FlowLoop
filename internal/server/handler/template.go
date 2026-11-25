package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/Sinkear/flowloop/internal/service"
)

// ── templates (GitCode YAML 模板：列表 / 详情 / 刷新 / apply) ──

// listTemplates returns the cached template metadata. ?type=squad|project
// filters (full kind strings also accepted; ” = all).
func (h *Handlers) listTemplates(w http.ResponseWriter, r *http.Request) {
	if h.Templates == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("template service not configured"))
		return
	}
	out, err := h.Templates.List(r.Context(), r.URL.Query().Get("type"))
	writeJSON(w, out, err)
}

// getTemplate returns one template in full: metadata + raw YAML + JSON spec.
func (h *Handlers) getTemplate(w http.ResponseWriter, r *http.Request) {
	if h.Templates == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("template service not configured"))
		return
	}
	out, err := h.Templates.Get(r.Context(), r.PathValue("kind"), r.PathValue("id"))
	writeJSON(w, out, err)
}

// refreshTemplates force-refetches the template repo (body.token optional —
// private template repos need it; public repos pass nothing).
func (h *Handlers) refreshTemplates(w http.ResponseWriter, r *http.Request) {
	if h.Templates == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("template service not configured"))
		return
	}
	var body struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body) // empty body = public repo
	out, err := h.Templates.Refresh(r.Context(), body.Token)
	writeJSON(w, out, err)
}

// applySquadTemplate applies a squad template: agents → squad → members.
func (h *Handlers) applySquadTemplate(w http.ResponseWriter, r *http.Request) {
	if h.TemplateApply == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("template apply service not configured"))
		return
	}
	var ov service.ApplySquadOverrides
	if err := json.NewDecoder(r.Body).Decode(&ov); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := h.TemplateApply.ApplySquad(r.Context(), r.PathValue("id"), ov)
	writeJSON(w, out, err)
}

// applyProjectTemplate applies a project template: repo → domain → team →
// goals → schedules.
func (h *Handlers) applyProjectTemplate(w http.ResponseWriter, r *http.Request) {
	if h.TemplateApply == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("template apply service not configured"))
		return
	}
	var ov service.ApplyProjectOverrides
	if err := json.NewDecoder(r.Body).Decode(&ov); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := h.TemplateApply.ApplyProject(r.Context(), r.PathValue("id"), ov)
	writeJSON(w, out, err)
}

// applyAgentTemplate applies an agent template: agent → same-named scratch
// domain → suggested goal prompt.
func (h *Handlers) applyAgentTemplate(w http.ResponseWriter, r *http.Request) {
	if h.TemplateApply == nil {
		writeErr(w, http.StatusInternalServerError, errors.New("template apply service not configured"))
		return
	}
	var ov service.ApplyAgentOverrides
	if err := json.NewDecoder(r.Body).Decode(&ov); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := h.TemplateApply.ApplyAgent(r.Context(), r.PathValue("id"), ov)
	writeJSON(w, out, err)
}

// ── practice package import (local JSON, no GitCode source required) ──

// previewTemplateImport validates a practice package and reports what an
// import would create, without writing anything.
func (h *Handlers) previewTemplateImport(w http.ResponseWriter, r *http.Request) {
	svc := h.practiceImportService(w)
	if svc == nil {
		return
	}
	req, ok := decodePracticeImportRequest(w, r)
	if !ok {
		return
	}
	out, err := svc.Preview(r.Context(), req)
	writeJSON(w, out, err)
}

// importTemplate imports copies of a practice package's contents.
func (h *Handlers) importTemplate(w http.ResponseWriter, r *http.Request) {
	svc := h.practiceImportService(w)
	if svc == nil {
		return
	}
	req, ok := decodePracticeImportRequest(w, r)
	if !ok {
		return
	}
	out, err := svc.Import(r.Context(), req)
	writeJSON(w, out, err)
}

func (h *Handlers) practiceImportService(w http.ResponseWriter) *service.PracticeImportService {
	if h.TemplateApply == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("实践库导入暂不可用：实践库服务未配置"))
		return nil
	}
	return service.NewPracticeImportService(h.TemplateApply, h.Blueprint)
}

func decodePracticeImportRequest(w http.ResponseWriter, r *http.Request) (service.PracticeImportRequest, bool) {
	var req service.PracticeImportRequest
	r.Body = http.MaxBytesReader(w, r.Body, 4<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, service.NewCodedError(service.CodeTemplateInvalid, "导入请求不是合法的 JSON："+err.Error()))
		return req, false
	}
	if dec.More() {
		writeErr(w, http.StatusBadRequest, service.NewCodedError(service.CodeTemplateInvalid, "导入请求 JSON 结尾存在多余内容"))
		return req, false
	}
	return req, true
}
