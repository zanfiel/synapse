package agent

import (
	"testing"

	"github.com/zanfiel/synapse/internal/types"
)

func TestCanParallelizeTools(t *testing.T) {
	parallel := []types.ToolCall{{Function: types.FunctionCall{Name: "read"}}, {Function: types.FunctionCall{Name: "glob"}}}
	serial := []types.ToolCall{{Function: types.FunctionCall{Name: "read"}}, {Function: types.FunctionCall{Name: "edit"}}}

	if !canParallelizeTools(parallel) {
		t.Fatal("expected read+glob to be parallelizable")
	}
	if canParallelizeTools(serial) {
		t.Fatal("expected read+edit to require serialization")
	}
}
