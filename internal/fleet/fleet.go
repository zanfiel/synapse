package fleet

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zanfiel/synapse/internal/engram"
	sshutil "github.com/zanfiel/synapse/internal/ssh"
)

// Server represents a managed infrastructure node.
type Server struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	KeyPath  string `json:"key_path"`
	HSIP     string `json:"headscale_ip,omitempty"` // Headscale VPN IP
	Tags     []string `json:"tags,omitempty"`
}

// HealthResult represents a single health check result.
type HealthResult struct {
	Server    string        `json:"server"`
	Status    string        `json:"status"` // "ok", "warn", "critical", "unreachable"
	Uptime    string        `json:"uptime,omitempty"`
	LoadAvg   string        `json:"load_avg,omitempty"`
	MemUsed   float64       `json:"mem_used_pct,omitempty"`
	DiskUsed  float64       `json:"disk_used_pct,omitempty"`
	CPU       float64       `json:"cpu_pct,omitempty"`
	Containers int          `json:"containers,omitempty"`
	Services   int          `json:"services_failed,omitempty"`
	Latency   time.Duration `json:"latency_ms"`
	Error     string        `json:"error,omitempty"`
	CheckedAt time.Time     `json:"checked_at"`
	Alerts    []string      `json:"alerts,omitempty"`
}

// FleetPulse manages health monitoring across infrastructure.
type FleetPulse struct {
	servers  []Server
	results  map[string]*HealthResult
	engram   *engram.Client
	ssh      *sshutil.Pool
	mu       sync.RWMutex
	interval time.Duration
	stopCh   chan struct{}
}

func New(servers []Server, engramClient *engram.Client, sshPool *sshutil.Pool, interval time.Duration) *FleetPulse {
	return &FleetPulse{
		servers:  servers,
		results:  make(map[string]*HealthResult),
		engram:   engramClient,
		ssh:      sshPool,
		interval: interval,
		stopCh:   make(chan struct{}),
	}
}

// CheckAll runs health checks on all servers concurrently.
func (fp *FleetPulse) CheckAll() map[string]*HealthResult {
	var wg sync.WaitGroup
	results := make(map[string]*HealthResult)
	var mu sync.Mutex

	for _, srv := range fp.servers {
		wg.Add(1)
		go func(s Server) {
			defer wg.Done()
			result := fp.checkServer(s)
			mu.Lock()
			results[s.Name] = result
			mu.Unlock()
		}(srv)
	}

	wg.Wait()

	// Store results
	fp.mu.Lock()
	fp.results = results
	fp.mu.Unlock()

	return results
}

