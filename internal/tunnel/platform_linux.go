//go:build linux

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

type Device interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
}

func attachFiles(cmd *exec.Cmd, files []*os.File) error {
	cmd.ExtraFiles = files
	return nil
}
func peerFile(conn net.Conn) (*os.File, error) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, errors.New("not TCP")
	}
	return tcp.File()
}
func OpenTUN(c Config) (Device, error) {
	// Refuse preexisting interfaces: this is an exclusive point-to-point test.
	if _, e := os.Stat("/sys/class/net/" + c.TUN); e == nil {
		return nil, errors.New("TUN name exists; choose a new tun name or remove the stale interface")
	}
	f, e := os.OpenFile("/dev/net/tun", os.O_RDWR, 0)
	if e != nil {
		return nil, e
	}
	var req [40]byte
	copy(req[:16], c.TUN)
	*(*uint16)(unsafe.Pointer(&req[16])) = 0x0001 | 0x1000 // IFF_TUN | IFF_NO_PI
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(0x400454ca), uintptr(unsafe.Pointer(&req[0])))
	if errno != 0 {
		f.Close()
		return nil, errno
	}
	return f, nil
}
func configureTUN(ctx context.Context, c Config) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	ip, e := exec.LookPath("ip")
	if e != nil {
		return fmt.Errorf("install iproute2 for automatic TUN setup: %w", e)
	}
	for _, args := range [][]string{
		{"address", "replace", c.CIDR, "dev", c.TUN},
		{"link", "set", "dev", c.TUN, "mtu", itoa(c.MTU), "up"},
	} {
		if out, e := exec.CommandContext(ctx, ip, args...).CombinedOutput(); e != nil {
			return fmt.Errorf("automatic TUN setup: ip %s: %w: %s", strings.Join(args, " "), e, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// Inspect without consuming any bytes: the native FreeRDP process must receive
// the original TCP stream, including its complete X.224 request. A short,
// bounded poll avoids the level-ready spin caused by peeking a partial packet.
func peekConnectionRequest(ctx context.Context, conn net.Conn, deadline time.Time) ([]byte, error) {
	tcp, ok := conn.(*net.TCPConn)
	if !ok {
		return nil, errors.New("RDP listener requires TCP")
	}
	raw, e := tcp.SyscallConn()
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	buf := make([]byte, maxInitialRDP)
	want := 4
	for {
		if e := ctx.Err(); e != nil {
			return nil, e
		}
		n := 0
		var recvErr error
		e = raw.Control(func(fd uintptr) {
			n, _, recvErr = syscall.Recvfrom(int(fd), buf[:want], syscall.MSG_PEEK|syscall.MSG_DONTWAIT)
		})
		if e != nil {
			return nil, e
		}
		if recvErr != nil && recvErr != syscall.EAGAIN && recvErr != syscall.EWOULDBLOCK && recvErr != syscall.EINTR {
			return nil, recvErr
		}
		if recvErr == nil && n == 0 {
			return nil, errors.New("connection closed before RDP request")
		}
		if n >= 4 {
			length, err := initialRDPLength(buf[:4])
			if err != nil {
				return nil, err
			}
			if want == 4 {
				want = length
				continue
			} else if n >= want {
				p := append([]byte(nil), buf[:want]...)
				if _, err := requestProtocols(p); err != nil {
					return nil, err
				}
				return p, nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-tick.C:
		}
	}
}
func configureTUNHint(c Config) string {
	return "set address/link manually: ip address add " + c.CIDR + " dev " + c.TUN +
		"; ip link set dev " + c.TUN + " mtu " + itoa(c.MTU) + " up"
}
func platformAddress(c Config) string { return strings.TrimSpace(c.CIDR) }
