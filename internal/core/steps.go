package core

const (
	stepID     = "key made"
	stepLink   = "content named twice"
	stepRows   = "rows moved"
	stepUnlink = "old name dropped"
)

const (
	stepTakeRemote  = "Drive's version taken"
	stepRestoreLink = "restored content named twice"
	stepRestoreRow  = "restored row made"
)

func ShelveSteps() []string { return []string{stepID, stepLink, stepRows, stepUnlink} }

const TakeRemoteStep = stepTakeRemote

func RestoreSteps() []string { return []string{stepRestoreLink, stepRestoreRow} }

func (c *conflicts) killed(step string) error {
	if c.fault == nil {
		return nil
	}
	return c.fault(step)
}
