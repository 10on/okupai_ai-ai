package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// local_transcribe: media on the NAS -> scp to the Mac -> `aimode transcribe`
// (ffmpeg + whisper.cpp on the Mac) -> transcript files on the NAS. The NAS CPU is
// weak, so it never decodes: audio files go as they are, video only gets its audio
// track stream-copied out (-c:a copy) so the big file doesn't cross the LAN. Goes over SSH only, so no extra network service or key on the Mac, and it
// works whether or not AI Mode / an LLM profile is loaded.

type transcript struct {
	Language   string `json:"language"`
	DurationMS int64  `json:"duration_ms"`
	Segments   []struct {
		From string `json:"from"`
		To   string `json:"to"`
		Text string `json:"text"`
	} `json:"segments"`
}

func (c Config) services() (map[string]any, error) {
	out, err := c.sshAimode(15*time.Second, "services", "--json")
	if err != nil {
		return nil, err
	}
	var s map[string]any
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		return nil, fmt.Errorf("unparseable `aimode services --json`: %v", err)
	}
	return s, nil
}

func (c Config) whisperAvailable() bool {
	s, err := c.services()
	if err != nil {
		return false
	}
	w, _ := s["whisper"].(map[string]any)
	ok, _ := w["available"].(bool)
	return ok
}

// hasVideo reports whether the file has a real video stream (cover art doesn't count).
func hasVideo(path string) bool {
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", "v",
		"-show_entries", "stream=codec_name:stream_disposition=attached_pic", "-of", "csv=p=0", path).Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" && !strings.HasSuffix(line, ",1") {
			return true
		}
	}
	return false
}

func mediaSeconds(path string) float64 {
	out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration",
		"-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0
	}
	f, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	return f
}

func (c Config) transcribe(src, lang, outDir string) (map[string]any, error) {
	src = expandHome(src)
	if _, err := os.Stat(src); err != nil {
		return nil, err
	}
	if lang != "" && !validName(lang) {
		return nil, fmt.Errorf("invalid language %q (use an ISO code like ru, en, or auto)", lang)
	}
	if !c.macReachable() {
		return nil, fmt.Errorf("Mac not reachable on the LAN (off, asleep or disconnected)")
	}
	seconds := mediaSeconds(src)
	id := newID("tr-")
	tmpDir, err := os.MkdirTemp("", "aimode-tr-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	upload := src
	if hasVideo(src) {
		upload = filepath.Join(tmpDir, id+".mka") // Matroska takes any audio codec as is
		if out, err := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-y", "-i", src,
			"-vn", "-map", "0:a:0", "-c:a", "copy", upload).CombinedOutput(); err != nil {
			return nil, fmt.Errorf("ffmpeg (audio stream copy): %v: %s", err, lastLines(string(out), 3))
		}
	}

	// Remote path is relative to the Mac user's home (ssh/scp start there).
	remote := ".aimode/tmp/" + id + strings.ToLower(filepath.Ext(upload))
	ssh := func(timeout time.Duration, name string, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return exec.CommandContext(ctx, name, args...).CombinedOutput()
	}
	opts := []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=5"}
	if out, err := ssh(30*time.Second, "ssh", append(opts, c.SSHTarget, "mkdir -p .aimode/tmp")...); err != nil {
		return nil, fmt.Errorf("ssh: %v: %s", err, lastLines(string(out), 3))
	}
	if out, err := ssh(30*time.Minute, "scp", append(opts, "-q", upload, c.SSHTarget+":"+remote)...); err != nil {
		return nil, fmt.Errorf("scp: %v: %s", err, lastLines(string(out), 3))
	}
	defer ssh(30*time.Second, "ssh", append(opts, c.SSHTarget, "rm -f "+remote)...)

	timeout := time.Duration(max(300, seconds)) * time.Second // turbo is far faster than real time
	start := time.Now()
	args := []string{"transcribe", remote}
	if lang != "" {
		args = append(args, lang)
	}
	out, err := c.sshAimode(timeout, args...)
	if err != nil {
		return nil, err
	}
	var t transcript
	if err := json.Unmarshal([]byte(out), &t); err != nil {
		return nil, fmt.Errorf("unparseable transcript: %v", err)
	}

	if outDir == "" {
		outDir = filepath.Join(c.DataDir, "transcripts")
	}
	outDir = expandHome(outDir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	base := filepath.Join(outDir, strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))+"-"+id)
	var txt, srt strings.Builder
	for i, s := range t.Segments {
		fmt.Fprintf(&txt, "[%s] %s\n", strings.SplitN(s.From, ",", 2)[0], s.Text)
		fmt.Fprintf(&srt, "%d\n%s --> %s\n%s\n\n", i+1, s.From, s.To, s.Text)
	}
	raw, _ := json.MarshalIndent(t, "", "  ")
	for ext, data := range map[string]string{".txt": txt.String(), ".srt": srt.String(), ".json": string(raw)} {
		if err := os.WriteFile(base+ext, []byte(data), 0o644); err != nil {
			return nil, err
		}
	}
	preview := txt.String()
	if len(preview) > 1500 {
		preview = preview[:1500] + "…"
	}
	return map[string]any{
		"id":             id,
		"language":       t.Language,
		"audio_seconds":  int(seconds + 0.5),
		"took_seconds":   int(time.Since(start).Seconds() + 0.5),
		"segments":       len(t.Segments),
		"chars":          txt.Len(),
		"files":          map[string]string{"txt": base + ".txt", "srt": base + ".srt", "json": base + ".json"},
		"preview":        preview,
		"next":           "read only what you need from the .txt; for summaries/notes use local_run with fast/summarize on the file content",
		"took_ms_on_mac": t.DurationMS,
	}, nil
}
