package datasheet

import (
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCalculateCostForUsage_TypesafeDecision pins that a decision is priced
// from the datasheet's "decisions" row (typesafe/jev-1.13.0: $0.042 per million
// input tokens, output free), so its spend is nonzero and covers input only.
func TestCalculateCostForUsage_TypesafeDecision(t *testing.T) {
	s := newTestStore()
	s.pricingData[makeKey("jev-1.13.0", "typesafe", "decisions")] = configstoreTables.TableModelPricing{
		Model: "jev-1.13.0", Provider: "typesafe", Mode: "decisions",
		InputCostPerToken:  bifrost.Ptr(4.2e-8),
		OutputCostPerToken: bifrost.Ptr(0.0),
	}
	usage := &schemas.BifrostLLMUsage{PromptTokens: 296, CompletionTokens: 20, TotalTokens: 316}
	got := s.CalculateCostForUsage(usage, schemas.Typesafe, "jev-1.13.0", schemas.DecisionRequest, nil)
	assert.InDelta(t, 296*4.2e-8, got, 1e-15)
	assert.Greater(t, got, 0.0)

	// A response without usage costs nothing.
	assert.Equal(t, 0.0, s.CalculateCostForUsage(nil, schemas.Typesafe, "jev-1.13.0", schemas.DecisionRequest, nil))
}

// The published Jev 1.14.0 row uses evaluation, while our request type is decisions.
// Exercise the imported row through both live-response and usage-only cost paths.
func TestCalculateCost_TypesafeEvaluationPricing(t *testing.T) {
	s := newTestStore()
	row := pricingRowFromDatasheetJSON(t, "typesafe/jev-1.14.0", `{
		"provider":"typesafe", "base_model":"jev-1.14.0", "mode":"evaluation",
		"input_cost_per_token":0.000000042, "output_cost_per_token":0
	}`)
	s.pricingData[makeKey(row.Model, row.Provider, row.Mode)] = row
	usage := &schemas.BifrostLLMUsage{PromptTokens: 1000000, CompletionTokens: 20, TotalTokens: 1000020}
	resp := &schemas.BifrostResponse{DecisionResponse: &schemas.BifrostDecisionResponse{Model: row.Model, Usage: usage}}
	resp.PopulateExtraFields(schemas.DecisionRequest, schemas.Typesafe, row.Model, row.Model)
	assert.InDelta(t, 0.042, s.CalculateCost(resp, nil), 1e-12)
	assert.InDelta(t, 0.042, s.CalculateCostForUsage(usage, schemas.Typesafe, row.Model, schemas.DecisionRequest, nil), 1e-12)

	// Request-scoped overrides still use decisions, not the datasheet's mode.
	require.NoError(t, s.SetOverrides([]configstoreTables.TablePricingOverride{{
		ID: "typesafe-price", ScopeKind: string(ScopeKindProvider), ProviderID: &row.Provider,
		MatchType: string(MatchTypeExact), Pattern: row.Model,
		RequestTypes:     []schemas.RequestType{schemas.DecisionRequest},
		PricingPatchJSON: `{"input_cost_per_token":0.0000002}`,
	}}))
	assert.InDelta(t, 0.2, s.CalculateCost(resp, nil), 1e-12)
	require.NoError(t, s.SetOverrides(nil))

	// Keep existing decisions prices authoritative when both modes are present.
	decisionRow := row
	decisionRow.Mode = "decisions"
	decisionRow.InputCostPerToken = bifrost.Ptr(0.0000001)
	s.pricingData[makeKey(row.Model, row.Provider, decisionRow.Mode)] = decisionRow
	assert.InDelta(t, 0.1, s.CalculateCost(resp, nil), 1e-12)

	// Evaluation must not become a generic fallback for other APIs/providers.
	assert.Zero(t, s.CalculateCostForUsage(usage, schemas.Typesafe, row.Model, schemas.ChatCompletionRequest, nil))
	row.Provider = string(schemas.OpenAI)
	s.pricingData[makeKey(row.Model, row.Provider, row.Mode)] = row
	assert.Zero(t, s.CalculateCostForUsage(usage, schemas.OpenAI, row.Model, schemas.DecisionRequest, nil))
}
