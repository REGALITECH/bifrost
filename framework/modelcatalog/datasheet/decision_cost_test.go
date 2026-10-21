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

func TestDecisionPricingOverrideScopesAndLifecycle(t *testing.T) {
	s := newTestStore()
	base := configstoreTables.TableModelPricing{
		Model: "jev-latest", Provider: "typesafe", Mode: "decisions",
		InputCostPerToken: bifrost.Ptr(1e-7), OutputCostPerToken: bifrost.Ptr(2e-7),
	}
	s.pricingData[makeKey(base.Model, base.Provider, base.Mode)] = base
	provider := configstoreTables.TablePricingOverride{
		ID: "provider-price", ScopeKind: string(ScopeKindProvider), ProviderID: bifrost.Ptr("typesafe"),
		MatchType: string(MatchTypeExact), Pattern: "jev-latest",
		RequestTypes:     []schemas.RequestType{schemas.DecisionRequest},
		PricingPatchJSON: `{"input_cost_per_token":0.000000042,"output_cost_per_token":0}`,
	}
	custom := provider
	custom.ID, custom.ProviderID = "custom-price", bifrost.Ptr("etla-system-one")
	tenant := provider
	tenant.ID, tenant.ScopeKind, tenant.VirtualKeyID = "tenant-price", string(ScopeKindVirtualKeyProvider), bifrost.Ptr("vk-1")
	tenant.PricingPatchJSON = `{"input_cost_per_token":0,"output_cost_per_token":0}`
	require.NoError(t, s.SetOverrides([]configstoreTables.TablePricingOverride{provider, custom, tenant}))
	usage := &schemas.BifrostLLMUsage{PromptTokens: 296, CompletionTokens: 20, TotalTokens: 316}
	check := func(provider schemas.ModelProvider, vk string, want float64) {
		t.Helper()
		scopes := &LookupScopes{VirtualKeyID: vk}
		assert.InDelta(t, want, s.CalculateCostForUsage(usage, provider, "jev-latest", schemas.DecisionRequest, scopes), 1e-15)
		response := &schemas.BifrostResponse{DecisionResponse: &schemas.BifrostDecisionResponse{Model: "jev-1.13.0", Usage: usage}}
		response.PopulateExtraFields(schemas.DecisionRequest, provider, "jev-latest", "jev-latest")
		assert.InDelta(t, want, s.CalculateCost(response, scopes), 1e-15)
	}
	check(schemas.Typesafe, "vk-1", 0)
	check(schemas.Typesafe, "vk-2", 296*4.2e-8)
	// Registered custom providers need no duplicate catalog row.
	check("etla-system-one", "vk-2", 296*4.2e-8)
	check("unmatched-provider", "vk-2", 0)
	assert.Zero(t, s.CalculateCostForUsage(usage, schemas.Typesafe, "jev-latest", schemas.ChatCompletionRequest, nil))

	provider.PricingPatchJSON = `{"input_cost_per_token":0.00000005,"output_cost_per_token":0.00000002}`
	require.NoError(t, s.UpsertOverrides(&provider))
	check(schemas.Typesafe, "vk-2", 296*5e-8+20*2e-8)
	check(schemas.Typesafe, "vk-1", 0)
	s.DeleteOverride(tenant.ID)
	check(schemas.Typesafe, "vk-1", 296*5e-8+20*2e-8)
	s.DeleteOverride(provider.ID)
	check(schemas.Typesafe, "vk-2", 296*1e-7+20*2e-7)
	// Overrides never mutate the underlying catalog price.
	assert.Equal(t, base, s.pricingData[makeKey(base.Model, base.Provider, base.Mode)])
}
