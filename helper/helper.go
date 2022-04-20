package helper

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"time"

	"github.com/jessevdk/go-flags"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/pkg/clog"
)

type Options struct {
}

type InstallCmd struct {
	Name string `short:"n" description:"name of the app"`
}

func (i *InstallCmd) Execute(args []string) error {
	if len(args) != 1 {
		fmt.Printf("Specify the selector for the app\n")
		return nil
	}

	ctx := context.Background()

	cl := guestapi.NewGuestAPIProtobufClient("http://10.4.0.1:1212", http.DefaultClient)
	_, err := cl.AddApp(ctx, &guestapi.AddAppReq{
		Selector: args[0],
		Name:     args[0],
	})
	if err != nil {
		fmt.Printf("error adding app: %s\n", err)
		return nil
	}

	fmt.Printf("App added: %s\n", args[0])

	return nil
}

var installCmd InstallCmd

type RemoveCmd struct {
	Name string `short:"n" description:"name of the app"`
}

func (i *RemoveCmd) Execute(args []string) error {
	if len(args) != 1 {
		fmt.Printf("Specify the selector for the app\n")
		return nil
	}

	ctx := context.Background()

	cl := guestapi.NewGuestAPIProtobufClient("http://10.4.0.1:1212", http.DefaultClient)
	_, err := cl.DisableApp(ctx, &guestapi.DisableAppReq{
		Id: args[0],
	})
	if err != nil {
		fmt.Printf("error removing app: %s\n", err)
		return nil
	}

	fmt.Printf("App removed: %s\n", args[0])

	return nil
}

var removeCmd RemoveCmd

type LogsCmd struct{}

func (i *LogsCmd) Execute(args []string) error {
	dir, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}

	r, err := clog.NewDirectoryReader(dir)
	if err != nil {
		return err
	}

	for {
		ent, err := r.Next()
		if err != nil {
			return nil
		}

		ts := time.UnixMilli(int64(ent.Timestamp.Time()))

		fmt.Printf("%s: %s\n", ts.Format(time.RFC3339Nano), ent.Data)
	}
}

var logsCmd LogsCmd

func Main(args []string) {
	parser := flags.NewNamedParser("isle", flags.Default)
	parser.AddCommand("install-app",
		"install an application",
		"install an application to your isle",
		&installCmd,
	)

	parser.AddCommand("remove-app",
		"remove an application",
		"remove an application to your isle",
		&removeCmd,
	)

	parser.AddCommand("read-logs",
		"read logs from a clog formatted dir",
		"read logs from a clog formatted dir",
		&logsCmd,
	)

	parser.ParseArgs(args)
}
