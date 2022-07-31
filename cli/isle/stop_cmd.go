package isle

import (
	"errors"
	"fmt"
	"io/fs"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
	"golang.org/x/sys/unix"
)

type StopCmd struct {
	Verbose  []bool `short:"V" description:"be more verbose"`
	StateDir string `long:"state-dir" description:"directory to store state in"`
	Attach   bool   `long:"attach-output" description:"should the VM console be available"`
	BGStart  bool   `long:"bg-start" description:"used internally" hidden:"true"`
}

func (s *StopCmd) Execute(args []string) error {
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
		Name:  "isle",
		Level: level,
	})

	stateDir, err := ComputeStateDir(s.StateDir)
	if err != nil {
		return err
	}

	pidPath := filepath.Join(stateDir, "vm.pid")

	data, err := ioutil.ReadFile(pidPath)
	if err != nil {
		// ignore the pidpath not being there, since we assume that means
		// it's not running.
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		log.Error("error reading pid file", "error", err)
		os.Exit(1)
	}

	var pid int
	fmt.Sscanf(string(data), "%d", &pid)

	log.Debug("detected pid of backend", "pid", pid)

	return unix.Kill(pid, unix.SIGTERM)
}
