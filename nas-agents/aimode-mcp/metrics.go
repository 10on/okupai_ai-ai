package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Usage tracking: one JSON line per offloaded call/job plus the cloud agent's
// verdict (local_feedback). `aimode-mcp report` turns it into a summary of which
// skills pay off and which get redone in the cloud.

type MetricEvent struct {
	TS         time.Time `json:"ts"`
	Kind       string    `json:"kind"` // run | job | browse | feedback
	ID         string    `json:"id"`
	Skill      string    `json:"skill,omitempty"`
	Profile    string    `json:"profile,omitempty"`
	Mode       string    `json:"mode,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	LoadMS     int64     `json:"load_ms,omitempty"`
	InChars    int       `json:"in_chars,omitempty"`
	OutChars   int       `json:"out_chars,omitempty"`
	OK         bool      `json:"ok"`
	Verdict    string    `json:"verdict,omitempty"` // accepted | fixed | redone | unverified
	Note       string    `json:"note,omitempty"`
}

var metricsMu sync.Mutex

func (c Config) metricsPath() string { return filepath.Join(c.DataDir, "metrics.jsonl") }

func (c Config) logMetric(e MetricEvent) {
	if e.TS.IsZero() {
		e.TS = time.Now()
	}
	data, err := json.Marshal(e)
	if err != nil {
		return
	}
	metricsMu.Lock()
	defer metricsMu.Unlock()
	f, err := os.OpenFile(c.metricsPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(data, '\n'))
}

func printReport(c Config, days int) error {
	f, err := os.Open(c.metricsPath())
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("no metrics yet")
			return nil
		}
		return err
	}
	defer f.Close()
	since := time.Now().AddDate(0, 0, -days)
	type agg struct {
		calls, failed     int
		durMS, loadMS     int64
		inChars, outChars int
		verdicts          map[string]int
	}
	bySkill := map[string]*agg{}
	skillOf := map[string]string{} // id -> skill, so feedback lands on the right row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	for sc.Scan() {
		var e MetricEvent
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.TS.Before(since) {
			continue
		}
		skill := e.Skill
		if skill == "" {
			skill = skillOf[e.ID]
		}
		if skill == "" {
			skill = "(none)"
		}
		a := bySkill[skill]
		if a == nil {
			a = &agg{verdicts: map[string]int{}}
			bySkill[skill] = a
		}
		if e.Kind == "feedback" {
			a.verdicts[e.Verdict]++
			continue
		}
		skillOf[e.ID] = skill
		a.calls++
		if !e.OK {
			a.failed++
		}
		a.durMS += e.DurationMS
		a.loadMS += e.LoadMS
		a.inChars += e.InChars
		a.outChars += e.OutChars
	}
	names := make([]string, 0, len(bySkill))
	for k := range bySkill {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Printf("aimode offload — last %d days\n\n", days)
	fmt.Printf("%-18s %6s %6s %9s %9s %10s %10s  %s\n", "SKILL", "CALLS", "FAILED", "RUN min", "LOAD min", "IN kchars", "OUT kchar", "VERDICTS (accepted/fixed/redone/unverified)")
	for _, n := range names {
		a := bySkill[n]
		fmt.Printf("%-18s %6d %6d %9.1f %9.1f %10.1f %10.1f  %d/%d/%d/%d\n", n, a.calls, a.failed,
			float64(a.durMS)/60000, float64(a.loadMS)/60000, float64(a.inChars)/1000, float64(a.outChars)/1000,
			a.verdicts["accepted"], a.verdicts["fixed"], a.verdicts["redone"], a.verdicts["unverified"])
	}
	return sc.Err()
}
