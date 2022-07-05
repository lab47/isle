package main

import (
	"fmt"
	"os"

	"github.com/hashicorp/go-hclog"
	"github.com/lab47/isle/cli/isle"
)

func main() {
	log := hclog.New(&hclog.LoggerOptions{
		Name:  "isle",
		Level: hclog.Trace,
	})

	c, err := isle.NewCLI(log)
	if err != nil {
		log.Error("error initializing cli", "error", err)
		os.Exit(1)
	}

	err = c.Run(os.Args)
	if err != nil {
		if ee, ok := err.(*isle.ExitError); ok {
			os.Exit(ee.Code)
		}

		fmt.Printf("An error occured: %s\n", err.Error())
		os.Exit(1)
	}
}
