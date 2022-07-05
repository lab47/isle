package isle

import (
	_ "github.com/lab47/isle/macos"
)

func (c *RunCmd) osSetup() error {
	c.forceSession = true
	// macos.RunInBackground()
	return nil
}
