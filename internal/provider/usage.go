package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// UsageTracker tracks provider usage locally to warn before hitting limits.
// Persists to ~/.synapse/usage.json between sessions.

type UsageRecord struct {
	Timestamp    time.Time `json:"ts"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"in"`
	OutputTokens int       `json:"out"`
}

type UsageLimits struct {
	MaxRequestsPerHour int `json:"max_requests_per_hour"`
	MaxRequestsPerDay  int `json:"max_requests_per_day"`
	WarnAtPercent      int `json:"warn_at_percent"` // warn when this % of limit is used
}

type UsageStats struct {
	RequestsLastHour int
	RequestsLastDay  int
	TokensLastHour   int
	TokensLastDay    int
	LimitHour        int
	LimitDay         int
	PercentHour      int
	PercentDay       int
	Warning          string
}

type UsageTracker struct {
	mu      sync.Mutex
	records []UsageRecord
	limits  UsageLimits
	path    string
}

func NewUsageTracker(configDir string, limits UsageLimits) *UsageTracker {
	if limits.MaxRequestsPerHour == 0 {
		limits.MaxRequestsPerHour = 50 // conservative default for Max 5x
	}
	if limits.MaxRequestsPerDay == 0 {
		limits.MaxRequestsPerDay = 500
	}
	if limits.WarnAtPercent == 0 {
		limits.WarnAtPercent = 75
	}

	t := &UsageTracker{
		limits: limits,
		path:   filepath.Join(configDir, "usage.json"),
	}
	t.load()
	t.prune() // drop records older than 24h
	return t
}

// Record logs a completed request.
func (t *UsageTracker) Record(model string, inputTokens, outputTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.records = append(t.records, UsageRecord{
		Timestamp:    time.Now(),
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	})
	t.prune()
	t.save()
}

// Check returns current usage stats and any warning.
func (t *UsageTracker) Check() UsageStats {
	t.mu.Lock()
	defer t.mu.Unlock()

	now := time.Now()
	hourAgo := now.Add(-1 * time.Hour)
	dayAgo := now.Add(-24 * time.Hour)

	var stats UsageStats
	stats.LimitHour = t.limits.MaxRequestsPerHour
	stats.LimitDay = t.limits.MaxRequestsPerDay

	for _, r := range t.records {
		if r.Timestamp.After(dayAgo) {
			stats.RequestsLastDay++
			stats.TokensLastDay += r.InputTokens + r.OutputTokens
		}
		if r.Timestamp.After(hourAgo) {
			stats.RequestsLastHour++
			stats.TokensLastHour += r.InputTokens + r.OutputTokens
		}
	}

	if stats.LimitHour > 0 {
		stats.PercentHour = (stats.RequestsLastHour * 100) / stats.LimitHour
	}
	if stats.LimitDay > 0 {
		stats.PercentDay = (stats.RequestsLastDay * 100) / stats.LimitDay
	}

	warn := t.limits.WarnAtPercent
	if stats.PercentHour >= 100 {
		stats.Warning = fmt.Sprintf("HOURLY LIMIT HIT: %d/%d requests", stats.RequestsLastHour, stats.LimitHour)
	} else if stats.PercentDay >= 100 {
		stats.Warning = fmt.Sprintf("DAILY LIMIT HIT: %d/%d requests", stats.RequestsLastDay, stats.LimitDay)
	} else if stats.PercentHour >= warn {
		stats.Warning = fmt.Sprintf("approaching hourly limit: %d/%d (%d%%)", stats.RequestsLastHour, stats.LimitHour, stats.PercentHour)
	} else if stats.PercentDay >= warn {
		stats.Warning = fmt.Sprintf("approaching daily limit: %d/%d (%d%%)", stats.RequestsLastDay, stats.LimitDay, stats.PercentDay)
	}

	return stats
}

