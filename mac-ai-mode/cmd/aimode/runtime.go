package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (a *App) resolveModel(name string) (Model, error) {
	m, ok := a.Models.Models[name]
	if !ok {
		keys := make([]string, 0, len(a.Models.Models))
		for k := range a.Models.Models {
			keys = append(keys, k)
		}
		return Model{}, fmt.Errorf("unknown model %q (available: %s)", name, strings.Join(keys, ", "))
	}
	if m.HF == "" && m.Path == "" {
		return Model{}, fmt.Errorf("model %q has neither hf nor path set", name)
	}
	return m, nil
}

func (a *App) serverArgs(name string, m Model) []string {
	ctx := m.CtxSize
	if ctx <= 0 {
		ctx = a.Cfg.Llama.CtxSize
	}
	host := a.Cfg.API.Host
	if host == "" {
		host = "127.0.0.1"
	}
	args := []string{
		"--host", host,
		"--port", strconv.Itoa(a.Cfg.API.Port),
		"-c", strconv.Itoa(ctx),
		"-np", strconv.Itoa(max(1, cmp.Or(m.Parallel, a.Cfg.Llama.Parallel))),
		"-ngl", strconv.Itoa(a.Cfg.Llama.NGpuLayers),
		"--alias", name,
	}
	if m.Path != "" {
		args = append(args, "-m", expandHome(m.Path))
	} else {
		args = append(args, "-hf", m.HF)
	}
	if m.MMProj != "" {
		args = append(args, "--mmproj", expandHome(m.MMProj))
	}
	if a.Cfg.Llama.FlashAttn {
		args = append(args, "-fa", "on")
	}
	if m.CacheTypeK != "" {
		args = append(args, "-ctk", m.CacheTypeK)
	}
	if m.CacheTypeV != "" {
		args = append(args, "-ctv", m.CacheTypeV)
	}
	if a.Cfg.API.APIKey != "" {
		// Via a 0600 file, not argv: argv is visible in ps and printed by --dry-run.
		args = append(args, "--api-key-file", a.Paths.APIKeyFile)
	}
	args = append(args, a.Cfg.Llama.ExtraArgs...)
	args = append(args, m.ExtraArgs...)
	return args
}

// healthHost returns the address to probe; a wildcard bind is checked via loopback.
func (a *App) healthHost() string {
	h := a.Cfg.API.Host
	if h == "" || h == "0.0.0.0" || h == "::" {
		return "127.0.0.1"
	}
	return h
}

func (a *App) apiBase() string {
	return fmt.Sprintf("http://%s:%d", a.healthHost(), a.Cfg.API.Port)
}

func (a *App) healthOK() bool {
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(a.apiBase() + "/health")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// findLlamaServer looks in PATH, then in the Homebrew prefixes: a non-interactive
// `ssh ai-mac aimode on` gets a bare PATH without /opt/homebrew/bin.
func findLlamaServer() (string, error) {
	if p, err := exec.LookPath("llama-server"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/llama-server", "/usr/local/bin/llama-server"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("llama-server not found in PATH or Homebrew prefixes (brew install llama.cpp)")
}

func (a *App) startServer(name string, m Model) (int, error) {
	bin, err := findLlamaServer()
	if err != nil {
		return 0, err
	}
	if a.Cfg.API.APIKey != "" {
		if err := os.WriteFile(a.Paths.APIKeyFile, []byte(a.Cfg.API.APIKey+"\n"), 0o600); err != nil {
			return 0, err
		}
	}
	args := a.serverArgs(name, m)

	logf, err := os.OpenFile(a.Paths.ServerLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer logf.Close()
	fmt.Fprintf(logf, "\n===== %s: starting %s (%s) =====\n", time.Now().Format(time.RFC3339), name, m.HF+m.Path)

	cmd := exec.Command(bin, args...)
	cmd.Stdout = logf
	cmd.Stderr = logf
	// Detach into its own session so it outlives this CLI invocation.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	// Release the child so we don't leave a zombie; it's tracked via pid/state.
	go func() { _ = cmd.Wait() }()
	_ = os.WriteFile(a.Paths.PidFile, []byte(strconv.Itoa(pid)), 0o644)
	return pid, nil
}

func (a *App) waitHealthy(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if a.healthOK() {
			return nil
		}
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("server did not become healthy within %s (see %s)", timeout, a.Paths.ServerLog)
}

func (a *App) stopServer(pid int) error {
	if pid <= 0 || !pidAlive(pid) {
		_ = os.Remove(a.Paths.PidFile)
		return nil
	}
	_ = syscall.Kill(pid, syscall.SIGTERM)
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(pid) {
			_ = os.Remove(a.Paths.PidFile)
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	// Last resort if it ignored SIGTERM.
	_ = syscall.Kill(pid, syscall.SIGKILL)
	time.Sleep(1 * time.Second)
	_ = os.Remove(a.Paths.PidFile)
	if pidAlive(pid) {
		return fmt.Errorf("failed to stop server pid %d", pid)
	}
	return nil
}

type timings struct {
	PredictedPerSecond float64 `json:"predicted_per_second"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedN         int     `json:"predicted_n"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Timings timings `json:"timings"`
}

// chat sends one prompt through the OpenAI-compatible API. Used by benchmark.
func (a *App) chat(prompt string, maxTokens int) (chatResp, error) {
	var cr chatResp
	body := map[string]any{
		"model":      "aimode",
		"messages":   []map[string]string{{"role": "user", "content": prompt}},
		"max_tokens": maxTokens,
		"stream":     false,
	}
	data, _ := json.Marshal(body)
	req, err := http.NewRequest("POST", a.apiBase()+"/v1/chat/completions", strings.NewReader(string(data)))
	if err != nil {
		return cr, err
	}
	req.Header.Set("Content-Type", "application/json")
	if a.Cfg.API.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.Cfg.API.APIKey)
	}
	client := http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return cr, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&cr); err != nil {
		return cr, err
	}
	return cr, nil
}
