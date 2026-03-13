package channels

import (
	"errors"
	"testing"
)

func TestValidateChannelName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid simple", "alerts", false},
		{"valid with hyphens", "my-channel", false},
		{"valid with underscores", "my_channel", false},
		{"valid mixed", "my-channel_123", false},
		{"valid single char", "a", false},
		{"valid numbers", "123", false},
		{"empty", "", true},
		{"spaces", "my channel", true},
		{"special chars", "ch@nnel!", true},
		{"starts with hyphen", "-channel", true},
		{"starts with underscore", "_channel", true},
		{"unicode", "ch\u00e4nnel", true},
		{"dot", "my.channel", true},
		{"too long (65 chars)", "aaaaaaaaaabbbbbbbbbbccccccccccddddddddddeeeeeeeeeeffffffffffggggg", true},
		{"exactly 64 chars", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateChannelName(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				if !errors.Is(err, ErrInvalidChannelName) {
					t.Errorf("expected ErrInvalidChannelName, got %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestNormalizeChannelName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"alerts", "alerts"},
		{"ALERTS", "alerts"},
		{"MyChannel", "mychannel"},
		{"Mixed-Case_123", "mixed-case_123"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeChannelName(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeChannelName(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
