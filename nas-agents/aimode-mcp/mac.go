package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Everything that talks to the Mac: reachability, `aimode` over SSH, and the
// OpenAI-compatible API. Every call has a short timeout — an asleep or switched-off
// Mac must produce a clear status fast, never a hang.

// Availability values reported by probe.
const (
	AvailReady       = "ready"           // AI Mode on, API healthy
	AvailLoading     = "loading"         // aimode is starting/switching a model
	AvailAimodeOff   = "aimode_off"      // Mac reachable, AI Mode off
	AvailError       = "aimode_error"    // aimode reports ERROR or the API is down while READY
	AvailUnreachable = "mac_unreachable" // off, asleep, or off the network
)

type MacStatus struct {
	Availability string         `json:"availability"`
	Mode         string         `json:"aimode_mode,omitempty"`
	Model        string         `json:"active_profile,omitempty"`
	APIHealthy   bool           `json:"api_healthy"`
	Detail       string         `json:"detail,omitempty"`
	Raw          map[string]any `json:"aimode_status,omitempty"`
}

type Profile struct {
	Name        string `json:"name"`
	Downloaded  bool   `json:"downloaded"`
	Active      bool   `json:"active"`
	Vision      bool   `json:"vision"`
	CtxSize     int    `json:"ctx_size"`
	Description string `json:"description"`
}

func (c Config) apiBase() string {
	return fmt.Sprintf("http://%s:%d", c.MacHost, c.MacPort)
}

// macReachable is a 2-second TCP probe of the Mac's sshd.
func (c Config) macReachable() bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.MacHost, "22"), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// sshAimode runs `aimode <args>` on the Mac.
func (c Config) sshAimode(timeout time.Duration, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	remote := c.AimodeCmd + " " + strings.Join(args, " ")
	cmd := exec.CommandContext(ctx, "ssh",
		"-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4",
		c.SSHTarget, remote)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), fmt.Errorf("`aimode %s` timed out after %s", strings.Join(args, " "), timeout)
	}
	if err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return out.String(), fmt.Errorf("`aimode %s` failed: %v: %s", strings.Join(args, " "), err, lastLines(msg, 6))
	}
	return out.String(), nil
}

