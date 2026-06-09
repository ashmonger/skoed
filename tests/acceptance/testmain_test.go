package acceptance

import (
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	os.Setenv("SKOED_TEST_MODE", "1")
	os.Exit(m.Run())
}
