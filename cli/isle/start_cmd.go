package isle

import "github.com/hashicorp/go-hclog"

type StartCmd struct {
	Verbose  []bool `short:"V" description:"be more verbose"`
	StateDir string `long:"state-dir" description:"directory to store state in"`
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

	return s.osExecute(log, args)
}
