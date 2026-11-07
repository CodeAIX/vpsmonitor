package tailnet

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func bindSocket(_, _ string, conn syscall.RawConn) error {
	var bindErr error
	if err := conn.Control(func(fd uintptr) {
		bindErr = unix.BindToDevice(int(fd), "tailscale0")
	}); err != nil {
		return err
	}
	return bindErr
}
