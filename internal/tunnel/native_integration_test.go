//go:build linux

package tunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"net"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"
)

type memoryDevice struct {
	fromKernel, toKernel chan []byte
	done                 chan struct{}
	once                 sync.Once
}

func newMemoryDevice(ctx context.Context) *memoryDevice {
	d := &memoryDevice{fromKernel: make(chan []byte, 16), toKernel: make(chan []byte, 16), done: make(chan struct{})}
	go func() { <-ctx.Done(); d.Close() }()
	return d
}
func (d *memoryDevice) Read(b []byte) (int, error) {
	select {
	case <-d.done:
		return 0, os.ErrClosed
	case p := <-d.fromKernel:
		return copy(b, p), nil
	}
}
func (d *memoryDevice) Write(b []byte) (int, error) {
	select {
	case <-d.done:
		return 0, os.ErrClosed
	case d.toKernel <- append([]byte(nil), b...):
		return len(b), nil
	}
}
func (d *memoryDevice) Close() error { d.once.Do(func() { close(d.done) }); return nil }
func testPacket(src, dst string) []byte {
	b := ipPacket()
	s := netip.MustParseAddr(src).As4()
	d := netip.MustParseAddr(dst).As4()
	copy(b[12:], s[:])
	copy(b[16:], d[:])
	return b
}
func receiveMemory(t *testing.T, d *memoryDevice, expected []byte) {
	t.Helper()
	select {
	case got := <-d.toKernel:
		if !bytes.Equal(got, expected) {
			t.Fatalf("IP corruption: %x != %x", got, expected)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native DVC IP delivery timed out")
	}
}
func TestNativeTwoClients(t *testing.T) {
	path := os.Getenv("RDPT_SERVER_CONFIG")
	if path == "" {
		t.Skip("set RDPT_SERVER_CONFIG for real FreeRDP integration test (no TUN)")
	}
	c, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if bridge := os.Getenv("RDPT_BRIDGE"); bridge != "" {
		c.Bridge = bridge
	}
	pemData, e := os.ReadFile(c.Certificate)
	if e != nil {
		t.Fatal(e)
	}
	block, _ := pem.Decode(pemData)
	if block == nil {
		t.Fatal("invalid cert")
	}
	cert, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		t.Fatal(e)
	}
	hash := sha256.Sum256(cert.Raw)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c.Clients = append([]Client(nil), c.Clients[0])
	id, e := NewUUID()
	if e != nil {
		t.Fatal(e)
	}
	c.Clients = append(c.Clients, Client{ID: "test-02", UUID: id, IPv4: "10.78.0.3"})
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	c.Address = listener.Addr().String()
	serverDev := newMemoryDevice(ctx)
	serverDone := make(chan error, 1)
	go func() { serverDone <- serveListener(ctx, c, serverDev, listener) }()
	devices := make([]*memoryDevice, 2)
	clientDone := make([]chan error, 2)
	for i := range devices {
		cc := c
		cc.Mode = "client"
		cc.UUID = c.Clients[i].UUID
		cc.IPv4 = c.Clients[i].IPv4
		cc.CIDR = cc.IPv4 + "/24"
		cc.ServerSHA256 = hex.EncodeToString(hash[:])
		cc.Clients = nil
		devices[i] = newMemoryDevice(ctx)
		clientDone[i] = make(chan error, 1)
		go func(index int, client Config) { clientDone[index] <- runClient(ctx, client, devices[index]) }(i, cc)
		packet := testPacket(cc.IPv4, "10.78.0.1")
		devices[i].fromKernel <- packet
		receiveMemory(t, serverDev, packet)
	}
	for i, d := range devices {
		packet := testPacket("10.78.0.1", c.Clients[i].IPv4)
		serverDev.fromKernel <- packet
		receiveMemory(t, d, packet)
	}
	cancel()
	select {
	case <-serverDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop/reap children")
	}
	for _, done := range clientDone {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("client did not stop")
		}
	}
}
func TestNativeSelfTest(t *testing.T) {
	path := os.Getenv("RDPT_SERVER_CONFIG")
	if path == "" {
		t.Skip("native test disabled")
	}
	c, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if bridge := os.Getenv("RDPT_BRIDGE"); bridge != "" {
		c.Bridge = bridge
	}
	if e = SelfTest(context.Background(), c); e != nil {
		t.Fatal(e)
	}
}

func TestNativeRejectPin(t *testing.T) {
	path := os.Getenv("RDPT_SERVER_CONFIG")
	if path == "" {
		t.Skip("native test disabled")
	}
	c, e := Load(path)
	if e != nil {
		t.Fatal(e)
	}
	if bridge := os.Getenv("RDPT_BRIDGE"); bridge != "" {
		c.Bridge = bridge
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, e := listener.Accept()
		if e != nil {
			return
		}
		f, e := peerFile(conn)
		conn.Close()
		if e != nil {
			return
		}
		p, e := StartBridge(ctx, c.Bridge, []string{"--server-fd", "3", "--cert", c.Certificate, "--key", c.PrivateKey}, []*os.File{f})
		f.Close()
		if e != nil {
			return
		}
		defer p.Close()
		_, _ = ServerAuth(p.Wire, c)
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, e := StartBridge(ctx, c.Bridge, []string{"--host", host, "--port", port, "--pin", hex.EncodeToString(make([]byte, 32))}, nil)
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	client := c
	client.UUID = c.Clients[0].UUID
	client.IPv4 = c.Clients[0].IPv4
	if _, e = ClientAuth(p.Wire, client); e == nil {
		t.Fatal("WRONG PIN ACCEPTED")
	}
	if ctx.Err() != nil {
		t.Fatal("pin rejection hung until timeout")
	}
	select {
	case <-serverDone:
	case <-ctx.Done():
		t.Fatal("server did not stop after failed TLS")
	}
}
