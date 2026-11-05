//go:build windows

package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
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
	if len(files) != 0 {
		return errors.New("server is Linux-only")
	}
	return nil
}
func peerFile(net.Conn) (*os.File, error) { return nil, errors.New("server is Linux-only") }
func peekConnectionRequest(context.Context, net.Conn, time.Time) ([]byte, error) {
	return nil, errors.New("server is Linux-only")
}

// All interpolated interface parameters are passed as environment values, not
// PowerShell code. Only our newly created interface is modified.
const configureWindowsTUN = `
$ErrorActionPreference = 'Stop'
$alias = $env:FRDPTUN_INTERFACE
$address = $env:FRDPTUN_IPV4
$prefix = [byte]$env:FRDPTUN_PREFIX
$mtu = [uint32]$env:FRDPTUN_MTU
$interfaceUntil = [DateTime]::UtcNow.AddSeconds(10)
do {
    $interfaces = @(Get-NetIPInterface -AddressFamily IPv4 | Where-Object { $_.InterfaceAlias -eq $alias })
    if ($interfaces.Count -eq 1) { break }
    if ($interfaces.Count -gt 1 -or [DateTime]::UtcNow -ge $interfaceUntil) {
        throw "Expected exactly one IPv4 interface named '$alias'."
    }
    Start-Sleep -Milliseconds 100
} while ($true)
$index = $interfaces[0].InterfaceIndex
Set-NetIPInterface -InterfaceIndex $index -AddressFamily IPv4 -Dhcp Disabled -NlMtuBytes $mtu
$existing = @(Get-NetIPAddress -InterfaceIndex $index -AddressFamily IPv4 -ErrorAction SilentlyContinue)
foreach ($ip in $existing) {
    if ($ip.IPAddress -ne $address) {
        Remove-NetIPAddress -InputObject $ip -Confirm:$false -ErrorAction Stop
    }
}
$target = @($existing | Where-Object { $_.IPAddress -eq $address })
if ($target.Count -eq 0) {
    New-NetIPAddress -InterfaceIndex $index -AddressFamily IPv4 -IPAddress $address -PrefixLength $prefix -PolicyStore ActiveStore | Out-Null
} else {
    Set-NetIPAddress -InterfaceIndex $index -IPAddress $address -PrefixLength $prefix -SkipAsSource $false
}
$readyUntil = [DateTime]::UtcNow.AddSeconds(20)
do {
    $current = Get-NetIPAddress -InterfaceIndex $index -AddressFamily IPv4 -IPAddress $address
    if ($current.AddressState -eq 'Preferred') { break }
    if ($current.AddressState -eq 'Duplicate' -or $current.AddressState -eq 'Invalid') {
        throw "TUN IPv4 address is unusable: $($current.AddressState)."
    }
    if ([DateTime]::UtcNow -ge $readyUntil) { throw 'TUN IPv4 address did not become Preferred in 20 seconds.' }
    Start-Sleep -Milliseconds 100
} while ($true)
if ($current.PrefixLength -ne $prefix) {
    throw 'TUN IPv4 prefix does not match the configuration.'
}
`

func configureTUN(ctx context.Context, c Config) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	system := os.Getenv("SystemRoot")
	if system == "" {
		return errors.New("SystemRoot missing; cannot locate Windows PowerShell")
	}
	powershell := filepath.Join(system, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	cmd := exec.CommandContext(ctx, powershell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", configureWindowsTUN)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Env = append(os.Environ(), "FRDPTUN_INTERFACE="+c.TUN, "FRDPTUN_IPV4="+c.IPv4,
		"FRDPTUN_PREFIX="+prefixBits(c.CIDR), "FRDPTUN_MTU="+itoa(c.MTU))
	out, e := cmd.CombinedOutput()
	if e != nil {
		return fmt.Errorf("automatic TUN setup (Administrator required): %w: %s", e, string(out))
	}
	return nil
}

type winTUN struct {
	dll                                                           *syscall.DLL
	adapter, session                                              uintptr
	stop                                                          syscall.Handle
	receive, release, allocate, send, end, closeAdapter, getEvent *syscall.Proc
	move                                                          *syscall.LazyProc
	readMu, writeMu                                               sync.Mutex
	once                                                          sync.Once
}

