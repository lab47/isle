package helper

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/jessevdk/go-flags"
	"github.com/lab47/isle/guestapi"
	"github.com/lab47/isle/network"
	"github.com/lab47/isle/pkg/clog"
	"google.golang.org/grpc"
)

type Options struct {
}

type InstallCmd struct {
	Name string `short:"n" description:"name of the app"`
}

const metadataAddr = network.MetadataIP + ":1212"

func (i *InstallCmd) Execute(args []string) error {
	if len(args) != 1 {
		fmt.Printf("Specify the selector for the app\n")
		return nil
	}

	ctx := context.Background()

	cc, err := grpc.Dial(metadataAddr, grpc.WithInsecure())
	if err != nil {
		return err
	}

	cl := guestapi.NewGuestAPIClient(cc)

	_, err = cl.AddApp(ctx, &guestapi.AddAppReq{
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

	cc, err := grpc.Dial(metadataAddr, grpc.WithInsecure())
	if err != nil {
		return err
	}

	cl := guestapi.NewGuestAPIClient(cc)

	_, err = cl.DisableApp(ctx, &guestapi.DisableAppReq{
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

type RunCmd struct{}

func (i *RunCmd) Execute(args []string) error {
	ctx := context.Background()

	cc, err := grpc.Dial(metadataAddr, grpc.WithInsecure())
	if err != nil {
		return err
	}

	cl := guestapi.NewGuestAPIClient(cc)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := cl.RunOnMac(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error running command: %s\n", err)
		return nil
	}

	stream.Send(&guestapi.RunInput{
		Command: args,
	})

	go func() {
		buf := make([]byte, 1024)

		for {
			n, _ := os.Stdin.Read(buf)
			if n == 0 {
				stream.Send(&guestapi.RunInput{
					Closed: true,
				})
				return
			}

			err := stream.Send(&guestapi.RunInput{
				Input: buf[:n],
			})
			if err != nil {
				return
			}
		}
	}()

	for {
		out, err := stream.Recv()
		if err != nil {
			os.Exit(1)
		}

		if out.Closed {
			os.Exit(int(out.ExitCode))
		}

		os.Stdout.Write(out.Data)
	}
}

var runCmd RunCmd

type ConsoleCmd struct{}

func (i *ConsoleCmd) Execute(args []string) error {
	ctx := context.Background()

	cc, err := grpc.Dial(metadataAddr, grpc.WithInsecure())
	if err != nil {
		return err
	}

	cl := guestapi.NewGuestAPIClient(cc)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	stream, err := cl.Console(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error running command: %s\n", err)
		return nil
	}

	stream.Send(&guestapi.RunInput{
		Command: args,
	})

	go func() {
		buf := make([]byte, 1024)

		for {
			n, _ := os.Stdin.Read(buf)
			if n == 0 {
				stream.Send(&guestapi.RunInput{
					Closed: true,
				})
				return
			}

			err := stream.Send(&guestapi.RunInput{
				Input: buf[:n],
			})
			if err != nil {
				return
			}
		}
	}()

	for {
		out, err := stream.Recv()
		if err != nil {
			os.Exit(1)
		}

		if out.Closed {
			os.Exit(int(out.ExitCode))
		}

		os.Stdout.Write(out.Data)
	}
}

var consoleCmd ConsoleCmd

type TrimCmd struct {
	Set    int32 `long:"set" description:"set total memory megabytes"`
	Add    int32 `short:"a" long:"adjust" description:"add memory to this isle"`
	Remove int32 `short:"r" long:"remove" description:"remove memory from this isle"`
	Reset  bool  `long:"reset" description:"reset total memory to default"`
}

func (i *TrimCmd) Execute(args []string) error {
	ctx := context.Background()

	cc, err := grpc.Dial(metadataAddr, grpc.WithInsecure())
	if err != nil {
		return err
	}

	cl := guestapi.NewGuestAPIClient(cc)

	var adjust int32

	if i.Add != 0 {
		adjust = i.Add
	} else if i.Remove != 0 {
		adjust = -i.Remove
	}

	_, err = cl.TrimMemory(ctx, &guestapi.TrimMemoryReq{
		Set:    i.Set,
		Adjust: adjust,
		Reset_: i.Reset,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error trimming memory: %s\n", err)
	}

	return nil
}

var trimCmd TrimCmd

func Main(args []string) {
	parser := flags.NewNamedParser("isle", flags.Default)
	parser.AddCommand("read-logs",
		"read logs from a clog formatted dir",
		"read logs from a clog formatted dir",
		&logsCmd,
	)

	parser.AddCommand("run",
		"execute a command on your mac",
		"execute a command on your mac",
		&runCmd,
	)

	parser.AddCommand("console",
		"execute a command on the isle base OS",
		"execute a command on the isle base OS",
		&consoleCmd,
	)

	parser.AddCommand("trim-memory",
		"trim memory from isle",
		"trim memory from isle",
		&trimCmd,
	)

	parser.ParseArgs(args)
}
