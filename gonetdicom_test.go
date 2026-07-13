package gonetdicom_test

import (
	"testing"

	"github.com/godicom-dev/godicom"
	_ "github.com/godicom-dev/gonetdicom"
)

func TestGodicomDependencyCompiles(t *testing.T) {
	// Smoke: module resolves godicom and builds.
	_ = godicom.MustTag(0x00100010)
}
