package isle

import (
	"github.com/lab47/isle/macos"
)

func (c *RunCmd) osSetup() error {
	c.forceSession = true
	return nil
}

func (c *RunCmd) osStartBackground() error {
	return macos.RunInBackground(c.stateDir)
}
