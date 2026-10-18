package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Session modes. Gating is enforced here, not by prompts: in "off" the agent
// cannot reach the Mac at all.
const (
	ModeOff    = "off"    // default — only local_enable is listed
	ModePrefer = "prefer" // offload when it pays; Mac unavailable -> cloud does the work
	ModeStrict = "strict" // minimal cloud tokens; Mac unavailable -> queue or stop, never cloud
)

const batchingRules = " Only one model is loaded at a time and switching costs seconds (fast) to " +
	"minutes (brain): plan first, group subtasks by profile, run each group as one batch, " +
	"don't switch mid-task."

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func obj(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
func num(desc string) map[string]any { return map[string]any{"type": "number", "description": desc} }

var enableTool = toolDef{
	Name: "local_enable",
	Description: "Enable local-LLM mode for this session. Only call after the user explicitly " +
		"confirmed enabling local mode via /local (\"prefer\") or /local strict (\"strict\"). " +
		"Never call it on your own initiative.",
	InputSchema: obj(map[string]any{
		"mode": map[string]any{"type": "string", "enum": []string{ModePrefer, ModeStrict},
			"description": "prefer: Mac unavailable -> do the work in the cloud. strict: never fall back to the cloud — queue (local_submit) or stop."},
	}, "mode"),
}

func (s *Server) allTools() []toolDef {
	skill := str("Skill name for usage tracking, e.g. local-batch")
	return []toolDef{
		enableTool,
		{Name: "local_disable", Description: "Turn local mode off for this session (all local tools disappear). " +
			"shutdown=true also runs `aimode off` on the Mac — ask the user first.",
			InputSchema: obj(map[string]any{"shutdown": map[string]any{"type": "boolean"}})},
		{Name: "local_status", Description: "Current session mode (off/prefer/strict) and Mac state: " +
			"availability (ready | loading | aimode_off | aimode_error | mac_unreachable), active profile, API health. " +
			"Fast; call it as step 0 of every local-* skill.",
			InputSchema: obj(map[string]any{})},
		{Name: "local_capabilities", Description: "Source of truth for what is actually available: model profiles " +
			"(downloaded, vision, ctx), services (whisper, browser), job worker, prompt templates. Skills must check this, not assume.",
			InputSchema: obj(map[string]any{})},
		{Name: "local_ensure_profile", Description: "Make <profile> the loaded model. No-op if already active; otherwise " +
			"`aimode on`/`aimode switch` on the Mac (turning AI Mode on quits the Mac's managed apps) and waits for health. " +
			"Returns seconds spent loading." + batchingRules,
			InputSchema: obj(map[string]any{"profile": str("fast | coding | browser | brain (see local_capabilities)")}, "profile")},
		{Name: "local_run", Description: "One chat completion on the currently loaded model. Never switches models: " +
			"pass `profile` to assert which one you expect (error if different). Prefer `template`+`vars` over hand-written " +
			"prompts. Output is a draft for you to verify (prefer mode)." + batchingRules,
			InputSchema: obj(map[string]any{
				"prompt":      str("User prompt (or the {{input}} var when a template is used)"),
				"system":      str("Optional system prompt"),
				"template":    str("Template name from local_capabilities, e.g. fast/extract-json"),
				"vars":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Template placeholder values"},
				"profile":     str("Expected active profile; errors instead of switching"),
				"max_tokens":  num("Generation cap"),
				"temperature": num("Sampling temperature"),
				"images":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Image file paths on the NAS or data: URIs (vision profile only)"},
				"json_schema": map[string]any{"type": "object", "description": "Constrain output to this JSON schema; result is validated"},
				"skill":       skill,
			})},
		{Name: "local_submit", Description: "Queue an async job (overnight / strict-mode work). The NAS worker groups " +
			"jobs by profile, waits while the Mac is unavailable, retries once. Returns job_id.",
			InputSchema: obj(map[string]any{
				"profile":     str("Model profile for this job"),
				"prompt":      str("Prompt (or {{input}} for a template)"),
				"task_file":   str("Alternatively: path of a NAS file whose content is the prompt"),
				"system":      str("Optional system prompt"),
				"template":    str("Optional template name"),
				"vars":        map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}},
				"max_tokens":  num("Generation cap"),
				"priority":    num("Higher runs first within a profile (default 0)"),
				"json_schema": map[string]any{"type": "object"},
				"skill":       skill,
			}, "profile")},
		{Name: "local_job_status", Description: "State of one job (queued/running/done/failed) without the result body.",
			InputSchema: obj(map[string]any{"job_id": str("")}, "job_id")},
		{Name: "local_job_result", Description: "Result of a finished job.",
			InputSchema: obj(map[string]any{"job_id": str("")}, "job_id")},
		{Name: "local_jobs", Description: "List queued, running and recently finished jobs.",
			InputSchema: obj(map[string]any{})},
		{Name: "local_browse", Description: "Browser work with the local model as eyes and hands: a headless Chromium on " +
			"the NAS executes one high-level instruction in up to max_steps micro-steps and returns a compact report " +
			"(status done|blocked|need_decision, url, title, summary, key elements, extracted data). Raw HTML and screenshots " +
			"never reach you. Session (cookies, last page) persists between calls. Never logs in, pays or submits personal " +
			"data unless allow_submit=true after explicit user OK.",
			InputSchema: obj(map[string]any{
				"instruction":  str("High-level goal for this page, e.g. 'find the pricing page' or 'extract the plans table as JSON'"),
				"url":          str("Optional start URL; omitted = continue on the last page"),
				"max_steps":    num("Micro-step cap (default 8)"),
				"extract":      str("Optional description of the JSON to extract"),
				"allow_submit": map[string]any{"type": "boolean", "description": "Allow submitting forms / login / purchase actions (requires explicit user OK)"},
				"skill":        skill,
			}, "instruction")},
		{Name: "local_transcribe", Description: "Speech-to-text with Whisper (large-v3-turbo) on the Mac. Takes an audio " +
			"or video file on the NAS (any ffmpeg format), keeps timestamps, writes .txt/.srt/.json transcripts on the NAS " +
			"and returns their paths plus a short preview — read only what you need from the files. Works without " +
			"loading an LLM profile; much faster than real time.",
			InputSchema: obj(map[string]any{
				"path":       str("Audio/video file on the NAS"),
				"language":   str("ISO code (ru, en, ...) or auto (default)"),
				"output_dir": str("Where to write transcripts (default ~/.local/share/aimode-mcp/transcripts)"),
				"skill":      skill,
			}, "path")},
		{Name: "local_feedback", Description: "Record how a local result turned out, for usage tracking: accepted, fixed " +
			"(you corrected it), redone (done in the cloud instead), unverified (strict mode).",
			InputSchema: obj(map[string]any{
				"id":      str("run_id or job_id"),
				"verdict": map[string]any{"type": "string", "enum": []string{"accepted", "fixed", "redone", "unverified"}},
				"skill":   skill,
				"note":    str("Optional short note"),
			}, "id", "verdict")},
	}
}

