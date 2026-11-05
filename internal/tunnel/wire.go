package tunnel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sync"
)

const (
	msgChallenge byte = 1
	msgAuth      byte = 2
	msgAccept    byte = 3
	msgIP        byte = 4
	msgPing      byte = 5
	msgPong      byte = 6
	msgEnvelope  byte = 7
	maxFrame          = 65535
)

type Wire struct {
	r       io.Reader
	w       io.Writer
	mu      sync.Mutex
	policy  *SessionPolicy
	session *sessionTransport
}

func NewWire(r io.Reader, w io.Writer) *Wire { return &Wire{r: r, w: w} }
func (w *Wire) Read() ([]byte, error) {
	if w.session != nil {
		return w.session.read(w)
	}
	return w.readRaw()
}
func (w *Wire) readRaw() ([]byte, error) {
	var h [4]byte
	if _, err := io.ReadFull(w.r, h[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n < 1 || n > maxFrame {
		return nil, errors.New("invalid frame size")
	}
	b := make([]byte, int(n))
	_, err := io.ReadFull(w.r, b)
	return b, err
}
func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, e := w.Write(b)
		if e != nil {
			return e
		}
		if n <= 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
func (w *Wire) Write(b []byte) error {
	if len(b) < 1 || len(b) > maxFrame {
		return errors.New("invalid output frame size")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.session != nil {
		return w.session.write(w, b)
	}
	return w.writeRaw(b)
}
func (w *Wire) writeRaw(b []byte) error {
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if err := writeFull(w.w, h[:]); err != nil {
		return err
	}
	return writeFull(w.w, b)
}
func proof(key []byte, role string, fields ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("FreeRDPTun/1/" + role + "\x00"))
	for _, f := range fields {
		m.Write(f)
	}
	return m.Sum(nil)
}
func publicID(key []byte) []byte {
	h := sha256.New()
	h.Write([]byte("FreeRDPTun/1/client-id\x00"))
	h.Write(key)
	return h.Sum(nil)[:16]
}
func ServerAuth(w *Wire, c Config) (Client, error) {
	var none Client
	sn := make([]byte, 32)
	if _, e := rand.Read(sn); e != nil {
		return none, e
	}
	if e := w.Write(append([]byte{msgChallenge, 1}, sn...)); e != nil {
		return none, e
	}
	b, e := w.Read()
	if e != nil {
		return none, e
	}
	// type + version + derived public ID(16) + nonce(32) + IPv4(4) + proof(32).
	// The UUID secret itself is never sent, even inside TLS.
	if len(b) != 86 || b[0] != msgAuth || (b[1] != 1 && b[1] != 2) {
		return none, errors.New("invalid auth")
	}
	id, cn, requested, received := b[2:18], b[18:50], b[50:54], b[54:]
	var found *Client
	for i := range c.Clients {
		key, _ := UUIDBytes(c.Clients[i].UUID)
		if hmac.Equal(publicID(key), id) {
			found = &c.Clients[i]
			break
		}
	}
	if found == nil {
		return none, errors.New("authentication refused")
	}
	key, _ := UUIDBytes(found.UUID)
	version := b[1]
	role := "client"
	if version == 2 {
		role = "client-v2"
	}
	expectedIP := netip.MustParseAddr(found.IPv4).As4()
	if !hmac.Equal(expectedIP[:], requested) ||
		!hmac.Equal(proof(key, role, sn, cn, requested), received) {
		return none, errors.New("authentication refused")
	}
	var params [6]byte
	copy(params[:4], expectedIP[:])
	binary.BigEndian.PutUint16(params[4:], uint16(c.MTU))
	if version == 2 {
		p, e := c.sessionPolicy(*found)
		if e != nil {
			return none, e
		}
		data, e := json.Marshal(p)
		if e != nil {
			return none, e
		}
		if len(data) > 4096 {
			return none, errors.New("transport policy exceeds bound")
		}
		reply := append([]byte{msgAccept, 2}, params[:]...)
		reply = binary.BigEndian.AppendUint16(reply, uint16(len(data)))
		reply = append(reply, data...)
		reply = append(reply, proof(key, "server-v2", sn, cn, params[:], data)...)
		if e = w.Write(reply); e != nil {
			return none, e
		}
		w.policy = &p
		return *found, nil
	}
	if c.Transport != nil || c.StatsIntervalSeconds != nil {
		return none, errors.New("transport/stats settings require client >=0.1.3")
	}
	reply := append([]byte{msgAccept, 1}, params[:]...)
	reply = append(reply, proof(key, "server", sn, cn, params[:])...)
	if e = w.Write(reply); e != nil {
		return none, e
	}
	return *found, nil
}
func ClientAuth(w *Wire, c Config) (int, error) {
	key, err := UUIDBytes(c.UUID)
	if err != nil {
		return 0, err
	}
	b, err := w.Read()
	if err != nil {
		return 0, err
	}
	if len(b) != 34 || b[0] != msgChallenge || b[1] != 1 {
		return 0, errors.New("invalid server challenge")
	}
	sn := b[2:]
	cn := make([]byte, 32)
	if _, err = rand.Read(cn); err != nil {
		return 0, err
	}
	ip := netip.MustParseAddr(c.IPv4).As4()
	reply := append([]byte{msgAuth, 2}, publicID(key)...)
	reply = append(reply, cn...)
	reply = append(reply, ip[:]...)
	reply = append(reply, proof(key, "client-v2", sn, cn, ip[:])...)
	if err = w.Write(reply); err != nil {
		return 0, err
	}
	b, err = w.Read()
	if err != nil {
		return 0, err
	}
	if len(b) < 43 || b[0] != msgAccept || b[1] != 2 || !hmac.Equal(b[2:6], ip[:]) {
		return 0, errors.New("invalid server proof")
	}
	n := int(binary.BigEndian.Uint16(b[8:10]))
	if n == 0 || n > 4096 || len(b) != 10+n+32 || !hmac.Equal(b[10+n:], proof(key, "server-v2", sn, cn, b[2:8], b[10:10+n])) {
		return 0, errors.New("invalid authenticated server policy")
	}
	mtu := int(binary.BigEndian.Uint16(b[6:8]))
	if mtu < 576 || mtu > 9000 {
		return 0, errors.New("invalid negotiated MTU")
	}
	if mtu != c.MTU {
		return 0, fmt.Errorf("MTU mismatch: server=%d client=%d; set same mtu", mtu, c.MTU)
	}
	p, e := decodePolicy(b[10:10+n], mtu)
	if e != nil {
		return 0, fmt.Errorf("server policy: %w", e)
	}
	w.policy = p
	return mtu, nil
}
func NewUUID() (string, error) {
	b := make([]byte, 16)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	s := hex.EncodeToString(b)
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:], nil
}
func IPv4Packet(b []byte, mtu int) (src, dst netip.Addr, err error) {
	if len(b) < 20 || len(b) > mtu || b[0]>>4 != 4 {
		return src, dst, errors.New("invalid IPv4 packet")
	}
	ihl := int(b[0]&15) * 4
	if ihl < 20 || ihl > len(b) || int(binary.BigEndian.Uint16(b[2:4])) != len(b) {
		return src, dst, errors.New("invalid IPv4 length")
	}
	return netip.AddrFrom4([4]byte(b[12:16])), netip.AddrFrom4([4]byte(b[16:20])), nil
}
