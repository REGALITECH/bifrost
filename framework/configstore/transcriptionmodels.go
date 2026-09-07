package configstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/google/uuid"
	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/configstore/tables"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TranscriptionUsageProvider is an application-level custom provider name,
// not a built-in inference provider.
const TranscriptionUsageProvider schemas.ModelProvider = "transcription"

// ErrTranscriptionProviderConfig marks a conflicting provider configuration.
var ErrTranscriptionProviderConfig = errors.New("transcription requires an OpenAI-based keyless custom provider with all operations disabled and no inference URL")

func transcriptionProvider(ctx context.Context, db *gorm.DB) (*tables.TableProvider, error) {
	var provider tables.TableProvider
	if err := db.WithContext(ctx).Where("name = ?", TranscriptionUsageProvider).First(&provider).Error; err != nil {
		return nil, err
	}
	c := provider.CustomProviderConfig
	if c == nil || c.BaseProviderType != schemas.OpenAI || !c.IsKeyLess || c.AllowedRequests == nil || *c.AllowedRequests != (schemas.AllowedRequests{}) || (provider.NetworkConfig != nil && provider.NetworkConfig.BaseURL != "") {
		return nil, ErrTranscriptionProviderConfig
	}
	return &provider, nil
}

var transcriptionModelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func ValidateTranscriptionModelName(model string) error {
	if !transcriptionModelName.MatchString(model) {
		return fmt.Errorf("model must match ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
	}
	return nil
}

// TranscriptionModelState identifies a registered STT usage model.
type TranscriptionModelState struct {
	Model     string                `json:"model"`
	Provider  schemas.ModelProvider `json:"provider"`
	UsageKind string                `json:"usage_kind"`
}

// EnsureTranscriptionModel creates a registration if missing without changing pricing.
func EnsureTranscriptionModel(ctx context.Context, store ConfigStore, model string, txs ...*gorm.DB) error {
	if err := ValidateTranscriptionModelName(model); err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("config store is required")
	}
	write := func(tx *gorm.DB) error {
		provider, err := transcriptionProvider(ctx, tx)
		if err != nil {
			return err
		}
		row := tables.TableModel{ID: uuid.NewString(), ProviderID: provider.ID, Name: model}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
	}
	if len(txs) > 0 {
		return write(txs[0])
	}
	return store.ExecuteTransaction(ctx, write)
}

func GetTranscriptionModel(ctx context.Context, store ConfigStore, model string) (*TranscriptionModelState, error) {
	if store == nil {
		return nil, fmt.Errorf("config store is required")
	}
	provider, err := transcriptionProvider(ctx, store.DB())
	if err != nil {
		return nil, err
	}
	var row tables.TableModel
	if err := store.DB().WithContext(ctx).Where("name = ? AND provider_id = ?", model, provider.ID).First(&row).Error; err != nil {
		return nil, err
	}
	return &TranscriptionModelState{Model: row.Name, Provider: TranscriptionUsageProvider, UsageKind: "stt"}, nil
}

func ListTranscriptionModels(ctx context.Context, store ConfigStore) ([]TranscriptionModelState, error) {
	if store == nil {
		return nil, fmt.Errorf("config store is required")
	}
	provider, err := transcriptionProvider(ctx, store.DB())
	if err != nil {
		return nil, err
	}
	var rows []tables.TableModel
	if err := store.DB().WithContext(ctx).Where("provider_id = ?", provider.ID).Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	states := make([]TranscriptionModelState, 0, len(rows))
	for _, row := range rows {
		states = append(states, TranscriptionModelState{Model: row.Name, Provider: TranscriptionUsageProvider, UsageKind: "stt"})
	}
	return states, nil
}
