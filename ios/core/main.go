// Command core builds libfrtcore.a (-buildmode=c-archive) for the iOS Packet
// Tunnel extension. It runs the unchanged FreeRDPTun client logic
// (internal/tunnel) over an in-memory packet device and the in-process native
// bridge (native/bridge.c built with -DFRT_LIBRARY).
package main

/*
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef int (*frt_run_fn)(const char *, unsigned, const char *, int, int);
typedef void (*frt_stop_fn)(void);
static frt_run_fn g_run;
static frt_stop_fn g_stop;
static void frt_set(void *run, void *stop) { g_run = (frt_run_fn)run; g_stop = (frt_stop_fn)stop; }
static int frt_run(const char *h, unsigned p, const char *pin, int in, int out) {
	return g_run ? g_run(h, p, pin, in, out) : -1;
}
static void frt_stop(void) { if (g_stop) g_stop(); }
static int frt_ready(void) { return g_run != 0 && g_stop != 0; }
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"freerdptun/internal/tunnel"
)

// Status values returned by FRTStatus.
const (
	stIdle       = 0
	stConnecting = 1
	stConnected  = 2
	stStopped    = 3
)

func init() {
	// Network Extensions are memory limited (~50 MB incl. FreeRDP).
	debug.SetGCPercent(50)
	debug.SetMemoryLimit(28 << 20)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetPrefix("[core] ")
}

// packetDevice is a tunnel.Device fed by Swift (NEPacketTunnelFlow).
type packetDevice struct {
	in, out chan []byte
	closed  chan struct{}
	once    sync.Once
}

func newPacketDevice() *packetDevice {
	return &packetDevice{in: make(chan []byte, 1024), out: make(chan []byte, 1024), closed: make(chan struct{})}
}
func (d *packetDevice) Read(b []byte) (int, error) {
	select {
	case p := <-d.in:
		return copy(b, p), nil
	case <-d.closed:
		return 0, os.ErrClosed
	}
}
func (d *packetDevice) Write(b []byte) (int, error) {
	p := append([]byte(nil), b...)
	select {
	case d.out <- p:
	default: // drop when Swift cannot keep up
	}
	return len(b), nil
}
func (d *packetDevice) Close() error { d.once.Do(func() { close(d.closed) }); return nil }

var (
	mu      sync.Mutex
	dev     *packetDevice
	cancel  context.CancelFunc
	done    chan struct{}
	state   atomic.Int32
	lastErr atomic.Value // string
)

func setErr(s string) { lastErr.Store(s) }

func fillBuf(dst *C.char, n C.int, s string) {
	if dst == nil || n <= 0 {
		return
	}
	b := []byte(s)
	if len(b) > int(n)-1 {
		b = b[:int(n)-1]
	}
	buf := unsafe.Slice((*byte)(unsafe.Pointer(dst)), int(n))
	copy(buf, b)
	buf[len(b)] = 0
}

// startBridge runs the C bridge in-process over two pipes.
func startBridge(ctx context.Context, c tunnel.Config) (*tunnel.Bridge, error) {
	if C.frt_ready() == 0 {
		return nil, errors.New("native bridge is not registered")
	}
	host, port, err := net.SplitHostPort(c.Address)
	if err != nil {
		return nil, err
	}
	var portN int
	if _, err = fmt.Sscanf(port, "%d", &portN); err != nil {
		return nil, err
	}
	toBridgeR, toBridgeW, err := os.Pipe() // Go -> C
	if err != nil {
		return nil, err
	}
	fromBridgeR, fromBridgeW, err := os.Pipe() // C -> Go
	if err != nil {
		toBridgeR.Close()
		toBridgeW.Close()
		return nil, err
	}
	pin := strings.ToLower(c.ServerSHA256)
	finished := make(chan struct{})
	go func() {
		chost, cpin := C.CString(host), C.CString(pin)
		rc := C.frt_run(chost, C.uint(portN), cpin, C.int(toBridgeR.Fd()), C.int(fromBridgeW.Fd()))
		C.free(unsafe.Pointer(chost))
		C.free(unsafe.Pointer(cpin))
		log.Printf("native bridge finished rc=%d", int(rc))
		// Close C-side ends so the Go reader sees EOF.
		toBridgeR.Close()
		fromBridgeW.Close()
		close(finished)
	}()
	closeFn := func() {
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		deadline := time.After(4 * time.Second)
	loop:
		for {
			C.frt_stop()
			select {
			case <-finished:
				break loop
			case <-deadline:
				break loop
			case <-t.C:
			}
		}
		toBridgeW.Close()
		fromBridgeR.Close()
	}
	return tunnel.NewCustomBridge(fromBridgeR, toBridgeW, closeFn), nil
}

//export FRTRegisterBridge
func FRTRegisterBridge(run, stop unsafe.Pointer) { C.frt_set(run, stop) }

// FRTStart parses the JSON config and starts the client in the background.
// Returns 0 on success, otherwise a message is written to errbuf.
//
//export FRTStart
func FRTStart(cfg *C.char, errbuf *C.char, errlen C.int) C.int {
	mu.Lock()
	defer mu.Unlock()
	if state.Load() == stConnecting || state.Load() == stConnected {
		fillBuf(errbuf, errlen, "already running")
		return 1
	}
	c, err := tunnel.ParseConfig([]byte(C.GoString(cfg)))
	if err == nil && c.Mode != "client" {
		err = errors.New("mode must be client")
	}
	if err != nil {
		fillBuf(errbuf, errlen, "config: "+err.Error())
		return 2
	}
	d := newPacketDevice()
	ctx, cf := context.WithCancel(context.Background())
	dev, cancel, done = d, cf, make(chan struct{})
	state.Store(stConnecting)
	setErr("")
	go func(done chan struct{}) {
		defer close(done)
		err := tunnel.RunClientDevice(ctx, c, d, startBridge, func() { state.Store(stConnected) })
		if err != nil && !errors.Is(err, context.Canceled) {
			setErr(err.Error())
			log.Printf("client stopped: %v", err)
		}
		d.Close()
		state.Store(stStopped)
	}(done)
	return 0
}

// FRTStop stops the client and waits (bounded) for shutdown.
//
//export FRTStop
func FRTStop() {
	mu.Lock()
	cf, dn := cancel, done
	mu.Unlock()
	if cf != nil {
		cf()
	}
	if dn != nil {
		select {
		case <-dn:
		case <-time.After(8 * time.Second):
		}
	}
}

// FRTStatus: 0 idle, 1 connecting, 2 connected, 3 stopped.
//
//export FRTStatus
func FRTStatus() C.int { return C.int(state.Load()) }

//export FRTLastError
func FRTLastError(buf *C.char, n C.int) {
	s, _ := lastErr.Load().(string)
	fillBuf(buf, n, s)
}

// FRTPushPacket hands one IPv4 packet read from the iOS tunnel to the client.
//
//export FRTPushPacket
func FRTPushPacket(p *C.uint8_t, n C.int) {
	mu.Lock()
	d := dev
	mu.Unlock()
	if d == nil || n <= 0 {
		return
	}
	b := append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(p)), int(n))...)
	select {
	case d.in <- b:
	default:
	}
}

// FRTPopPacket waits up to timeoutMs for a packet to inject into iOS.
// Returns length, 0 on timeout, -1 when the tunnel has ended.
//
//export FRTPopPacket
func FRTPopPacket(p *C.uint8_t, capacity C.int, timeoutMs C.int) C.int {
	mu.Lock()
	d := dev
	mu.Unlock()
	if d == nil {
		return -1
	}
	select {
	case b := <-d.out:
		return C.int(copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), int(capacity)), b))
	default:
	}
	t := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer t.Stop()
	select {
	case b := <-d.out:
		return C.int(copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), int(capacity)), b))
	case <-d.closed:
		select {
		case b := <-d.out:
			return C.int(copy(unsafe.Slice((*byte)(unsafe.Pointer(p)), int(capacity)), b))
		default:
			return -1
		}
	case <-t.C:
		return 0
	}
}

func main() {}
