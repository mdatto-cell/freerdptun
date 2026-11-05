package tunnel

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const Version = "0.1.3"

type DecoyConfig struct {
	Enabled          bool `json:"enabled"`
	HandshakeSeconds int  `json:"handshake_timeout_seconds,omitempty"`
	MaxConnections   int  `json:"max_connections,omitempty"`
}

type Client struct {
	ID          string `json:"id"`
	UUID        string `json:"uuid"`
	IPv4        string `json:"ipv4"`
	UploadBPS   int64  `json:"upload_bps"`
	DownloadBPS int64  `json:"download_bps"`
}
type Config struct {
	Mode                 string            `json:"mode"`
	Address              string            `json:"address"`
	TUN                  string            `json:"tun"`
	CIDR                 string            `json:"cidr"`
	MTU                  int               `json:"mtu"`
	Bridge               string            `json:"bridge"`
	Certificate          string            `json:"certificate"`
	PrivateKey           string            `json:"private_key"`
	ServerSHA256         string            `json:"server_sha256"`
	UUID                 string            `json:"uuid"`
	IPv4                 string            `json:"ipv4"`
	UploadBPS            int64             `json:"total_upload_bps"`
	DownloadBPS          int64             `json:"total_download_bps"`
	MaxConnections       int               `json:"max_connections"`
	HandshakeSeconds     int               `json:"handshake_timeout_seconds"`
	IdleSeconds          int               `json:"idle_timeout_seconds"`
	QueuePackets         int               `json:"queue_packets"`
	Clients              []Client          `json:"clients"`
	AutoConfigureTUN     *bool             `json:"auto_configure_tun,omitempty"`
	RDPDecoy             *DecoyConfig      `json:"rdp_decoy,omitempty"`
	Transport            *TransportOptions `json:"transport,omitempty"`
	StatsIntervalSeconds *int              `json:"stats_interval_seconds,omitempty"`
}

