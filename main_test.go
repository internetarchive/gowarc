package warc

import (
	"testing"

	"go.uber.org/goleak"
)

// Verify leaks in ALL package tests.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
