package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"time"
)

// Restores the bounded early refusal used by RDPTun 0.3/0.4. This is not a
// desktop/login server, does not issue an NTLM challenge and never logs tokens.
// Our FreeRDP client offers TLS only and does not enter this NLA branch.
const maxInitialRDP = 260

func initialRDPLength(h []byte) (int, error) {
	if len(h) < 4 {
		return 0, errors.New("short TPKT header")
	}
	n := int(binary.BigEndian.Uint16(h[2:4]))
	if h[0] != 3 || h[1] != 0 || n < 11 || n > maxInitialRDP {
		return 0, errors.New("invalid initial TPKT length/header")
	}
	return n, nil
}
func readInitialRDP(r io.Reader) ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	n, err := initialRDPLength(h[:])
	if err != nil {
		return nil, err
	}
	p := make([]byte, n)
	copy(p, h[:])
	if _, err = io.ReadFull(r, p[4:]); err != nil {
		return nil, err
	}
	if _, err = requestProtocols(p); err != nil {
		return nil, err
	}
	return p, nil
}
func requestProtocols(p []byte) (uint32, error) {
	n, err := initialRDPLength(p)
	if err != nil {
		return 0, err
	}
	if len(p) != n || int(p[4]) != n-5 || p[5] != 0xe0 || p[6] != 0 || p[7] != 0 || p[10] != 0 {
		return 0, errors.New("invalid X.224 connection request")
	}
	tail := p[11:]
	if bytes.HasPrefix(tail, []byte("Cookie: ")) {
		end := bytes.Index(tail, []byte("\r\n"))
		if end < 0 {
			return 0, errors.New("unterminated RDP cookie")
		}
		tail = tail[end+2:]
	}
	if len(tail) == 0 {
		return 0, nil
	}
	if len(tail) < 8 || tail[0] != 1 || binary.LittleEndian.Uint16(tail[2:4]) != 8 {
		return 0, errors.New("invalid RDP negotiation request")
	}
	extra := tail[8:]
	if tail[1]&8 != 0 {
		if len(extra) != 36 || extra[0] != 6 || binary.LittleEndian.Uint16(extra[2:4]) != 36 {
			return 0, errors.New("invalid RDP correlation info")
		}
	} else if len(extra) != 0 {
		return 0, errors.New("unexpected RDP negotiation bytes")
	}
	return binary.LittleEndian.Uint32(tail[4:8]), nil
}
func selectNLA(protocols uint32) uint32 {
	if protocols&2 != 0 {
		return 2
	}
	if protocols&8 != 0 {
		return 8
	}
	return 0
}
func rdpConfirm(req []byte, selected uint32) []byte {
	p := []byte{3, 0, 0, 19, 14, 0xd0, 0, 0, 0, 0, 0, 2, 0, 8, 0, 0, 0, 0, 0}
	copy(p[6:8], req[8:10])
	binary.LittleEndian.PutUint32(p[15:19], selected)
	return p
}

func readCredSSP(r io.Reader) ([]byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return nil, err
	}
	if h[0] != 0x30 {
		return nil, errors.New("expected CredSSP DER sequence")
	}
	header := append([]byte(nil), h[:]...)
	n := int(h[1])
	if h[1]&0x80 != 0 {
		count := int(h[1] & 0x7f)
		if count < 1 || count > 2 {
			return nil, errors.New("invalid CredSSP DER length")
		}
		b := make([]byte, count)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		if b[0] == 0 {
			return nil, errors.New("nonminimal CredSSP DER length")
		}
		n = 0
		for _, v := range b {
			n = n<<8 | int(v)
		}
		if n < 128 {
			return nil, errors.New("nonminimal CredSSP DER length")
		}
		header = append(header, b...)
	}
	if n < 1 || n > 16384 {
		return nil, errors.New("CredSSP message exceeds bound")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, err
	}
	return append(header, b...), nil
}

type credSSPRequest struct {
	Version int           `asn1:"explicit,tag:0"`
	Nego    asn1.RawValue `asn1:"optional,explicit,tag:1"`
	Auth    asn1.RawValue `asn1:"optional,explicit,tag:2"`
	Pub     asn1.RawValue `asn1:"optional,explicit,tag:3"`
	Error   int64         `asn1:"optional,explicit,tag:4"`
	Nonce   []byte        `asn1:"optional,explicit,tag:5"`
}

func credSSPRefusal(version int) ([]byte, error) {
	if version < 3 {
		return nil, errors.New("CredSSP v2 has no supported errorCode response")
	}
	if version > 6 {
		version = 6
	}
	if version == 5 {
		version = 4
	}
	return asn1.Marshal(struct {
		Version int   `asn1:"explicit,tag:0"`
		Error   int64 `asn1:"explicit,tag:4"`
	}{version, 0xc000006d})
}
func refuseNLA(ctx context.Context, raw net.Conn, cfg *tls.Config, selected uint32, deadline time.Time) error {
	if err := raw.SetDeadline(deadline); err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	req, err := readInitialRDP(raw)
	if err != nil {
		return err
	}
	protocols, err := requestProtocols(req)
	if err != nil {
		return err
	}
	if selected == 0 || selected != selectNLA(protocols) {
		return errors.New("NLA selection mismatch")
	}
	if err := writeFull(raw, rdpConfirm(req, selected)); err != nil {
		return err
	}
	t := tls.Server(raw, cfg)
	if err := t.HandshakeContext(ctx); err != nil {
		return err
	}
	b, err := readCredSSP(t)
	if err != nil {
		return err
	}
	var request credSSPRequest
	rest, err := asn1.Unmarshal(b, &request)
	if err != nil || len(rest) != 0 || request.Version < 2 {
		return errors.New("invalid CredSSP TSRequest")
	}
	// Opaque negotiation tokens are never interpreted or persisted. In
	// particular no NTLM challenge or delegated-credential phase is performed.
	reply, err := credSSPRefusal(request.Version)
	if err != nil {
		return err
	}
	return writeFull(t, reply)
}
