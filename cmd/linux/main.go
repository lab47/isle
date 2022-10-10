package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/console"
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

var Version = "unknown"

const defaultImage = "ghcr.io/lab47/ubuntu:latest"

var (
	fVersion  = pflag.Bool("version", false, "print out the version")
	fName     = pflag.StringP("name", "n", "isle", "name of vm to connect to")
	fImage    = pflag.StringP("image", "i", defaultImage, "OCI image to load")
	fDir      = pflag.StringP("dir", "d", "", "directory to start in")
	fRoot     = pflag.Bool("as-root", false, "establish the shell as root")
	fStateDir = pflag.String("state-dir", "", "directory that isle stores state in")
	fVerbose  = pflag.CountP("verbose", "V", "how verbose to be in output")
	fStart    = pflag.Bool("start", false, "explicitly start VM")
	fBgStart  = pflag.Bool("bg-start", false, "used to start vm in background")
	fConfig   = pflag.Bool("configure", false, "configure isle")
	fStop     = pflag.Bool("stop", false, "stop the background VM")
)

func main() {
	pflag.Parse()

	if *fVersion {
		fmt.Printf("isle version: %s\n", Version)
		os.Exit(0)
	}

	if *fBgStart {
		execPath, err := os.Executable()
		if err != nil {
			panic(err)
		}

		r, w, err := os.Pipe()
		cmd := exec.Command(execPath, "--state-dir="+*fStateDir, "--start")
		cmd.Env = os.Environ()
		cmd.Env = append(cmd.Env, "_VM_BACKGROUND=3")
		cmd.ExtraFiles = append(cmd.ExtraFiles, w)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		cmd.Start()

		data := make([]byte, 1)
		r.Read(data)

		return
	}

	level := hclog.Warn

	switch *fVerbose {
	case 1:
		level = hclog.Info
	case 2:
		level = hclog.Debug
	case 3:
		level = hclog.Trace
	}

	log := hclog.New(&hclog.LoggerOptions{
		Name:  "linux",
		Level: level,
	})

	var (
		stateDir string
		err      error
	)

	if *fStateDir != "" {
		stateDir, err = filepath.Abs(*fStateDir)
		if err != nil {
			log.Error("unable to compute state dir", "error", err)
			os.Exit(1)
		}
	}

	if stateDir == "" {
		dir := os.Getenv("ISLE_STATE_DIR")
		if stateDir != "" {
			stateDir, err = filepath.Abs(dir)
			if err != nil {
				log.Error("unable to compute state dir", "error", err)
				os.Exit(1)
			}
		}
	}

	if stateDir == "" {
		dir, err := os.UserConfigDir()
		if err != nil {
			panic(err)
		}

		stateDir = filepath.Join(dir, "isle")

		// Migrate for older releases
		oldStateDir := filepath.Join(dir, "yalr4m")
		if _, err := os.Stat(oldStateDir); err == nil {
			os.Rename(oldStateDir, stateDir)
		}
	}

	log.Debug("calculate state dir", "dir", stateDir)

	configPath := filepath.Join(stateDir, "config.json")

	if *fConfig {
		cmd := exec.Command("sh", "-c", fmt.Sprintf("${EDITOR:-vi} %s", configPath))
		cmd.Stdout = os.Stdout
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr

		cmd.Run()
		return
	}

	pidPath := filepath.Join(stateDir, "vm.pid")

	if *fStop {
		data, err := ioutil.ReadFile(pidPath)
		if err != nil {
			log.Error("error reading pid file", "error", err)
			os.Exit(1)
		}

		var pid int
		fmt.Sscanf(string(data), "%d", &pid)

		unix.Kill(pid, unix.SIGTERM)
		return
	}

	err = setupStateDir(log, stateDir)
	if err != nil {
		log.Error("error setting up state", "error", err)
		os.Exit(1)
	}

	err = vm.CheckConfig(log, configPath)
	if err != nil {
		log.Error("error checking configuration", "error", err.Error())
		os.Exit(1)
	}

	if *fStart {
		startVM(log, stateDir, configPath, pidPath)
		return
	}

	var autoStart bool

	data, err := ioutil.ReadFile(pidPath)
	if err != nil {
		autoStart = true
	} else {
		var pid int

		fmt.Sscanf(string(data), "%d", &pid)

		err = unix.Kill(pid, 0)
		if err != nil {
			autoStart = true
		}
	}

	isTerm := isatty.IsTerminal(os.Stderr.Fd())

	if autoStart {
		if isTerm {
			fmt.Printf("🚨 Starting VM...%s",
				aec.EmptyBuilder.Column(0).ANSI.String(),
			)
		}

		log.Info("autostarting VM in background...")
		execPath, err := os.Executable()
		if err != nil {
			log.Error("unable to calculate executable to start", "error", err)
		}

		cmd := exec.Command(execPath, "--state-dir="+stateDir, "--bg-start")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr

		cmd.Start()
		time.Sleep(3 * time.Second)
	}

	path := filepath.Join(stateDir, "control.sock")
	_, err = os.Stat(path)
	if err != nil {
		log.Error("error validating state, missing listen-addr", "error", err)
		os.Exit(1)
	}

	named, err := reference.ParseNormalizedNamed(*fImage)
	if err != nil {
		log.Error("error parsing image name", "error", err)
		os.Exit(1)
	}

	named = reference.TagNameOnly(named)

	if *fName == "" {
		fam := strings.NewReplacer("/", "_", ":", "_").Replace(reference.FamiliarName(named))
		*fName = fam
	}

	c := &isle.CLI{
		L:      log,
		Path:   path,
		Image:  named.String(),
		Name:   *fName,
		Dir:    *fDir,
		AsRoot: *fRoot,
		IsTerm: isTerm,
	}

	err = c.Shell(strings.Join(pflag.Args(), " "), os.Stdin, os.Stdout)
	if err != nil {
		if ee, ok := err.(*ssh.ExitError); ok {
			os.Exit(ee.ExitStatus())
		}
		c.L.Error("error starting shell", "error", err)
	}
}