// checkServer performs health check on a single server via SSH.
func (fp *FleetPulse) checkServer(srv Server) *HealthResult {
	start := time.Now()
	result := &HealthResult{
		Server:    srv.Name,
		CheckedAt: time.Now(),
	}

	// Build SSH target
	target := fmt.Sprintf("%s@%s", srv.User, srv.Host)
	if srv.Port != 0 && srv.Port != 22 {
		target = fmt.Sprintf("%s@%s:%d", srv.User, srv.Host, srv.Port)
	}

	// Single SSH command that gathers everything
	cmd := `echo "===UPTIME===" && uptime && echo "===MEM===" && free -m 2>/dev/null || vm_stat 2>/dev/null && echo "===DISK===" && df -h / 2>/dev/null && echo "===LOAD===" && cat /proc/loadavg 2>/dev/null || sysctl -n vm.loadavg 2>/dev/null && echo "===CONTAINERS===" && (docker ps -q 2>/dev/null | wc -l || podman ps -q 2>/dev/null | wc -l) 2>/dev/null && echo "===FAILED===" && systemctl --failed --no-legend 2>/dev/null | wc -l`

	output, err := fp.ssh.Exec(target, srv.KeyPath, cmd)
	result.Latency = time.Since(start)

	if err != nil {
		result.Status = "unreachable"
		result.Error = err.Error()
		result.Alerts = append(result.Alerts, fmt.Sprintf("%s is unreachable: %s", srv.Name, err.Error()))
		return result
	}

	// Parse output
	result.Status = "ok"
	sections := parseSections(output)

	// Uptime
	if s, ok := sections["UPTIME"]; ok {
		result.Uptime = strings.TrimSpace(s)
	}

	// Memory
	if s, ok := sections["MEM"]; ok {
		result.MemUsed = parseMemPercent(s)
		if result.MemUsed > 90 {
			result.Status = "warn"
			result.Alerts = append(result.Alerts, fmt.Sprintf("%s memory at %.0f%%", srv.Name, result.MemUsed))
		}
		if result.MemUsed > 95 {
			result.Status = "critical"
		}
	}

	// Disk
	if s, ok := sections["DISK"]; ok {
		result.DiskUsed = parseDiskPercent(s)
		if result.DiskUsed > 85 {
			if result.Status == "ok" {
				result.Status = "warn"
			}
			result.Alerts = append(result.Alerts, fmt.Sprintf("%s disk at %.0f%%", srv.Name, result.DiskUsed))
		}
		if result.DiskUsed > 95 {
			result.Status = "critical"
		}
	}

	// Load average
	if s, ok := sections["LOAD"]; ok {
		result.LoadAvg = strings.TrimSpace(strings.Split(s, "\n")[0])
	}

	// Containers
	if s, ok := sections["CONTAINERS"]; ok {
		fmt.Sscanf(strings.TrimSpace(s), "%d", &result.Containers)
	}

	// Failed services
	if s, ok := sections["FAILED"]; ok {
		fmt.Sscanf(strings.TrimSpace(s), "%d", &result.Services)
		if result.Services > 0 {
			if result.Status == "ok" {
				result.Status = "warn"
			}
			result.Alerts = append(result.Alerts, fmt.Sprintf("%s has %d failed service(s)", srv.Name, result.Services))
		}
	}

	return result
}

// Start begins periodic health monitoring.
func (fp *FleetPulse) Start() {
	go func() {
		// Initial check
		fp.CheckAll()
		fp.storeAlerts()

		ticker := time.NewTicker(fp.interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				fp.CheckAll()
				fp.storeAlerts()
			case <-fp.stopCh:
				return
			}
		}
	}()
}

// Stop halts periodic monitoring.
func (fp *FleetPulse) Stop() {
	close(fp.stopCh)
}

// storeAlerts pushes critical/warning alerts to Engram.
func (fp *FleetPulse) storeAlerts() {
	if fp.engram == nil {
		return
	}

	fp.mu.RLock()
	defer fp.mu.RUnlock()

	var criticals, warnings []string
	for _, r := range fp.results {
		for _, alert := range r.Alerts {
			switch r.Status {
			case "critical", "unreachable":
				criticals = append(criticals, alert)
			case "warn":
				warnings = append(warnings, alert)
			}
		}
	}

	if len(criticals) > 0 {
		content := fmt.Sprintf("[fleet-pulse] CRITICAL alerts (%s):\n- %s",
			time.Now().Format("2006-01-02 15:04"), strings.Join(criticals, "\n- "))
		fp.engram.Store(content, "issue")
	}

	// Only store warnings if they're new (avoid spam)
	if len(warnings) > 0 && len(criticals) == 0 {
		content := fmt.Sprintf("[fleet-pulse] Warnings (%s):\n- %s",
			time.Now().Format("2006-01-02 15:04"), strings.Join(warnings, "\n- "))
		fp.engram.Store(content, "state")
	}
}

