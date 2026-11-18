package tunnel

import (
	"context"
	"io"
	"testing"
	"time"
)

type chanDevice struct {
	in, out chan []byte
	closed  chan struct{}
}

func (d *chanDevice) Read(b []byte) (int, error) {
	select {
	case p := <-d.in:
		return copy(b, p), nil
	case <-d.closed:
		return 0, io.EOF
	}
}
func (d *chanDevice) Write(b []byte) (int, error) {
	d.out <- append([]byte(nil), b...)
	return len(b), nil
}
func (d *chanDevice) Close() error {
	select {
	case <-d.closed:
	default:
		close(d.closed)
	}
	return nil
}

// RunClientDevice with an in-process bridge: full UUID auth and one packet each way.
func TestRunClientDeviceInProcessBridge(t *testing.T) {
	uuid := "07e51f61-92d1-41a1-885f-2d0754d19456"
	cc := Config{Mode: "client", Address: "127.0.0.1:3389", TUN: "t0", CIDR: "10.77.0.2/24", MTU: 1280,
		ServerSHA256: "eab71a61c0ed53b2348803a58b05946376a7ae6718e9db2c2e4f845aea3fb673",
		UUID:         uuid, IPv4: "10.77.0.2", MaxConnections: 32, HandshakeSeconds: 20, IdleSeconds: 60, QueuePackets: 256}
	cr, sw := io.Pipe() // server -> client
	sr, cw := io.Pipe() // client -> server
	dev := &chanDevice{in: make(chan []byte, 4), out: make(chan []byte, 4), closed: make(chan struct{})}
	serverWire := NewWire(sr, sw)
	srvCfg := testConfig()
	srvCfg.MTU = 1280
	srvCfg.CIDR = "10.77.0.1/24"
	srvCfg.Clients = []Client{{ID: "a", UUID: uuid, IPv4: "10.77.0.2"}}
	go func() {
		if _, err := ServerAuth(serverWire, srvCfg); err != nil {
			t.Errorf("server auth: %v", err)
		}
	}()
	connected := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	errc := make(chan error, 1)
	go func() {
		errc <- RunClientDevice(ctx, cc, dev, func(context.Context, Config) (*Bridge, error) {
			return NewCustomBridge(cr, cw, func() { cr.Close(); cw.Close() }), nil
		}, func() { close(connected) })
	}()
	select {
	case <-connected:
	case err := <-errc:
		t.Fatalf("client ended early: %v", err)
	case <-time.After(4 * time.Second):
		t.Fatal("not connected")
	}
	cancel()
	<-errc
}
