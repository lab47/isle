package guest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"text/tabwriter"

	"github.com/alexflint/go-arg"
	"github.com/anmitsu/go-shlex"
	"github.com/lab47/isle/pkg/crypto/ssh/terminal"
	"github.com/lab47/isle/pkg/ssh"
)

type consoleCommand interface {
	Run(ctx context.Context, g *Guest, w io.Writer) int
}

func (g *Guest) runConsole(ctx context.Context, s ssh.Session) {
	defer s.Close()

	if len(s.Command()) > 0 {
		g.runCommand(ctx, s, s.Command())
		return
	}

	const banner = `
 _     _
(_)___| | ___  isle
| / __| |/ _ \ admin
| \__ \ |  __/ console
|_|___/_|\___| activated!

Type help for, well, help.

`

	t := terminal.NewTerminal(s, "> ")
	fmt.Fprint(t, banner)

	for {
		line, err := t.ReadLine()
		if err != nil {
			return
		}

		parts, err := shlex.Split(line, true)
		if err != nil {
			fmt.Fprintf(s, "Syntax error: %s\n", err)
			return
		}

		var ret int

		var cmd consoleCommand

		switch parts[0] {
		case "help":
			tw := tabwriter.NewWriter(t, 1, 4, 1, ' ', 0)
			fmt.Fprintf(tw, "delete <name>\t Delete an isle by name\n")
			fmt.Fprintf(tw, "list\t List all isles\n")
			fmt.Fprintf(tw, "exit\t exit the admin console\n")
			tw.Flush()
		case "delete":
			cmd = &deleteCommand{}
		case "list":
			cmd = &listCommand{}
		case "exit":
			s.Exit(0)
			return
		}

		if cmd != nil {
			parser, err := arg.NewParser(arg.Config{
				Program:   parts[0],
				IgnoreEnv: true,
			}, cmd)
			if err != nil {
				s.Exit(10)
				return
			}

			err = parser.Parse(parts[1:])
			if err != nil {
				if err == arg.ErrHelp {
					parser.WriteHelp(s)
				}
			}
			ret = cmd.Run(ctx, g, t)
		}

		t.Write([]byte("\n"))

		if ret != 0 {
			t.SetPrompt("!> ")
		} else {
			t.SetPrompt("> ")
		}
	}
}

func (g *Guest) runCommand(ctx context.Context, s ssh.Session, parts []string) {
	switch parts[0] {
	case "delete":
		cmd := &deleteCommand{}
		ret := cmd.Run(ctx, g, s)
		s.Exit(ret)
	case "list":
		cmd := &listCommand{}
		ret := cmd.Run(ctx, g, s)
		s.Exit(ret)
	}
}

type deleteCommand struct {
	Id string `arg:"positional"`
}

func (d *deleteCommand) Run(ctx context.Context, g *Guest, s io.Writer) int {
	g.mu.Lock()
	defer g.mu.Unlock()

	name := d.Id

	rc, ok := g.running[name]
	if ok {
		rc.cancel()
		delete(g.running, name)
	}

	cont, err := g.C.Containers(ctx, "labels.name=="+name)
	if err != nil {
		fmt.Fprintf(s, "Error reading containers: %s\n", err)
		return 1
	}

	if len(cont) >= 1 {
		g.CleanupContainer(ctx, cont[0].ID())
	}

	bundlePath := filepath.Join(basePath, name)

	err = os.RemoveAll(bundlePath)
	if err != nil {
		fmt.Fprintf(s, "error: %s\n", err)
		return 1
	}

	fmt.Fprintf(s, "isle deleted: %s\n", d.Id)
	return 0
}

type listCommand struct{}

func (l *listCommand) Run(ctx context.Context, g *Guest, s io.Writer) int {
	entries, err := os.ReadDir(basePath)
	if err != nil {
		fmt.Fprintf(s, "error listing containers: %s\n", err)
		return 1
	}

	tw := tabwriter.NewWriter(s, 1, 4, 1, ' ', 0)
	defer tw.Flush()

	fmt.Fprintf(tw, "NAME\tIMAGE\tSIZE\n")

	for _, ent := range entries {

		var info isleInfo

		bundlePath := filepath.Join(basePath, ent.Name())
		f, err := os.Open(filepath.Join(bundlePath, "info.json"))
		if err == nil {
			json.NewDecoder(f).Decode(&info)
			f.Close()
		}

		var size string

		out, err := exec.Command("du", "-sh", bundlePath).CombinedOutput()
		if err == nil {
			idx := bytes.IndexByte(out, '\t')
			if idx != -1 {
				size = string(out[:idx])
			}
		}

		fmt.Fprintf(tw, "%s\t%s\t%s\n", ent.Name(), info.Image, size)
	}

	return 0
}
