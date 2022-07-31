package isle

import (
	"context"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/client"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/labels"
	"github.com/lab47/isle/pkg/pbstream"
	"github.com/samber/do"
)

type DeleteCmd struct {
	Name     string `short:"n" long:"name" default:"default" description:"name of instance to delete"`
	Verbose  []bool `short:"V" description:"be more verbose"`
	StateDir string `long:"state-dir" env:"ISLE_STATE_DIR" description:"directory runtime data is in"`
	Connect  string `long:"connect" description:"socket to connect to"`
}

func (c *DeleteCmd) Execute(args []string) error {
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

	req := &guestapi.RemoveShellSessionReq{
		Name: c.Name,
	}

	ctx := context.Background()

	_, err = client.RemoveShellSession(ctx, pbstream.NewRequest(req))
	return err
}
