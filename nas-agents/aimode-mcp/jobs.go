package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Async jobs for overnight / strict-mode work. A job is one JSON file that moves
// queued/ -> running/ -> done/ under DataDir/jobs. The single worker drains the
// queue grouped by profile to minimise model switches, and simply waits while the
// Mac is unavailable — that is the strict-mode "no cloud fallback" path.

type Job struct {
	ID        string            `json:"id"`
	Profile   string            `json:"profile"`
	Priority  int               `json:"priority"` // higher runs first within a profile
	Skill     string            `json:"skill,omitempty"`
	System    string            `json:"system,omitempty"`
	Prompt    string            `json:"prompt,omitempty"`
	TaskFile  string            `json:"task_file,omitempty"` // prompt read from this NAS file at run time
	Template  string            `json:"template,omitempty"`
	Vars      map[string]string `json:"vars,omitempty"`
	MaxTokens int               `json:"max_tokens,omitempty"`
	Schema    json.RawMessage   `json:"json_schema,omitempty"`

	State      string      `json:"state"` // queued | running | done | failed
	Submitted  time.Time   `json:"submitted"`
	Started    *time.Time  `json:"started,omitempty"`
	Finished   *time.Time  `json:"finished,omitempty"`
	Attempts   int         `json:"attempts"`
	Error      string      `json:"error,omitempty"`
	Result     *ChatResult `json:"result,omitempty"`
	LoadSecond float64     `json:"load_seconds,omitempty"`
}

func (c Config) jobsDir(state string) string { return filepath.Join(c.DataDir, "jobs", state) }

func newID(prefix string) string {
	b := make([]byte, 4)
	rand.Read(b)
	return prefix + time.Now().Format("0102-150405") + "-" + hex.EncodeToString(b)
}

