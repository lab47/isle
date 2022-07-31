package isle

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/lab47/isle/pkg/clog"
)

type LogsCmd struct{}

func (i *LogsCmd) Execute(args []string) error {
	dir, err := filepath.Abs(args[0])
	if err != nil {
		return err
	}

	r, err := clog.NewDirectoryReader(dir)
	if err != nil {
		return err
	}

	for {
		ent, err := r.Next()
		if err != nil {
			return nil
		}

		ts := time.UnixMilli(int64(ent.Timestamp.Time()))

		fmt.Printf("%s: %s\n", ts.Format(time.RFC3339Nano), ent.Data)
	}
}
