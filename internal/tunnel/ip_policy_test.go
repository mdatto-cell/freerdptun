package tunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

// Regression coverage added with 0.1.1. Not executed for that hotfix at the
// user's request; run it in the user's sandbox or a future validation session.
func TestIncomingIPPolicy(t *testing.T) {
	tests := []struct {
		name     string
		src, dst string
		server   bool
		allow    bool
		errPart  string
	}{
		{"server unicast", "10.77.0.2", "1.1.1.1", true, true, ""},
		{"server IGMP multicast", "10.77.0.2", "224.0.0.22", true, false, ""},
		{"server mDNS multicast", "10.77.0.2", "224.0.0.251", true, false, ""},
		{"server limited broadcast", "10.77.0.2", "255.255.255.255", true, false, ""},
		{"server subnet broadcast", "10.77.0.2", "10.77.0.255", true, false, ""},
		{"server link local", "10.77.0.2", "169.254.1.1", true, false, ""},
		{"server source spoof", "10.77.0.3", "1.1.1.1", true, false, "source ownership"},
		{"server multicast spoof still fails", "10.77.0.3", "224.0.0.22", true, false, "source ownership"},
		{"client unicast", "10.77.0.1", "10.77.0.2", false, true, ""},
		{"client wrong unicast destination", "10.77.0.1", "10.77.0.3", false, false, "destination ownership"},
		{"client multicast", "10.77.0.1", "224.0.0.22", false, false, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &ipPolicy{subnet: netip.MustParsePrefix("10.77.0.1/24"),
				expected: netip.MustParseAddr("10.77.0.2"), server: tc.server}
			allow, err := p.incoming(netip.MustParseAddr(tc.src), netip.MustParseAddr(tc.dst))
			if allow != tc.allow {
				t.Fatalf("allow=%v want %v", allow, tc.allow)
			}
			if tc.errPart == "" && err != nil {
				t.Fatal(err)
			}
			if tc.errPart != "" && (err == nil || !strings.Contains(err.Error(), tc.errPart)) {
				t.Fatalf("expected %q, got %v", tc.errPart, err)
			}
			if err != nil && (!strings.Contains(err.Error(), tc.src) || !strings.Contains(err.Error(), tc.dst)) {
				t.Fatalf("ownership error lacks packet addresses: %v", err)
			}
		})
	}
}

func TestClientOutgoingIPPolicy(t *testing.T) {
	p := &ipPolicy{subnet: netip.MustParsePrefix("10.77.0.2/24"), expected: netip.MustParseAddr("10.77.0.2")}
	for _, dst := range []string{"224.0.0.22", "224.0.0.251", "255.255.255.255", "10.77.0.255", "127.0.0.1"} {
		if p.clientOutgoing(p.expected, netip.MustParseAddr(dst)) {
			t.Fatalf("accepted unsupported destination %s", dst)
		}
	}
	if p.clientOutgoing(netip.MustParseAddr("169.254.83.255"), netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("accepted APIPA source")
	}
	if !p.clientOutgoing(p.expected, netip.MustParseAddr("1.1.1.1")) {
		t.Fatal("rejected unicast")
	}
	if p.filtered.Load() != 5 {
		t.Fatalf("filtered count=%d want 5", p.filtered.Load())
	}
}

type packetSink struct{ packets [][]byte }

func (s *packetSink) Read([]byte) (int, error) { return 0, io.EOF }
func (s *packetSink) Write(b []byte) (int, error) {
	s.packets = append(s.packets, append([]byte(nil), b...))
	return len(b), nil
}
func (s *packetSink) Close() error { return nil }

func TestReceiverContinuesAfterUnsupportedDestination(t *testing.T) {
	var frames bytes.Buffer
	w := NewWire(&frames, &frames)
	packet := func(dst string) []byte {
		b := ipPacket()
		srcIP := netip.MustParseAddr("10.77.0.2").As4()
		dstIP := netip.MustParseAddr(dst).As4()
		copy(b[12:16], srcIP[:])
		copy(b[16:20], dstIP[:])
		binary.BigEndian.PutUint16(b[2:4], uint16(len(b)))
		return b
	}
	good := packet("10.77.0.1")
	for _, b := range [][]byte{packet("224.0.0.22"), packet("255.255.255.255"), packet("10.77.0.255"), good} {
		if err := w.Write(append([]byte{msgIP}, b...)); err != nil {
			t.Fatal(err)
		}
	}
	p := &ipPolicy{subnet: netip.MustParsePrefix("10.77.0.1/24"),
		expected: netip.MustParseAddr("10.77.0.2"), server: true}
	sink := &packetSink{}
	var last atomic.Int64
	err := receiver(context.Background(), NewWire(&frames, io.Discard), sink, make(chan []byte, 1),
		p, 1400, NewPacer(0), NewPacer(0), &last)
	if err != io.EOF {
		t.Fatalf("receiver closed on unsupported packet: %v", err)
	}
	if len(sink.packets) != 1 || !bytes.Equal(sink.packets[0], good) || p.filtered.Load() != 3 {
		t.Fatalf("unicast delivery or filtering failed: delivered=%d filtered=%d", len(sink.packets), p.filtered.Load())
	}
}
