//go:build !linux

package tailnet

import (
	"errors"
	"syscall"
)

func bindSocket(_, _ string, _ syscall.RawConn) error {
	return errors.New("the Tailnet Agent requires Linux host networking")
}