func (s *Server) getMode() string {
	s.smu.Lock()
	defer s.smu.Unlock()
	return s.mode
}

func (s *Server) setMode(m string) {
	s.smu.Lock()
	changed := s.mode != m
	s.mode = m
	s.smu.Unlock()
	if changed {
		s.notify("notifications/tools/list_changed")
	}
}

func (s *Server) listTools() []toolDef {
	if s.getMode() == ModeOff && !s.cfg.ListAll {
		return []toolDef{enableTool}
	}
	return s.allTools()
}

func toJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	enc.Encode(v)
	return strings.TrimSpace(buf.String())
}

func (s *Server) callTool(name string, raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		raw = json.RawMessage("{}")
	}
	if name == "local_enable" {
		var a struct {
			Mode string `json:"mode"`
		}
		_ = json.Unmarshal(raw, &a)
		if a.Mode == "" {
			a.Mode = ModePrefer
		}
		if a.Mode != ModePrefer && a.Mode != ModeStrict {
			return "mode must be prefer or strict", true
		}
		s.setMode(a.Mode)
		return fmt.Sprintf("local mode %q enabled for this session. Next: local_status.", a.Mode), false
	}
	mode := s.getMode()
	if mode == ModeOff {
		return "local mode is disabled — the user must enable it with /local", true
	}
	switch name {
	case "local_disable":
		var a struct {
			Shutdown bool `json:"shutdown"`
		}
		_ = json.Unmarshal(raw, &a)
		msg := "local mode off for this session"
		if a.Shutdown {
			if out, err := s.cfg.sshAimode(2*time.Minute, "off"); err != nil {
				msg += "; `aimode off` failed: " + err.Error()
			} else {
				msg += "; Mac: " + lastLines(out, 2)
			}
		}
		s.setMode(ModeOff)
		return msg, false
	case "local_status":
		st := s.cfg.probe()
		st.Raw = nil
		return toJSON(map[string]any{"session_mode": mode, "mac": st, "fallback": fallbackHint(mode)}), false
	case "local_capabilities":
		return s.capabilities(), false
	case "local_ensure_profile":
		var a struct {
			Profile string `json:"profile"`
		}
		_ = json.Unmarshal(raw, &a)
		load, how, err := s.cfg.ensureProfile(a.Profile)
		if err != nil {
			return s.unavailable(mode, err), true
		}
		return toJSON(map[string]any{"profile": a.Profile, "action": how, "load_seconds": int(load + 0.5)}), false
	case "local_run":
		return s.toolRun(mode, raw)
	case "local_submit":
		var j Job
		if err := json.Unmarshal(raw, &j); err != nil {
			return "invalid arguments: " + err.Error(), true
		}
		if !validName(j.Profile) {
			return "invalid profile", true
		}
		if j.Prompt == "" && j.TaskFile == "" && j.Template == "" {
			return "need prompt, task_file or template", true
		}
		if err := s.cfg.submitJob(&j); err != nil {
			return err.Error(), true
		}
		note := ""
		if !s.cfg.workerAlive() {
			note = " WARNING: no job worker is running on the NAS (systemctl --user start aimode-worker)."
		}
		return toJSON(map[string]any{"job_id": j.ID, "state": j.State}) + note, false
	case "local_job_status", "local_job_result":
		var a struct {
			JobID string `json:"job_id"`
		}
		_ = json.Unmarshal(raw, &a)
		j, err := s.cfg.findJob(a.JobID)
		if err != nil {
			return err.Error(), true
		}
		if name == "local_job_status" {
			j.Result, j.Prompt, j.System, j.Vars = nil, "", "", nil
		} else if j.State != "done" && j.State != "failed" {
			return fmt.Sprintf("job %s is %s — no result yet", j.ID, j.State), true
		}
		return toJSON(j), false
	case "local_jobs":
		brief := func(js []*Job) []map[string]any {
			var out []map[string]any
			for _, j := range js {
				out = append(out, map[string]any{"id": j.ID, "profile": j.Profile, "skill": j.Skill,
					"state": j.State, "priority": j.Priority, "submitted": j.Submitted.Format(time.RFC3339)})
			}
			return out
		}
		done := s.cfg.listJobs("done")
		if len(done) > 20 {
			done = done[len(done)-20:]
		}
		return toJSON(map[string]any{"worker_running": s.cfg.workerAlive(),
			"queued": brief(s.cfg.listJobs("queued")), "running": brief(s.cfg.listJobs("running")),
			"finished_recent": brief(done)}), false
	case "local_browse":
		return s.toolBrowse(mode, raw)
	case "local_transcribe":
		var a struct {
			Path      string `json:"path"`
			Language  string `json:"language"`
			OutputDir string `json:"output_dir"`
			Skill     string `json:"skill"`
		}
		_ = json.Unmarshal(raw, &a)
		start := time.Now()
		res, err := s.cfg.transcribe(a.Path, a.Language, a.OutputDir)
		ev := MetricEvent{Kind: "transcribe", Skill: a.Skill, Profile: "whisper", Mode: mode,
			DurationMS: time.Since(start).Milliseconds(), OK: err == nil}
		if err != nil {
			s.cfg.logMetric(ev)
			return s.unavailable(mode, err), true
		}
		ev.ID, _ = res["id"].(string)
		ev.OutChars, _ = res["chars"].(int)
		s.cfg.logMetric(ev)
		return toJSON(res), false
	case "local_feedback":
		var a struct {
			ID, Verdict, Skill, Note string
		}
		_ = json.Unmarshal(raw, &a)
		s.cfg.logMetric(MetricEvent{Kind: "feedback", ID: a.ID, Skill: a.Skill, Verdict: a.Verdict, Note: a.Note, OK: true})
		return "recorded", false
	}
	return "unknown tool " + name, true
}

