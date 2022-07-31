package isle

import (
	"context"
	"os"
	"os/signal"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/macos"
	"golang.org/x/sys/unix"
)

func (c *StartCmd) osExecute(log hclog.Logger, args []string) error {
	ctx := context.Background()

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt, unix.SIGQUIT, unix.SIGTERM)
	defer cancel()

	return macos.StartVMInForeground(ctx, log, c.StateDir, c.Attach)
}
