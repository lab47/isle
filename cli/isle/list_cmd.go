package isle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/client"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/samber/do"
)

type ListCmd struct {
	Verbose  []bool `short:"V" description:"be more verbose"`
	StateDir string `long:"state-dir" env:"ISLE_STATE_DIR" description:"directory runtime data is in"`
	Connect  string `long:"connect" description:"socket to connect to"`
}

func (c *ListCmd) Execute(args []string) error {
	var r RunCmd

	r.osSetup()

	log := hclog.New(&hclog.LoggerOptions{
		Name:  "isle",
		Level: ComputeLevel(c.Verbose),
	})

	dir, err := ComputeStateDir(c.StateDir)
	if err != nil {
		return err
	}

	inj := do.New()
	do.ProvideValue(inj, log)
	do.Provide(inj, client.NewConnector)

	connector, err := do.Invoke[*client.Connector](inj)
	if err != nil {
		return err
	}

	var conn *client.Connection

	if r.forceSession {
		conn, err = r.OpenViaSession(log, filepath.Join(dir, "session.sock"), connector)
		if err != nil {
			return err
		}
	} else if c.Connect != "" {
		conn, err = connector.Connect("unix", c.Connect)
		if err != nil {
			return err
		}
	} else {
		conn, err = connector.Connect("unix", filepath.Join(dir, "isle.sock"))
		if err != nil {
			return err
		}
	}

	client := guestapi.PBSNewShellAPIClient(conn.Opener(labels.New("component", "shell")))

	ctx := context.Background()

	log.Debug("sending list-shell-sessions RPC...")

	resp, err := client.ListShellSessions(ctx, pbstream.NewRequest(&guestapi.Empty{}))
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(os.Stdout, 3, 2, 1, ' ', 0)
	defer tw.Flush()

	fmt.Fprint(tw, "NAME\tID\tIMAGE\tSTATUS\n")

	for _, sess := range resp.Value.Sessions {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n",
			sess.Session.Name,
			sess.Id.Short()[0:7],
			sess.Session.Image,
			sess.ProvisionStatus.Status.String(),
		)
	}

	return nil
}
