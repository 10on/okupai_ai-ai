package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type MemStats struct {
	TotalBytes      int64
	UsedBytes       int64
	AvailableBytes  int64
	WiredBytes      int64
	CompressedBytes int64
	FreePct         float64
	PressureLabel   string
}

func sysctlInt(key string) (int64, error) {
	out, err := run(3*time.Second, "sysctl", "-n", key)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(strings.TrimSpace(out), 10, 64)
}

func pageSize() int64 {
	ps, err := sysctlInt("hw.pagesize")
	if err != nil || ps <= 0 {
		return 16384 // Apple silicon default
	}
	return ps
}

func memStats() (MemStats, error) {
	var m MemStats
	total, err := sysctlInt("hw.memsize")
	if err != nil {
		return m, err
	}
	m.TotalBytes = total

	out, err := run(3*time.Second, "vm_stat")
	if err != nil {
		return m, err
	}
	ps := pageSize()
	pages := map[string]int64{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(strings.TrimRight(parts[1], "."))
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			pages[key] = n
		}
	}
	wired := pages["Pages wired down"] * ps
	active := pages["Pages active"] * ps
	compressed := pages["Pages occupied by compressor"] * ps
	m.WiredBytes = wired
	m.CompressedBytes = compressed
	// Matches Activity Monitor's "Memory Used" closely enough to be honest.
	m.UsedBytes = wired + active + compressed
	m.AvailableBytes = total - m.UsedBytes
	if total > 0 {
		m.FreePct = float64(m.AvailableBytes) / float64(total) * 100
	}

	// Prefer the kernel's real pressure level; fall back to a derived label.
	if lvl, err := sysctlInt("kern.memorystatus_vm_pressure_level"); err == nil {
		switch lvl {
		case 1:
			m.PressureLabel = "Normal"
		case 2:
			m.PressureLabel = "Warning"
		case 4:
			m.PressureLabel = "Critical"
		default:
			m.PressureLabel = fmt.Sprintf("level %d", lvl)
		}
	} else {
		switch {
		case m.FreePct >= 40:
			m.PressureLabel = "Normal (approx)"
		case m.FreePct >= 20:
			m.PressureLabel = "Warning (approx)"
		default:
			m.PressureLabel = "Critical (approx)"
		}
	}
	return m, nil
}

type SwapStats struct {
	UsedBytes int64
	Active    bool
}

func swapStats() (SwapStats, error) {
	var s SwapStats
	out, err := run(3*time.Second, "sysctl", "-n", "vm.swapusage")
	if err != nil {
		return s, err
	}
	// Format: total = 0.00M  used = 1024.00M  free = 512.00M  (encrypted)
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "used" && i+2 < len(fields) && fields[i+1] == "=" {
			val := strings.TrimSuffix(fields[i+2], "M")
			if mb, err := strconv.ParseFloat(val, 64); err == nil {
				s.UsedBytes = int64(mb * 1024 * 1024)
				s.Active = mb > 0
			}
		}
	}
	return s, nil
}

func loadAvg() string {
	out, err := run(3*time.Second, "sysctl", "-n", "vm.loadavg")
	if err != nil {
		return "n/a"
	}
	out = strings.Trim(out, "{} ")
	return strings.TrimSpace(out)
}

// tcpOpen reports whether a TCP port accepts connections. Used to verify SSH
// is reachable before we touch any services (ТЗ §9).
func tcpOpen(host string, port int, timeout time.Duration) bool {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func sshListening() bool {
	return tcpOpen("127.0.0.1", 22, 2*time.Second)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// signal 0 probes existence without affecting the process.
	return syscall.Kill(pid, 0) == nil
}

// ---- GUI application control -------------------------------------------------

func appRunning(name string) bool {
	out, err := run(5*time.Second, "osascript", "-e",
		fmt.Sprintf("application %q is running", name))
	return err == nil && strings.TrimSpace(out) == "true"
}

func quitApp(name string) error {
	_, err := run(20*time.Second, "osascript", "-e",
		fmt.Sprintf("tell application %q to quit", name))
	return err
}

// matchesNever guards every stop action: if the target matches a protected
// substring, we refuse. Defense in depth on top of the allowlist.
func matchesNever(target string, never []string) (string, bool) {
	t := strings.ToLower(target)
	for _, n := range never {
		if n == "" {
			continue
		}
		if strings.Contains(t, strings.ToLower(n)) {
			return n, true
		}
	}
	return "", false
}

// ---- launchd user agents -----------------------------------------------------

func launchctlBootout(label string) error {
	uid := syscall.Getuid()
	_, err := run(10*time.Second, "launchctl", "disable", fmt.Sprintf("gui/%d/%s", uid, label))
	return err
}

func launchctlEnable(label string) error {
	uid := syscall.Getuid()
	_, err := run(10*time.Second, "launchctl", "enable", fmt.Sprintf("gui/%d/%s", uid, label))
	return err
}

// ---- GPU wired memory limit --------------------------------------------------

func wiredLimitGet() (int, error) {
	v, err := sysctlInt("iogpu.wired_limit_mb")
	return int(v), err
}

// wiredLimitSet needs root. We only use sudo -n (non-interactive): if it isn't
// already authorized we return an error and the caller continues without it,
// never prompting or blocking a remote session.
func wiredLimitSet(mb int) error {
	_, err := run(5*time.Second, "sudo", "-n", "sysctl", fmt.Sprintf("iogpu.wired_limit_mb=%d", mb))
	return err
}
