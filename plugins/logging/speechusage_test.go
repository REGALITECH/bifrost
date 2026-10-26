package logging

import (
	"testing"

	"github.com/maximhq/bifrost/core/schemas"
	"github.com/maximhq/bifrost/framework/logstore"
)

// TestApplyNonStreamingOutputToEntrySpeechInputCharsFallback verifies that a
// speech response carrying only InputChars (e.g. Fish Audio, which bills per
// UTF-8 byte and reports no tokens) is logged with those units as prompt tokens
// instead of 0, while explicit token counts still win.
func TestApplyNonStreamingOutputToEntrySpeechInputCharsFallback(t *testing.T) {
	plugin := &LoggerPlugin{}

	tests := []struct {
		name       string
		usage      *schemas.SpeechUsage
		wantPrompt int
		wantTotal  int
	}{
		{
			name:       "input chars only",
			usage:      &schemas.SpeechUsage{InputChars: 18},
			wantPrompt: 18,
			wantTotal:  18,
		},
		{
			name:       "input tokens take precedence",
			usage:      &schemas.SpeechUsage{InputTokens: 5, InputChars: 18, OutputTokens: 7},
			wantPrompt: 5,
			wantTotal:  12,
		},
		{
			name:       "explicit total is kept",
			usage:      &schemas.SpeechUsage{InputChars: 18, TotalTokens: 40},
			wantPrompt: 18,
			wantTotal:  40,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := &logstore.Log{}
			plugin.applyNonStreamingOutputToEntry(entry, &schemas.BifrostResponse{
				SpeechResponse: &schemas.BifrostSpeechResponse{Usage: tt.usage},
			}, false, false)
			if entry.TokenUsageParsed == nil {
				t.Fatal("expected token usage to be parsed")
			}
			if entry.PromptTokens != tt.wantPrompt || entry.TokenUsageParsed.PromptTokens != tt.wantPrompt {
				t.Errorf("prompt tokens = %d (parsed %d), want %d", entry.PromptTokens, entry.TokenUsageParsed.PromptTokens, tt.wantPrompt)
			}
			if entry.TotalTokens != tt.wantTotal || entry.TokenUsageParsed.TotalTokens != tt.wantTotal {
				t.Errorf("total tokens = %d (parsed %d), want %d", entry.TotalTokens, entry.TokenUsageParsed.TotalTokens, tt.wantTotal)
			}
		})
	}
}
