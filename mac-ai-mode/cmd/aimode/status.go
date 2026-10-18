package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// cmdStatusJSON prints a machine-readable status for an orchestrating agent.
func (a *App) cmdStatusJSON() error {
	st, _ := loadState(a.Paths)
	mem, _ := memStats()
	sw, _ := swapStats()
	ready := a.healthOK()
	out := map[string]any{
		"mode":             st.Mode,
		"model":            st.Model,
		"ready":            ready && st.Mode == ModeReady,
		"api":              a.apiBase(),
		"api_healthy":      ready,
		"server_pid":       st.ServerPID,
		"server_alive":     pidAlive(st.ServerPID),
		"ram_total_bytes":  mem.TotalBytes,
		"ram_used_bytes":   mem.UsedBytes,
		"ram_avail_bytes":  mem.AvailableBytes,
		"ram_free_pct":     mem.FreePct,
		"pressure":         mem.PressureLabel,
		"swap_used_bytes":  sw.UsedBytes,
		"swap_active":      sw.Active,
	}
	if st.Mode == ModeReady && !st.Since.IsZero() {
		out["uptime_seconds"] = int(time.Since(st.Since).Seconds())
		out["since"] = st.Since.Format(time.RFC3339)
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(data))
	return nil
}

func (a *App) cmdStatus() error {
	st, err := loadState(a.Paths)
	if err != nil {
		return err
	}
	mem, _ := memStats()
	sw, _ := swapStats()

	api := a.apiBase()
	apiState := "down"
	if a.healthOK() {
		apiState = "ok"
	}
	uptime := "-"
	if st.Mode == ModeReady && !st.Since.IsZero() {
		uptime = time.Since(st.Since).Round(time.Second).String()
	}
	model := st.Model
	if model == "" {
		model = "-"
	}

	fmt.Printf("Mode:       %s\n", st.Mode)
	fmt.Printf("Model:      %s\n", model)
	fmt.Printf("Backend:    llama.cpp / Metal\n")
	fmt.Printf("RAM:        %s used / %s total  (wired %s, compressed %s)\n",
		humanBytes(mem.UsedBytes), humanBytes(mem.TotalBytes),
		humanBytes(mem.WiredBytes), humanBytes(mem.CompressedBytes))
	fmt.Printf("Available:  %s (%.0f%%)\n", humanBytes(mem.AvailableBytes), mem.FreePct)
	fmt.Printf("Pressure:   %s\n", mem.PressureLabel)
	swapState := "idle"
	if sw.Active {
		swapState = "ACTIVE"
	}
	fmt.Printf("Swap:       %s used (%s)\n", humanBytes(sw.UsedBytes), swapState)
	fmt.Printf("CPU:        load avg %s\n", loadAvg())
	fmt.Printf("GPU:        telemetry n/a without sudo powermetrics (chip: Apple M-series, Metal active)\n")
	fmt.Printf("API:        %s (%s)\n", api, apiState)
	fmt.Printf("Server PID: %d (alive=%v)\n", st.ServerPID, pidAlive(st.ServerPID))
	fmt.Printf("Uptime:     %s\n", uptime)
	if len(st.StoppedApps) > 0 {
		fmt.Printf("Stopped:    %s\n", strings.Join(st.StoppedApps, ", "))
	}
	return nil
}

// hfCacheName mirrors huggingface_hub's directory naming.
func hfCacheName(repo string) string {
	repo = strings.SplitN(repo, ":", 2)[0]
	return "models--" + strings.ReplaceAll(repo, "/", "--")
}

