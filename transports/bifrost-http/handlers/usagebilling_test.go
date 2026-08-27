package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore"
	configstoreTables "github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/maximhq/bifrost/plugins/governance"
	"github.com/maximhq/bifrost/transports/bifrost-http/lib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"
)

// usageBillingVK is a virtual key that counts its requests on its own rate limit.
type usageBillingVK struct {
	id, value, rateLimitID string
}

var (
	usageBillingVKA = usageBillingVK{id: "vk-usage-a", value: "sk-bf-usage-a", rateLimitID: "rl-usage-a"}
	usageBillingVKB = usageBillingVK{id: "vk-usage-b", value: "sk-bf-usage-b", rateLimitID: "rl-usage-b"}
)

func expectedUsageBillingNonce(owner, requestID string) string {
	sum := sha256.Sum256([]byte(owner + "\x00" + requestID))
	return hex.EncodeToString(sum[:])
}

// newUsageBillingGovernance starts the real governance plugin, in memory, with
// usageBillingVKA and usageBillingVKB allowed to use every model of provider.
func newUsageBillingGovernance(t *testing.T, provider schemas.ModelProvider) *governance.GovernancePlugin {
	t.Helper()
	config := &configstore.GovernanceConfig{}
	for _, key := range []usageBillingVK{usageBillingVKA, usageBillingVKB} {
		tokenLimit, requestLimit, window := int64(1_000_000), int64(1_000), "1h"
		rateLimit := configstoreTables.TableRateLimit{
			ID:                   key.rateLimitID,
			TokenMaxLimit:        &tokenLimit,
			TokenResetDuration:   &window,
			TokenLastReset:       time.Now(),
			RequestMaxLimit:      &requestLimit,
			RequestResetDuration: &window,
			RequestLastReset:     time.Now(),
		}
		active := true
		rateLimitID := key.rateLimitID
		config.RateLimits = append(config.RateLimits, rateLimit)
		config.VirtualKeys = append(config.VirtualKeys, configstoreTables.TableVirtualKey{
			ID:          key.id,
			Name:        key.id,
			Value:       *schemas.NewSecretVar(key.value),
			IsActive:    &active,
			RateLimitID: &rateLimitID,
			RateLimit:   &rateLimit,
			ProviderConfigs: []configstoreTables.TableVirtualKeyProviderConfig{
				{Provider: string(provider), AllowedModels: schemas.WhiteList{"*"}, Keys: []configstoreTables.TableKey{}},
			},
		})
	}
	plugin, err := governance.Init(context.Background(), nil, testLogger{}, nil, config, nil, nil, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = plugin.Cleanup() })
	return plugin
}

// billedRequests waits for governance's asynchronous billing to finish and
// returns how many requests each virtual key was charged.
func billedRequests(t *testing.T, plugin *governance.GovernancePlugin) map[string]int64 {
	t.Helper()
	require.NoError(t, plugin.Cleanup())
	data := plugin.GetGovernanceStore().GetGovernanceData(context.Background())
	require.NotNil(t, data)
	billed := map[string]int64{}
	for _, key := range []usageBillingVK{usageBillingVKA, usageBillingVKB} {
		rateLimit := data.RateLimits[key.rateLimitID]
		require.NotNil(t, rateLimit, key.rateLimitID)
		billed[key.id] = rateLimit.RequestCurrentUsage
	}
	return billed
}

// usageReport is one POST of a usage report: the key it presents and its
// x-request-id ("" sends none).
type usageReport struct {
	vk        usageBillingVK
	requestID string
}

