package kcmdline

import (
	"os"
	"strings"

	"github.com/google/shlex"
)

func CommandLine() map[string]string {
	data, err := os.ReadFile("/proc/cmdline")
	if err != nil {
		return nil
	}

	parts, err := shlex.Split(string(data))
	if err != nil {
		return nil
	}

	vars := map[string]string{}

	for _, p := range parts {
		idx := strings.IndexByte(p, '=')
		if idx == -1 {
			vars[p] = ""
		} else {
			vars[p[:idx]] = p[idx+1:]
		}
	}

	return vars
}
