package main

import (
	"os"

	"github.com/lab47/isle/helper"
)

func main() {
	if os.Args[0] == "init" || os.Args[0] == "/dev/init" {
		helper.InitMain()
	} else {
		helper.Main(os.Args[1:])
	}
}
