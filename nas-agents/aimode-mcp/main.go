// aimode-mcp — opt-in MCP layer that lets NAS agents (Claude Code, Codex) offload
// work to the local LLM on the Mac. It starts in mode "off" and exposes nothing but
// local_enable until the user explicitly turns local mode on (/local).
//
//	aimode-mcp            stdio MCP server (one process per agent session)
//	aimode-mcp worker     async job worker (one per NAS, systemd --user)
//	aimode-mcp report [N] usage summary over the last N days (default 7)
package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var version = "0.1.0"

// Config comes from nas-agents/config.env (same file scripts/connect.sh uses);
// real environment variables override it.
type Config struct {
	MacHost      string // AI_MAC_HOST — LAN address of the Mac (API + reachability probe)
	MacPort      int    // AI_MAC_PORT — inference API port
	APIKey       string // AI_MAC_API_KEY
	SSHTarget    string // AI_MAC_SSH — ssh destination, e.g. "ai-mac"
	AimodeCmd    string // AI_MAC_AIMODE — aimode command on the Mac (may carry an env prefix)
	WOLMac       string // AI_MAC_WOL_MAC — optional MAC address for Wake-on-LAN
	DataDir      string // AIMODE_DATA_DIR — jobs, metrics, browser logs
	TemplatesDir string // AIMODE_TEMPLATES_DIR
	BrowserDir   string // AIMODE_BROWSER_DIR — node browser agent (browser/agent.mjs)
	NotifyCmd    string // AIMODE_NOTIFY_CMD — run with the job summary on stdin when a job finishes
	ListAll      bool   // AIMODE_MCP_LIST_ALL=1 — for clients that ignore tools/list_changed
}

func loadConfig() (Config, error) {
	exe, _ := os.Executable()
	root := filepath.Dir(filepath.Dir(exe)) // nas-agents/bin/aimode-mcp -> nas-agents
	envFile := os.Getenv("AIMODE_MCP_CONFIG")
	if envFile == "" {
		envFile = filepath.Join(root, "config.env")
	}
	vals, err := readEnvFile(envFile)
	if err != nil && !os.IsNotExist(err) {
		return Config{}, err
	}
	get := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		if v := vals[k]; v != "" {
			return v
		}
		return def
	}
	home, _ := os.UserHomeDir()
	port, _ := strconv.Atoi(get("AI_MAC_PORT", "8080"))
	c := Config{
		MacHost:      get("AI_MAC_HOST", ""),
		MacPort:      port,
		APIKey:       get("AI_MAC_API_KEY", ""),
		SSHTarget:    get("AI_MAC_SSH", "ai-mac"),
		AimodeCmd:    get("AI_MAC_AIMODE", "aimode"),
		WOLMac:       get("AI_MAC_WOL_MAC", ""),
		DataDir:      expandHome(get("AIMODE_DATA_DIR", filepath.Join(home, ".local", "share", "aimode-mcp"))),
		TemplatesDir: expandHome(get("AIMODE_TEMPLATES_DIR", filepath.Join(root, "templates"))),
		BrowserDir:   expandHome(get("AIMODE_BROWSER_DIR", filepath.Join(root, "browser"))),
		NotifyCmd:    get("AIMODE_NOTIFY_CMD", ""),
		ListAll:      get("AIMODE_MCP_LIST_ALL", "") == "1",
	}
	if c.MacHost == "" {
		return c, fmt.Errorf("AI_MAC_HOST is not set (%s)", envFile)
	}
	for _, d := range []string{c.DataDir, c.jobsDir("queued"), c.jobsDir("running"), c.jobsDir("done")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return c, err
		}
	}
	return c, nil
}

// readEnvFile parses KEY=VALUE lines (optional quotes, # comments) without a shell.
func readEnvFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	out := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, sc.Err()
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return home + strings.TrimPrefix(p, "~")
		}
	}
	return p
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "aimode-mcp:", err)
		os.Exit(1)
	}
	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "", "serve":
		err = serve(cfg, os.Stdin, os.Stdout)
	case "worker":
		err = runWorker(cfg)
	case "report":
		days := 7
		if len(args) > 1 {
			if n, e := strconv.Atoi(args[1]); e == nil {
				days = n
			}
		}
		err = printReport(cfg, days)
	case "version":
		fmt.Println("aimode-mcp", version)
	default:
		fmt.Fprintf(os.Stderr, "usage: aimode-mcp [serve|worker|report [days]|version]\n")
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "aimode-mcp:", err)
		os.Exit(1)
	}
}
