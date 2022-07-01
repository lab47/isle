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
	"github.com/lab47/isle/cli/config"
	"github.com/lab47/isle/pkg/crypto/ssh"
	"github.com/lab47/isle/pkg/ghrelease"
	"github.com/mattn/go-isatty"
	"github.com/morikuni/aec"
	"github.com/spf13/pflag"
	"golang.org/x/sys/unix"
)

var Version = "unknown"

type CLI struct {
}

func (cli *CLI) Run(args []string) {
	fs := pflag.NewFlagSet("isle", pflag.ExitOnError)

	var (
		fVersion  = fs.Bool("version", false, "print out the version")
		fName     = fs.StringP("name", "n", "", "name of vm to connect to")
		fImage    = fs.StringP("image", "i", "ubuntu", "OCI image to load")
		fDir      = fs.StringP("dir", "d", "", "directory to start in")
		fRoot     = fs.Bool("as-root", false, "establish the shell as root")
		fStateDir = fs.String("state-dir", "", "directory that isle stores state in")
		fVerbose  = fs.CountP("verbose", "V", "how verbose to be in output")
		fStart    = fs.Bool("start", false, "explicitly start VM")
		fBgStart  = fs.Bool("bg-start", false, "used to start vm in background")
		fConfig   = fs.Bool("configure", false, "configure isle")
		fStop     = fs.Bool("stop", false, "stop the background VM")
	)

	fs.Parse(args)

	if *fVersion {
		fmt.Printf("yal4rm version: %s\n", Version)
		os.Exit(0)
	}

	if *fBgStart {
		execPath, err := os.Executable()
		if err != nil {
			panic(err)
		}

		r, w, err := os.Pipe()
		if err != nil {
			panic(err)
		}

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

	err = config.CheckConfig(log, configPath)
	if err != nil {
		log.Error("error checking configuration", "error", err.Error())
		os.Exit(1)
	}

	if *fStart {
		cli.startGuest(log, stateDir, configPath, pidPath)
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

	path := filepath.Join(stateDir, "listen-addr")
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