// runUsageBillingCases posts each case's reports to an endpoint wired to real
// governance and checks what each virtual key was billed. newPoster returns a
// function that posts one report to a fresh handler using gov and returns the
// response status.
func runUsageBillingCases(t *testing.T, provider schemas.ModelProvider, newPoster func(t *testing.T, gov *governance.GovernancePlugin) func(usageReport) int) {
	cases := []struct {
		name    string
		reports []usageReport
		want    map[string]int64
	}{
		{
			name:    "retry with the same x-request-id is billed once",
			reports: []usageReport{{usageBillingVKA, "report-1"}, {usageBillingVKA, "report-1"}},
			want:    map[string]int64{usageBillingVKA.id: 1, usageBillingVKB.id: 0},
		},
		{
			name:    "distinct x-request-ids are each billed",
			reports: []usageReport{{usageBillingVKA, "report-1"}, {usageBillingVKA, "report-2"}},
			want:    map[string]int64{usageBillingVKA.id: 2, usageBillingVKB.id: 0},
		},
		{
			name:    "reports without x-request-id are each billed",
			reports: []usageReport{{usageBillingVKA, ""}, {usageBillingVKA, ""}},
			want:    map[string]int64{usageBillingVKA.id: 2, usageBillingVKB.id: 0},
		},
		{
			name:    "another key reusing the x-request-id is billed",
			reports: []usageReport{{usageBillingVKA, "report-1"}, {usageBillingVKB, "report-1"}},
			want:    map[string]int64{usageBillingVKA.id: 1, usageBillingVKB.id: 1},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gov := newUsageBillingGovernance(t, provider)
			post := newPoster(t, gov)
			for _, report := range tc.reports {
				require.Equal(t, fasthttp.StatusAccepted, post(report))
			}
			assert.Equal(t, tc.want, billedRequests(t, gov))
		})
	}
}

// usageHookRecorder stands in for the logging or governance plugin and records
// every hook call, in order, into calls.
type usageHookRecorder struct {
	name  string
	calls *[]string
	// Context values seen when PreLLMHook ran.
	preProvider string
	preVKID     string
	// BillingNonce seen when PostLLMHook ran.
	postNonce string
}

func (p *usageHookRecorder) GetName() string { return p.name }
func (p *usageHookRecorder) Cleanup() error  { return nil }
func (p *usageHookRecorder) PreRequestHook(_ *schemas.BifrostContext, _ *schemas.BifrostRequest) error {
	*p.calls = append(*p.calls, p.name+".request")
	return nil
}
func (p *usageHookRecorder) PreLLMHook(ctx *schemas.BifrostContext, req *schemas.BifrostRequest) (*schemas.BifrostRequest, *schemas.LLMPluginShortCircuit, error) {
	*p.calls = append(*p.calls, p.name+".pre")
	p.preProvider, _ = ctx.Value(schemas.BifrostContextKeyGovernanceRequestProvider).(string)
	p.preVKID, _ = ctx.Value(schemas.BifrostContextKeyGovernanceVirtualKeyID).(string)
	return req, nil, nil
}
func (p *usageHookRecorder) PostLLMHook(ctx *schemas.BifrostContext, resp *schemas.BifrostResponse, bifrostErr *schemas.BifrostError) (*schemas.BifrostResponse, *schemas.BifrostError, error) {
	*p.calls = append(*p.calls, p.name+".post")
	p.postNonce, _ = ctx.Value(schemas.BifrostContextKeyBillingNonce).(string)
	return resp, bifrostErr, nil
}

// usageHookCoreOrder is the order core's handleRequest runs the logging
// (registered first) and governance hooks in.
var usageHookCoreOrder = []string{
	"logging.request", "governance.request",
	"logging.pre", "governance.pre",
	"governance.post", "logging.post",
}

func TestStampUsageBillingNonce(t *testing.T) {
	newContexts := func(virtualKey, requestID string) (*fasthttp.RequestCtx, *schemas.BifrostContext, string) {
		ctx := newFishAudioUsageTestContext(`{}`, virtualKey, requestID)
		bifrostCtx, cancel := lib.ConvertToBifrostContext(ctx, &lib.Config{ClientConfig: &configstore.ClientConfig{}})
		t.Cleanup(cancel)
		minted, _ := bifrostCtx.Value(schemas.BifrostContextKeyBillingNonce).(string)
		require.NotEmpty(t, minted)
		return ctx, bifrostCtx, minted
	}
	nonce := func(bifrostCtx *schemas.BifrostContext) string {
		value, _ := bifrostCtx.Value(schemas.BifrostContextKeyBillingNonce).(string)
		return value
	}

	t.Run("resolved virtual key ID", func(t *testing.T) {
		ctx, bifrostCtx, _ := newContexts("sk-bf-secret", "report-1")
		bifrostCtx.SetValue(schemas.BifrostContextKeyGovernanceVirtualKeyID, "vk-id")
		stampUsageBillingNonce(ctx, bifrostCtx)
		assert.Equal(t, expectedUsageBillingNonce("vk-id", "report-1"), nonce(bifrostCtx))
	})
	t.Run("presented virtual key when none resolved", func(t *testing.T) {
		ctx, bifrostCtx, _ := newContexts("sk-bf-secret", "report-1")
		stampUsageBillingNonce(ctx, bifrostCtx)
		assert.Equal(t, expectedUsageBillingNonce("sk-bf-secret", "report-1"), nonce(bifrostCtx))
	})
	t.Run("without x-request-id the minted nonce is kept", func(t *testing.T) {
		ctx, bifrostCtx, minted := newContexts("sk-bf-secret", "")
		stampUsageBillingNonce(ctx, bifrostCtx)
		assert.Equal(t, minted, nonce(bifrostCtx))
	})
	t.Run("blank x-request-id counts as none", func(t *testing.T) {
		ctx, bifrostCtx, minted := newContexts("sk-bf-secret", "   ")
		stampUsageBillingNonce(ctx, bifrostCtx)
		assert.Equal(t, minted, nonce(bifrostCtx))
	})
}

