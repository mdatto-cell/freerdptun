package tunnel

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

// SelfTest exercises real FreeRDP server/client and DVC on loopback without a TUN,
// privileges, routes, firewall changes or external network test.
func SelfTest(parent context.Context, c Config) error {
	if c.Mode != "server" {
		return errors.New("selftest requires Linux server config")
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	b, e := os.ReadFile(c.Certificate)
	if e != nil {
		return e
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return errors.New("invalid certificate PEM")
	}
	cert, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		return e
	}
	hash := sha256.Sum256(cert.Raw)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	defer listener.Close()
	go func() { <-ctx.Done(); listener.Close() }()
	serverResult := make(chan error, 1)
	go func() {
		conn, e := listener.Accept()
		if e != nil {
			serverResult <- e
			return
		}
		f, e := peerFile(conn)
		conn.Close()
		if e != nil {
			serverResult <- e
			return
		}
		p, e := StartBridge(ctx, c.Bridge, []string{"--server-fd", "3", "--cert", c.Certificate, "--key", c.PrivateKey}, []*os.File{f})
		f.Close()
		if e != nil {
			serverResult <- e
			return
		}
		defer p.Close()
		if _, e = ServerAuth(p.Wire, c); e != nil {
			serverResult <- e
			return
		}
		for i := 0; i < 5; i++ {
			data, e := p.Wire.Read()
			if e != nil {
				serverResult <- e
				return
			}
			if e = p.Wire.Write(data); e != nil {
				serverResult <- e
				return
			}
		}
		// Wait for an explicit acknowledgment before closing stdout/transport.
		ack, e := p.Wire.Read()
		if e == nil && !bytes.Equal(ack, []byte{msgPong}) {
			e = errors.New("invalid echo ack")
		}
		serverResult <- e
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	p, e := StartBridge(ctx, c.Bridge, []string{"--host", host, "--port", port, "--pin", hex.EncodeToString(hash[:])}, nil)
	if e != nil {
		return e
	}
	defer p.Close()
	client := c
	client.Mode = "client"
	client.UUID = c.Clients[0].UUID
	client.IPv4 = c.Clients[0].IPv4
	if _, e = ClientAuth(p.Wire, client); e != nil {
		return fmt.Errorf("auth: %w", e)
	}
	for _, n := range []int{1, 64, 1401, 17000, 65535} {
		data := make([]byte, n)
		if _, e = rand.Read(data); e != nil {
			return e
		}
		if e = p.Wire.Write(data); e != nil {
			return e
		}
		reply, e := p.Wire.Read()
		if e != nil {
			return e
		}
		if !bytes.Equal(data, reply) {
			return fmt.Errorf("DVC corruption at size %d", n)
		}
		fmt.Printf("PASS real RDP/DVC echo %d bytes\n", n)
	}
	if e = p.Wire.Write([]byte{msgPong}); e != nil {
		return e
	}
	select {
	case e = <-serverResult:
	case <-ctx.Done():
		e = ctx.Err()
	}
	if e == nil {
		fmt.Println("PASS UUID mutual proof + certificate pin + real RDP/DVC (no TUN)")
	}
	return e
}
