package tunnel

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

// Source coverage for the user's sandbox. Not executed for release 0.1.3.
func TestTransportRoundtrip(t *testing.T) {
	for _, tc := range []struct {
		name, mode    string
		pad, fragment bool
		small, n      int
	}{
		{"baseline", "baseline", true, true, 0, 1400},
		{"padding", "profiled", true, false, 0, 400},
		{"oversize without fragmentation", "profiled", true, false, 0, 1400},
		{"fragments padded", "profiled", true, true, 0, 1400},
		{"fragments unpadded", "profiled", false, true, 0, 1400},
		{"small exempt", "profiled", true, true, 128, 40},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig()
			off := false
			d := DirectionProfile{Padding: tc.pad, Fragment: tc.fragment, FrameMin: 400, FrameMax: 900, SmallNoPadding: tc.small}
			c.Transport = &TransportOptions{Mode: tc.mode, RateLimitEnabled: &off, Upload: d, Download: d}
			p, e := c.sessionPolicy(c.Clients[0])
			if e != nil {
				t.Fatal(e)
			}
			var buf bytes.Buffer
			tx, rx := NewWire(nil, &buf), NewWire(&buf, io.Discard)
			tx.policy, rx.policy = &p, &p
			client := c
			client.Mode = "client"
			s, e := attachTransport(context.Background(), tx, client, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			r, e := attachTransport(context.Background(), rx, c, nil, nil)
			if e != nil {
				t.Fatal(e)
			}
			packet := make([]byte, tc.n)
			copy(packet, ipPacket())
			binary.BigEndian.PutUint16(packet[2:4], uint16(tc.n))
			b := append([]byte{msgIP}, packet...)
			if e = tx.Write(b); e != nil {
				t.Fatal(e)
			}
			raw := append([]byte(nil), buf.Bytes()...)
			decoded, e := rx.Read()
			if e != nil || !bytes.Equal(b, decoded) {
				t.Fatalf("roundtrip %v", e)
			}
			if s.c.txIPPackets.Load() != 1 || r.c.rxIPPackets.Load() != 1 || s.c.txFrameBytes.Load() != r.c.rxFrameBytes.Load() {
				t.Fatal("counter mismatch")
			}
			if s.c.txFrameBytes.Load() != s.c.txPayload.Load()+s.c.txHeaders.Load()+s.c.txPadding.Load() {
				t.Fatal("TX decomposition mismatch")
			}
			if r.c.rxFrameBytes.Load() != r.c.rxPayload.Load()+r.c.rxHeaders.Load()+r.c.rxPadding.Load() {
				t.Fatal("RX decomposition mismatch")
			}
			reader := NewWire(bytes.NewReader(raw), nil)
			for {
				f, e := reader.readRaw()
				if e == io.EOF {
					break
				}
				if e != nil {
					t.Fatal(e)
				}
				exempt := tc.small > 0 && tc.n <= tc.small
				if tc.mode == "baseline" || exempt {
					if f[0] != msgIP {
						t.Fatal("baseline/small packet was enveloped")
					}
				} else if tc.fragment {
					if len(f)+4 > 900 {
						t.Fatal("fragment too large")
					}
					if tc.pad && len(f)+4 < 400 {
						t.Fatal("padded fragment too short")
					}
				}
			}
		})
	}
}
func TestTransportRejectMalformedFragments(t *testing.T) {
	valid := make([]byte, 36)
	valid[0], valid[1], valid[2] = msgEnvelope, 1, 3
	binary.BigEndian.PutUint32(valid[4:8], 1)
	binary.BigEndian.PutUint16(valid[8:10], 20)
	binary.BigEndian.PutUint16(valid[12:14], 20)
	for _, mutate := range []func([]byte){
		func(b []byte) { b[1] = 2 }, func(b []byte) { b[2] = 4 }, func(b []byte) { b[3] = 1 },
		func(b []byte) { b[4] = 0; b[7] = 0 }, func(b []byte) { b[8] = 0xff },
		func(b []byte) { b[11] = 1 }, func(b []byte) { b[13] = 19 }, func(b []byte) { b[15] = 1 },
	} {
		b := append([]byte(nil), valid...)
		mutate(b)
		s := &sessionTransport{mtu: 1400}
		if _, e := s.decodeEnvelope(b); e == nil {
			t.Fatalf("accepted malformed fragment %x", b[:16])
		}
	}
	first := append([]byte(nil), valid...)
	first[2] = 1
	binary.BigEndian.PutUint16(first[8:10], 40)
	s := &sessionTransport{mtu: 1400}
	if out, e := s.decodeEnvelope(first); e != nil || out != nil {
		t.Fatal("first fragment rejected")
	}
	if _, e := s.decodeEnvelope(first); e == nil {
		t.Fatal("duplicate offset accepted")
	}
}
func TestTransportConfigurationValidation(t *testing.T) {
	c := testConfig()
	p, e := c.sessionPolicy(c.Clients[0])
	if e != nil {
		t.Fatal(e)
	}
	if p.Transport.Mode != "baseline" || !p.Transport.RateLimitEnabled || p.Transport.BurstBytes != 65536 || p.StatsInterval != 10 {
		t.Fatal("defaults changed")
	}
	for _, bad := range []TransportOptions{
		{Mode: "rdp-video"}, {RateAccounting: "tcp"}, {BurstBytes: 100},
		{KeepaliveMin: 31}, {KeepaliveMin: 10, KeepaliveMax: 5},
		{ReassemblySeconds: 121},
		{Upload: DirectionProfile{FrameMin: 63}},
		{Upload: DirectionProfile{FrameMax: 16385}},
		{Download: DirectionProfile{JitterMaxMS: 101}},
		{Download: DirectionProfile{SmallNoPadding: 1401}},
	} {
		c.Transport = &bad
		if c.Validate() == nil {
			t.Fatalf("accepted invalid options %+v", bad)
		}
	}
	client := testConfig()
	client.Mode = "client"
	client.CIDR = "10.78.0.2/24"
	client.IPv4 = "10.78.0.2"
	client.UUID = testUUID
	client.ServerSHA256 = string(bytes.Repeat([]byte{'1'}, 64))
	client.Clients = nil
	client.Transport = &TransportOptions{}
	if client.Validate() == nil {
		t.Fatal("client accepted server-controlled settings")
	}
}

