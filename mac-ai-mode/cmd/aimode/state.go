package main

import (
	"encoding/json"
	"os"
	"time"
)

// Mode values for the lifecycle state machine (ТЗ §6).
const (
	ModeNormal    = "NORMAL"
	ModePreparing = "PREPARING"
	ModeReleasing = "RELEASING RESOURCES"
	ModeStarting  = "STARTING LLM"
	ModeReady     = "AI READY"
	ModeError     = "ERROR"
)

type Transition struct {
	At   time.Time `json:"at"`
	From string    `json:"from"`
	To   string    `json:"to"`
	Note string    `json:"note,omitempty"`
}

// State is persisted to disk so `off` can reverse exactly what `on` changed,
// even after a crash or reboot.
type State struct {
	Mode           string       `json:"mode"`
	Model          string       `json:"model"`
	Since          time.Time    `json:"since"`
	ServerPID      int          `json:"server_pid"`
	CaffeinatePID  int          `json:"caffeinate_pid"`
	StoppedApps    []string     `json:"stopped_apps"`
	DisabledAgents []string     `json:"disabled_agents"`
	WiredLimitPrev int          `json:"wired_limit_prev"`
	WiredLimitSet  bool         `json:"wired_limit_set"`
	Transitions    []Transition `json:"transitions"`
}

func newState() *State {
	return &State{Mode: ModeNormal}
}

func loadState(p Paths) (*State, error) {
	data, err := os.ReadFile(p.StateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return newState(), nil
		}
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		// Corrupt state must never block recovery: fall back to NORMAL.
		return newState(), nil
	}
	if s.Mode == "" {
		s.Mode = ModeNormal
	}
	return &s, nil
}

func (s *State) save(p Paths) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p.StateFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p.StateFile) // atomic replace
}

func (s *State) transition(p Paths, to, note string) {
	s.Transitions = append(s.Transitions, Transition{
		At: time.Now(), From: s.Mode, To: to, Note: note,
	})
	if len(s.Transitions) > 200 {
		s.Transitions = s.Transitions[len(s.Transitions)-200:]
	}
	s.Mode = to
	_ = s.save(p)
}
