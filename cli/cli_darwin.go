package cli

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/docker/distribution/reference"
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle"
	"github.com/lab47/isle/pkg/crypto/ssh"
	"github.com/lab47/isle/pkg/ghrelease"
	"github.com/lab47/isle/vm"
	"github.com/mattn/go-isatty"
	"github.com/morikuni/aec"
	"github.com/spf13/pflag"
	"golang.org/x/sys/unix"
)

func (cli *CLI) startGuest(log hclog.Logger, stateDir, configPath, pidPath string) {
	bgStr := os.Getenv("_VM_BACKGROUND")
	background := bgStr != ""

	var (
		vmOut io.Writer
		vmIn  io.Reader
	)

	if background {
		wp := os.NewFile(3, "pipe")
		fmt.Fprintf(wp, "!")
		wp.Close()

		f, err := os.Create(filepath.Join(stateDir, "vm.log"))
		if err != nil {
			log.Error("error creating vm log", "error", err)
			os.Exit(1)
		}

		r, w := io.Pipe()

		go func() {
			time.Sleep(5 * time.Second)
			fmt.Fprintf(w, "\ntail -f /var/log/guest.log\n")
		}()

		log = hclog.New(&hclog.LoggerOptions{
			Name:   "host",
			Output: f,
			Level:  hclog.Info,
		})

		vmOut = f
		vmIn = r
	} else {
		vmOut = os.Stdout
		vmIn = os.Stdin
	}

	f, err := os.Open(configPath)
	if err != nil {
		log.Error("error opening config path", "error", err)
		os.Exit(1)
	}

	var cfg vm.Config

	err = json.NewDecoder(f).Decode(&cfg)
	if err != nil {
		log.Error("error decoding config", "error", err)
		os.Exit(1)
	}

	f.Close()

	f, err = os.Create(pidPath)
	if err != nil {
		log.Error("error creating pid file", "error", err)
		os.Exit(1)
	}

	fmt.Fprintf(f, "%d\n", os.Getpid())

	f.Close()

	defer os.Remove(pidPath)

	v := vm.VM{
		L:        log,
		StateDir: stateDir,
		Config:   cfg,
	}

	ctx := context.Background()

	sig := make(chan os.Signal, 1)

	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	errCh := make(chan error, 1)
	stateCh := make(chan vm.State, 1)

	go func() {
		defer os.Remove(pidPath)
		errCh <- v.Run(ctx, stateCh, sig)
	}()

	c := console.Current()
	defer c.Reset()

	for {
		select {
		case err := <-errCh:
			if err != nil {
				log.Error("error executing VM", "error", err)
			}
			return
		case state := <-stateCh:
			if state.Running {
				log.Info("vm detected as running, forwarding IO")
				c.SetRaw()
				go io.Copy(vmOut, state.Info.Stdout)
				go io.Copy(state.Info.Stdin, vmIn)
			} else {
				c.Reset()
				log.Warn("vm detected as no longer running")
			}
		}
	}

}
