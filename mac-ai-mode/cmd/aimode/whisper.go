package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Speech-to-text via whisper.cpp (`brew install whisper-cpp`). Independent of AI
// Mode: it runs on demand next to whatever LLM is loaded (large-v3-turbo needs
// ~2 GB), so it takes no lock and changes no state.

func findWhisperCLI() (string, error) {
	if p, err := exec.LookPath("whisper-cli"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/whisper-cli", "/usr/local/bin/whisper-cli"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("whisper-cli not found (brew install whisper-cpp)")
}

func (a *App) whisperModel() string {
	m := a.Cfg.Whisper.Model
	if m == "" {
		m = "~/.aimode/models/whisper/ggml-large-v3-turbo.bin"
	}
	return expandHome(m)
}

type whisperService struct {
	Available bool   `json:"available"`
	Binary    string `json:"binary,omitempty"`
	Model     string `json:"model"`
	Detail    string `json:"detail,omitempty"`
}

func (a *App) whisperStatus() whisperService {
	ws := whisperService{Model: a.whisperModel()}
	bin, err := findWhisperCLI()
	if err != nil {
		ws.Detail = err.Error()
		return ws
	}
	ws.Binary = bin
	if _, err := os.Stat(ws.Model); err != nil {
		ws.Detail = "model file missing: " + ws.Model
		return ws
	}
	ws.Available = true
	return ws
}

// cmdServicesJSON reports on-demand services for an orchestrating agent.
func (a *App) cmdServicesJSON() error {
	data, _ := json.MarshalIndent(map[string]any{"whisper": a.whisperStatus()}, "", "  ")
	fmt.Println(string(data))
	return nil
}

type transcriptSegment struct {
	From string `json:"from"`
	To   string `json:"to"`
	Text string `json:"text"`
}

func findFFmpeg() (string, error) {
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("ffmpeg not found (brew install ffmpeg)")
}

// cmdTranscribe converts any audio/video file to 16 kHz mono WAV (ffmpeg — done
// here, the NAS CPU is weak), runs whisper-cli on it and prints JSON:
// {language, segments:[{from,to,text}], duration_ms}.
func (a *App) cmdTranscribe(file, lang string) error {
	ws := a.whisperStatus()
	if !ws.Available {
		return fmt.Errorf("whisper unavailable: %s", ws.Detail)
	}
	if lang == "" {
		lang = "auto"
	}
	file = expandHome(file)
	if _, err := os.Stat(file); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "aimode-whisper-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ffmpeg, err := findFFmpeg()
	if err != nil {
		return err
	}
	wav := filepath.Join(tmp, "in.wav")
	if _, err := run(time.Hour, ffmpeg, "-nostdin", "-loglevel", "error", "-y", "-i", file,
		"-vn", "-ac", "1", "-ar", "16000", "-c:a", "pcm_s16le", wav); err != nil {
		return err
	}
	outBase := filepath.Join(tmp, "out")
	args := []string{"-m", ws.Model, "-f", wav, "-l", lang, "-oj", "-of", outBase, "-np"}
	if a.Cfg.Whisper.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(a.Cfg.Whisper.Threads))
	}
	start := time.Now()
	// Generous cap: turbo on M1 Max runs far faster than real time.
	if _, err := run(3*time.Hour, ws.Binary, args...); err != nil {
		return err
	}
	data, err := os.ReadFile(outBase + ".json")
	if err != nil {
		return err
	}
	var raw struct {
		Result struct {
			Language string `json:"language"`
		} `json:"result"`
		Transcription []struct {
			Timestamps struct {
				From string `json:"from"`
				To   string `json:"to"`
			} `json:"timestamps"`
			Text string `json:"text"`
		} `json:"transcription"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("unparseable whisper output: %w", err)
	}
	segs := make([]transcriptSegment, 0, len(raw.Transcription))
	for _, s := range raw.Transcription {
		segs = append(segs, transcriptSegment{From: s.Timestamps.From, To: s.Timestamps.To, Text: strings.TrimSpace(s.Text)})
	}
	a.Log.Printf("transcribe %s: lang=%s segments=%d in %s", filepath.Base(file), raw.Result.Language, len(segs), time.Since(start).Round(time.Millisecond))
	out, _ := json.Marshal(map[string]any{
		"language":    raw.Result.Language,
		"segments":    segs,
		"duration_ms": time.Since(start).Milliseconds(),
	})
	fmt.Println(string(out))
	return nil
}
