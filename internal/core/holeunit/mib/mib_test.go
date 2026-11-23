package mib_test

import (
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core/holeunit"
)

func TestUnitOfAMebibyte(t *testing.T) { holeunit.Run(t, 1<<20, 1<<20) }
