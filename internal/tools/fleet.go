package tools

import (
	"fmt"

	"github.com/zanfiel/synapse/internal/fleet"
)

// RegisterFleetTools adds fleet monitoring tools to the registry.
func RegisterFleetTools(r *Registry, fp *fleet.FleetPulse) {
	r.Register(fleetStatusTool(fp))
	r.Register(fleetCheckTool(fp))
}

func fleetStatusTool(fp *fleet.FleetPulse) *ToolDef {
	return &ToolDef{
		Name:        "fleet_status",
		Description: "Show the health status of all infrastructure servers. Returns a table with memory, disk, containers, and latency for each server.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			summary := fp.StatusSummary()
			if summary == "" {
				return "No fleet data available. Use fleet_check first.", nil
			}
			return summary, nil
		},
	}
}

func fleetCheckTool(fp *fleet.FleetPulse) *ToolDef {
	return &ToolDef{
		Name:        "fleet_check",
		Description: "Run health checks on all infrastructure servers RIGHT NOW. Returns fresh status for all servers.",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Execute: func(args map[string]interface{}) (string, error) {
			results := fp.CheckAll()
			if len(results) == 0 {
				return "No servers configured.", nil
			}
			return fmt.Sprintf("Checked %d servers.\n\n%s", len(results), fp.StatusSummary()), nil
		},
	}
}
