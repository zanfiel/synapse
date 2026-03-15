package token

import "strings"

type ModelPricing struct {
	InputPer1M  float64
	OutputPer1M float64
	CachePer1M  float64
}

var pricing = map[string]ModelPricing{
	"claude-sonnet-4":  {3.00, 15.00, 0.30},
	"claude-opus-4":    {15.00, 75.00, 1.50},
	"gpt-4o":           {2.50, 10.00, 1.25},
	"gpt-4o-mini":      {0.15, 0.60, 0.075},
	"o3-mini":          {1.10, 4.40, 0.55},
	"o4-mini":          {1.10, 4.40, 0.55},
	"gemini-2.0-flash": {0.10, 0.40, 0.025},
}

func GetPricing(model string) ModelPricing {
	model = strings.ToLower(model)
	for key, p := range pricing {
		if strings.Contains(model, key) {
			return p
		}
	}
	return ModelPricing{2.50, 10.00, 1.25} // default to GPT-4o rates
}

// CostTracker accumulates token costs across a session.
type CostTracker struct {
	InputTokens  int
	OutputTokens int
	CacheTokens  int
	Model        string
}

func NewCostTracker(model string) *CostTracker {
	return &CostTracker{Model: model}
}

func (ct *CostTracker) Add(input, output, cache int) {
	ct.InputTokens += input
	ct.OutputTokens += output
	ct.CacheTokens += cache
}

func (ct *CostTracker) TotalCost() float64 {
	p := GetPricing(ct.Model)
	return float64(ct.InputTokens)/1_000_000*p.InputPer1M +
		float64(ct.OutputTokens)/1_000_000*p.OutputPer1M +
		float64(ct.CacheTokens)/1_000_000*p.CachePer1M
}

func (ct *CostTracker) SetModel(model string) {
	ct.Model = model
}
