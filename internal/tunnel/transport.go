package tunnel

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync/atomic"
	"time"
)

type transportCounters struct {
	txIPPackets, txIPBytes, rxIPPackets, rxIPBytes atomic.Uint64
	rxTunPackets, rxTunBytes                       atomic.Uint64
	txFrames, txFrameBytes, rxFrames, rxFrameBytes atomic.Uint64
	txPadding, rxPadding, txControls, rxControls   atomic.Uint64
	txPayload, rxPayload, txHeaders, rxHeaders     atomic.Uint64
	txLimiterNS, rxLimiterNS, txJitterNS, txPipeNS atomic.Uint64
}
type pendingPacket struct {
	id            uint32
	total, offset int
	data          []byte
	since         time.Time
}
type sessionTransport struct {
	ctx                                  context.Context
	p                                    SessionPolicy
	mtu                                  int
	tx, rx                               DirectionProfile
	txLocal, txGlobal, rxLocal, rxGlobal *Pacer
	nextID                               uint32
	pending                              *pendingPacket
	c                                    transportCounters
	lastSend                             atomic.Int64
	started                              time.Time
}

func attachTransport(ctx context.Context, w *Wire, c Config, globalUp, globalDown *Pacer) (*sessionTransport, error) {
	if w.policy == nil {
		return nil, nil
	} // Authorized legacy client, unchanged wire format.
	if e := w.policy.validate(c.MTU); e != nil {
		return nil, e
	}
	p := *w.policy
	s := &sessionTransport{ctx: ctx, p: p, mtu: c.MTU, started: time.Now()}
	s.lastSend.Store(time.Now().UnixNano())
	up, down := p.UploadBPS, p.DownloadBPS
	if c.Mode == "client" {
		up = minRate(up, p.TotalUploadBPS, c.UploadBPS)
		down = minRate(down, p.TotalDownloadBPS, c.DownloadBPS)
		s.tx, s.rx = p.Transport.Upload, p.Transport.Download
		s.txLocal, s.rxLocal = newPacer(up, p.Transport.BurstBytes), newPacer(down, p.Transport.BurstBytes)
	} else {
		s.tx, s.rx = p.Transport.Download, p.Transport.Upload
		s.txLocal, s.rxLocal = newPacer(down, p.Transport.BurstBytes), newPacer(up, p.Transport.BurstBytes)
		s.txGlobal, s.rxGlobal = globalDown, globalUp
	}
	if s.txGlobal == nil {
		s.txGlobal = NewPacer(0)
	}
	if s.rxGlobal == nil {
		s.rxGlobal = NewPacer(0)
	}
	w.session = s
	return s, nil
}
func (s *sessionTransport) wait(n int, tx bool) error {
	if !s.p.Transport.RateLimitEnabled {
		return s.ctx.Err()
	}
	a, b, counter := s.rxLocal, s.rxGlobal, &s.c.rxLimiterNS
	if tx {
		a, b, counter = s.txLocal, s.txGlobal, &s.c.txLimiterNS
	}
	if a.bps == 0 && b.bps == 0 {
		return s.ctx.Err()
	}
	start := time.Now()
	defer func() { counter.Add(uint64(time.Since(start))) }()
	if e := a.Wait(s.ctx, n); e != nil {
		return e
	}
	return b.Wait(s.ctx, n)
}
func (s *sessionTransport) jitter() error {
	if s.tx.JitterMaxMS == 0 {
		return s.ctx.Err()
	}
	d := time.Duration(rand.IntN(s.tx.JitterMaxMS+1)) * time.Millisecond
	if d == 0 {
		return s.ctx.Err()
	}
	start := time.Now()
	defer func() { s.c.txJitterNS.Add(uint64(time.Since(start))) }()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	case <-t.C:
		return nil
	}
}
func dataMessage(b []byte) bool { return len(b) > 0 && (b[0] == msgIP || b[0] == msgEnvelope) }
func (s *sessionTransport) sendFrame(w *Wire, b []byte) error {
	if s.p.Transport.RateAccounting == "frame" && dataMessage(b) {
		if e := s.wait(len(b)+4, true); e != nil {
			return e
		}
	}
	start := time.Now()
	e := w.writeRaw(b)
	s.c.txPipeNS.Add(uint64(time.Since(start)))
	if e != nil {
		return e
	}
	s.lastSend.Store(time.Now().UnixNano())
	s.c.txFrames.Add(1)
	s.c.txFrameBytes.Add(uint64(len(b) + 4))
	if b[0] == msgEnvelope {
		s.c.txPadding.Add(uint64(binary.BigEndian.Uint16(b[14:16])))
		s.c.txPayload.Add(uint64(binary.BigEndian.Uint16(b[12:14])))
		s.c.txHeaders.Add(envelopeHeader + 4)
	} else {
		s.c.txHeaders.Add(5)
		if b[0] == msgIP {
			s.c.txPayload.Add(uint64(len(b) - 1))
		}
	}
	if b[0] == msgPing || b[0] == msgPong {
		s.c.txControls.Add(1)
	}
	return nil
}
func (s *sessionTransport) sentIP(n int) { s.c.txIPPackets.Add(1); s.c.txIPBytes.Add(uint64(n)) }
func (s *sessionTransport) write(w *Wire, b []byte) error {
	if e := s.ctx.Err(); e != nil {
		return e
	}
	if b[0] != msgIP {
		if (b[0] != msgPing && b[0] != msgPong) || len(b) != 1 {
			return errors.New("invalid post-auth control")
		}
		return s.sendFrame(w, b)
	}
	payload := b[1:]
	if len(payload) < 20 || len(payload) > s.mtu {
		return errors.New("outgoing IP message exceeds MTU/bound")
	}
	if s.p.Transport.RateAccounting == "ip" {
		if e := s.wait(len(payload), true); e != nil {
			return e
		}
	}
	small := s.tx.SmallNoPadding > 0 && len(payload) <= s.tx.SmallNoPadding
	if small || (!s.tx.Padding && !s.tx.Fragment) {
		if e := s.sendFrame(w, b); e != nil {
			return e
		}
		s.sentIP(len(payload))
		return s.jitter()
	}
	s.nextID++
	if s.nextID == 0 {
		s.nextID++
	}
	for offset := 0; offset < len(payload); {
		target := s.tx.FrameMin + rand.IntN(s.tx.FrameMax-s.tx.FrameMin+1)
		n := len(payload) - offset
		if s.tx.Fragment && n > target-4-envelopeHeader {
			n = target - 4 - envelopeHeader
		}
		padding := 0
		if s.tx.Padding && target > 4+envelopeHeader+n {
			padding = target - 4 - envelopeHeader - n
		}
		frame := make([]byte, envelopeHeader+n+padding)
		frame[0], frame[1] = msgEnvelope, 1
		if offset == 0 {
			frame[2] |= 1
		}
		if offset+n == len(payload) {
			frame[2] |= 2
		}
		binary.BigEndian.PutUint32(frame[4:8], s.nextID)
		binary.BigEndian.PutUint16(frame[8:10], uint16(len(payload)))
		binary.BigEndian.PutUint16(frame[10:12], uint16(offset))
		binary.BigEndian.PutUint16(frame[12:14], uint16(n))
		binary.BigEndian.PutUint16(frame[14:16], uint16(padding))
		copy(frame[envelopeHeader:], payload[offset:offset+n])
		if padding > 0 {
			if _, e := crand.Read(frame[envelopeHeader+n:]); e != nil {
				return e
			}
		}
		if e := s.sendFrame(w, frame); e != nil {
			return e
		}
		offset += n
		if offset == len(payload) {
			s.sentIP(len(payload))
		}
		if e := s.jitter(); e != nil {
			return e
		}
	}
	return nil
}
func (s *sessionTransport) decodeEnvelope(b []byte) ([]byte, error) {
	if len(b) < envelopeHeader+1 || len(b)+4 > maxProfileFrame || b[1] != 1 || b[2]&^byte(3) != 0 || b[3] != 0 {
		return nil, errors.New("invalid transport envelope")
	}
	id := binary.BigEndian.Uint32(b[4:8])
	total := int(binary.BigEndian.Uint16(b[8:10]))
	offset := int(binary.BigEndian.Uint16(b[10:12]))
	n := int(binary.BigEndian.Uint16(b[12:14]))
	padding := int(binary.BigEndian.Uint16(b[14:16]))
	if id == 0 || total < 20 || total > s.mtu || n == 0 || offset+n > total || envelopeHeader+n+padding != len(b) ||
		(b[2]&1 != 0) != (offset == 0) || (b[2]&2 != 0) != (offset+n == total) {
		return nil, errors.New("invalid transport fragment lengths/flags")
	}
	if s.pending == nil {
		if offset != 0 {
			return nil, errors.New("fragment without packet start")
		}
		s.pending = &pendingPacket{id: id, total: total, data: make([]byte, total+1), since: time.Now()}
		s.pending.data[0] = msgIP
	}
	p := s.pending
	if id != p.id || total != p.total || offset != p.offset {
		return nil, errors.New("fragment ordering/ownership mismatch")
	}
	copy(p.data[1+offset:], b[envelopeHeader:envelopeHeader+n])
	p.offset += n
	s.c.rxPadding.Add(uint64(padding))
	s.c.rxPayload.Add(uint64(n))
	s.c.rxHeaders.Add(envelopeHeader + 4)
	if p.offset != p.total {
		return nil, nil
	}
	s.pending = nil
	return p.data, nil
}
func (s *sessionTransport) read(w *Wire) ([]byte, error) {
	for {
		b, e := w.readRaw()
		if e != nil {
			return nil, e
		}
		s.c.rxFrames.Add(1)
		s.c.rxFrameBytes.Add(uint64(len(b) + 4))
		if s.pending != nil && time.Since(s.pending.since) > time.Duration(s.p.Transport.ReassemblySeconds)*time.Second {
			return nil, errors.New("transport reassembly timeout")
		}
		if s.p.Transport.RateAccounting == "frame" && dataMessage(b) {
			if e = s.wait(len(b)+4, false); e != nil {
				return nil, e
			}
		}
		switch b[0] {
		case msgEnvelope:
			if !s.rx.Padding && !s.rx.Fragment {
				return nil, errors.New("envelope not enabled by authenticated policy")
			}
			b, e = s.decodeEnvelope(b)
			if e != nil {
				return nil, e
			}
			if b == nil {
				continue
			}
		case msgIP:
			if s.pending != nil {
				return nil, errors.New("IP message interrupted fragment sequence")
			}
			s.c.rxHeaders.Add(5)
			s.c.rxPayload.Add(uint64(len(b) - 1))
		case msgPing, msgPong:
			if len(b) != 1 {
				return nil, errors.New("invalid transport control")
			}
			s.c.rxControls.Add(1)
			s.c.rxHeaders.Add(5)
			return b, nil
		default:
			return nil, fmt.Errorf("unexpected post-auth message type %d", b[0])
		}
		if len(b) < 21 || len(b) > s.mtu+1 {
			return nil, errors.New("received IP message exceeds MTU/bound")
		}
		if s.p.Transport.RateAccounting == "ip" {
			if e = s.wait(len(b)-1, false); e != nil {
				return nil, e
			}
		}
		s.c.rxIPPackets.Add(1)
		s.c.rxIPBytes.Add(uint64(len(b) - 1))
		return b, nil
	}
}
func (s *sessionTransport) keepaliveDelay() time.Duration {
	return time.Duration(s.p.Transport.KeepaliveMin+rand.IntN(s.p.Transport.KeepaliveMax-s.p.Transport.KeepaliveMin+1)) * time.Second
}
func (s *sessionTransport) runWriter(w *Wire, queue <-chan []byte) error {
	delay := s.keepaliveDelay()
	t := time.NewTimer(delay)
	defer t.Stop()
	reset := func(d time.Duration) {
		if !t.Stop() {
			select {
			case <-t.C:
			default:
			}
		}
		t.Reset(d)
	}
	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case <-t.C:
			idle := time.Since(time.Unix(0, s.lastSend.Load()))
			if idle < delay {
				t.Reset(delay - idle)
				continue
			}
			if e := w.Write([]byte{msgPing}); e != nil {
				return e
			}
			delay = s.keepaliveDelay()
			t.Reset(delay)
		case b, ok := <-queue:
			if !ok {
				return nil
			}
			if len(b) == 0 {
				continue
			}
			if e := w.Write(b); e != nil {
				return e
			}
			delay = s.keepaliveDelay()
			reset(delay)
		}
	}
}
