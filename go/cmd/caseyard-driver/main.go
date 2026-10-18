package main

import (
	"os"

	"github.com/totto2727-org/e2e/go/internal/driver"
)

func main() { os.Exit(driver.Run(os.Args[1:], os.Stdout)) }