// PreCheck returns an error if we're at or over limits.
func (t *UsageTracker) PreCheck() error {
	stats := t.Check()
	if stats.PercentHour >= 100 {
		return fmt.Errorf("hourly usage limit reached (%d/%d). Resets in ~%d min",
			stats.RequestsLastHour, stats.LimitHour, t.minutesToHourReset())
	}
	if stats.PercentDay >= 100 {
		return fmt.Errorf("daily usage limit reached (%d/%d)", stats.RequestsLastDay, stats.LimitDay)
	}
	return nil
}

// FormatStatus returns a human-readable usage summary.
func (t *UsageTracker) FormatStatus() string {
	s := t.Check()
	bar := func(pct int) string {
		filled := pct / 5 // 20 chars wide
		if filled > 20 {
			filled = 20
		}
		empty := 20 - filled
		b := ""
		for i := 0; i < filled; i++ {
			b += "█"
		}
		for i := 0; i < empty; i++ {
			b += "░"
		}
		return b
	}

	out := fmt.Sprintf("Hour:  %s %d/%d (%d%%)\n", bar(s.PercentHour), s.RequestsLastHour, s.LimitHour, s.PercentHour)
	out += fmt.Sprintf("Day:   %s %d/%d (%d%%)\n", bar(s.PercentDay), s.RequestsLastDay, s.LimitDay, s.PercentDay)
	out += fmt.Sprintf("Tokens (1h): %dk in, %dk out\n", s.TokensLastHour/1000, 0)

	// Break down tokens
	t.mu.Lock()
	hourAgo := time.Now().Add(-1 * time.Hour)
	var inTok, outTok int
	for _, r := range t.records {
		if r.Timestamp.After(hourAgo) {
			inTok += r.InputTokens
			outTok += r.OutputTokens
		}
	}
	t.mu.Unlock()
	out = fmt.Sprintf("Hour:  %s %d/%d (%d%%)\n", bar(s.PercentHour), s.RequestsLastHour, s.LimitHour, s.PercentHour)
	out += fmt.Sprintf("Day:   %s %d/%d (%d%%)\n", bar(s.PercentDay), s.RequestsLastDay, s.LimitDay, s.PercentDay)
	out += fmt.Sprintf("Tokens (1h): %dk in / %dk out\n", inTok/1000, outTok/1000)

	if s.Warning != "" {
		out += fmt.Sprintf("⚠ %s\n", s.Warning)
	}
	return out
}

func (t *UsageTracker) minutesToHourReset() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.records) == 0 {
		return 60
	}
	// Find oldest record in the last hour
	hourAgo := time.Now().Add(-1 * time.Hour)
	for _, r := range t.records {
		if r.Timestamp.After(hourAgo) {
			// This is the oldest record in the window — when it ages out, a slot opens
			return int(time.Until(r.Timestamp.Add(1*time.Hour)).Minutes()) + 1
		}
	}
	return 60
}

func (t *UsageTracker) prune() {
	cutoff := time.Now().Add(-24 * time.Hour)
	pruned := make([]UsageRecord, 0, len(t.records))
	for _, r := range t.records {
		if r.Timestamp.After(cutoff) {
			pruned = append(pruned, r)
		}
	}
	t.records = pruned
}

func (t *UsageTracker) load() {
	data, err := os.ReadFile(t.path)
	if err != nil {
		return
	}
	if err := json.Unmarshal(data, &t.records); err != nil {
		fmt.Fprintf(os.Stderr, "warn: corrupt usage tracker file %s: %v\n", t.path, err)
	}
}

func (t *UsageTracker) save() {
	data, err := json.Marshal(t.records)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: usage tracker marshal failed: %v\n", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(t.path), 0700); err != nil {
		fmt.Fprintf(os.Stderr, "warn: usage tracker mkdir failed: %v\n", err)
		return
	}
	if err := os.WriteFile(t.path, data, 0600); err != nil {
		fmt.Fprintf(os.Stderr, "warn: usage tracker save failed: %v\n", err)
	}
}
