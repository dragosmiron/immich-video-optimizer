package ui

import (
	"testing"
)

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"Valid HTTP", "http://example.com", false},
		{"Valid HTTPS", "https://example.com/api", false},
		{"Empty String", "", true},
		{"Whitespace", "   ", true},
		{"Invalid Prefix", "ftp://example.com", true},
		{"No Prefix", "example.com", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateAPIKey(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"Valid Key", "some-api-key", false},
		{"Empty String", "", true},
		{"Whitespace", "   ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAPIKey(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAPIKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateThreshold(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"Valid Threshold", "500", false},
		{"Empty String", "", true},
		{"Whitespace", "   ", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateThreshold(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateThreshold() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
