package identity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Habits tracks workflow patterns over time.
// Persisted to disk so patterns survive across sessions.
// After enough repetitions, Synapse can suggest or auto-execute common sequences.
type Habits struct {
	mu       sync.Mutex
	path     string
	Patterns map[string]*Pattern `json:"patterns"`
	Sequences []ActionSequence   `json:"sequences"`
}

// Pattern is a repeated behavior with a counter.
type Pattern struct {
	Action    string    `json:"action"`
	Count     int       `json:"count"`
	LastSeen  time.Time `json:"last_seen"`
	AutoOffer bool      `json:"auto_offer"` // suggest this automatically
}

// ActionSequence tracks "A then B" patterns.
type ActionSequence struct {
	First     string    `json:"first"`
	Then      string    `json:"then"`
	Count     int       `json:"count"`
	LastSeen  time.Time `json:"last_seen"`
}

func NewHabits(configDir string) *Habits {
	path := filepath.Join(configDir, "habits.json")
	h := &Habits{
		path:     path,
		Patterns: make(map[string]*Pattern),
	}
	h.load()
	return h
}

// Track records an action. Call this after every tool execution.
func (h *Habits) Track(action string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()

	// Update pattern count
	if p, ok := h.Patterns[action]; ok {
		p.Count++
		p.LastSeen = now
	} else {
		h.Patterns[action] = &Pattern{
			Action: action, Count: 1, LastSeen: now,
		}
	}

	// Check sequences — was there a recent previous action?
	if len(h.Sequences) > 0 {
		// Find the most recent sequence entry and see if this extends it
		for i := range h.Sequences {
			seq := &h.Sequences[i]
			if seq.Then == action && time.Since(seq.LastSeen) < 5*time.Minute {
				seq.Count++
				seq.LastSeen = now
				h.save()
				return
			}
		}
	}

	h.save()
}

// TrackSequence records that action A was followed by action B.
func (h *Habits) TrackSequence(first, then string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()

	for i := range h.Sequences {
		if h.Sequences[i].First == first && h.Sequences[i].Then == then {
			h.Sequences[i].Count++
			h.Sequences[i].LastSeen = now
			h.save()
			return
		}
	}

	h.Sequences = append(h.Sequences, ActionSequence{
		First: first, Then: then, Count: 1, LastSeen: now,
	})
	h.save()
}

// SuggestNext returns likely next actions based on what just happened.
// Returns actions that have been done 3+ times after the given action.
func (h *Habits) SuggestNext(lastAction string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()

	type scored struct {
		action string
		count  int
	}

	var suggestions []scored
	for _, seq := range h.Sequences {
		if seq.First == lastAction && seq.Count >= 3 {
			suggestions = append(suggestions, scored{seq.Then, seq.Count})
		}
	}

	sort.Slice(suggestions, func(i, j int) bool {
		return suggestions[i].count > suggestions[j].count
	})

	var result []string
	for _, s := range suggestions {
		result = append(result, s.action)
		if len(result) >= 3 {
			break
		}
	}
	return result
}

// TopPatterns returns the most frequent actions.
func (h *Habits) TopPatterns(n int) []Pattern {
	h.mu.Lock()
	defer h.mu.Unlock()

	var patterns []Pattern
	for _, p := range h.Patterns {
		patterns = append(patterns, *p)
	}

	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Count > patterns[j].Count
	})

	if n > len(patterns) {
		n = len(patterns)
	}
	return patterns[:n]
}

// Summary returns a human-readable habits report.
func (h *Habits) Summary() string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if len(h.Patterns) == 0 {
		return "No habits tracked yet."
	}

	var sb strings.Builder
	sb.WriteString("🧠 WORKFLOW PATTERNS\n")
	sb.WriteString(strings.Repeat("─", 40) + "\n")

	// Top actions
	var patterns []Pattern
	for _, p := range h.Patterns {
		patterns = append(patterns, *p)
	}
	sort.Slice(patterns, func(i, j int) bool {
		return patterns[i].Count > patterns[j].Count
	})

	sb.WriteString("Top actions:\n")
	for i, p := range patterns {
		if i >= 10 {
			break
		}
		sb.WriteString(fmt.Sprintf("  %3d× %s\n", p.Count, p.Action))
	}

	// Top sequences
	if len(h.Sequences) > 0 {
		seqs := make([]ActionSequence, len(h.Sequences))
		copy(seqs, h.Sequences)
		sort.Slice(seqs, func(i, j int) bool {
			return seqs[i].Count > seqs[j].Count
		})

		sb.WriteString("\nCommon sequences:\n")
		for i, s := range seqs {
			if i >= 5 || s.Count < 2 {
				break
			}
			sb.WriteString(fmt.Sprintf("  %3d× %s → %s\n", s.Count, s.First, s.Then))
		}
	}

	return sb.String()
}

func (h *Habits) load() {
	data, err := os.ReadFile(h.path)
	if err != nil {
		return
	}
	json.Unmarshal(data, h)
	if h.Patterns == nil {
		h.Patterns = make(map[string]*Pattern)
	}
}

func (h *Habits) save() {
	data, _ := json.MarshalIndent(h, "", "  ")
	os.MkdirAll(filepath.Dir(h.path), 0755)
	os.WriteFile(h.path, data, 0644)
}