func (c Config) saveJob(j *Job, dir string) error {
	data, _ := json.MarshalIndent(j, "", "  ")
	tmp := filepath.Join(dir, "."+j.ID+".tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, j.ID+".json"))
}

func (c Config) submitJob(j *Job) error {
	j.ID = newID("job-")
	j.State = "queued"
	j.Submitted = time.Now()
	return c.saveJob(j, c.jobsDir("queued"))
}

// findJob looks the id up in every state directory.
func (c Config) findJob(id string) (*Job, error) {
	if !validJobID(id) {
		return nil, fmt.Errorf("invalid job id %q", id)
	}
	for _, st := range []string{"done", "running", "queued"} {
		data, err := os.ReadFile(filepath.Join(c.jobsDir(st), id+".json"))
		if err != nil {
			continue
		}
		var j Job
		if err := json.Unmarshal(data, &j); err != nil {
			return nil, err
		}
		return &j, nil
	}
	return nil, fmt.Errorf("job %s not found", id)
}

func validJobID(id string) bool {
	return strings.HasPrefix(id, "job-") && !strings.ContainsAny(id, "/\\.") && len(id) < 64
}

func (c Config) listJobs(state string) []*Job {
	var out []*Job
	files, _ := filepath.Glob(filepath.Join(c.jobsDir(state), "job-*.json"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var j Job
		if json.Unmarshal(data, &j) == nil {
			out = append(out, &j)
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Priority != out[b].Priority {
			return out[a].Priority > out[b].Priority
		}
		return out[a].Submitted.Before(out[b].Submitted)
	})
	return out
}

// workerAlive reports whether a worker holds the lock (best effort).
func (c Config) workerAlive() bool {
	f, err := os.OpenFile(filepath.Join(c.DataDir, "worker.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return false
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true // someone else holds it
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return false
}

func runWorker(c Config) error {
	f, err := os.OpenFile(filepath.Join(c.DataDir, "worker.lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("another worker is already running")
	}
	logf := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, time.Now().Format(time.RFC3339)+" "+format+"\n", a...)
	}
	// A crash mid-job leaves files in running/: requeue them.
	for _, j := range c.listJobs("running") {
		j.State = "queued"
		if c.saveJob(j, c.jobsDir("queued")) == nil {
			os.Remove(filepath.Join(c.jobsDir("running"), j.ID+".json"))
		}
	}
	logf("worker started, data=%s", c.DataDir)
	backoff := 30 * time.Second
	for {
		queued := c.listJobs("queued")
		if len(queued) == 0 {
			time.Sleep(10 * time.Second)
			continue
		}
		st := c.probe()
		if st.Availability == AvailUnreachable && c.WOLMac != "" {
			logf("Mac unreachable, sending Wake-on-LAN")
			if c.wake() == nil {
				st = c.probe()
			}
		}
		if st.Availability == AvailUnreachable || st.Availability == AvailLoading {
			logf("%d job(s) waiting: %s %s", len(queued), st.Availability, st.Detail)
			time.Sleep(backoff)
			backoff = min(backoff*2, 10*time.Minute)
			continue
		}
		backoff = 30 * time.Second

		// Prefer the profile already loaded; otherwise the profile of the top job.
		profile := queued[0].Profile
		for _, j := range queued {
			if st.Availability == AvailReady && j.Profile == st.Model {
				profile = st.Model
				break
			}
		}
		load, how, err := c.ensureProfile(profile)
		if err != nil {
			logf("ensure %s failed: %v", profile, err)
			time.Sleep(backoff)
			continue
		}
		if how != "already active" {
			logf("loaded %s (%s) in %.0fs", profile, how, load)
		}
		for _, j := range queued {
			if j.Profile != profile {
				continue
			}
			c.runJob(j, load, logf)
			load = 0 // attribute the model load to the first job of the batch only
		}
	}
}

func (c Config) runJob(j *Job, load float64, logf func(string, ...any)) {
	os.Remove(filepath.Join(c.jobsDir("queued"), j.ID+".json"))
	now := time.Now()
	j.State, j.Started, j.LoadSecond = "running", &now, load
	j.Attempts++
	c.saveJob(j, c.jobsDir("running"))

	res, err := c.execJob(j)
	end := time.Now()
	j.Finished = &end
	if err != nil {
		j.Error = err.Error()
		if j.Attempts < 2 {
			j.State = "queued" // one retry, then give up
			j.Started, j.Finished = nil, nil
			c.saveJob(j, c.jobsDir("queued"))
			os.Remove(filepath.Join(c.jobsDir("running"), j.ID+".json"))
			logf("job %s failed, requeued: %v", j.ID, err)
			return
		}
		j.State = "failed"
	} else {
		j.State, j.Error, j.Result = "done", "", &res
	}
	c.saveJob(j, c.jobsDir("done"))
	os.Remove(filepath.Join(c.jobsDir("running"), j.ID+".json"))
	c.logMetric(MetricEvent{Kind: "job", ID: j.ID, Skill: j.Skill, Profile: j.Profile,
		DurationMS: end.Sub(*j.Started).Milliseconds(), LoadMS: int64(load * 1000),
		InChars: len(j.Prompt) + len(j.System), OutChars: len(res.Content), OK: err == nil})
	logf("job %s %s", j.ID, j.State)
	c.notifyJob(j)
}

func (c Config) execJob(j *Job) (ChatResult, error) {
	system, prompt := j.System, j.Prompt
	maxTokens := j.MaxTokens
	if j.TaskFile != "" {
		data, err := os.ReadFile(expandHome(j.TaskFile))
		if err != nil {
			return ChatResult{}, err
		}
		prompt = string(data)
	}
	if j.Template != "" {
		t, err := c.loadTemplate(j.Template)
		if err != nil {
			return ChatResult{}, err
		}
		vars := map[string]string{}
		for k, v := range j.Vars {
			vars[k] = v
		}
		if _, ok := vars["input"]; !ok && prompt != "" {
			vars["input"] = prompt
		}
		if system, prompt, err = t.render(vars); err != nil {
			return ChatResult{}, err
		}
		if maxTokens == 0 {
			maxTokens = t.MaxTokens
		}
	}
	res, err := c.chat(ChatRequest{System: system, Prompt: prompt, MaxTokens: maxTokens,
		JSONSchema: j.Schema, Timeout: 30 * time.Minute})
	if err == nil && len(j.Schema) > 0 && !json.Valid([]byte(res.Content)) {
		return res, fmt.Errorf("output is not valid JSON")
	}
	return res, err
}

func (c Config) notifyJob(j *Job) {
	if c.NotifyCmd == "" {
		return
	}
	msg := fmt.Sprintf("aimode job %s (%s, %s): %s", j.ID, j.Profile, j.Skill, j.State)
	if j.Error != "" {
		msg += " — " + j.Error
	}
	cmd := exec.Command("sh", "-c", c.NotifyCmd)
	cmd.Stdin = strings.NewReader(msg + "\n")
	_ = cmd.Run()
}
