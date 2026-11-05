package tunnel

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func itoa(n int) string                 { return strconv.Itoa(n) }
func prefixBits(s string) string        { p, _ := netip.ParsePrefix(s); return itoa(p.Bits()) }
func newTimerSeconds(n int) *time.Timer { return time.NewTimer(time.Duration(n) * time.Second) }

type active struct {
	client Client
	queue  chan []byte
	cancel context.CancelFunc
	drops  atomic.Uint64
}
type hub struct {
	mu    sync.RWMutex
	peers map[netip.Addr]*active
}

func (h *hub) add(a *active) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	ip := netip.MustParseAddr(a.client.IPv4)
	if h.peers[ip] != nil {
		return errDuplicate
	}
	h.peers[ip] = a
	return nil
}
func (h *hub) remove(a *active) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ip := netip.MustParseAddr(a.client.IPv4)
	if h.peers[ip] == a {
		delete(h.peers, ip)
	}
}
func (h *hub) route(packet []byte, mtu int) {
	_, dst, e := IPv4Packet(packet, mtu)
	if e != nil {
		return
	}
	h.mu.RLock()
	a := h.peers[dst]
	if a != nil {
		select {
		case a.queue <- append([]byte{msgIP}, packet...):
		default:
			a.drops.Add(1)
		}
	}
	h.mu.RUnlock()
}
func tunReader(ctx context.Context, t Device, consume func([]byte)) error {
	b := make([]byte, 65536)
	for {
		n, e := t.Read(b)
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		if n > 0 {
			consume(b[:n])
		}
	}
}
func writer(ctx context.Context, w *Wire, queue <-chan []byte, local, global *Pacer) error {
	if w.session != nil {
		return w.session.runWriter(w, queue)
	}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if e := w.Write([]byte{msgPing}); e != nil {
				return e
			}
		case b := <-queue:
			if len(b) == 0 {
				continue
			}
			if b[0] == msgIP {
				if e := local.Wait(ctx, len(b)-1); e != nil {
					return e
				}
				if e := global.Wait(ctx, len(b)-1); e != nil {
					return e
				}
			}
			if e := w.Write(b); e != nil {
				return e
			}
		}
	}
}
func watchIdle(ctx context.Context, cancel context.CancelFunc, last *atomic.Int64, seconds int) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(time.Unix(0, last.Load())) > time.Duration(seconds)*time.Second {
				cancel()
				return
			}
		}
	}
}
func receiver(ctx context.Context, w *Wire, t Device, queue chan<- []byte, policy *ipPolicy,
	mtu int, local, global *Pacer, last *atomic.Int64) error {
	for {
		b, e := w.Read()
		if e != nil {
			return e
		}
		last.Store(time.Now().UnixNano())
		switch b[0] {
		case msgIP:
			src, dst, e := IPv4Packet(b[1:], mtu)
			if e != nil {
				return e
			}
			allowed, e := policy.incoming(src, dst)
			if e != nil {
				return e
			}
			if !allowed {
				continue
			}
			if w.session == nil {
				if e = local.Wait(ctx, len(b)-1); e != nil {
					return e
				}
				if e = global.Wait(ctx, len(b)-1); e != nil {
					return e
				}
			}
			n, e := t.Write(b[1:])
			if e != nil {
				return e
			}
			if n != len(b)-1 {
				return io.ErrShortWrite
			}
			if s := w.session; s != nil {
				s.c.rxTunPackets.Add(1)
				s.c.rxTunBytes.Add(uint64(n))
			}
		case msgPing:
			if len(b) != 1 {
				return errors.New("invalid ping")
			}
			select {
			case queue <- []byte{msgPong}:
			case <-ctx.Done():
				return ctx.Err()
			}
		case msgPong:
			if len(b) != 1 {
				return errors.New("invalid pong")
			}
		default:
			return errors.New("unexpected message after authentication")
		}
	}
}
func Run(ctx context.Context, c Config) error {
	if e := c.Validate(); e != nil {
		return e
	}
	if _, e := os.Stat(c.Bridge); e != nil {
		return fmt.Errorf("native bridge: %w", e)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	t, e := OpenTUN(c)
	if e != nil {
		return e
	}
	defer t.Close()
	if c.autoTUN() {
		if e := configureTUN(ctx, c); e != nil {
			return e
		}
		log.Printf("TUN %s configured automatically: address=%s MTU=%d", c.TUN, c.CIDR, c.MTU)
	} else {
		log.Printf("TUN %s created; %s", c.TUN, configureTUNHint(c))
	}
	go func() { <-ctx.Done(); _ = t.Close() }()
	if c.Mode == "client" {
		return runClient(ctx, c, t)
	}
	return runServer(ctx, c, t)
}
func runClient(parent context.Context, c Config, t Device) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	host, port, _ := net.SplitHostPort(c.Address)
	p, e := StartBridge(ctx, c.Bridge, []string{"--host", host, "--port", port, "--pin", strings.ToLower(c.ServerSHA256)}, nil)
	if e != nil {
		return e
	}
	defer p.Close()
	stop := authTimeout(ctx, p, c.HandshakeSeconds)
	mtu, e := ClientAuth(p.Wire, c)
	stop()
	if e != nil {
		return fmt.Errorf("RDP/channel authentication: %w", e)
	}
	transport, e := attachTransport(ctx, p.Wire, c, nil, nil)
	if e != nil {
		return e
	}
	log.Printf("connected: %s, IPv4=%s MTU=%d", c.Address, c.IPv4, mtu)
	policy := &ipPolicy{subnet: netip.MustParsePrefix(c.CIDR), expected: netip.MustParseAddr(c.IPv4)}
	defer func() { log.Printf("session ended: filtered_packets=%d", policy.filtered.Load()) }()
	queue := make(chan []byte, c.QueuePackets)
	var workers sync.WaitGroup
	finishStats := transport.stats(ctx, "client", "client", queue, policy, func() uint64 { return 0 })
	defer func() { cancel(); _ = t.Close(); p.Close(); workers.Wait(); finishStats() }()
	var last atomic.Int64
	last.Store(time.Now().UnixNano())
	idle := c.IdleSeconds
	if transport != nil {
		idle = transport.p.IdleSeconds
	}
	go watchIdle(ctx, cancel, &last, idle)
	errorsOut := make(chan error, 3)
	workers.Add(3)
	go func() {
		defer workers.Done()
		errorsOut <- writer(ctx, p.Wire, queue, NewPacer(c.UploadBPS), NewPacer(0))
	}()
	go func() {
		defer workers.Done()
		errorsOut <- receiver(ctx, p.Wire, t, queue, policy, mtu, NewPacer(c.DownloadBPS), NewPacer(0), &last)
	}()
	go func() {
		defer workers.Done()
		errorsOut <- tunReader(ctx, t, func(b []byte) {
			src, dst, e := IPv4Packet(b, mtu)
			if e != nil || !policy.clientOutgoing(src, dst) {
				return
			}
			packet := append([]byte{msgIP}, b...)
			select {
			case queue <- packet:
			case <-ctx.Done():
			}
		})
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case e := <-errorsOut:
		cancel()
		return e
	}
}
func runServer(parent context.Context, c Config, t Device) error {
	listener, e := net.Listen("tcp", c.Address)
	if e != nil {
		return e
	}
	return serveListener(parent, c, t, listener)
}
func serveListener(parent context.Context, c Config, t Device, listener net.Listener) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer listener.Close()
	decoy := c.decoyPolicy()
	var decoyTLS *tls.Config
	if decoy.Enabled {
		cert, err := tls.LoadX509KeyPair(c.Certificate, c.PrivateKey)
		if err != nil {
			return fmt.Errorf("load RDP decoy certificate: %w", err)
		}
		decoyTLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}, SessionTicketsDisabled: true}
	}
	decoySlots := make(chan struct{}, decoy.MaxConnections)
	h := &hub{peers: make(map[netip.Addr]*active)}
	basePolicy, e := c.sessionPolicy(Client{})
	if e != nil {
		return e
	}
	totalUp, totalDown := newPacer(c.UploadBPS, basePolicy.Transport.BurstBytes), newPacer(c.DownloadBPS, basePolicy.Transport.BurstBytes)
	go func() { <-ctx.Done(); listener.Close() }()
	go func() {
		if e := tunReader(ctx, t, func(b []byte) { h.route(b, c.MTU) }); e != nil {
			log.Printf("TUN read: %v", e)
			cancel()
		}
	}()
	semaphore := make(chan struct{}, c.MaxConnections)
	var wg sync.WaitGroup
	defer func() { cancel(); listener.Close(); wg.Wait() }()
	log.Printf("listening %s, allowlist=%d, limit=%d; tunnel=FreeRDP/TLS, NLA refusal=%t", c.Address, len(c.Clients), c.MaxConnections, decoy.Enabled)
	for {
		conn, e := listener.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		select {
		case semaphore <- struct{}{}:
		default:
			conn.Close()
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-semaphore }()
			ctx, stopSession := context.WithCancel(ctx)
			defer stopSession()
			remote := conn.RemoteAddr().String()
			defer conn.Close()
			accepted := time.Now()
			authDeadline := accepted.Add(time.Duration(c.HandshakeSeconds) * time.Second)
			if decoy.Enabled {
				req, err := peekConnectionRequest(ctx, conn, authDeadline)
				if err != nil {
					log.Printf("RDP preflight refused %s: %v", remote, err)
					return
				}
				protocols, err := requestProtocols(req)
				if err != nil {
					log.Printf("RDP preflight refused %s: %v", remote, err)
					return
				}
				if selected := selectNLA(protocols); selected != 0 {
					select {
					case decoySlots <- struct{}{}:
					default:
						log.Printf("RDP refusal busy peer=%s", remote)
						return
					}
					defer func() { <-decoySlots }()
					deadline := accepted.Add(time.Duration(decoy.HandshakeSeconds) * time.Second)
					if authDeadline.Before(deadline) {
						deadline = authDeadline
					}
					if err := refuseNLA(ctx, conn, decoyTLS, selected, deadline); err != nil {
						log.Printf("RDP NLA refusal peer=%s: %v", remote, err)
					} else {
						log.Printf("RDP NLA refusal sent peer=%s status=STATUS_LOGON_FAILURE", remote)
					}
					return
				}
			}
			f, e := peerFile(conn)
			conn.Close()
			if e != nil {
				log.Printf("peer fd: %v", e)
				return
			}
			p, e := StartBridge(ctx, c.Bridge, []string{"--server-fd", "3", "--cert", c.Certificate, "--key", c.PrivateKey}, []*os.File{f})
			f.Close()
			if e != nil {
				log.Printf("bridge %s: %v", remote, e)
				return
			}
			defer p.Close()
			stopAuth := authTimeoutDuration(ctx, p, time.Until(authDeadline))
			cl, e := ServerAuth(p.Wire, c)
			stopAuth()
			if e != nil {
				log.Printf("refused %s: %v", remote, e)
				return
			}
			a := &active{client: cl, queue: make(chan []byte, c.QueuePackets), cancel: stopSession}
			if e = h.add(a); e != nil {
				log.Printf("refused duplicate %s", cl.ID)
				return
			}
			log.Printf("connected %s IPv4=%s remote=%s", cl.ID, cl.IPv4, remote)
			policy := &ipPolicy{subnet: netip.MustParsePrefix(c.CIDR), expected: netip.MustParseAddr(cl.IPv4), server: true}
			transport, e := attachTransport(ctx, p.Wire, c, totalUp, totalDown)
			if e != nil {
				h.remove(a)
				log.Printf("transport refused %s: %v", cl.ID, e)
				return
			}
			var workers sync.WaitGroup
			finishStats := transport.stats(ctx, "server", cl.ID, a.queue, policy, a.drops.Load)
			defer func() { h.remove(a); stopSession(); p.Close(); workers.Wait(); finishStats() }()
			var last atomic.Int64
			last.Store(time.Now().UnixNano())
			go watchIdle(ctx, stopSession, &last, c.IdleSeconds)
			worker := make(chan error, 2)
			workers.Add(2)
			go func() {
				defer workers.Done()
				worker <- writer(ctx, p.Wire, a.queue, NewPacer(cl.DownloadBPS), totalDown)
			}()
			go func() {
				defer workers.Done()
				worker <- receiver(ctx, p.Wire, t, a.queue, policy, c.MTU, NewPacer(cl.UploadBPS), totalUp, &last)
			}()
			select {
			case <-ctx.Done():
			case e = <-worker:
			}
			stopSession()
			log.Printf("disconnected %s: %v, queue_drops=%d, filtered_packets=%d", cl.ID, e, a.drops.Load(), policy.filtered.Load())
		}()
	}
}
