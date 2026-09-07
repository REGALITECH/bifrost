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

func TestTranscriptionModelPersistenceWithoutPricing(t *testing.T) {
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
	require.NoError(t, store.DB().AutoMigrate(&tables.TableModelPricing{}, &tables.TableProvider{}))
	require.NoError(t, store.DB().Exec("CREATE TABLE config_models (id TEXT PRIMARY KEY, provider_id INTEGER NOT NULL, name TEXT, created_at DATETIME, updated_at DATETIME, UNIQUE(provider_id, name))").Error)
	require.NoError(t, store.DB().Create(&tables.TableProvider{Name: "transcription", CustomProviderConfigJSON: `{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{}}`}).Error)
	require.NoError(t, store.DB().Exec("INSERT INTO config_models (id,provider_id,name) VALUES ('existing',999,'legacy')").Error)
	// Existing vLLM prices survive registration against the unchanged schema.
	old := tables.TableModelPricing{Model: "asr-new", Provider: "vllm", Mode: "audio_transcription"}
	require.NoError(t, store.DB().Create(&old).Error)
	require.False(t, store.DB().Migrator().HasColumn(&tables.TableModel{}, "enabled"))
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
	state, err := GetTranscriptionModel(ctx, open(), "asr-new")
	require.NoError(t, err)
	require.Equal(t, "asr-new", state.Model)
	var prices int64
	require.NoError(t, store.DB().Model(&tables.TableModelPricing{}).Where("provider = ?", "transcription").Count(&prices).Error)
	require.Zero(t, prices)
	// Re-registration leaves an existing base price untouched.
	price := tables.TableModelPricing{Model: "asr-new", Provider: "transcription", Mode: "audio_transcription", InputCostPerToken: new(0.002)}
	require.NoError(t, store.DB().Create(&price).Error)
	require.NoError(t, EnsureTranscriptionModel(ctx, store, "asr-new"))
	require.NoError(t, store.DB().First(&price, price.ID).Error)
	require.Equal(t, 0.002, *price.InputCostPerToken)
	var count int64
	require.NoError(t, store.DB().Model(&tables.TableModel{}).Count(&count).Error)
	require.EqualValues(t, 2, count)
	var legacy tables.TableModel
	require.NoError(t, store.DB().First(&legacy, "id = ?", "existing").Error)
	require.Equal(t, "legacy", legacy.Name)
	require.False(t, store.DB().Migrator().HasColumn(&tables.TableModel{}, "enabled"))
	require.NoError(t, store.DB().First(&old, old.ID).Error)
	require.Equal(t, "vllm", old.Provider)
}

func TestTranscriptionModelRegistrationWithoutPricingTable(t *testing.T) {
	store := setupRDBTestStore(t)
	require.NoError(t, store.DB().AutoMigrate(&tables.TableModel{}))
	require.NoError(t, store.DB().Create(&tables.TableProvider{Name: "transcription", CustomProviderConfigJSON: `{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{}}`}).Error)
	require.NoError(t, EnsureTranscriptionModel(context.Background(), store, "asr"))
	_, err := GetTranscriptionModel(context.Background(), store, "asr")
	require.NoError(t, err)
}

func TestTranscriptionModelNames(t *testing.T) {
	for _, name := range []string{"qwen3-asr", "A.b_9-", strings.Repeat("a", 128)} {
		require.NoError(t, ValidateTranscriptionModelName(name))
	}
	for _, name := range []string{"", " a", "a ", "a/b", "*", "日本語", "-a", strings.Repeat("a", 129), "a\n"} {
		require.Error(t, ValidateTranscriptionModelName(name), name)
	}
}

func TestTranscriptionRegistrationRejectsConflictingProvider(t *testing.T) {
	for _, cfg := range []string{
		`null`,
		`{"base_provider_type":"anthropic","is_key_less":true,"allowed_requests":{}}`,
		`{"base_provider_type":"openai","is_key_less":false,"allowed_requests":{}}`,
		`{"base_provider_type":"openai","is_key_less":true}`,
		`{"base_provider_type":"openai","is_key_less":true,"allowed_requests":null}`,
		`{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{"list_models":true}}`,
		`{"base_provider_type":"openai","is_key_less":true,"allowed_requests":{"transcription":true}}`,
	} {
		t.Run(cfg, func(t *testing.T) {
			store := setupRDBTestStore(t)
			require.NoError(t, store.DB().AutoMigrate(&tables.TableModel{}, &tables.TableModelPricing{}))
			require.NoError(t, store.DB().Create(&tables.TableProvider{Name: "transcription", CustomProviderConfigJSON: cfg}).Error)
			require.ErrorIs(t, EnsureTranscriptionModel(context.Background(), store, "asr"), ErrTranscriptionProviderConfig)
			var count int64
			require.NoError(t, store.DB().Model(&tables.TableModel{}).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, store.DB().Model(&tables.TableModelPricing{}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}