func fallbackHint(mode string) string {
	if mode == ModeStrict {
		return "strict: if the Mac is unavailable, queue with local_submit or stop and tell the user — never do the work in the cloud"
	}
	return "prefer: if the Mac is unavailable, do the work in the cloud yourself"
}

// unavailable phrases a Mac-side failure as an instruction that depends on the mode.
// A Mac that is up but simply has no (or another) model loaded is not "unavailable":
// the fix is local_ensure_profile, not a fallback.
func (s *Server) unavailable(mode string, err error) string {
	st := s.cfg.probe()
	what := st.Availability
	if st.Detail != "" {
		what += " (" + st.Detail + ")"
	}
	msg := fmt.Sprintf("local LLM unavailable: %v. Mac: %s. ", err, what)
	switch st.Availability {
	case AvailAimodeOff:
		return msg + "The Mac is up: load a model with local_ensure_profile (it turns AI Mode on)."
	case AvailLoading:
		return msg + "A model is loading — retry in a minute."
	}
	if mode == ModeStrict {
		return msg + "Strict mode: do NOT do this in the cloud — queue it with local_submit, or stop and report to the user."
	}
	return msg + "Prefer mode: do this step in the cloud yourself and mention it in your report."
}

// activeProfile asks the llama-server which alias (= aimode profile) it serves.
func (c Config) activeProfile() (string, error) {
	req, _ := http.NewRequest("GET", c.apiBase()+"/v1/models", nil)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("no model loaded (API down) — call local_ensure_profile first")
	}
	defer resp.Body.Close()
	var r struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil || len(r.Data) == 0 {
		return "", fmt.Errorf("cannot read /v1/models (HTTP %d)", resp.StatusCode)
	}
	return r.Data[0].ID, nil
}

