package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// isRemote reports whether this invocation arrived over SSH.
func isRemote() bool {
	return os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
}

// isInteractive reports whether stdin is a terminal (stdlib, no x/term dep).
func isInteractive() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// confirmQuit guards against an accidental interactive `on` silently closing
// apps. Over SSH or with --yes it does not prompt (the allowlist is the policy).
func (a *App) confirmQuit(targets []string) bool {
	if a.Yes || !isInteractive() || len(targets) == 0 {
		return true
	}
	fmt.Printf("AI Mode will quit these apps to free memory:\n  %s\nProceed? [y/N] ", strings.Join(targets, ", "))
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	default:
		return false
	}
}

func (a *App) startCaffeinate() (int, error) {
	bin, err := exec.LookPath("caffeinate")
	if err != nil {
		return 0, err
	}
	// -i idle sleep, -m disk sleep, -s system sleep (on AC). Closed-lid/clamshell
	// behavior is validated separately in docs/recovery.md (ТЗ §5).
	cmd := exec.Command(bin, "-i", "-m", "-s")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

func killPID(pid int) {
	if pid > 0 && pidAlive(pid) {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

// cmdOn drives NORMAL -> PREPARING -> RELEASING -> STARTING LLM -> AI READY.
func (a *App) cmdOn(modelName string) error {
	lock, err := acquireLock(a.Paths)
	if err != nil {
		return err
	}
	defer lock.release()

	st, err := loadState(a.Paths)
	if err != nil {
		return err
	}
	if modelName == "" {
		modelName = a.Cfg.DefaultModel
		if st.Mode == ModeReady && st.Model != "" {
			modelName = st.Model
		}
	}

	// Idempotency: already up and healthy with the requested model.
	if st.Mode == ModeReady && st.Model == modelName && a.healthOK() {
		a.logboth("AI Mode already ON (model=%s, %s). Nothing to do.", st.Model, a.apiBase())
		return nil
	}

	model, err := a.resolveModel(modelName)
	if err != nil {
		return err
	}

	// SSH preflight (ТЗ §9): never risk remote lockout. aimode never stops sshd,
	// but we still confirm it is reachable before touching anything.
	if !sshListening() {
		msg := "SSH (port 22) is not reachable"
		if isRemote() {
			return fmt.Errorf("%s — refusing to proceed over a remote session", msg)
		}
		a.logboth("warning: %s; remote control may be unavailable", msg)
	}

	a.Log.Printf("=== aimode on: model=%s dry_run=%v remote=%v ===", modelName, a.DryRun, isRemote())
	st.transition(a.Paths, ModePreparing, "model="+modelName)

	// --- PREPARING: figure out what we'd stop -------------------------------
	var targets []string
	for _, app := range a.Services.Apps {
		if n, bad := matchesNever(app, a.Services.Never); bad {
			a.logboth("skip %q: protected by never-list (%q)", app, n)
			continue
		}
		if appRunning(app) {
			targets = append(targets, app)
		}
	}

	if a.DryRun {
		a.logboth("%splan for model %q:", a.dryPrefix(), modelName)
		a.logboth("  apps to quit:      %v", targets)
		a.logboth("  launchd to disable:%v", a.Services.LaunchdUser)
		if a.Cfg.Power.WiredLimitMB > 0 {
			a.logboth("  iogpu.wired_limit_mb -> %d (needs sudo)", a.Cfg.Power.WiredLimitMB)
		}
		a.logboth("  start llama-server: %v", a.serverArgs(modelName, model))
		a.logboth("  caffeinate:        %v", a.Cfg.Power.Caffeinate)
		st.Mode = ModeNormal
		_ = st.save(a.Paths)
		return nil
	}

	if !a.confirmQuit(targets) {
		a.logboth("aborted by user; no changes made")
		st.Mode = ModeNormal
		_ = st.save(a.Paths)
		return nil
	}

	// --- RELEASING RESOURCES ------------------------------------------------
	st.transition(a.Paths, ModeReleasing, fmt.Sprintf("%d apps", len(targets)))
	for _, app := range targets {
		if err := quitApp(app); err != nil {
			a.logboth("could not quit %q: %v (continuing)", app, err)
			continue
		}
		a.logboth("quit %s", app)
		st.StoppedApps = append(st.StoppedApps, app)
	}
	for _, label := range a.Services.LaunchdUser {
		if n, bad := matchesNever(label, a.Services.Never); bad {
			a.logboth("skip launchd %q: protected (%q)", label, n)
			continue
		}
		if err := launchctlBootout(label); err != nil {
			a.logboth("could not disable %q: %v (continuing)", label, err)
			continue
		}
		st.DisabledAgents = append(st.DisabledAgents, label)
	}
	_ = st.save(a.Paths)

	// Give the OS a moment to reclaim memory, then report pressure.
	time.Sleep(2 * time.Second)
	if m, err := memStats(); err == nil {
		a.logboth("memory after release: %s available / %s total, pressure %s",
			humanBytes(m.AvailableBytes), humanBytes(m.TotalBytes), m.PressureLabel)
	}

	// Optional GPU wired-memory bump for large models.
	if a.Cfg.Power.WiredLimitMB > 0 {
		if prev, err := wiredLimitGet(); err == nil {
			if err := wiredLimitSet(a.Cfg.Power.WiredLimitMB); err != nil {
				a.logboth("could not raise GPU wired limit (needs sudo): %v — continuing with default", err)
			} else {
				st.WiredLimitPrev = prev
				st.WiredLimitSet = true
				a.logboth("GPU wired limit: %d -> %d MB", prev, a.Cfg.Power.WiredLimitMB)
				_ = st.save(a.Paths)
			}
		}
	}

	// --- STARTING LLM -------------------------------------------------------
	st.transition(a.Paths, ModeStarting, "model="+modelName)
	pid, err := a.startServer(modelName, model)
	if err != nil {
		a.rollback(st, "start failed: "+err.Error())
		return fmt.Errorf("start llama-server: %w", err)
	}
	st.ServerPID = pid
	_ = st.save(a.Paths)

	timeout := time.Duration(max(30, a.Cfg.StartTimeoutSeconds)) * time.Second
	a.logboth("waiting for model to load (timeout %s)...", timeout)
	if err := a.waitHealthy(timeout); err != nil {
		a.rollback(st, "health check failed")
		return err
	}

	// --- AI READY -----------------------------------------------------------
	if a.Cfg.Power.Caffeinate {
		if cpid, err := a.startCaffeinate(); err != nil {
			a.logboth("caffeinate not started: %v", err)
		} else {
			st.CaffeinatePID = cpid
		}
	}
	st.Model = modelName
	st.Since = time.Now()
	st.transition(a.Paths, ModeReady, "api="+a.apiBase())
	a.logboth("AI READY — model=%s api=%s", modelName, a.apiBase())
	return nil
}

// rollback returns the system to a safe NORMAL state after a failed `on`.
// Per ТЗ it does not reopen GUI apps; it undoes the reversible changes.
func (a *App) rollback(st *State, reason string) {
	a.logboth("rollback: %s", reason)
	if st.ServerPID > 0 {
		_ = a.stopServer(st.ServerPID)
		st.ServerPID = 0
	}
	killPID(st.CaffeinatePID)
	st.CaffeinatePID = 0
	if st.WiredLimitSet {
		if err := wiredLimitSet(st.WiredLimitPrev); err == nil {
			st.WiredLimitSet = false
		}
	}
	for _, label := range st.DisabledAgents {
		_ = launchctlEnable(label)
	}
	st.DisabledAgents = nil
	st.transition(a.Paths, ModeNormal, "rolled back: "+reason)
}

// cmdOff stops the LLM and restores managed settings. Idempotent.
func (a *App) cmdOff() error {
	lock, err := acquireLock(a.Paths)
	if err != nil {
		return err
	}
	defer lock.release()

	st, err := loadState(a.Paths)
	if err != nil {
		return err
	}

	if a.DryRun {
		a.logboth("%swould stop server pid=%d, caffeinate pid=%d, re-enable agents %v, restore wired limit set=%v",
			a.dryPrefix(), st.ServerPID, st.CaffeinatePID, st.DisabledAgents, st.WiredLimitSet)
		return nil
	}

	if st.Mode == ModeNormal && !pidAlive(st.ServerPID) {
		a.logboth("AI Mode already OFF. Nothing to do.")
		return nil
	}

	if err := a.stopServer(st.ServerPID); err != nil {
		a.logboth("stop server: %v", err)
	} else {
		a.logboth("llama-server stopped")
	}
	killPID(st.CaffeinatePID)

	if st.WiredLimitSet {
		if err := wiredLimitSet(st.WiredLimitPrev); err != nil {
			a.logboth("could not restore GPU wired limit: %v", err)
		} else {
			a.logboth("GPU wired limit restored to %d MB", st.WiredLimitPrev)
		}
	}
	for _, label := range st.DisabledAgents {
		if err := launchctlEnable(label); err != nil {
			a.logboth("could not re-enable %q: %v", label, err)
		}
	}

	// Reset to NORMAL, keep the transition history.
	hist := st.Transitions
	fresh := newState()
	fresh.Transitions = hist
	fresh.transition(a.Paths, ModeNormal, "off")
	a.logboth("AI Mode OFF. GUI apps are not reopened by design.")
	return nil
}

// cmdSwitch swaps the active model without leaving AI Mode.
func (a *App) cmdSwitch(modelName string) error {
	lock, err := acquireLock(a.Paths)
	if err != nil {
		return err
	}
	defer lock.release()

	st, err := loadState(a.Paths)
	if err != nil {
		return err
	}
	if st.Mode != ModeReady {
		return fmt.Errorf("AI Mode is not ON; use `aimode on %s` instead", modelName)
	}
	model, err := a.resolveModel(modelName)
	if err != nil {
		return err
	}
	if a.DryRun {
		a.logboth("%swould switch %s -> %s and restart server", a.dryPrefix(), st.Model, modelName)
		return nil
	}

	a.logboth("switching %s -> %s", st.Model, modelName)
	if err := a.stopServer(st.ServerPID); err != nil {
		a.logboth("stop current server: %v", err)
	}
	st.ServerPID = 0
	_ = st.save(a.Paths)

	pid, err := a.startServer(modelName, model)
	if err != nil {
		st.transition(a.Paths, ModeError, "switch start failed")
		return fmt.Errorf("start %s: %w", modelName, err)
	}
	st.ServerPID = pid
	_ = st.save(a.Paths)

	timeout := time.Duration(max(30, a.Cfg.StartTimeoutSeconds)) * time.Second
	if err := a.waitHealthy(timeout); err != nil {
		st.transition(a.Paths, ModeError, "switch health failed")
		return err
	}
	st.Model = modelName
	st.Since = time.Now()
	_ = st.save(a.Paths)
	a.logboth("switched to %s — api=%s", modelName, a.apiBase())
	return nil
}
