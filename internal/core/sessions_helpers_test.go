package core_test

import (
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const realSessionFrom = 100 << 20

const sessionFrom = 1 << 20

func smallSessions(env *testenv.Env) {
	tune := env.Tune
	env.Tune = func(o *core.Options) {
		if tune != nil {
			tune(o)
		}
		o.ResumableFrom = sessionFrom
	}
}
