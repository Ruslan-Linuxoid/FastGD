package odd_test

import (
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core/holeunit"
)

func TestUnitThatDoesNotDivide256KiB(t *testing.T) { holeunit.Run(t, 384<<10, 384<<10) }
