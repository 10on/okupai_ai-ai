package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

// Minimal MCP (JSON-RPC 2.0 over newline-delimited stdio): initialize, ping,
// tools/list, tools/call, plus the tools/list_changed notification the gating needs.

const latestProtocol = "2025-06-18"

var supportedProtocols = map[string]bool{"2024-11-05": true, "2025-03-26": true, "2025-06-18": true}

type rpcMsg struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Server struct {
	cfg  Config
	out  io.Writer
	wmu  sync.Mutex
	smu  sync.Mutex
	mode string // off | prefer | strict — per process = per agent session, never persisted
}

func serve(cfg Config, in io.Reader, out io.Writer) error {
	s := &Server{cfg: cfg, out: out, mode: ModeOff}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1<<20), 64<<20) // images can make requests large
	var wg sync.WaitGroup
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		if len(line) == 0 {
			continue
		}
		var m rpcMsg
		if err := json.Unmarshal(line, &m); err != nil {
			s.send(rpcMsg{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}})
			continue
		}
		if m.Method == "" {
			continue // a response to something we never send requests for
		}
		// Tool calls can take minutes (model loads); handle concurrently so ping
		// and status keep answering.
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handle(m)
		}()
	}
	wg.Wait()
	return sc.Err()
}

func (s *Server) send(m rpcMsg) {
	m.JSONRPC = "2.0"
	data, err := json.Marshal(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "aimode-mcp: marshal:", err)
		return
	}
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.out.Write(append(data, '\n'))
}

func (s *Server) notify(method string) {
	s.send(rpcMsg{Method: method})
}

func (s *Server) handle(m rpcMsg) {
	isNotification := len(m.ID) == 0
	reply := func(result any, err *rpcError) {
		if isNotification {
			return
		}
		s.send(rpcMsg{ID: m.ID, Result: result, Error: err})
	}
	switch m.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(m.Params, &p)
		ver := latestProtocol
		if supportedProtocols[p.ProtocolVersion] {
			ver = p.ProtocolVersion
		}
		reply(map[string]any{
			"protocolVersion": ver,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": true}},
			"serverInfo":      map[string]any{"name": "aimode", "version": version},
			"instructions": "Local LLM on the Mac. Off by default: only call local_enable after the user " +
				"explicitly confirmed enabling local mode (the /local command).",
		}, nil)
	case "ping":
		reply(map[string]any{}, nil)
	case "tools/list":
		reply(map[string]any{"tools": s.listTools()}, nil)
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			reply(nil, &rpcError{-32602, "invalid params"})
			return
		}
		text, isErr := s.callTool(p.Name, p.Arguments)
		reply(map[string]any{
			"content": []map[string]any{{"type": "text", "text": text}},
			"isError": isErr,
		}, nil)
	default:
		if isNotification {
			return // notifications/initialized, cancelled, etc.
		}
		reply(nil, &rpcError{-32601, "method not found: " + m.Method})
	}
}