func (s *Server) toolRun(mode string, raw json.RawMessage) (string, bool) {
	var a struct {
		Prompt      string            `json:"prompt"`
		System      string            `json:"system"`
		Template    string            `json:"template"`
		Vars        map[string]string `json:"vars"`
		Profile     string            `json:"profile"`
		MaxTokens   int               `json:"max_tokens"`
		Temperature *float64          `json:"temperature"`
		Images      []string          `json:"images"`
		Schema      json.RawMessage   `json:"json_schema"`
		Skill       string            `json:"skill"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	system, prompt, maxTokens := a.System, a.Prompt, a.MaxTokens
	if a.Template != "" {
		t, err := s.cfg.loadTemplate(a.Template)
		if err != nil {
			return err.Error(), true
		}
		vars := map[string]string{}
		for k, v := range a.Vars {
			vars[k] = v
		}
		if _, ok := vars["input"]; !ok && a.Prompt != "" {
			vars["input"] = a.Prompt
		}
		ts, tb, err := t.render(vars)
		if err != nil {
			return err.Error(), true
		}
		if system == "" {
			system = ts
		}
		prompt = tb
		if maxTokens == 0 {
			maxTokens = t.MaxTokens
		}
	}
	if strings.TrimSpace(prompt) == "" {
		return "empty prompt", true
	}
	active, err := s.cfg.activeProfile()
	if err != nil {
		return s.unavailable(mode, err), true
	}
	if a.Profile != "" && a.Profile != active {
		return fmt.Sprintf("active profile is %q, not %q — local_run never switches; call local_ensure_profile(%q) "+
			"if switching is worth it (see batching rules)", active, a.Profile, a.Profile), true
	}
	var images []string
	for _, img := range a.Images {
		uri, err := imageURI(img)
		if err != nil {
			return err.Error(), true
		}
		images = append(images, uri)
	}
	id := newID("run-")
	res, err := s.cfg.chat(ChatRequest{System: system, Prompt: prompt, Images: images,
		MaxTokens: maxTokens, Temperature: a.Temperature, JSONSchema: a.Schema})
	ok := err == nil
	if ok && len(a.Schema) > 0 && !json.Valid([]byte(res.Content)) {
		ok, err = false, fmt.Errorf("model output is not valid JSON: %.300s", res.Content)
	}
	s.cfg.logMetric(MetricEvent{Kind: "run", ID: id, Skill: a.Skill, Profile: active, Mode: mode,
		DurationMS: res.DurationMS, InChars: len(prompt) + len(system), OutChars: len(res.Content), OK: ok})
	if err != nil {
		if res.Content != "" { // got an answer, it just failed validation
			return err.Error(), true
		}
		return s.unavailable(mode, err), true
	}
	header := fmt.Sprintf("[run_id=%s profile=%s %.1fs gen=%d tok @ %.1f tok/s]\n", id, active,
		float64(res.DurationMS)/1000, res.GenTokens, res.GenTPS)
	return header + res.Content, false
}

// imageURI turns a NAS file path into a data: URI; data: URIs pass through.
func imageURI(s string) (string, error) {
	if strings.HasPrefix(s, "data:") {
		return s, nil
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return "", fmt.Errorf("remote image URLs are not supported; download to the NAS and pass the path")
	}
	data, err := os.ReadFile(expandHome(s))
	if err != nil {
		return "", err
	}
	typ := mime.TypeByExtension(strings.ToLower(filepath.Ext(s)))
	if typ == "" {
		typ = http.DetectContentType(data)
	}
	return "data:" + typ + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func (s *Server) capabilities() string {
	out := map[string]any{"session_mode": s.getMode()}
	st := s.cfg.probe()
	st.Raw = nil
	out["mac"] = st
	if st.Availability != AvailUnreachable {
		if ps, err := s.cfg.profiles(); err == nil {
			out["profiles"] = ps
		} else {
			out["profiles_error"] = err.Error()
		}
	}
	_, nodeErr := exec.LookPath("node")
	_, agentErr := os.Stat(filepath.Join(s.cfg.BrowserDir, "agent.mjs"))
	_, pwErr := os.Stat(filepath.Join(s.cfg.BrowserDir, "node_modules", "playwright-core"))
	browser := nodeErr == nil && agentErr == nil && pwErr == nil
	out["services"] = map[string]any{
		"browser": browser,
		"whisper": st.Availability != AvailUnreachable && s.cfg.whisperAvailable(),
	}
	out["job_worker_running"] = s.cfg.workerAlive()
	out["templates"] = s.cfg.listTemplates()
	return toJSON(out)
}

func (s *Server) toolBrowse(mode string, raw json.RawMessage) (string, bool) {
	var a struct {
		Instruction string `json:"instruction"`
		URL         string `json:"url"`
		MaxSteps    int    `json:"max_steps"`
		Extract     string `json:"extract"`
		AllowSubmit bool   `json:"allow_submit"`
		Skill       string `json:"skill"`
	}
	if err := json.Unmarshal(raw, &a); err != nil {
		return "invalid arguments: " + err.Error(), true
	}
	active, err := s.cfg.activeProfile()
	if err != nil {
		return s.unavailable(mode, err), true
	}
	vision := false
	if ps, err := s.cfg.profiles(); err == nil {
		for _, p := range ps {
			if p.Name == active {
				vision = p.Vision
			}
		}
	}
	if a.MaxSteps <= 0 {
		a.MaxSteps = 8
	}
	input := map[string]any{
		"instruction": a.Instruction, "url": a.URL, "max_steps": min(a.MaxSteps, 30),
		"extract": a.Extract, "allow_submit": a.AllowSubmit, "vision": vision,
		"api_base": s.cfg.apiBase() + "/v1", "api_key": s.cfg.APIKey,
		"session_dir": filepath.Join(s.cfg.DataDir, "browser-session"),
		"log_file":    filepath.Join(s.cfg.DataDir, "browser.log.jsonl"),
	}
	data, _ := json.Marshal(input)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(s.cfg.BrowserDir, "agent.mjs"))
	cmd.Dir = s.cfg.BrowserDir
	cmd.Stdin = bytes.NewReader(data)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	start := time.Now()
	runErr := cmd.Run()
	id := newID("run-")
	s.cfg.logMetric(MetricEvent{Kind: "browse", ID: id, Skill: a.Skill, Profile: active, Mode: mode,
		DurationMS: time.Since(start).Milliseconds(), InChars: len(a.Instruction), OutChars: out.Len(), OK: runErr == nil})
	if runErr != nil {
		return fmt.Sprintf("browser agent failed: %v: %s", runErr, lastLines(errb.String(), 8)), true
	}
	return fmt.Sprintf("[run_id=%s profile=%s vision=%v %.0fs]\n%s", id, active, vision,
		time.Since(start).Seconds(), strings.TrimSpace(out.String())), false
}
