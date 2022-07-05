package macos

import (
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/containerd/console"
	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/cli/config"
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

	if attach {
		vm.SetConsoleIO(os.Stdin, os.Stdout)
		c := console.Current()
		c.SetRaw()
		defer c.Reset()
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

func RunInBackground(dir string) {
	os.Setenv(EnvStateDir, dir)
}