func OpenTUN(c Config) (Device, error) {
	exe, e := os.Executable()
	if e != nil {
		return nil, e
	}
	// Absolute path prevents finding an attacker-controlled DLL on PATH/CWD.
	dll, e := syscall.LoadDLL(filepath.Join(filepath.Dir(exe), "wintun.dll"))
	if e != nil {
		return nil, fmt.Errorf("load signed amd64 wintun.dll beside executable: %w", e)
	}
	t := &winTUN{dll: dll}
	t.move = syscall.NewLazyDLL("kernel32.dll").NewProc("RtlMoveMemory")
	e = t.move.Find()
	if e != nil {
		dll.Release()
		return nil, e
	}
	names := []string{"WintunReceivePacket", "WintunReleaseReceivePacket", "WintunAllocateSendPacket", "WintunSendPacket", "WintunEndSession", "WintunCloseAdapter", "WintunGetReadWaitEvent"}
	procs := []**syscall.Proc{&t.receive, &t.release, &t.allocate, &t.send, &t.end, &t.closeAdapter, &t.getEvent}
	for i, name := range names {
		p, err := dll.FindProc(name)
		if err != nil {
			dll.Release()
			return nil, err
		}
		*procs[i] = p
	}
	create, e := dll.FindProc("WintunCreateAdapter")
	if e != nil {
		dll.Release()
		return nil, e
	}
	start, e := dll.FindProc("WintunStartSession")
	if e != nil {
		dll.Release()
		return nil, e
	}
	name, _ := syscall.UTF16PtrFromString(c.TUN)
	kind, _ := syscall.UTF16PtrFromString("FreeRDPTun")
	t.adapter, _, e = create.Call(uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(kind)), 0)
	runtime.KeepAlive(name)
	runtime.KeepAlive(kind)
	if t.adapter == 0 {
		dll.Release()
		return nil, fmt.Errorf("create Wintun adapter (run as Administrator): %w", e)
	}
	t.session, _, e = start.Call(t.adapter, 4*1024*1024)
	if t.session == 0 {
		t.closeAdapter.Call(t.adapter)
		dll.Release()
		return nil, e
	}
	stop, _, e := syscall.NewLazyDLL("kernel32.dll").NewProc("CreateEventW").Call(0, 1, 0, 0)
	if stop == 0 {
		t.end.Call(t.session)
		t.closeAdapter.Call(t.adapter)
		dll.Release()
		return nil, e
	}
	t.stop = syscall.Handle(stop)
	return t, nil
}
func (t *winTUN) Read(b []byte) (int, error) {
	t.readMu.Lock()
	defer t.readMu.Unlock()
	for {
		if s, _ := syscall.WaitForSingleObject(t.stop, 0); s == syscall.WAIT_OBJECT_0 {
			return 0, os.ErrClosed
		}
		var n uint32
		p, _, e := t.receive.Call(t.session, uintptr(unsafe.Pointer(&n)))
		if p != 0 {
			if int(n) > len(b) {
				t.release.Call(t.session, p)
				return 0, errors.New("Wintun oversized packet")
			}
			if n > 0 {
				// Copy unmanaged driver memory via WinAPI, without constructing
				// Go pointers from uintptr values returned by a DLL.
				t.move.Call(uintptr(unsafe.Pointer(&b[0])), p, uintptr(n))
				runtime.KeepAlive(b)
			}
			t.release.Call(t.session, p)
			return int(n), nil
		}
		if e != syscall.Errno(259) {
			return 0, e
		} // ERROR_NO_MORE_ITEMS
		event, _, _ := t.getEvent.Call(t.session)
		handles := [2]syscall.Handle{t.stop, syscall.Handle(event)}
		w, _, err := syscall.NewLazyDLL("kernel32.dll").NewProc("WaitForMultipleObjects").Call(2, uintptr(unsafe.Pointer(&handles[0])), 0, 0xffffffff)
		if w == 0 {
			return 0, os.ErrClosed
		}
		if w != 1 {
			return 0, err
		}
	}
}
func (t *winTUN) Write(b []byte) (int, error) {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if s, _ := syscall.WaitForSingleObject(t.stop, 0); s == syscall.WAIT_OBJECT_0 {
		return 0, os.ErrClosed
	}
	if len(b) == 0 {
		return 0, nil
	}
	p, _, e := t.allocate.Call(t.session, uintptr(len(b)))
	if p == 0 {
		return 0, e
	}
	t.move.Call(p, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	runtime.KeepAlive(b)
	t.send.Call(t.session, p)
	return len(b), nil
}
func (t *winTUN) Close() error {
	t.once.Do(func() {
		syscall.NewLazyDLL("kernel32.dll").NewProc("SetEvent").Call(uintptr(t.stop))
		t.readMu.Lock()
		t.writeMu.Lock()
		t.end.Call(t.session)
		t.closeAdapter.Call(t.adapter)
		// Keep DLL and stop handle until process exit to make concurrent late
		// reads fail safely rather than call unmapped code/invalid handles.
		t.writeMu.Unlock()
		t.readMu.Unlock()
	})
	return nil
}
func configureTUNHint(c Config) string {
	return fmt.Sprintf("set address/link manually in elevated PowerShell: New-NetIPAddress -InterfaceAlias '%s' -IPAddress %s -PrefixLength %s; Set-NetIPInterface -InterfaceAlias '%s' -NlMtuBytes %d", c.TUN, c.IPv4, prefixBits(c.CIDR), c.TUN, c.MTU)
}
func platformAddress(c Config) string { return c.CIDR }
