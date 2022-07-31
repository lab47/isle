package isle

import "github.com/hashicorp/go-hclog"

func ComputeLevel(cnt []bool) hclog.Level {
	switch len(cnt) {
	case 0:
		return hclog.Warn
		// nothing
	case 1:
		return hclog.Info
	case 2:
		return hclog.Debug
	default:
		return hclog.Trace
	}
}
