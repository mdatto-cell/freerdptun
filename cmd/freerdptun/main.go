package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"freerdptun/internal/tunnel"
)

func writeNew(path string, b []byte) error {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return e
	}
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	return f.Close()
}
func initServer(args []string) error {
	fs := flag.NewFlagSet("init-server", flag.ContinueOnError)
	dir := fs.String("dir", "./state", "new output directory")
	address := fs.String("server", "SERVER_IP:3389", "public server host:port for client template")
	if e := fs.Parse(args); e != nil {
		return e
	}
	if e := os.Mkdir(*dir, 0700); e != nil {
		return fmt.Errorf("choose a NEW directory: %w", e)
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return e
	}
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if e != nil {
		return e
	}
	cert := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "FreeRDPTun Test Server"},
		NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, cert, cert, &key.PublicKey, key)
	if e != nil {
		return e
	}
	hash := sha256.Sum256(der)
	if e = writeNew(filepath.Join(*dir, "server.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})); e != nil {
		return e
	}
	if e = writeNew(filepath.Join(*dir, "server.key"), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})); e != nil {
		return e
	}
	u, e := tunnel.NewUUID()
	if e != nil {
		return e
	}
	rateEnabled := true
	statsInterval := 10
	server := tunnel.Config{Mode: "server", Address: "0.0.0.0:3389", TUN: "frdptun0", CIDR: "10.78.0.1/24",
		MTU: 1400, Certificate: "server.crt", PrivateKey: "server.key",
		UploadBPS: 100000000, DownloadBPS: 100000000, MaxConnections: 32, HandshakeSeconds: 20, IdleSeconds: 60, QueuePackets: 256,
		RDPDecoy: &tunnel.DecoyConfig{Enabled: true, HandshakeSeconds: 10, MaxConnections: 16},
		Transport: &tunnel.TransportOptions{Mode: "baseline", RateLimitEnabled: &rateEnabled, RateAccounting: "ip", BurstBytes: 65536,
			KeepaliveMin: 10, KeepaliveMax: 10, ReassemblySeconds: 30,
			Upload:   tunnel.DirectionProfile{FrameMin: 1200, FrameMax: 1800, SmallNoPadding: 128},
			Download: tunnel.DirectionProfile{FrameMin: 1200, FrameMax: 1800, SmallNoPadding: 128}},
		StatsIntervalSeconds: &statsInterval,
		Clients:              []tunnel.Client{{ID: "test-01", UUID: u, IPv4: "10.78.0.2"}}}
	client := tunnel.Config{Mode: "client", Address: *address, TUN: "frdptun0", CIDR: "10.78.0.2/24", IPv4: "10.78.0.2",
		MTU: 1400, UUID: u, ServerSHA256: hex.EncodeToString(hash[:]),
		MaxConnections: 32, HandshakeSeconds: 20, IdleSeconds: 60, QueuePackets: 256}
	for name, c := range map[string]tunnel.Config{"server.json": server, "client.json": client} {
		b, _ := json.MarshalIndent(c, "", "  ")
		b = append(b, '\n')
		if e = writeNew(filepath.Join(*dir, name), b); e != nil {
			return e
		}
	}
	fmt.Printf("Created %s (NEW test UUID, certificate and configs).\nServer SHA256: %x\nCopy only client.json to the client. UUID is a SECRET.\n", *dir, hash)
	return nil
}
func fingerprint(args []string) error {
	fs := flag.NewFlagSet("fingerprint", flag.ContinueOnError)
	path := fs.String("cert", "server.crt", "PEM certificate")
	if e := fs.Parse(args); e != nil {
		return e
	}
	b, e := os.ReadFile(*path)
	if e != nil {
		return e
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return fmt.Errorf("invalid PEM")
	}
	c, e := x509.ParseCertificate(block.Bytes)
	if e != nil {
		return e
	}
	fmt.Printf("%x\n", sha256.Sum256(c.Raw))
	return nil
}
func run() error {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: freerdptun {init-server|run|check|new-uuid|fingerprint|selftest|version}")
	}
	switch strings.ToLower(os.Args[1]) {
	case "version", "--version":
		fmt.Println("FreeRDPTun", tunnel.Version)
		return nil
	case "new-uuid":
		s, e := tunnel.NewUUID()
		if e == nil {
			fmt.Println(s)
		}
		return e
	case "init-server":
		return initServer(os.Args[2:])
	case "fingerprint":
		return fingerprint(os.Args[2:])
	case "run", "check", "selftest":
		fs := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
		path := fs.String("config", "server.json", "JSON configuration")
		if e := fs.Parse(os.Args[2:]); e != nil {
			return e
		}
		c, e := tunnel.Load(*path)
		if e != nil {
			return e
		}
		if os.Args[1] == "check" {
			fmt.Println("Configuration OK")
			return nil
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if os.Args[1] == "selftest" {
			return tunnel.SelfTest(ctx, c)
		}
		return tunnel.Run(ctx, c)
	default:
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}
func main() {
	if e := run(); e != nil {
		log.Print(e)
		os.Exit(1)
	}
}
