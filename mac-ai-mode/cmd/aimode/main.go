package main

import (
	"fmt"
	"os"
	"strconv"
)

const usage = `aimode — controlled AI Mode for this Mac

Usage:
  aimode on [model]        free resources and start the LLM server
  aimode off               stop the LLM and restore managed settings
  aimode status            show mode, model, memory, API health
  aimode doctor            diagnose dependencies, network, power, GPU
  aimode models            list configured model profiles and download state
  aimode switch <model>    swap the active model without leaving AI Mode
  aimode logs [N]          tail the last N lines of the logs (default 40)
  aimode benchmark         measure prompt/gen tokens-per-second, save JSON
  aimode transcribe <file> [lang]  speech-to-text (ffmpeg + whisper.cpp), prints JSON
  aimode services          on-demand services (whisper) — use with --json
  aimode version           print version

Flags:
  --dry-run, -n            show what would happen without changing anything
  --yes, -y                assume yes (reserved for interactive prompts)
  --json                   machine-readable output (status, models) for orchestrating agents

Config lives in ~/.aimode/config/ (created on first run).
`

var version = "0.2.0"

func main() {
	var (
		dryRun  bool
		yes     bool
		jsonOut bool
		args    []string
	)
	for _, a := range os.Args[1:] {
		switch a {
		case "--dry-run", "-n":
			dryRun = true
		case "--yes", "-y":
			yes = true
		case "--json":
			jsonOut = true
		case "-h", "--help", "help":
			fmt.Print(usage)
			return
		default:
			args = append(args, a)
		}
	}
	if len(args) == 0 {
		fmt.Print(usage)
		os.Exit(2)
	}

	cmd := args[0]
	rest := args[1:]

	if cmd == "version" {
		fmt.Println("aimode", version)
		return
	}

	app, err := newApp(dryRun, yes)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	switch cmd {
	case "on":
		model := ""
		if len(rest) > 0 {
			model = rest[0]
		}
		err = app.cmdOn(model)
	case "off":
		err = app.cmdOff()
	case "status":
		if jsonOut {
			err = app.cmdStatusJSON()
		} else {
			err = app.cmdStatus()
		}
	case "doctor":
		err = app.cmdDoctor()
	case "models":
		if jsonOut {
			err = app.cmdModelsJSON()
		} else {
			err = app.cmdModels()
		}
	case "switch":
		if len(rest) == 0 {
			err = fmt.Errorf("switch needs a model name (see `aimode models`)")
			break
		}
		err = app.cmdSwitch(rest[0])
	case "logs":
		n := 40
		if len(rest) > 0 {
			if v, e := strconv.Atoi(rest[0]); e == nil {
				n = v
			}
		}
		err = app.cmdLogs(n)
	case "transcribe":
		if len(rest) == 0 {
			err = fmt.Errorf("transcribe needs an audio or video file")
			break
		}
		lang := ""
		if len(rest) > 1 {
			lang = rest[1]
		}
		err = app.cmdTranscribe(rest[0], lang)
	case "services":
		err = app.cmdServicesJSON()
	case "benchmark", "bench":
		err = app.cmdBenchmark()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}

	if err != nil {
		app.Log.Printf("command %q failed: %v", cmd, err)
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