func (c Config) autoTUN() bool { return c.AutoConfigureTUN == nil || *c.AutoConfigureTUN }
func (c Config) decoyPolicy() DecoyConfig {
	if c.Mode != "server" {
		return DecoyConfig{}
	}
	d := DecoyConfig{Enabled: true, HandshakeSeconds: 10, MaxConnections: 16}
	if c.RDPDecoy != nil {
		d = *c.RDPDecoy
		if d.HandshakeSeconds == 0 {
			d.HandshakeSeconds = 10
		}
		if d.MaxConnections == 0 {
			d.MaxConnections = 16
		}
	}
	return d
}
func UUIDBytes(s string) ([]byte, error) {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return nil, errors.New("UUID must have canonical 8-4-4-4-12 format")
	}
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		return nil, errors.New("invalid UUID")
	}
	allZero := true
	for _, v := range b {
		allZero = allZero && v == 0
	}
	if allZero {
		return nil, errors.New("zero UUID is forbidden")
	}
	return b, nil
}
func Load(path string) (Config, error) {
	var c Config
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1024*1024))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return c, errors.New("trailing JSON data")
	}
	if c.MTU == 0 {
		c.MTU = 1400
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = 32
	}
	if c.HandshakeSeconds == 0 {
		c.HandshakeSeconds = 20
	}
	if c.IdleSeconds == 0 {
		c.IdleSeconds = 60
	}
	if c.QueuePackets == 0 {
		c.QueuePackets = 256
	}
	base, _ := filepath.Abs(filepath.Dir(path))
	for _, p := range []*string{&c.Certificate, &c.PrivateKey, &c.Bridge} {
		if *p != "" && !filepath.IsAbs(*p) {
			*p = filepath.Join(base, *p)
		}
	}
	if c.Bridge == "" {
		exe, err := os.Executable()
		if err != nil {
			return c, fmt.Errorf("cannot locate executable directory; set an absolute bridge path: %w", err)
		}
		c.Bridge = filepath.Join(filepath.Dir(exe), "rdpt-bridge")
		if runtime.GOOS == "windows" {
			c.Bridge += ".exe"
		}
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	if c.Mode != "server" && c.Mode != "client" {
		return errors.New("mode must be server or client")
	}
	if _, port, err := net.SplitHostPort(c.Address); err != nil {
		return fmt.Errorf("address: %w", err)
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return errors.New("address requires numeric TCP port 1..65535")
	}
	if c.TUN == "" || len(c.TUN) > 15 || strings.ContainsAny(c.TUN, " /\\\x00") {
		return errors.New("invalid TUN name (1..15 characters)")
	}
	prefix, err := netip.ParsePrefix(c.CIDR)
	if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() {
		return errors.New("cidr must be IPv4 CIDR")
	}
	if prefix.Bits() <= 30 && (prefix.Addr() == prefix.Masked().Addr() || isBroadcast(prefix, prefix.Addr())) {
		return errors.New("cidr must specify a host address, not network/broadcast")
	}
	if c.MTU < 576 || c.MTU > 9000 {
		return errors.New("mtu must be 576..9000")
	}
	if c.HandshakeSeconds < 3 || c.HandshakeSeconds > 120 || c.IdleSeconds < 15 ||
		c.IdleSeconds > 3600 || c.MaxConnections < 1 || c.MaxConnections > 256 ||
		c.QueuePackets < 16 || c.QueuePackets > 4096 {
		return errors.New("invalid timeout, connection or queue limit")
	}
	if !validRate(c.UploadBPS) || !validRate(c.DownloadBPS) {
		return errors.New("rate must be 0..1000000000000 bits/s")
	}
	if c.Mode == "client" {
		if c.Transport != nil || c.StatsIntervalSeconds != nil {
			return errors.New("transport and stats_interval_seconds are server-controlled; omit them on client")
		}
		if c.RDPDecoy != nil {
			return errors.New("rdp_decoy is server-only")
		}
		if _, err = UUIDBytes(c.UUID); err != nil {
			return err
		}
		ip, e := netip.ParseAddr(c.IPv4)
		if e != nil || !ip.Is4() || ip != prefix.Addr() {
			return errors.New("ipv4 must match address in cidr")
		}
		pin, e := hex.DecodeString(c.ServerSHA256)
		if e != nil || len(pin) != 32 {
			return errors.New("server_sha256: required 64 hex characters (DER leaf SHA256)")
		}
		if len(c.Clients) != 0 {
			return errors.New("clients list is server-only")
		}
		return nil
	}
	if runtime.GOOS == "windows" {
		return errors.New("server is Linux-only")
	}
	if c.Certificate == "" || c.PrivateKey == "" || len(c.Clients) == 0 {
		return errors.New("server needs certificate, private_key and clients")
	}
	d := c.decoyPolicy()
	if d.HandshakeSeconds < 3 || d.HandshakeSeconds > 120 || d.MaxConnections < 1 || d.MaxConnections > 256 {
		return errors.New("invalid rdp_decoy timeout or connection limit")
	}
	if _, err := c.sessionPolicy(Client{}); err != nil {
		return err
	}
	ids, uuids, ips := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, cl := range c.Clients {
		key, e := UUIDBytes(cl.UUID)
		if e != nil {
			return fmt.Errorf("client %s: %w", cl.ID, e)
		}
		u := hex.EncodeToString(key)
		ip, e := netip.ParseAddr(cl.IPv4)
		if cl.ID == "" || ids[cl.ID] || uuids[u] || ips[cl.IPv4] {
			return errors.New("empty or duplicate client id/UUID/IP")
		}
		if e != nil || !ip.Is4() || !prefix.Contains(ip) || ip == prefix.Addr() ||
			ip == prefix.Masked().Addr() || !ip.IsGlobalUnicast() || isBroadcast(prefix, ip) {
			return fmt.Errorf("invalid client IPv4 %s", cl.IPv4)
		}
		if !validRate(cl.UploadBPS) || !validRate(cl.DownloadBPS) {
			return errors.New("client rate must be 0..1000000000000 bits/s")
		}
		ids[cl.ID], uuids[u], ips[cl.IPv4] = true, true, true
	}
	return nil
}
func isBroadcast(prefix netip.Prefix, ip netip.Addr) bool {
	if prefix.Bits() > 30 {
		return false
	}
	base := prefix.Masked().Addr().As4()
	n := binary.BigEndian.Uint32(base[:])
	n |= ^uint32(0) >> uint(prefix.Bits())
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], n)
	return ip == netip.AddrFrom4(b)
}
