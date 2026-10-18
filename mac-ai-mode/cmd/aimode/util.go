package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// App bundles everything a command needs: resolved paths, parsed config and a
// file logger. Keeps the command functions small and testable.
type App struct {
	Paths    Paths
	Cfg      Config
	Models   ModelsConfig
	Services ServicesConfig
	Log      *log.Logger
	DryRun   bool
	Yes      bool
}

func newApp(dryRun, yes bool) (*App, error) {
	p, err := resolvePaths()
	if err != nil {
		return nil, err
	}
	if err := ensureConfigs(p); err != nil {
		return nil, err
	}
	cfg, err := loadConfig(p)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	models, err := loadModels(p)
	if err != nil {
		return nil, fmt.Errorf("load models: %w", err)
	}
	svc, err := loadServices(p)
	if err != nil {
		return nil, fmt.Errorf("load services: %w", err)
	}
	logf, err := os.OpenFile(p.AppLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	logger := log.New(logf, "", log.LstdFlags|log.Lmsgprefix)
	return &App{
		Paths: p, Cfg: cfg, Models: models, Services: svc,
		Log: logger, DryRun: dryRun, Yes: yes,
	}, nil
}

// logboth writes to the rotating app log and echoes to the user.
func (a *App) logboth(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	a.Log.Print(msg)
	fmt.Println(msg)
}

func (a *App) dryPrefix() string {
	if a.DryRun {
		return "[dry-run] "
	}
	return ""
}

// run executes a command with a timeout and returns trimmed stdout.
func run(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var out, errb bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	if err != nil {
		e := strings.TrimSpace(errb.String())
		if e != "" {
			return strings.TrimSpace(out.String()), fmt.Errorf("%s: %v: %s", name, err, e)
		}
		return strings.TrimSpace(out.String()), fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(out.String()), nil
}

func tailFile(path string, n int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n"), nil
}

// expandHome turns a leading ~ into the user's home directory.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