type corruptPolicyWriter struct{ io.Writer }

func (w corruptPolicyWriter) Write(b []byte) (int, error) {
	if len(b) > 42 && b[0] == msgAccept && b[1] == 2 {
		b = append([]byte(nil), b...)
		b[10] ^= 1
	}
	return w.Writer.Write(b)
}
func TestAuthenticatedPolicyRejectTampering(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	a.SetDeadline(time.Now().Add(2 * time.Second))
	b.SetDeadline(time.Now().Add(2 * time.Second))
	cfg := testConfig()
	done := make(chan struct{})
	go func() { defer close(done); defer a.Close(); _, _ = ServerAuth(NewWire(a, corruptPolicyWriter{a}), cfg) }()
	client := cfg
	client.Mode = "client"
	client.UUID = testUUID
	client.IPv4 = cfg.Clients[0].IPv4
	if _, e := ClientAuth(NewWire(b, b), client); e == nil {
		t.Fatal("tampered policy accepted")
	}
	<-done
}
func TestLegacyAuthCompatibility(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		a, b := net.Pipe()
		cfg := testConfig()
		if explicit {
			cfg.Transport = &TransportOptions{Mode: "baseline"}
		}
		a.SetDeadline(time.Now().Add(2 * time.Second))
		b.SetDeadline(time.Now().Add(2 * time.Second))
		done := make(chan error, 1)
		go func() { _, e := ServerAuth(NewWire(a, a), cfg); a.Close(); done <- e }()
		w := NewWire(b, b)
		challenge, e := w.Read()
		if e != nil {
			t.Fatal(e)
		}
		key, _ := UUIDBytes(testUUID)
		cn := make([]byte, 32)
		ip := netip.MustParseAddr(cfg.Clients[0].IPv4).As4()
		auth := append([]byte{msgAuth, 1}, publicID(key)...)
		auth = append(auth, cn...)
		auth = append(auth, ip[:]...)
		auth = append(auth, proof(key, "client", challenge[2:], cn, ip[:])...)
		if e = w.Write(auth); e != nil {
			t.Fatal(e)
		}
		reply, e := w.Read()
		if explicit {
			if e == nil {
				t.Fatal("legacy client accepted explicit new policy")
			}
		} else if e != nil || len(reply) != 40 || reply[1] != 1 {
			t.Fatal("legacy compatibility broken")
		}
		se := <-done
		if (se != nil) != explicit {
			t.Fatalf("server result %v", se)
		}
		b.Close()
	}
}
