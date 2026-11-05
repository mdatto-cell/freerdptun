package tunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

const testUUID = "67f7c7e2-b5eb-4bd6-9472-52f0781d2731"

func testConfig() Config {
	return Config{Mode: "server", Address: "127.0.0.1:3389", TUN: "test0", CIDR: "10.78.0.1/24",
		MTU: 1400, Certificate: "server.crt", PrivateKey: "server.key", MaxConnections: 32, HandshakeSeconds: 20,
		IdleSeconds: 60, QueuePackets: 256, Clients: []Client{{ID: "test", UUID: testUUID, IPv4: "10.78.0.2"}}}
}
func authPair(t *testing.T, cfg Config, uuid, ip string) error {
	t.Helper()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	a.SetDeadline(time.Now().Add(2 * time.Second))
	b.SetDeadline(time.Now().Add(2 * time.Second))
	done := make(chan error, 1)
	go func() { _, e := ServerAuth(NewWire(a, a), cfg); a.Close(); done <- e }()
	client := cfg
	client.Mode = "client"
	client.UUID = uuid
	client.IPv4 = ip
	_, e := ClientAuth(NewWire(b, b), client)
	se := <-done
	if e == nil {
		return se
	}
	return e
}
func TestAuthSuccess(t *testing.T) {
	if e := authPair(t, testConfig(), testUUID, "10.78.0.2"); e != nil {
		t.Fatal(e)
	}
}
func TestAuthRejectUUID(t *testing.T) {
	if e := authPair(t, testConfig(), "0348476e-0127-4973-8851-156a71a077e8", "10.78.0.2"); e == nil {
		t.Fatal("accepted unknown UUID")
	}
}
func TestAuthRejectIP(t *testing.T) {
	if e := authPair(t, testConfig(), testUUID, "10.78.0.3"); e == nil {
		t.Fatal("accepted stolen IP")
	}
}
func TestNoSecretInIdentifier(t *testing.T) {
	key, _ := UUIDBytes(testUUID)
	if bytes.Equal(key, publicID(key)) {
		t.Fatal("UUID sent as ID")
	}
}
func TestProofSeparationAndFreshness(t *testing.T) {
	key, _ := UUIDBytes(testUUID)
	a := proof(key, "client", []byte("nonce-1"), []byte("client"))
	if bytes.Equal(a, proof(key, "server", []byte("nonce-1"), []byte("client"))) {
		t.Fatal("role reflection")
	}
	if bytes.Equal(a, proof(key, "client", []byte("nonce-2"), []byte("client"))) {
		t.Fatal("replay not nonce-bound")
	}
}
func TestFraming(t *testing.T) {
	for _, n := range []int{1, 1401, 65535} {
		var buf bytes.Buffer
		b := bytes.Repeat([]byte{0x7e}, n)
		w := NewWire(&buf, &buf)
		if e := w.Write(b); e != nil {
			t.Fatal(e)
		}
		got, e := w.Read()
		if e != nil || !bytes.Equal(got, b) {
			t.Fatal(n, e)
		}
	}
}
func TestMalformedLengths(t *testing.T) {
	for _, n := range []uint32{0, 65536, 0xffffffff} {
		var h [4]byte
		binary.BigEndian.PutUint32(h[:], n)
		if _, e := NewWire(bytes.NewReader(h[:]), io.Discard).Read(); e == nil {
			t.Fatal(n)
		}
	}
}

type shortWriter struct{ bytes.Buffer }