func (c Config) healthOK() bool {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(c.apiBase() + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (c Config) probe() MacStatus {
	if !c.macReachable() {
		return MacStatus{Availability: AvailUnreachable,
			Detail: "Mac not reachable on the LAN (off, asleep or disconnected)"}
	}
	out, err := c.sshAimode(15*time.Second, "status", "--json")
	if err != nil {
		return MacStatus{Availability: AvailError, Detail: err.Error()}
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return MacStatus{Availability: AvailError, Detail: "unparseable aimode status: " + lastLines(out, 3)}
	}
	st := MacStatus{Raw: raw}
	st.Mode, _ = raw["mode"].(string)
	st.Model, _ = raw["model"].(string)
	st.APIHealthy = c.healthOK()
	switch {
	case st.Mode == "AI READY" && st.APIHealthy:
		st.Availability = AvailReady
	case st.Mode == "NORMAL":
		st.Availability = AvailAimodeOff
		st.Model = ""
	case st.Mode == "PREPARING" || st.Mode == "RELEASING RESOURCES" || st.Mode == "STARTING LLM":
		st.Availability = AvailLoading
	default:
		st.Availability = AvailError
		st.Detail = "aimode mode " + st.Mode + ", api_healthy=" + strconv.FormatBool(st.APIHealthy)
	}
	return st
}

func (c Config) profiles() ([]Profile, error) {
	out, err := c.sshAimode(20*time.Second, "models", "--json")
	if err != nil {
		return nil, err
	}
	var ps []Profile
	if err := json.Unmarshal([]byte(out), &ps); err != nil {
		return nil, fmt.Errorf("unparseable `aimode models --json`: %v", err)
	}
	return ps, nil
}

// ensureProfile makes `profile` the loaded model: no-op if already active,
// `aimode on` from NORMAL, `aimode switch` otherwise. Returns seconds spent loading.
func (c Config) ensureProfile(profile string) (float64, string, error) {
	if !validName(profile) {
		return 0, "", fmt.Errorf("invalid profile name %q", profile)
	}
	st := c.probe()
	if st.Availability == AvailReady && st.Model == profile {
		return 0, "already active", nil
	}
	if st.Availability == AvailUnreachable && c.WOLMac != "" {
		if err := c.wake(); err == nil {
			st = c.probe()
		}
	}
	var args []string
	switch st.Availability {
	case AvailUnreachable:
		return 0, "", fmt.Errorf("%s", st.Detail)
	case AvailLoading:
		return 0, "", fmt.Errorf("aimode is busy (%s) — retry in a minute", st.Mode)
	case AvailAimodeOff:
		args = []string{"on", profile}
	default: // ready with another model, or error state
		if st.Mode == "AI READY" {
			args = []string{"switch", profile}
		} else {
			args = []string{"on", profile} // `on` from ERROR restarts cleanly
		}
	}
	start := time.Now()
	// brain/heavy load ~47 GB from disk; aimode's own health timeout is the real bound.
	if _, err := c.sshAimode(20*time.Minute, args...); err != nil {
		return time.Since(start).Seconds(), "", err
	}
	if !c.healthOK() {
		return time.Since(start).Seconds(), "", fmt.Errorf("aimode reported success but %s/health is not OK", c.apiBase())
	}
	return time.Since(start).Seconds(), strings.Join(args, " "), nil
}

// wake sends a Wake-on-LAN magic packet and waits up to 90 s for sshd.
func (c Config) wake() error {
	hw, err := net.ParseMAC(c.WOLMac)
	if err != nil {
		return err
	}
	pkt := bytes.Repeat([]byte{0xff}, 6)
	for i := 0; i < 16; i++ {
		pkt = append(pkt, hw...)
	}
	conn, err := net.Dial("udp", "255.255.255.255:9")
	if err != nil {
		return err
	}
	conn.Write(pkt)
	conn.Close()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if c.macReachable() {
			return nil
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("Mac did not wake within 90s")
}

type ChatRequest struct {
	System      string
	Prompt      string
	Images      []string // data: URIs
	MaxTokens   int
	Temperature *float64
	JSONSchema  json.RawMessage // optional: constrain output to this JSON schema
	Timeout     time.Duration
}

type ChatResult struct {
	Content    string  `json:"content"`
	Model      string  `json:"model"`
	GenTokens  int     `json:"gen_tokens"`
	GenTPS     float64 `json:"gen_tokens_per_sec"`
	PromptTPS  float64 `json:"prompt_tokens_per_sec"`
	DurationMS int64   `json:"duration_ms"`
}

func (c Config) chat(r ChatRequest) (ChatResult, error) {
	var res ChatResult
	var user any = r.Prompt
	if len(r.Images) > 0 {
		parts := []map[string]any{{"type": "text", "text": r.Prompt}}
		for _, img := range r.Images {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]any{"url": img}})
		}
		user = parts
	}
	msgs := []map[string]any{}
	if r.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": r.System})
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": user})
	body := map[string]any{"model": "aimode", "messages": msgs, "stream": false}
	if r.MaxTokens > 0 {
		body["max_tokens"] = r.MaxTokens
	}
	if r.Temperature != nil {
		body["temperature"] = *r.Temperature
	}
	if len(r.JSONSchema) > 0 {
		body["response_format"] = map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": "output", "schema": r.JSONSchema},
		}
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", c.apiBase()+"/v1/chat/completions", bytes.NewReader(data))
	if err != nil {
		return res, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	client := http.Client{Timeout: timeout}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return res, err
	}
	defer resp.Body.Close()
	var cr struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Timings struct {
			PredictedPerSecond float64 `json:"predicted_per_second"`
			PromptPerSecond    float64 `json:"prompt_per_second"`
			PredictedN         int     `json:"predicted_n"`
		} `json:"timings"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return res, fmt.Errorf("HTTP %d: undecodable response: %v", resp.StatusCode, err)
	}
	if cr.Error != nil {
		return res, fmt.Errorf("HTTP %d: %s", resp.StatusCode, cr.Error.Message)
	}
	if resp.StatusCode != http.StatusOK || len(cr.Choices) == 0 {
		return res, fmt.Errorf("HTTP %d: no completion returned", resp.StatusCode)
	}
	res = ChatResult{
		Content:    stripThink(cr.Choices[0].Message.Content),
		Model:      cr.Model,
		GenTokens:  cr.Timings.PredictedN,
		GenTPS:     cr.Timings.PredictedPerSecond,
		PromptTPS:  cr.Timings.PromptPerSecond,
		DurationMS: time.Since(start).Milliseconds(),
	}
	return res, nil
}

// stripThink drops a leading reasoning block if the server left it in content.
func stripThink(s string) string {
	if i := strings.Index(s, "</think>"); i >= 0 {
		return strings.TrimSpace(s[i+len("</think>"):])
	}
	return strings.TrimSpace(s)
}

func validName(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}
