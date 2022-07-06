package macos

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/containerd/console"
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/cli/config"
	"github.com/pkg/errors"
)

func loadConfig(log hclog.Logger, path string) config.Config {
	f, err := os.Open(path)
	if err != nil {
		log.Error("error opening config path", "error", err)
		os.Exit(1)
	}

	defer f.Close()

	var cfg config.Config

	err = json.NewDecoder(f).Decode(&cfg)
	if err != nil {
		log.Error("error decoding config", "error", err)
		os.Exit(1)
	}

	return cfg
}

func StartVMInForeground(ctx context.Context, log hclog.Logger, dir string, attach bool) error {
	configPath := filepath.Join(dir, "config.json")

	err := config.CheckConfig(log, configPath)
	if err != nil {
		log.Error("error checking config", "error", err)
		os.Exit(1)
	}

	vm, err := NewVM(log, dir, loadConfig(log, configPath))
	if err != nil {
		log.Error("error checking config", "error", err)
		os.Exit(1)
	}

	bgStr := os.Getenv("_VM_BACKGROUND")
	if bgStr != "" {
		wp := os.NewFile(3, "pipe")

		eventCh := make(chan Event, 1)

		vm.SetEventCh(eventCh)

		go func() {
			var startupHandled bool

			for {
				select {
				case <-ctx.Done():
					return
				case ev := <-eventCh:
					if startupHandled {
						continue
					}

					switch ev.Type {
					case VMStarted:
						fmt.Fprintf(wp, "+")
						startupHandled = true
						wp.Close()

					case VMStopped:
						fmt.Fprintf(wp, "!")
						startupHandled = true
						wp.Close()
					}
				}
			}
		}()
	}

	pidPath := filepath.Join(dir, "vm.pid")
	pf, err := os.Create(pidPath)
	if err != nil {
		panic(err)
	}

	fmt.Fprintln(pf, os.Getpid())

	pf.Close()

	defer os.Remove(pidPath)

	if attach {
		vm.SetConsoleIO(os.Stdin, os.Stdout)
		c := console.Current()
		c.SetRaw()
		defer c.Reset()
	} else {
		f, err := os.Create(filepath.Join(dir, "vm.log"))
		if err != nil {
			log.Error("error creating vm log", "error", err)
			return err
		}

		r, w := io.Pipe()

		go func() {
			time.Sleep(5 * time.Second)
			fmt.Fprintf(w, "\ntail -f /var/log/isle-guest.log\n")
		}()

		// Redirect output to vm.log, including the guest log
		vm.SetConsoleIO(r, f)
	}

	log.Info("running vm")
	return vm.Run(ctx)
}

const (
	EnvStateDir = "ISLE_STATE_DIR"
)

func BackgroundExec() {
	log := hclog.New(&hclog.LoggerOptions{
		Name: "isle-host",
	})

	dir := os.Getenv(EnvStateDir)

	ctx := context.Background()

	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	StartVMInForeground(ctx, log, dir, false)
}

func RunInBackground(dir string) error {
	execPath, err := os.Executable()
	if err != nil {
		return errors.Wrapf(err, "unable to calculate executable to start")
	}

	cmd := exec.Command(execPath, "start", "--state-dir="+dir, "--bg-start")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd.Run()
}
