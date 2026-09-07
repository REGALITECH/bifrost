package tables

import "time"

// TableTranscriptionModel registers usage-only STT identities. Prices live in
// governance_model_pricing; deleting an ETLA service never deletes this row.
type TableTranscriptionModel struct {
	Model     string    `gorm:"primaryKey;type:varchar(128)" json:"model"`
	Enabled   bool      `gorm:"not null" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (TableTranscriptionModel) TableName() string { return "governance_transcription_models" }
