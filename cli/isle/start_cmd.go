package isle

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/hashicorp/go-hclog"
)

type StartCmd struct {
	Verbose  []bool `short:"V" description:"be more verbose"`
	StateDir string `long:"state-dir" description:"directory to store state in"`
	Attach   bool   `long:"attach-output" description:"should the VM console be available"`
	BGStart  bool   `long:"bg-start" description:"used internally" hidden:"true"`
}

func (s *StartCmd) Execute(args []string) error {
	level := hclog.Info

	switch len(s.Verbose) {
	case 0:
		// nothing
	case 1:
		level = hclog.Debug
	default:
		level = hclog.Trace
	}

	log := hclog.New(&hclog.LoggerOptions{
		Name:  "isle-host",
		Level: level,
	})

	if s.BGStart {
		log.Info("starting VM in background via fork/exec")
		return s.bgStart(log)
	}

	return s.osExecute(log, args)
}

func (c *StartCmd) bgStart(log hclog.Logger) error {
	execPath, err := os.Executable()
	if err != nil {
		panic(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	cmd := exec.Command(execPath, "start", "--state-dir="+c.StateDir)
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "_VM_BACKGROUND=3")
	cmd.ExtraFiles = append(cmd.ExtraFiles, w)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Start()

	log.Info("waiting on background started VM before exitting")

	data := make([]byte, 1)
	r.Read(data)

	if data[0] != '+' {
		return fmt.Errorf("error starting background VM")
	}

	return nil
}