func (w *shortWriter) Write(b []byte) (int, error) {
	if len(b) > 2 {
		b = b[:2]
	}
	return w.Buffer.Write(b)
}
func TestShortWrites(t *testing.T) {
	w := &shortWriter{}
	if e := writeFull(w, []byte("abcdefgh")); e != nil || w.String() != "abcdefgh" {
		t.Fatal(e, w.String())
	}
}
func ipPacket() []byte {
	b := make([]byte, 20)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:], 20)
	copy(b[12:], []byte{10, 78, 0, 2})
	copy(b[16:], []byte{1, 1, 1, 1})
	return b
}
func TestIPv4Validation(t *testing.T) {
	b := ipPacket()
	src, dst, e := IPv4Packet(b, 1400)
	if e != nil || src.String() != "10.78.0.2" || dst.String() != "1.1.1.1" {
		t.Fatal(src, dst, e)
	}
	b[2] = 0x7f
	if _, _, e = IPv4Packet(b, 1400); e == nil {
		t.Fatal("bad total length")
	}
	b = ipPacket()
	b[0] = 0x65
	if _, _, e = IPv4Packet(b, 1400); e == nil {
		t.Fatal("IPv6")
	}
}
func TestUUIDValidation(t *testing.T) {
	for _, s := range []string{"", strings.Repeat("a", 32), "00000000-0000-0000-0000-000000000000", "zzzzzzzz-0000-4000-8000-000000000000"} {
		if _, e := UUIDBytes(s); e == nil {
			t.Fatal(s)
		}
	}
	u, e := NewUUID()
	if e != nil {
		t.Fatal(e)
	}
	if _, e = UUIDBytes(u); e != nil {
		t.Fatal(e)
	}
}
func TestConfigAndDuplicates(t *testing.T) {
	c := testConfig()
	if e := c.Validate(); e != nil {
		t.Fatal(e)
	}
	c.Clients = append(c.Clients, c.Clients[0])
	if e := c.Validate(); e == nil {
		t.Fatal("duplicates")
	}
	c = testConfig()
	c.Clients[0].IPv4 = "10.79.0.2"
	if e := c.Validate(); e == nil {
		t.Fatal("outside subnet")
	}
}
func TestHubDuplicateAndQueue(t *testing.T) {
	c := testConfig()
	h := &hub{peers: map[netip.Addr]*active{}}
	a := &active{client: c.Clients[0], queue: make(chan []byte, 1)}
	if e := h.add(a); e != nil {
		t.Fatal(e)
	}
	if e := h.add(a); e != errDuplicate {
		t.Fatal(e)
	}
	b := ipPacket()
	copy(b[16:], []byte{10, 78, 0, 2})
	h.route(b, 1400)
	h.route(b, 1400)
	if a.drops.Load() != 1 {
		t.Fatal("unbounded queue")
	}
	h.remove(a)
	if len(h.peers) != 0 {
		t.Fatal("stale route")
	}
}
func TestPacerCancellation(t *testing.T) {
	p := NewPacer(8)
	ctx, cancel := context.WithCancel(context.Background())
	if e := p.Wait(ctx, 1000); e != nil {
		t.Fatal(e)
	}
	cancel()
	start := time.Now()
	if e := p.Wait(ctx, 1000); e == nil {
		t.Fatal("ignored cancellation")
	}
	if time.Since(start) > time.Second {
		t.Fatal("slow cancellation")
	}
}
func TestPacerSustainedLimit(t *testing.T) {
	p := NewPacer(1000000)
	start := time.Now()
	for i := 0; i < 100; i++ {
		if e := p.Wait(context.Background(), 1000); e != nil {
			t.Fatal(e)
		}
	}
	// 100000 bytes less the initial 64 KiB, at 125000 bytes/s, is ~276 ms.
	if elapsed := time.Since(start); elapsed < 240*time.Millisecond || elapsed > 3*time.Second {
		t.Fatalf("sustained rate outside expected bounds: %v", elapsed)
	}
}
func FuzzFrameRead(f *testing.F) {
	f.Add([]byte{0, 0, 0, 1, 5})
	f.Add([]byte{255, 255, 255, 255})
	f.Fuzz(func(t *testing.T, b []byte) { _, _ = NewWire(bytes.NewReader(b), io.Discard).Read() })
}
func FuzzIPv4(f *testing.F) {
	f.Add(ipPacket())
	f.Fuzz(func(t *testing.T, b []byte) { _, _, _ = IPv4Packet(b, 1400) })
}
