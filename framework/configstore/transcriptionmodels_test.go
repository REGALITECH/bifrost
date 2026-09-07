package configstore

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/framework/configstore/tables"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTranscriptionModelPersistenceAndRepair(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "models.db")
	open := func() *RDBConfigStore {
		db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { sqlDB.Close() })
		store := &RDBConfigStore{logger: &mockLogger{}}
		store.db.Store(db)
		return store
	}
	store := open()
	require.NoError(t, store.DB().AutoMigrate(&tables.TableModelPricing{}))
	// Existing vLLM prices survive the additive migration and all registration operations.
	old := tables.TableModelPricing{Model: "asr-new", Provider: "vllm", Mode: "audio_transcription"}
	require.NoError(t, store.DB().Create(&old).Error)
	require.NoError(t, migrationAddTranscriptionModelsTable(ctx, store.DB(), &mockLogger{}))
	require.NoError(t, migrationAddTranscriptionModelsTable(ctx, store.DB(), &mockLogger{}))
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- EnsureTranscriptionModel(ctx, store, "asr-new") }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	state, err := GetTranscriptionModel(ctx, store, "asr-new")
	require.NoError(t, err)
	require.True(t, state.Enabled)
	require.True(t, state.PricingConfigured)
	require.Equal(t, 0.0, *state.InputCostPerToken)
	priceQuery := store.DB().Model(&tables.TableModelPricing{}).Where("provider = ? AND model = ?", "transcription", "asr-new")
	require.NoError(t, priceQuery.Update("input_cost_per_token", 0.002).Error)
	require.NoError(t, SetTranscriptionModelEnabled(ctx, store, "asr-new", false))
	require.NoError(t, EnsureTranscriptionModel(ctx, store, "asr-new"))
	state, err = GetTranscriptionModel(ctx, open(), "asr-new")
	require.NoError(t, err)
	require.False(t, state.Enabled)
	require.Equal(t, 0.002, *state.InputCostPerToken)
	// A null existing price fails closed and is never silently reset to zero.
	require.NoError(t, priceQuery.Update("input_cost_per_token", nil).Error)
	require.NoError(t, EnsureTranscriptionModel(ctx, store, "asr-new"))
	state, err = GetTranscriptionModel(ctx, store, "asr-new")
	require.NoError(t, err)
	require.False(t, state.PricingConfigured)
	// Missing row (e.g. partial historical setup) is repairable, preserving disabled state.
	require.NoError(t, priceQuery.Delete(&tables.TableModelPricing{}).Error)
	require.NoError(t, EnsureTranscriptionModel(ctx, store, "asr-new"))
	state, err = GetTranscriptionModel(ctx, store, "asr-new")
	require.NoError(t, err)
	require.True(t, state.PricingConfigured)
	require.False(t, state.Enabled)
	var count int64
	require.NoError(t, store.DB().Model(&tables.TableTranscriptionModel{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	require.NoError(t, store.DB().First(&old, old.ID).Error)
	require.Equal(t, "vllm", old.Provider)
}

func TestTranscriptionModelAtomicFailure(t *testing.T) {
	store := setupRDBTestStore(t)
	require.NoError(t, store.DB().AutoMigrate(&tables.TableTranscriptionModel{}))
	// No pricing table: the second insert fails and the registration must roll back.
	require.Error(t, EnsureTranscriptionModel(context.Background(), store, "atomic"))
	var count int64
	require.NoError(t, store.DB().Model(&tables.TableTranscriptionModel{}).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, store.DB().AutoMigrate(&tables.TableModelPricing{}))
	require.NoError(t, EnsureTranscriptionModel(context.Background(), store, "atomic"))
}

func TestTranscriptionModelNames(t *testing.T) {
	for _, name := range []string{"qwen3-asr", "A.b_9-", strings.Repeat("a", 128)} {
		require.NoError(t, ValidateTranscriptionModelName(name))
	}
	for _, name := range []string{"", " a", "a ", "a/b", "*", "日本語", "-a", strings.Repeat("a", 129), "a\n"} {
		require.Error(t, ValidateTranscriptionModelName(name), name)
	}
}