func (a *App) modelDownloaded(m Model) bool {
	if m.Path != "" {
		if _, err := os.Stat(expandHome(m.Path)); err != nil {
			return false
		}
		if m.MMProj != "" {
			if _, err := os.Stat(expandHome(m.MMProj)); err != nil {
				return false
			}
		}
		return true
	}
	// A completed HF download has a resolvable snapshot symlink to a .gguf. While
	// downloading, only a blobs/*.incomplete file exists and no snapshot link, so
	// os.Stat (which follows symlinks) correctly reports "not downloaded".
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".cache", "huggingface", "hub", hfCacheName(m.HF))
	matches, _ := filepath.Glob(filepath.Join(base, "snapshots", "*", "*.gguf"))
	for _, p := range matches {
		if _, err := os.Stat(p); err == nil { // follows symlink -> fails if incomplete
			return true
		}
	}
	return false
}

func (a *App) cmdModels() error {
	st, _ := loadState(a.Paths)
	names := make([]string, 0, len(a.Models.Models))
	for k := range a.Models.Models {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Printf("%-10s %-9s %-7s %s\n", "PROFILE", "STATE", "ACTIVE", "SOURCE")
	for _, name := range names {
		m := a.Models.Models[name]
		state := "not downloaded"
		if a.modelDownloaded(m) {
			state = "downloaded"
		}
		active := ""
		if st.Mode == ModeReady && st.Model == name {
			active = "*"
		}
		src := m.HF
		if src == "" {
			src = m.Path
		}
		fmt.Printf("%-10s %-9s %-7s %s\n", name, state, active, src)
	}
	return nil
}

func (a *App) cmdLogs(n int) error {
	for _, f := range []struct {
		title, path string
	}{
		{"aimode.log", a.Paths.AppLog},
		{"server.log", a.Paths.ServerLog},
	} {
		fmt.Printf("===== %s (last %d) =====\n", f.title, n)
		out, err := tailFile(f.path, n)
		if err != nil {
			fmt.Printf("(%v)\n", err)
			continue
		}
		fmt.Println(out)
	}
	return nil
}

type benchResult struct {
	Timestamp     string  `json:"timestamp"`
	Model         string  `json:"model"`
	CtxSize       int     `json:"ctx_size"`
	PromptTPS     float64 `json:"prompt_tokens_per_sec"`
	GenTPS        float64 `json:"gen_tokens_per_sec"`
	GenTokens     int     `json:"gen_tokens"`
	PeakUsedBytes int64   `json:"peak_used_bytes"`
	Pressure      string  `json:"pressure_after"`
	SwapUsedBytes int64   `json:"swap_used_bytes"`
}

func (a *App) cmdBenchmark() error {
	if !a.healthOK() {
		return fmt.Errorf("API not healthy at %s — is AI Mode on?", a.apiBase())
	}
	st, _ := loadState(a.Paths)
	prompt := "Write a concise 200-word technical explanation of how Metal GPU acceleration speeds up LLM inference on Apple silicon."
	resp, err := a.chat(prompt, 400)
	if err != nil {
		return err
	}
	mem, _ := memStats()
	sw, _ := swapStats()
	res := benchResult{
		Timestamp:     time.Now().Format(time.RFC3339),
		Model:         st.Model,
		CtxSize:       a.Cfg.Llama.CtxSize,
		PromptTPS:     resp.Timings.PromptPerSecond,
		GenTPS:        resp.Timings.PredictedPerSecond,
		GenTokens:     resp.Timings.PredictedN,
		PeakUsedBytes: mem.UsedBytes,
		Pressure:      mem.PressureLabel,
		SwapUsedBytes: sw.UsedBytes,
	}
	data, _ := json.MarshalIndent(res, "", "  ")
	out := filepath.Join(a.Paths.LogDir, "bench-"+time.Now().Format("20060102-150405")+".json")
	_ = os.WriteFile(out, data, 0o644)
	fmt.Printf("model=%s  prompt=%.1f tok/s  gen=%.1f tok/s  gen_tokens=%d\n",
		res.Model, res.PromptTPS, res.GenTPS, res.GenTokens)
	fmt.Printf("used=%s  pressure=%s  swap=%s\n", humanBytes(res.PeakUsedBytes), res.Pressure, humanBytes(res.SwapUsedBytes))
	fmt.Printf("saved: %s\n", out)
	return nil
}
