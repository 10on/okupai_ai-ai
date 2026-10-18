package main

import (
	"os"
	"path/filepath"
)

// Runtime layout under ~/.aimode. Everything aimode writes lives here, so it
// survives reboots and lets `off` reverse whatever `on` did.
type Paths struct {
	Home       string // ~/.aimode
	ConfigDir  string // ~/.aimode/config
	LogDir     string // ~/.aimode/logs
	StateFile  string // ~/.aimode/state.json
	LockFile   string // ~/.aimode/aimode.lock
	PidFile    string // ~/.aimode/server.pid
	AppLog     string // ~/.aimode/logs/aimode.log
	ServerLog  string // ~/.aimode/logs/server.log
	APIKeyFile string // ~/.aimode/api_key — written from config for llama-server
}

func resolvePaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	base := filepath.Join(home, ".aimode")
	if v := os.Getenv("AIMODE_HOME"); v != "" {
		base = v
	}
	p := Paths{
		Home:      base,
		ConfigDir: filepath.Join(base, "config"),
		LogDir:    filepath.Join(base, "logs"),
		StateFile: filepath.Join(base, "state.json"),
		LockFile:  filepath.Join(base, "aimode.lock"),
		PidFile:   filepath.Join(base, "server.pid"),
	}
	p.AppLog = filepath.Join(p.LogDir, "aimode.log")
	p.ServerLog = filepath.Join(p.LogDir, "server.log")
	p.APIKeyFile = filepath.Join(base, "api_key")
	for _, d := range []string{p.Home, p.ConfigDir, p.LogDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Paths{}, err
		}
	}
	return p, nil
}
