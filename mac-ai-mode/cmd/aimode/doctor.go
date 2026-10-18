package main

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type check struct {
	name string
	ok   bool
	note string
}

func (a *App) cmdDoctor() error {
	var checks []check
	add := func(name string, ok bool, note string) {
		checks = append(checks, check{name, ok, note})
	}

	// llama-server
	if path, err := exec.LookPath("llama-server"); err == nil {
		ver, _ := run(5*time.Second, "llama-server", "--version")
		line := firstLine(ver)
		add("llama-server", true, path+"  "+line)
	} else {
		add("llama-server", false, "not in PATH — brew install llama.cpp")
	}

	// caffeinate
	if _, err := exec.LookPath("caffeinate"); err == nil {
		add("caffeinate", true, "present")
	} else {
		add("caffeinate", false, "missing (unexpected on macOS)")
	}

	// SSH reachable
	add("ssh :22", sshListening(), "needed for remote control")

	// API bind host sanity
	host := a.Cfg.API.Host
	add("api host", host != "", fmt.Sprintf("%s:%d (bind target for inference API)", host, a.Cfg.API.Port))

	// sudo for GPU wired limit — test the EXACT command aimode needs (setting the
	// limit to its current value is a harmless no-op), not a generic `sudo -n true`.
	sudoOK := false
	sudoNote := "needed only to raise iogpu.wired_limit_mb for the brain/70B profile"
	if cur, err := wiredLimitGet(); err == nil {
		if _, err := run(3*time.Second, "sudo", "-n", "/usr/sbin/sysctl", fmt.Sprintf("iogpu.wired_limit_mb=%d", cur)); err == nil {
			sudoOK = true
			sudoNote = "passwordless sysctl iogpu.wired_limit_mb works"
		} else {
			sudoNote = "not set up — run scripts/setup-sudoers.sh (only needed for the brain/70B profile)"
		}
	}
	add("sudo (passwordless)", sudoOK, sudoNote)

	// current wired limit
	if wl, err := wiredLimitGet(); err == nil {
		note := fmt.Sprintf("%d MB", wl)
		if wl == 0 {
			note = "0 = system default (~48 GB usable by GPU on 64 GB)"
		}
		add("iogpu.wired_limit_mb", true, note)
	}

	// memory snapshot
	if m, err := memStats(); err == nil {
		add("memory", m.FreePct > 10, fmt.Sprintf("%s available / %s total, pressure %s",
			humanBytes(m.AvailableBytes), humanBytes(m.TotalBytes), m.PressureLabel))
	}

	// disk free on data volume
	if out, err := run(5*time.Second, "df", "-h", "/System/Volumes/Data"); err == nil {
		add("disk", true, lastField(out))
	}

	// power settings relevant to headless/clamshell (ТЗ §5)
	if out, err := run(5*time.Second, "pmset", "-g"); err == nil {
		add("power: sleep", true, grepVal(out, "sleep "))
		add("power: powernap", true, grepVal(out, "powernap"))
		add("power: womp (wake-on-net)", true, grepVal(out, "womp"))
	}

	fmt.Println("aimode doctor")
	fmt.Println(strings.Repeat("-", 60))
	allOK := true
	for _, c := range checks {
		mark := "OK "
		if !c.ok {
			mark = "!! "
			allOK = false
		}
		fmt.Printf("[%s] %-24s %s\n", mark, c.name, c.note)
	}
	fmt.Println(strings.Repeat("-", 60))
	if allOK {
		fmt.Println("All core checks passed.")
	} else {
		fmt.Println("Some checks need attention (see !! above).")
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func lastField(dfOut string) string {
	lines := strings.Split(strings.TrimSpace(dfOut), "\n")
	if len(lines) < 2 {
		return dfOut
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) >= 4 {
		return fmt.Sprintf("%s free of %s (%s used)", f[3], f[1], f[4])
	}
	return lines[len(lines)-1]
}

func grepVal(out, key string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, key) {
			return strings.TrimSpace(line)
		}
	}
	return "(not reported)"
}
