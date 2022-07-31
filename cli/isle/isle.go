package isle

import (
	"fmt"
	"strings"

	"github.com/hashicorp/go-hclog"
	"github.com/jessevdk/go-flags"
	"github.com/lab47/isle/pkg/values"
	"github.com/morikuni/aec"
)

var banner = strings.ReplaceAll(`
             ___
 __         /\_ \
/\_\    ____\//\ \      __      %s
\/\ \  /',__\ \ \ \   /'__B\    %s
 \ \ \/\__, B\ \_\ \_/\  __/    %s
  \ \_\/\____/ /\____\ \____\
   \/_/\/___/  \/____/\/____/

`, "B", "`")

type CLI struct {
	*values.Universe

	log hclog.Logger
}

var (
	bannerOnScreen bool
	bannerLines    int = strings.Count(banner, "\n")
)

func updateBanner(one string) {
	if bannerOnScreen {
		clearBanner()
	}

	bannerOnScreen = true
	fmt.Printf("+ %s", one)
}

func clearBanner() {
	if !bannerOnScreen {
		return
	}

	fmt.Print(aec.Apply("",
		aec.Column(0),
		aec.EraseLine(aec.EraseModes.All),
	))

	/*
		var parts []aec.ANSI

		for i := 0; i <= bannerLines; i++ {
			parts = append(parts,
				aec.EraseLine(aec.EraseModes.All),
				aec.PreviousLine(1),
			)
		}

		// parts = append(parts, aec.EraseLine(aec.EraseModes.All))

		fmt.Print(aec.Apply("", parts...))
	*/
	bannerOnScreen = false
}

type bannerStatus struct{}

func (bannerStatus) UpdateStatus(status string) {
	updateBanner(status)
}

func (bannerStatus) ClearStatus() {
	clearBanner()
}

func NewCLI(log hclog.Logger) (*CLI, error) {
	u := values.NewUniverse(log)

	u.Provide(log)

	c := &CLI{
		Universe: u,
		log:      log,
	}

	err := c.Seed()
	if err != nil {
		return nil, err
	}

	return c, nil
}

func (c *CLI) Seed() error {
	return nil
}

type ExitError struct {
	Code int
}

func (ee *ExitError) Error() string {
	return fmt.Sprintf("exited with code: %d", ee.Code)
}

func (c *CLI) Run(args []string) error {
	parser := flags.NewNamedParser("isle", flags.Default)
	parser.AddCommand("start",
		"start the background manager",
		"start the background manager process",
		&StartCmd{},
	)

	parser.AddCommand("stop",
		"stop the background manager",
		"stop the background manager process",
		&StopCmd{},
	)

	parser.AddCommand("run",
		"run a command",
		"run a command inside an environment",
		&RunCmd{},
	)

	parser.AddCommand("delete",
		"delete a shell session",
		"delet a shell session and all it's data",
		&DeleteCmd{},
	)

	parser.AddCommand("list",
		"list all shell sessions",
		"list all shell sessions",
		&ListCmd{},
	)

	parser.AddCommand("read-logs",
		"read logs from a clog formatted dir",
		"read logs from a clog formatted dir",
		&LogsCmd{},
	)

	_, err := parser.ParseArgs(args[1:])
	if err != nil {
		if perr, ok := err.(*flags.Error); ok {
			if perr.Type == flags.ErrHelp {
				return nil
			}
		}

		return err
	}

	return nil
}
