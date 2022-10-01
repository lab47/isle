package main

import (
	"context"
	"io"
	"os"
	"sync"

	"github.com/containerd/containerd/runtime/v2/logging"

	"github.com/lab47/isle/pkg/clog"
)

func main() {
	logging.Run(log)
}

func log(ctx context.Context, cfg *logging.Config, ready func() error) error {
	dir := os.Args[1]

	dw, err := clog.NewDirectoryWriter(dir, 0, 0)
	if err != nil {
		return err
	}

	w1, err := dw.IOInput(ctx)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(w1, cfg.Stderr)

	}()

	w2, err := dw.IOInput(ctx)
	if err != nil {
		return err
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(w2, cfg.Stdout)
	}()

	err = ready()
	if err != nil {
		return err
	}

	wg.Wait()

	return nil
}
