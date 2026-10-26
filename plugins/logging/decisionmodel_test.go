package logging

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/stretchr/testify/require"
)

func TestDecisionResponseModelSurvivesLogPersistence(t *testing.T) {
	cases := []struct {
		name, requested, routed, reported string
		provider                          schemas.ModelProvider
		disableContent                    bool
	}{
		{"latest_1_13", "jev-latest", "jev-latest", "jev-1.13.0", schemas.Typesafe, true},
		{"preview_1_14", "jev-preview", "jev-preview", "jev-1.14.0", schemas.Typesafe, true},
		{"fixed_1_14", "jev-1.14.0", "jev-1.14.0", "jev-1.14.0", schemas.Typesafe, false},
		{"custom_provider_and_key_alias", "decision-model", "custom-latest", "custom-v2", schemas.ModelProvider("custom-decision"), true},
		{"missing_response_model", "jev-latest", "jev-latest", "", schemas.Typesafe, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			plugin, err := Init(context.Background(), &Config{DisableContentLogging: &tc.disableContent}, testLogger{}, store, nil, nil, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, plugin.Cleanup()) })

			ctx := schemas.NewBifrostContext(context.Background(), schemas.NoDeadline)
			ctx.SetValue(schemas.BifrostContextKeyRequestID, tc.name)
			ctx.SetValue(schemas.BifrostContextKeyRequestHeaders, map[string]string{"x-bf-lh-tenant": "demo"})
			req := &schemas.BifrostRequest{
				RequestType: schemas.DecisionRequest,
				DecisionRequest: &schemas.BifrostDecisionRequest{
					Provider: tc.provider, Model: tc.requested,
					Input: schemas.DecisionInput{Text: schemas.Ptr("private input")},
				},
			}
			_, _, err = plugin.PreLLMHook(ctx, req)
			require.NoError(t, err)
			result := &schemas.BifrostResponse{DecisionResponse: &schemas.BifrostDecisionResponse{
				Model:   tc.reported,
				Answers: []schemas.DecisionAnswer{{Name: schemas.Ptr("q"), Type: schemas.DecisionTypePredicate, Probability: schemas.Ptr(0.75)}},
				Usage:   &schemas.BifrostLLMUsage{PromptTokens: 3, CompletionTokens: 1, TotalTokens: 4},
			}}
			result.PopulateExtraFields(schemas.DecisionRequest, tc.provider, tc.requested, tc.routed)
			_, _, err = plugin.PostLLMHook(ctx, result, nil)
			require.NoError(t, err)
			require.NoError(t, plugin.Cleanup())

			entry, err := store.FindByID(context.Background(), tc.name)
			require.NoError(t, err)
			require.Equal(t, "success", entry.Status)
			require.Equal(t, "demo", entry.MetadataParsed["tenant"])
			if tc.reported == "" {
				require.NotContains(t, entry.MetadataParsed, "response_model", "do not invent a resolved version")
			} else {
				require.Equal(t, tc.reported, entry.MetadataParsed["response_model"])
			}
			// Keep routing identity intact: cost recomputation uses entry.Model.
			require.Equal(t, tc.routed, entry.Model)
			require.Equal(t, tc.routed, result.GetExtraFields().ResolvedModelUsed)
			if tc.requested != tc.routed {
				require.NotNil(t, entry.Alias)
				require.Equal(t, tc.requested, *entry.Alias)
			} else {
				require.Nil(t, entry.Alias)
			}
			require.Equal(t, 4, entry.TotalTokens)
			if tc.disableContent {
				require.Nil(t, entry.InputHistoryParsed)
				require.Nil(t, entry.OutputMessageParsed)
				require.Empty(t, entry.RawResponse)
			} else {
				require.NotNil(t, entry.OutputMessageParsed)
			}
		})
	}
}
