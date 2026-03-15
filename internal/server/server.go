package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zanfiel/synapse/internal/agent"
	"github.com/zanfiel/synapse/internal/engram"
	"github.com/zanfiel/synapse/internal/provider"
	"github.com/zanfiel/synapse/internal/tools"
	"github.com/zanfiel/synapse/internal/types"
)

// Server provides an HTTP API for headless agent operation.
type Server struct {
	client   provider.Provider
	engram   *engram.Client
	sessions map[string]*SessionState
	mu       sync.RWMutex
	toolReg  *tools.Registry
	opts     agent.Options

	// Pending edit confirms: tool_call_id -> chan bool
	confirmMu       sync.Mutex
	pendingConfirms map[string]chan bool
}

type SessionState struct {
	Agent      *agent.Agent
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastAccess time.Time `json:"last_access"`
	Messages   int       `json:"messages"`
	runMu      sync.Mutex
	activeRuns int32
}

type ChatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
	Model     string `json:"model,omitempty"`
}

type ChatResponse struct {
	SessionID string           `json:"session_id"`
	Text      string           `json:"text"`
	ToolCalls []ToolCallResult `json:"tool_calls,omitempty"`
	Usage     *types.Usage     `json:"usage,omitempty"`
}

type ToolCallResult struct {
	Name   string `json:"name"`
	Args   string `json:"args"`
	Result string `json:"result"`
}