// StatusSummary returns a formatted string for the /fleet command.
func (fp *FleetPulse) StatusSummary() string {
	fp.mu.RLock()
	defer fp.mu.RUnlock()

	if len(fp.results) == 0 {
		return "No health data yet. Run /fleet check first."
	}

	var sb strings.Builder
	sb.WriteString("╭──────────────────────────────────────────────────────╮\n")
	sb.WriteString("│  ⚡ Fleet Pulse                                      │\n")
	sb.WriteString("├──────────────┬────────┬──────┬──────┬────────┬───────┤\n")
	sb.WriteString("│ Server       │ Status │ Mem% │ Disk │ Contrs │ Ping  │\n")
	sb.WriteString("├──────────────┼────────┼──────┼──────┼────────┼───────┤\n")

	for _, srv := range fp.servers {
		r, ok := fp.results[srv.Name]
		if !ok {
			sb.WriteString(fmt.Sprintf("│ %-12s │   ?    │  --  │  --  │   --   │  --   │\n", srv.Name))
			continue
		}

		icon := "✓"
		switch r.Status {
		case "warn":
			icon = "⚠"
		case "critical":
			icon = "✗"
		case "unreachable":
			icon = "☠"
		}

		name := srv.Name
		if len(name) > 12 {
			name = name[:12]
		}

		sb.WriteString(fmt.Sprintf("│ %-12s │  %s %-3s │ %3.0f%% │ %3.0f%% │   %3d  │ %4dms│\n",
			name, icon, r.Status[:3],
			r.MemUsed, r.DiskUsed,
			r.Containers, r.Latency.Milliseconds()))
	}

	sb.WriteString("╰──────────────┴────────┴──────┴──────┴────────┴───────╯\n")

	// Alerts
	var alerts []string
	for _, r := range fp.results {
		alerts = append(alerts, r.Alerts...)
	}
	if len(alerts) > 0 {
		sb.WriteString("\n⚠ Alerts:\n")
		for _, a := range alerts {
			sb.WriteString("  • " + a + "\n")
		}
	}

	return sb.String()
}

// FormatCompact returns a single-line status for the status bar.
func (fp *FleetPulse) FormatCompact() string {
	fp.mu.RLock()
	defer fp.mu.RUnlock()

	ok, warn, crit, down := 0, 0, 0, 0
	for _, r := range fp.results {
		switch r.Status {
		case "ok":
			ok++
		case "warn":
			warn++
		case "critical":
			crit++
		case "unreachable":
			down++
		}
	}

	if crit > 0 || down > 0 {
		return fmt.Sprintf("🔴 %d/%d", crit+down, len(fp.results))
	}
	if warn > 0 {
		return fmt.Sprintf("🟡 %d/%d", warn, len(fp.results))
	}
	return fmt.Sprintf("🟢 %d/%d", ok, len(fp.results))
}

// --- Parsing helpers ---

func parseSections(output string) map[string]string {
	sections := make(map[string]string)
	current := ""
	var content strings.Builder

	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "===") && strings.HasSuffix(line, "===") {
			if current != "" {
				sections[current] = content.String()
			}
			current = strings.Trim(line, "= ")
			content.Reset()
		} else if current != "" {
			content.WriteString(line + "\n")
		}
	}
	if current != "" {
		sections[current] = content.String()
	}
	return sections
}

func parseMemPercent(memOutput string) float64 {
	for _, line := range strings.Split(memOutput, "\n") {
		if strings.HasPrefix(line, "Mem:") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				var total, used float64
				fmt.Sscanf(fields[1], "%f", &total)
				fmt.Sscanf(fields[2], "%f", &used)
				if total > 0 {
					return (used / total) * 100
				}
			}
		}
	}
	return 0
}

func parseDiskPercent(diskOutput string) float64 {
	for _, line := range strings.Split(diskOutput, "\n") {
		fields := strings.Fields(line)
		for _, f := range fields {
			if strings.HasSuffix(f, "%") {
				var pct float64
				fmt.Sscanf(f, "%f%%", &pct)
				if pct > 0 {
					return pct
				}
			}
		}
	}
	return 0
}
