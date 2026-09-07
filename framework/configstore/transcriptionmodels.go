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

var transcriptionModelName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func ValidateTranscriptionModelName(model string) error {
	if !transcriptionModelName.MatchString(model) {
		return fmt.Errorf("model must match ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
	}
	return nil
}

// TranscriptionModelState exposes the persisted base price, before scoped pricing overrides.
type TranscriptionModelState struct {
	Model              string                `json:"model"`
	Provider           schemas.ModelProvider `json:"provider"`
	UsageKind          string                `json:"usage_kind"`
	Enabled            bool                  `json:"enabled"`
	PricingConfigured  bool                  `json:"pricing_configured"`
	InputCostPerToken  *float64              `json:"input_cost_per_token"`
	OutputCostPerToken *float64              `json:"output_cost_per_token"`
}

// EnsureTranscriptionModel atomically creates a registration and its explicit
// zero base price. Concurrent/repeated calls never overwrite existing state or
// pricing. A missing price row is repaired, but a null existing price is not.
func EnsureTranscriptionModel(ctx context.Context, store ConfigStore, model string, txs ...*gorm.DB) error {
	if err := ValidateTranscriptionModelName(model); err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("config store is required")
	}
	write := func(tx *gorm.DB) error {
		var provider tables.TableProvider
		if err := tx.Where("name = ?", schemas.Transcription).First(&provider).Error; err != nil {
			return err
		}
		row := tables.TableModel{ID: uuid.NewString(), ProviderID: provider.ID, Name: model, Enabled: true}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		zero := 0.0
		price := tables.TableModelPricing{Model: model, Provider: string(schemas.Transcription), Mode: "audio_transcription", InputCostPerToken: &zero, OutputCostPerToken: &zero}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&price).Error
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
	var row tables.TableModel
	if err := store.DB().WithContext(ctx).Where("name = ? AND provider_id IN (?)", model, store.DB().Model(&tables.TableProvider{}).Select("id").Where("name = ?", schemas.Transcription)).First(&row).Error; err != nil {
		return nil, err
	}
	state := &TranscriptionModelState{Model: row.Name, Provider: schemas.Transcription, UsageKind: "stt", Enabled: row.Enabled}
	price, err := GetTranscriptionModelPrice(ctx, store, model)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return state, nil
	}
	if err != nil {
		return nil, err
	}
	state.InputCostPerToken, state.OutputCostPerToken = price.InputCostPerToken, price.OutputCostPerToken
	state.PricingConfigured = price.InputCostPerToken != nil && price.OutputCostPerToken != nil && *price.InputCostPerToken >= 0 && *price.OutputCostPerToken >= 0
	return state, nil
}

func GetTranscriptionModelPrice(ctx context.Context, store ConfigStore, model string) (*tables.TableModelPricing, error) {
	if store == nil {
		return nil, fmt.Errorf("config store is required")
	}
	var price tables.TableModelPricing
	err := store.DB().WithContext(ctx).Where("model = ? AND provider = ? AND mode = ?", model, schemas.Transcription, "audio_transcription").First(&price).Error
	return &price, err
}

func SetTranscriptionModelEnabled(ctx context.Context, store ConfigStore, model string, enabled bool, txs ...*gorm.DB) error {
	if store == nil {
		return fmt.Errorf("config store is required")
	}
	db := store.DB()
	if len(txs) > 0 {
		db = txs[0]
	}
	result := db.WithContext(ctx).Model(&tables.TableModel{}).Where("name = ? AND provider_id IN (?)", model, store.DB().Model(&tables.TableProvider{}).Select("id").Where("name = ?", schemas.Transcription)).Update("enabled", enabled)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func ListTranscriptionModels(ctx context.Context, store ConfigStore) ([]TranscriptionModelState, error) {
	if store == nil {
		return nil, fmt.Errorf("config store is required")
	}
	var rows []tables.TableModel
	if err := store.DB().WithContext(ctx).Where("provider_id IN (?)", store.DB().Model(&tables.TableProvider{}).Select("id").Where("name = ?", schemas.Transcription)).Order("name").Find(&rows).Error; err != nil {
		return nil, err
	}
	states := make([]TranscriptionModelState, 0, len(rows))
	for _, row := range rows {
		state, err := GetTranscriptionModel(ctx, store, row.Name)
		if err != nil {
			return nil, err
		}
		states = append(states, *state)
	}
	return states, nil
}