const fishAudioUsageBillingBody = `{"billable_bytes":42,"audio_ms":1250,"outcome":"completed","turn_id":"turn-1","sub_id":"sub-1","model":"fishaudio/s2-pro"}`

func TestFishAudioUsageRunsHooksInCoreOrder(t *testing.T) {
	SetLogger(testLogger{})
	calls := []string{}
	loggingPlugin := &usageHookRecorder{name: "logging", calls: &calls}
	governancePlugin := &usageHookRecorder{name: "governance", calls: &calls}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin, governancePlugin}
	config.BasePlugins.Store(&plugins)
	handler := NewFishAudioUsageHandler(config, "logging", "governance")

	ctx := newFishAudioUsageTestContext(fishAudioUsageBillingBody, "sk-bf-secret", "report-1")
	handler.recordUsage(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	assert.Equal(t, usageHookCoreOrder, calls)
	// Nothing resolved the key, so the nonce falls back to the presented one.
	assert.Equal(t, expectedUsageBillingNonce("sk-bf-secret", "report-1"), governancePlugin.postNonce)
}

func TestFishAudioUsageWithRealGovernance(t *testing.T) {
	SetLogger(testLogger{})
	gov := newUsageBillingGovernance(t, schemas.FishAudio)
	calls := []string{}
	loggingPlugin := &usageHookRecorder{name: "logging", calls: &calls}
	config := &lib.Config{ClientConfig: &configstore.ClientConfig{}}
	plugins := []schemas.BasePlugin{loggingPlugin, gov}
	config.BasePlugins.Store(&plugins)
	handler := NewFishAudioUsageHandler(config, "logging", governance.PluginName)

	ctx := newFishAudioUsageTestContext(fishAudioUsageBillingBody, usageBillingVKA.value, "report-1")
	handler.recordUsage(ctx)

	require.Equal(t, fasthttp.StatusAccepted, ctx.Response.StatusCode(), string(ctx.Response.Body()))
	// Governance's PreRequestHook ran before logging's PreLLMHook, as in core:
	// the provider was stamped for access resolution and the key resolved.
	assert.Equal(t, string(schemas.FishAudio), loggingPlugin.preProvider)
	assert.Equal(t, usageBillingVKA.id, loggingPlugin.preVKID)
	assert.Equal(t, expectedUsageBillingNonce(usageBillingVKA.id, "report-1"), loggingPlugin.postNonce)
	assert.Equal(t, map[string]int64{usageBillingVKA.id: 1, usageBillingVKB.id: 0}, billedRequests(t, gov))
}

func TestFishAudioUsageRetryBilling(t *testing.T) {
	SetLogger(testLogger{})
	runUsageBillingCases(t, schemas.FishAudio, func(t *testing.T, gov *governance.GovernancePlugin) func(usageReport) int {
		events := []string{}
		handler, loggingPlugin, _ := newFishAudioUsageTestHandler(&events)
		plugins := []schemas.BasePlugin{loggingPlugin, gov}
		handler.config.BasePlugins.Store(&plugins)
		return func(report usageReport) int {
			ctx := newFishAudioUsageTestContext(fishAudioUsageBillingBody, report.vk.value, report.requestID)
			handler.recordUsage(ctx)
			return ctx.Response.StatusCode()
		}
	})
}
