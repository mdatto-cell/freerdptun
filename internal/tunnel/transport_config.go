package tunnel

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const maxRateBPS int64 = 1_000_000_000_000
const envelopeHeader = 16
const maxProfileFrame = 16384

type DirectionProfile struct {
	Padding        bool `json:"padding"`
	Fragment       bool `json:"fragment"`
	FrameMin       int  `json:"frame_min_bytes"`
	FrameMax       int  `json:"frame_max_bytes"`
	JitterMaxMS    int  `json:"jitter_max_ms"`
	SmallNoPadding int  `json:"small_packet_no_padding_bytes"`
}
type TransportOptions struct {
	Mode              string           `json:"mode"`
	RateLimitEnabled  *bool            `json:"rate_limit_enabled,omitempty"`
	RateAccounting    string           `json:"rate_accounting,omitempty"`
	BurstBytes        int              `json:"burst_bytes,omitempty"`
	KeepaliveMin      int              `json:"keepalive_min_seconds,omitempty"`
	KeepaliveMax      int              `json:"keepalive_max_seconds,omitempty"`
	ReassemblySeconds int              `json:"reassembly_timeout_seconds,omitempty"`
	Upload            DirectionProfile `json:"upload"`
	Download          DirectionProfile `json:"download"`
}
type transportPolicy struct {
	Mode              string           `json:"mode"`
	RateLimitEnabled  bool             `json:"rate_limit_enabled"`
	RateAccounting    string           `json:"rate_accounting"`
	BurstBytes        int              `json:"burst_bytes"`
	KeepaliveMin      int              `json:"keepalive_min_seconds"`
	KeepaliveMax      int              `json:"keepalive_max_seconds"`
	ReassemblySeconds int              `json:"reassembly_timeout_seconds"`
	Upload            DirectionProfile `json:"upload"`
	Download          DirectionProfile `json:"download"`
}
type SessionPolicy struct {
	Version          int             `json:"version"`
	Transport        transportPolicy `json:"transport"`
	UploadBPS        int64           `json:"upload_bps"`
	DownloadBPS      int64           `json:"download_bps"`
	TotalUploadBPS   int64           `json:"total_upload_bps"`
	TotalDownloadBPS int64           `json:"total_download_bps"`
	IdleSeconds      int             `json:"idle_timeout_seconds"`
	StatsInterval    int             `json:"stats_interval_seconds"`
}

