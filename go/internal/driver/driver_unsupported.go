//go:build !linux

package driver

import "fmt"

func dispatch(_ string, _ request) reply {
	return failure(fmt.Errorf("caseyard-driver requires Linux"), nil)
}
