package tunnel

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"
)

func (s *sessionTransport) stats(ctx context.Context, side, id string, queue <-chan []byte, ip *ipPolicy, drops func() uint64) func() {
	if s == nil {
		return func() {}
	}
	policy, _ := json.Marshal(struct {
		Side   string        `json:"side"`
		ID     string        `json:"id"`
		Policy SessionPolicy `json:"policy"`
	}{side, id, s.p})
	log.Printf("POLICY %s", policy)
	var previous [4]uint64
	last := s.started
	emit := func(final bool) {
		now := time.Now()
		interval := now.Sub(last).Seconds()
		if final {
			interval = now.Sub(s.started).Seconds()
			previous = [4]uint64{}
		}
		v := [4]uint64{s.c.txIPBytes.Load(), s.c.rxIPBytes.Load(), s.c.txFrameBytes.Load(), s.c.rxFrameBytes.Load()}
		mbps := func(i int) float64 {
			if interval <= 0 {
				return 0
			}
			return float64(v[i]-previous[i]) * 8 / interval / 1e6
		}
		direction := "upload"
		if side == "server" {
			direction = "download"
		}
		m := map[string]any{
			"side": side, "id": id, "tx_direction": direction, "final": final,
			"elapsed_seconds": now.Sub(s.started).Seconds(), "interval_seconds": interval,
			"tx_ip_packets": s.c.txIPPackets.Load(), "tx_ip_bytes": v[0],
			"rx_ip_packets": s.c.rxIPPackets.Load(), "rx_ip_bytes": v[1],
			"rx_tun_packets": s.c.rxTunPackets.Load(), "rx_tun_bytes": s.c.rxTunBytes.Load(),
			"tx_frames": s.c.txFrames.Load(), "tx_frame_bytes": v[2],
			"rx_frames": s.c.rxFrames.Load(), "rx_frame_bytes": v[3],
			"tx_padding_bytes": s.c.txPadding.Load(), "rx_padding_bytes": s.c.rxPadding.Load(),
			"tx_payload_bytes": s.c.txPayload.Load(), "rx_payload_bytes": s.c.rxPayload.Load(),
			"tx_header_bytes": s.c.txHeaders.Load(), "rx_header_bytes": s.c.rxHeaders.Load(),
			"tx_control_frames": s.c.txControls.Load(), "rx_control_frames": s.c.rxControls.Load(),
			"tx_limiter_wait_ms": float64(s.c.txLimiterNS.Load()) / 1e6,
			"rx_limiter_wait_ms": float64(s.c.rxLimiterNS.Load()) / 1e6,
			"tx_jitter_wait_ms":  float64(s.c.txJitterNS.Load()) / 1e6,
			"tx_pipe_write_ms":   float64(s.c.txPipeNS.Load()) / 1e6,
			"tx_ip_mbps":         mbps(0), "rx_ip_mbps": mbps(1), "tx_frame_mbps": mbps(2), "rx_frame_mbps": mbps(3),
			"queue_depth": len(queue), "queue_drops": drops(), "filtered_packets": ip.filtered.Load(),
		}
		b, e := json.Marshal(m)
		if e == nil {
			log.Printf("STATS %s", b)
		}
		last, previous = now, v
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if s.p.StatsInterval == 0 {
			select {
			case <-ctx.Done():
			case <-stop:
			}
			return
		}
		t := time.NewTicker(time.Duration(s.p.StatsInterval) * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-t.C:
				emit(false)
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(stop); wg.Wait(); emit(true) }) }
}
