package warc

import (
	"testing"
)

// Tests for the Header methods and NewHeader
func TestHeaderMethods(t *testing.T) {
	rotatorSettings := NewRotatorSettings()

	rotatorSettings.WarcinfoContent.Set("test-header", "test-value")
	if rotatorSettings.WarcinfoContent["test-header"] != "test-value" {
		t.Error("Failed to set warcinfo header")
	}

	if rotatorSettings.WarcinfoContent.Get("test-header") != "test-value" {
		t.Error("Failed to get warcinfo header")
	}

	rotatorSettings.WarcinfoContent.Del("test-header")
	if rotatorSettings.WarcinfoContent["test-header"] != "" {
		t.Error("Failed to delete warcinfo header")
	}
}
