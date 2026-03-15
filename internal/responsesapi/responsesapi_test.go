package responsesapi

import (
	"strings"
	"testing"

	"github.com/zanfiel/synapse/internal/types"
)

func TestParseStreamHandlesMultilineSSEData(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"type":"response.output_text.delta",`,
		`data: "delta":"hello"}`,
		"",
	}, "\n")

	out := make(chan types.StreamEvent, 8)
	err := ParseStream(strings.NewReader(stream), out)
	close(out)
	if err != nil {
		t.Fatalf("ParseStream error: %v", err)
	}

	var got []string
	for evt := range out {
		if evt.TextDelta != "" {
			got = append(got, evt.TextDelta)
		}
	}

	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("text deltas = %#v, want []string{\"hello\"}", got)
	}
}