var assetSuffix = "-" + runtime.GOARCH + ".tar.gz"

func setupStateDir(log hclog.Logger, stateDir string) error {
	var neededFiles = map[string]struct{}{
		"initrd":  {},
		"vmlinux": {},
		"os.fs":   {},
		"version": {},
	}

	entries, err := os.ReadDir(stateDir)
	if err == nil {
		for _, ent := range entries {
			delete(neededFiles, ent.Name())
		}
	}

	if len(neededFiles) == 0 {
		if Version == "unknown" {
			return nil
		}

		data, err := ioutil.ReadFile(filepath.Join(stateDir, "version"))
		curVersion := strings.TrimSpace(string(data))
		if err == nil {
			if Version == curVersion {
				return nil
			}
		}

		log.Warn("current version of state dir does not match CLI, switching version",
			"current", curVersion, "expected", Version)
	}

	os.MkdirAll(stateDir, 0755)

	if cacheDir := os.Getenv("ISLE_CACHE_DIR"); cacheDir != "" {
		name := "os-" + Version + assetSuffix

		path := filepath.Join(cacheDir, name)

		f, err := os.Open(path)
		if err == nil {
			log.Info("using cached os bundle", "path", path)
			defer f.Close()

			ioutil.WriteFile(filepath.Join(stateDir, "version"), []byte(Version), 0644)

			fi, _ := f.Stat()

			size := fi.Size()

			return ghrelease.Unpack(f, size, name, stateDir)
		}
	}

	var rel *ghrelease.Release

	if Version == "unknown" || Version == "" {
		rel, err = ghrelease.Latest("lab47", "isle")
		if err != nil {
			return err
		}
	} else {
		rel, err = ghrelease.Find("lab47", "isle", Version)
		if err != nil {
			log.Warn("Unable to find github release for configured yal4rm version", "version", Version)

			rel, err = ghrelease.Latest("lab47", "isle")
			if err != nil {
				return err
			}

			log.Warn("Using latest release of isle instead", "version", rel.TagName)
		}
	}

	for _, asset := range rel.Assets {
		if strings.HasPrefix(asset.Name, "os-") &&
			strings.HasSuffix(asset.Name, assetSuffix) &&
			asset.ContentType == "application/x-gtar" {
			ioutil.WriteFile(filepath.Join(stateDir, "version"), []byte(Version), 0644)
			return ghrelease.UnpackAsset(&asset, stateDir)
		}
	}

	return fmt.Errorf("release missing os asset: %s", rel.TagName)
}

func startVM(log hclog.Logger, stateDir, configPath, pidPath string) {
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
			fmt.Fprintf(w, "\ntail -f /var/log/isle-guest.log\n")
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
