package handlers

import (
	"context"
	"encoding/json"
	"testing"

	bifrost "github.com/maximhq/bifrost/core"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/framework/modelcatalog/datasheet"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

type decisionPricingManager struct {
	pricingOverrideTestGovernanceManager
	catalog *datasheet.Store
}

func (m decisionPricingManager) UpsertPricingOverride(_ context.Context, row *tables.TablePricingOverride) error {
	return m.catalog.UpsertOverrides(row)
}

func (m decisionPricingManager) DeletePricingOverride(_ context.Context, id string) error {
	m.catalog.DeleteOverride(id)
	return nil
}

// Exercise the existing HTTP payload, SQLite persistence and cost resolver
// together, including runtime updates and a fresh catalog loading saved rows.
func TestDecisionPricingOverridePersistenceAndRuntime(t *testing.T) {
	SetLogger(&mockLogger{})
	ctx := context.Background()
	store := setupPricingOverrideHandlerStore(t)
	base := tables.TableModelPricing{Model: "jev-latest", Provider: "typesafe", Mode: "decisions",
		InputCostPerToken: bifrost.Ptr(1e-7), OutputCostPerToken: bifrost.Ptr(2e-7)}
	require.NoError(t, store.UpsertModelPrices(ctx, &base))
	catalog := datasheet.New(store, &mockLogger{}, datasheet.Config{})
	require.NoError(t, catalog.LoadFromDB(ctx))
	handler := &GovernanceHandler{configStore: store, governanceManager: decisionPricingManager{catalog: catalog}}
	create := newTestRequestCtx(`{"name":"Jev input pricing","scope_kind":"provider","provider_id":"typesafe","match_type":"exact","pattern":"jev-latest","request_types":["decisions"],"patch":{"input_cost_per_token":0.000000042,"output_cost_per_token":0}}`)
	handler.createPricingOverride(create)
	require.Equal(t, fasthttp.StatusCreated, create.Response.StatusCode(), string(create.Response.Body()))
	var result struct {
		Override tables.TablePricingOverride `json:"pricing_override"`
	}
	require.NoError(t, json.Unmarshal(create.Response.Body(), &result))
	row, err := store.GetPricingOverrideByID(ctx, result.Override.ID)
	require.NoError(t, err)
	require.Equal(t, []schemas.RequestType{schemas.DecisionRequest}, row.RequestTypes)
	var patch datasheet.Options
	require.NoError(t, json.Unmarshal([]byte(row.PricingPatchJSON), &patch))
	require.NotNil(t, patch.InputCostPerToken)
	require.Equal(t, 4.2e-8, *patch.InputCostPerToken)
	require.NotNil(t, patch.OutputCostPerToken)
	require.Zero(t, *patch.OutputCostPerToken)
	usage := &schemas.BifrostLLMUsage{PromptTokens: 296, CompletionTokens: 20, TotalTokens: 316}
	cost := func(s *datasheet.Store) float64 {
		return s.CalculateCostForUsage(usage, schemas.Typesafe, "jev-latest", schemas.DecisionRequest, nil)
	}
	require.InDelta(t, 296*4.2e-8, cost(catalog), 1e-15)

	reloaded := datasheet.New(store, &mockLogger{}, datasheet.Config{})
	require.NoError(t, reloaded.LoadFromDB(ctx))
	rows, err := store.GetPricingOverrides(ctx, configstore.PricingOverrideFilters{})
	require.NoError(t, err)
	require.NoError(t, reloaded.SetOverrides(rows))
	require.InDelta(t, cost(catalog), cost(reloaded), 1e-15)

	update := newTestRequestCtx(`{"patch":{"input_cost_per_token":0,"output_cost_per_token":0}}`)
	update.SetUserValue("id", row.ID)
	handler.updatePricingOverride(update)
	require.Equal(t, fasthttp.StatusOK, update.Response.StatusCode(), string(update.Response.Body()))
	require.Zero(t, cost(catalog))
	row, err = store.GetPricingOverrideByID(ctx, row.ID)
	require.NoError(t, err)
	require.NoError(t, reloaded.SetOverrides([]tables.TablePricingOverride{*row}))
	require.Zero(t, cost(reloaded))

	remove := newTestRequestCtx("")
	remove.SetUserValue("id", row.ID)
	handler.deletePricingOverride(remove)
	require.Equal(t, fasthttp.StatusOK, remove.Response.StatusCode(), string(remove.Response.Body()))
	require.InDelta(t, 296*1e-7+20*2e-7, cost(catalog), 1e-15)
	rows, err = store.GetPricingOverrides(ctx, configstore.PricingOverrideFilters{})
	require.NoError(t, err)
	require.Empty(t, rows)
}
