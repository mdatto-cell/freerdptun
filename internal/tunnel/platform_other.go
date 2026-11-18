//go:build !linux && !windows

package tunnel

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"time"
)

// Platforms other than Linux/Windows (e.g. iOS) have no built-in TUN device or
// server support: the host application supplies a Device (see RunClientDevice).

type Device interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

var errUnsupported = errors.New("not supported on this platform")

func attachFiles(cmd *exec.Cmd, files []*os.File) error {
	if len(files) != 0 {
		return errUnsupported
	}
	return nil
}
func peerFile(net.Conn) (*os.File, error) { return nil, errUnsupported }
func peekConnectionRequest(context.Context, net.Conn, time.Time) ([]byte, error) {
	return nil, errUnsupported
}
func OpenTUN(Config) (Device, error)             { return nil, errUnsupported }
func configureTUN(context.Context, Config) error { return errUnsupported }
func configureTUNHint(Config) string             { return "unsupported platform" }
func platformAddress(c Config) string            { return c.CIDR }
