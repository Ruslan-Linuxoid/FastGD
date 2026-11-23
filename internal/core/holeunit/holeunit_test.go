package holeunit_test

import (
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core/holeunit"
)

func TestUnitOf64KiB(t *testing.T) { holeunit.Run(t, 64<<10, 256<<10) }