func validRate(n int64) bool { return n >= 0 && n <= maxRateBPS }
func profileDefaults(p DirectionProfile) DirectionProfile {
	if p.FrameMin == 0 {
		p.FrameMin = 1200
	}
	if p.FrameMax == 0 {
		p.FrameMax = 1800
	}
	return p
}
func validateProfile(p DirectionProfile, mtu int) error {
	if p.FrameMin < 64 || p.FrameMax < p.FrameMin || p.FrameMax > maxProfileFrame ||
		p.JitterMaxMS < 0 || p.JitterMaxMS > 100 || p.SmallNoPadding < 0 || p.SmallNoPadding > mtu {
		return errors.New("profile: frame range 64..16384 (min<=max), jitter 0..100 ms, small_packet threshold 0..MTU required")
	}
	return nil
}
func (p transportPolicy) validate(mtu, idle int) error {
	if p.Mode != "baseline" && p.Mode != "profiled" {
		return errors.New("transport.mode must be baseline or profiled")
	}
	if p.RateAccounting != "ip" && p.RateAccounting != "frame" {
		return errors.New("transport.rate_accounting must be ip or frame")
	}
	if p.BurstBytes < 16384 || p.BurstBytes > 4*1024*1024 {
		return errors.New("transport.burst_bytes must be 16384..4194304")
	}
	if p.KeepaliveMin < 3 || p.KeepaliveMax < p.KeepaliveMin || p.KeepaliveMax > 30 || p.KeepaliveMax >= idle {
		return errors.New("keepalive range must be 3..30 seconds, min<=max, max<idle_timeout_seconds")
	}
	if p.ReassemblySeconds < 1 || p.ReassemblySeconds > 120 {
		return errors.New("reassembly_timeout_seconds must be 1..120")
	}
	if e := validateProfile(p.Upload, mtu); e != nil {
		return fmt.Errorf("transport.upload: %w", e)
	}
	if e := validateProfile(p.Download, mtu); e != nil {
		return fmt.Errorf("transport.download: %w", e)
	}
	if p.Mode == "baseline" && (p.Upload.Padding || p.Upload.Fragment || p.Upload.JitterMaxMS != 0 ||
		p.Download.Padding || p.Download.Fragment || p.Download.JitterMaxMS != 0) {
		return errors.New("invalid effective baseline policy")
	}
	return nil
}
func (c Config) sessionPolicy(cl Client) (SessionPolicy, error) {
	p := SessionPolicy{Version: 1, UploadBPS: cl.UploadBPS, DownloadBPS: cl.DownloadBPS,
		TotalUploadBPS: c.UploadBPS, TotalDownloadBPS: c.DownloadBPS, IdleSeconds: c.IdleSeconds, StatsInterval: 10}
	if c.StatsIntervalSeconds != nil {
		p.StatsInterval = *c.StatsIntervalSeconds
	}
	t := transportPolicy{Mode: "baseline", RateLimitEnabled: true, RateAccounting: "ip", BurstBytes: rateBurst,
		KeepaliveMin: 10, KeepaliveMax: 10, ReassemblySeconds: 30}
	if o := c.Transport; o != nil {
		if o.Mode != "" {
			t.Mode = o.Mode
		}
		if o.RateLimitEnabled != nil {
			t.RateLimitEnabled = *o.RateLimitEnabled
		}
		if o.RateAccounting != "" {
			t.RateAccounting = o.RateAccounting
		}
		if o.BurstBytes != 0 {
			t.BurstBytes = o.BurstBytes
		}
		if o.KeepaliveMin != 0 {
			t.KeepaliveMin = o.KeepaliveMin
		}
		t.KeepaliveMax = t.KeepaliveMin
		if o.KeepaliveMax != 0 {
			t.KeepaliveMax = o.KeepaliveMax
		}
		if o.ReassemblySeconds != 0 {
			t.ReassemblySeconds = o.ReassemblySeconds
		}
		t.Upload, t.Download = o.Upload, o.Download
	}
	t.Upload, t.Download = profileDefaults(t.Upload), profileDefaults(t.Download)
	// Check even inactive profiles, then remove ignored modifiers explicitly.
	if e := validateProfile(t.Upload, c.MTU); e != nil {
		return p, e
	}
	if e := validateProfile(t.Download, c.MTU); e != nil {
		return p, e
	}
	if t.Mode == "baseline" {
		t.Upload.Padding, t.Upload.Fragment, t.Upload.JitterMaxMS = false, false, 0
		t.Download.Padding, t.Download.Fragment, t.Download.JitterMaxMS = false, false, 0
	}
	p.Transport = t
	return p, p.validate(c.MTU)
}
func (p SessionPolicy) validate(mtu int) error {
	if p.Version != 1 || p.IdleSeconds < 15 || p.IdleSeconds > 3600 || p.StatsInterval < 0 || p.StatsInterval > 3600 ||
		!validRate(p.UploadBPS) || !validRate(p.DownloadBPS) || !validRate(p.TotalUploadBPS) || !validRate(p.TotalDownloadBPS) {
		return errors.New("invalid authenticated transport policy")
	}
	return p.Transport.validate(mtu, p.IdleSeconds)
}
func decodePolicy(b []byte, mtu int) (*SessionPolicy, error) {
	if len(b) == 0 || len(b) > 4096 {
		return nil, errors.New("policy size exceeds bound")
	}
	var p SessionPolicy
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if e := d.Decode(&p); e != nil {
		return nil, e
	}
	if e := d.Decode(new(any)); e != io.EOF {
		return nil, errors.New("trailing policy JSON")
	}
	if e := p.validate(mtu); e != nil {
		return nil, e
	}
	return &p, nil
}
func minRate(values ...int64) int64 {
	var n int64
	for _, v := range values {
		if v > 0 && (n == 0 || v < n) {
			n = v
		}
	}
	return n
}
