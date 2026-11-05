package tunnel

import (
	"bytes"
	"encoding/asn1"
	"encoding/binary"
	"testing"
)

// Added for the user's sandbox. Not executed during preparation of 0.1.2.
func initialRequest(protocols uint32, cookie string, correlation bool) []byte {
	p := []byte{3, 0, 0, 0, 0, 0xe0, 0, 0, 0x12, 0x34, 0}
	if cookie != "" {
		p = append(p, []byte("Cookie: mstshash="+cookie+"\r\n")...)
	}
	n := len(p)
	p = append(p, 1, 0, 8, 0, 0, 0, 0, 0)
	binary.LittleEndian.PutUint32(p[n+4:], protocols)
	if correlation {
		p[n+1] = 8
		p = append(p, 6, 0, 36, 0)
		p = append(p, make([]byte, 32)...)
	}
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	p[4] = byte(len(p) - 5)
	return p
}

func TestRDPRequestProtocols(t *testing.T) {
	for _, tc := range []struct {
		protocols   uint32
		cookie      string
		correlation bool
		selected    uint32
	}{
		{1, "", false, 0},
		{1, "freerdp", false, 0},
		{3, "user", false, 2},
		{11, "user", true, 2},
		{9, "", true, 8},
	} {
		p := initialRequest(tc.protocols, tc.cookie, tc.correlation)
		got, err := requestProtocols(p)
		if err != nil || got != tc.protocols || selectNLA(got) != tc.selected {
			t.Fatalf("protocols=%d: got=%d err=%v", tc.protocols, got, err)
		}
		read, err := readInitialRDP(bytes.NewReader(p))
		if err != nil || !bytes.Equal(read, p) {
			t.Fatalf("read changed request: %v", err)
		}
		if tc.selected != 0 {
			cc := rdpConfirm(p, tc.selected)
			if len(cc) != 19 || cc[5] != 0xd0 || !bytes.Equal(cc[6:8], p[8:10]) ||
				binary.LittleEndian.Uint32(cc[15:19]) != tc.selected {
				t.Fatal("invalid connection confirm")
			}
		}
	}
	p := initialRequest(11, "test", true)
	for n := 0; n < len(p); n++ {
		if _, err := requestProtocols(p[:n]); err == nil {
			t.Fatalf("accepted truncated packet length %d", n)
		}
	}
	bad := append([]byte(nil), p...)
	bad[4]--
	if _, err := requestProtocols(bad); err == nil {
		t.Fatal("accepted wrong X.224 LI")
	}
	bad = append(p, 0)
	if _, err := requestProtocols(bad); err == nil {
		t.Fatal("accepted trailing bytes")
	}
}

func TestCredSSPRefusalEncoding(t *testing.T) {
	for _, version := range []int{3, 4, 5, 6, 7} {
		p, err := credSSPRefusal(version)
		if err != nil {
			t.Fatal(err)
		}
		read, err := readCredSSP(bytes.NewReader(p))
		if err != nil || !bytes.Equal(read, p) {
			t.Fatalf("DER read: %v", err)
		}
		var reply credSSPRequest
		rest, err := asn1.Unmarshal(p, &reply)
		want := version
		if want > 6 {
			want = 6
		}
		if want == 5 {
			want = 4
		}
		if err != nil || len(rest) != 0 || reply.Version != want || reply.Error != 0xc000006d {
			t.Fatalf("bad refusal v%d: %+v %v", version, reply, err)
		}
	}
	if _, err := credSSPRefusal(2); err == nil {
		t.Fatal("CredSSP v2 cannot encode supported refusal")
	}
	for _, p := range [][]byte{
		{0x30, 0x80}, {0x30, 0x83, 1, 0, 0}, {0x30, 0x81, 0x7f},
		{0x30, 0x82, 0, 0x80}, {0x30, 0x82, 0x40, 1}, {0x31, 1, 0}, {0x30, 4, 1},
	} {
		if _, err := readCredSSP(bytes.NewReader(p)); err == nil {
			t.Fatalf("accepted bad DER %x", p)
		}
	}
}

func TestStartupPolicyDefaults(t *testing.T) {
	c := Config{Mode: "server"}
	d := c.decoyPolicy()
	if !c.autoTUN() || !d.Enabled || d.HandshakeSeconds != 10 || d.MaxConnections != 16 {
		t.Fatal("old configuration does not enable new defaults")
	}
	no := false
	c.AutoConfigureTUN = &no
	c.RDPDecoy = &DecoyConfig{Enabled: false}
	if c.autoTUN() || c.decoyPolicy().Enabled {
		t.Fatal("explicit disable ignored")
	}
	c.Mode = "client"
	if c.decoyPolicy().Enabled {
		t.Fatal("client must not run RDP refusal listener")
	}
}