// StreamEvent is sent as SSE data during streaming.
type StreamEvent struct {
	Type       string       `json:"type"`
	Text       string       `json:"text,omitempty"`
	ToolName   string       `json:"tool_name,omitempty"`
	ToolArgs   string       `json:"tool_args,omitempty"`
	ToolResult string       `json:"tool_result,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Usage      *types.Usage `json:"usage,omitempty"`
	SessionID  string       `json:"session_id,omitempty"`
	// Pending edit fields
	FilePath   string `json:"file,omitempty"`
	OldContent string `json:"old_content,omitempty"`
	NewContent string `json:"new_content,omitempty"`
}

func New(client provider.Provider, eg *engram.Client, toolReg *tools.Registry, opts agent.Options) *Server {
	return &Server{
		client:          client,
		engram:          eg,
		sessions:        make(map[string]*SessionState),
		toolReg:         toolReg,
		opts:            opts,
		pendingConfirms: make(map[string]chan bool),
	}
}

func (s *Server) getOrCreateSession(id string) *SessionState {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sess, ok := s.sessions[id]; ok {
		sess.LastAccess = time.Now()
		return sess
	}

	now := time.Now()
	ag := agent.New(s.client, s.engram, s.toolReg, s.opts)
	sess := &SessionState{
		Agent:      ag,
		ID:         id,
		CreatedAt:  now,
		LastAccess: now,
	}
	s.sessions[id] = sess
	return sess
}

// evictStaleSessions removes sessions idle for more than 30 minutes.
func (s *Server) evictStaleSessions() {
	for {
		time.Sleep(5 * time.Minute)
		s.mu.Lock()
		for id, sess := range s.sessions {
			if atomic.LoadInt32(&sess.activeRuns) > 0 {
				continue
			}
			if time.Since(sess.LastAccess) > 30*time.Minute {
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
	}
}

// cors wraps a handler with permissive CORS headers (needed for Electron).
func cors(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next(w, r)
	}
}

func (s *Server) Serve(addr string) error {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1/chat", cors(s.handleChat))
	mux.HandleFunc("/v1/chat/stream", cors(s.handleChatStream))
	mux.HandleFunc("/v1/chat/confirm", cors(s.handleConfirm))
	mux.HandleFunc("/v1/sessions", cors(s.handleSessions))
	mux.HandleFunc("/health", cors(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "version": "0.9.0"})
	}))

	fmt.Printf("⚡ Synapse headless server on %s\n", addr)

	go s.evictStaleSessions()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	return http.Serve(ln, mux)
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			http.Error(w, fmt.Sprintf(`{"error":"panic: %v"}`, r), 500)
		}
	}()

	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("api-%d", time.Now().UnixMilli())
	}

	sess := s.getOrCreateSession(req.SessionID)
	atomic.AddInt32(&sess.activeRuns, 1)
	defer atomic.AddInt32(&sess.activeRuns, -1)
	sess.runMu.Lock()
	defer sess.runMu.Unlock()
	sess.LastAccess = time.Now()

	originalModel := sess.Agent.Model()
	if req.Model != "" && req.Model != originalModel {
		sess.Agent.SetModel(req.Model)
		defer sess.Agent.SetModel(originalModel)
	}

	events := make(chan agent.Event, 100)
	go sess.Agent.Run(req.Message, events)

	resp := ChatResponse{SessionID: req.SessionID}
	var textBuf string

	for evt := range events {
		sess.LastAccess = time.Now()
		switch evt.Type {
		case agent.EventText:
			textBuf += evt.Text
		case agent.EventToolCall:
			resp.ToolCalls = append(resp.ToolCalls, ToolCallResult{
				Name: evt.ToolName, Args: evt.ToolArgs,
			})
		case agent.EventToolResult:
			if len(resp.ToolCalls) > 0 {
				resp.ToolCalls[len(resp.ToolCalls)-1].Result = evt.ToolResult
			}
		case agent.EventUsage:
			resp.Usage = evt.Usage
		case agent.EventConfirm:
			if ch := evt.ConfirmCh(); ch != nil {
				ch <- true
			}
		case agent.EventPendingWrite:
			// Non-streaming: auto-approve writes (no interactive client)
			if ch := evt.ConfirmCh(); ch != nil {
				ch <- true
			}
		case agent.EventError:
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, evt.Text), 500)
			return
		}
	}

	resp.Text = textBuf
	sess.Messages = sess.Agent.MessageCount()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(w, "data: %s\n\n", mustJSON(StreamEvent{Type: "error", Text: fmt.Sprintf("panic: %v", r)}))
		}
	}()

	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	if req.SessionID == "" {
		req.SessionID = fmt.Sprintf("api-%d", time.Now().UnixMilli())
	}

	sess := s.getOrCreateSession(req.SessionID)
	atomic.AddInt32(&sess.activeRuns, 1)
	defer atomic.AddInt32(&sess.activeRuns, -1)
	sess.runMu.Lock()
	defer sess.runMu.Unlock()
	sess.LastAccess = time.Now()

	originalModel := sess.Agent.Model()
	if req.Model != "" && req.Model != originalModel {
		sess.Agent.SetModel(req.Model)
		defer sess.Agent.SetModel(originalModel)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", 500)
		return
	}

	events := make(chan agent.Event, 100)
	go sess.Agent.Run(req.Message, events)

	for evt := range events {
		sess.LastAccess = time.Now()
		var se StreamEvent

		switch evt.Type {
		case agent.EventText:
			se = StreamEvent{Type: "text", Text: evt.Text}
		case agent.EventToolCall:
			se = StreamEvent{Type: "tool_call", ToolName: evt.ToolName, ToolArgs: evt.ToolArgs, ToolCallID: evt.ToolCallID}
		case agent.EventToolResult:
			se = StreamEvent{Type: "tool_result", ToolName: evt.ToolName, ToolResult: evt.ToolResult, ToolCallID: evt.ToolCallID}
		case agent.EventUsage:
			se = StreamEvent{Type: "usage", Usage: evt.Usage}
		case agent.EventConfirm:
			if ch := evt.ConfirmCh(); ch != nil {
				ch <- true
			}
			se = StreamEvent{Type: "tool_call", ToolName: evt.ToolName, Text: "⚠️ Auto-approved: " + evt.ToolArgs}
		case agent.EventPendingWrite:
			// Register confirm channel so /v1/chat/confirm can respond
			if ch := evt.ConfirmCh(); ch != nil {
				s.confirmMu.Lock()
				s.pendingConfirms[evt.ToolCallID] = ch
				s.confirmMu.Unlock()
			}
			// Emit pending_edit SSE — client shows diff, POSTs accept/reject
			se = StreamEvent{
				Type:       "pending_edit",
				ToolCallID: evt.ToolCallID,
				FilePath:   evt.FilePath,
				OldContent: evt.OldContent,
				NewContent: evt.NewContent,
			}
		case agent.EventError:
			se = StreamEvent{Type: "error", Text: evt.Text}
		default:
			continue
		}

		fmt.Fprintf(w, "data: %s\n\n", mustJSON(se))
		flusher.Flush()
	}

	sess.Messages = sess.Agent.MessageCount()
	fmt.Fprintf(w, "data: %s\n\n", mustJSON(StreamEvent{Type: "done", SessionID: req.SessionID}))
	flusher.Flush()
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var sessions []map[string]interface{}
	for id, sess := range s.sessions {
		sessions = append(sessions, map[string]interface{}{
			"id":         id,
			"created_at": sess.CreatedAt,
			"messages":   sess.Agent.MessageCount(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (s *Server) handleConfirm(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST only", 405)
		return
	}

	var req struct {
		ToolCallID string `json:"tool_call_id"`
		Accepted   bool   `json:"accepted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}

	s.confirmMu.Lock()
	ch, ok := s.pendingConfirms[req.ToolCallID]
	if ok {
		delete(s.pendingConfirms, req.ToolCallID)
	}
	s.confirmMu.Unlock()

	if !ok {
		http.Error(w, "no pending edit for that tool_call_id", 404)
		return
	}

	ch <- req.Accepted
	w.WriteHeader(200)
	json.NewEncoder(w).Encode(map[string]bool{"ok": true})
}

func mustJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
