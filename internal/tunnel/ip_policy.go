package tunnel

import (
	"fmt"
	"log"
	"net/netip"
	"sync/atomic"
)

// Unsupported background traffic is not an ownership violation. In particular,
// IPv4 multicast/limited broadcast may appear when a Windows interface becomes
// usable. This IPv4-unicast tunnel drops it without tearing down the session.
// Actual source spoofing remains a fatal error, even for a multicast packet.
type ipPolicy struct {
	subnet   netip.Prefix
	expected netip.Addr
	server   bool
	filtered atomic.Uint64
}

func (p *ipPolicy) supportsDestination(dst netip.Addr) bool {
	return p.subnet.IsValid() && p.subnet.Addr().Is4() && dst.Is4() &&
		dst.IsGlobalUnicast() && !isBroadcast(p.subnet, dst)
}

func (p *ipPolicy) dropUnsupported(src, dst netip.Addr, direction string) {
	n := p.filtered.Add(1)
	// Bound logging independently of packet rate. The final count is included
	// in the session-end log; no UUID, proof or packet payload is logged.
	if n <= 3 {
		log.Printf("filtered unsupported IPv4 destination: direction=%s src=%s dst=%s count=%d (session kept)",
			direction, src, dst, n)
	}
}

func (p *ipPolicy) incoming(src, dst netip.Addr) (bool, error) {
	if p.server && src != p.expected {
		return false, fmt.Errorf("IPv4 source ownership violation: src=%s dst=%s expected_source=%s",
			src, dst, p.expected)
	}
	if !p.supportsDestination(dst) {
		p.dropUnsupported(src, dst, "received")
		return false, nil
	}
	if !p.server && dst != p.expected {
		return false, fmt.Errorf("IPv4 destination ownership violation: src=%s dst=%s expected_destination=%s",
			src, dst, p.expected)
	}
	return true, nil
}

func (p *ipPolicy) clientOutgoing(src, dst netip.Addr) bool {
	// Keep the existing client-side source filter, including during APIPA/DAD.
	if src != p.expected {
		return false
	}
	if !p.supportsDestination(dst) {
		p.dropUnsupported(src, dst, "outgoing")
		return false
	}
	return true
}
