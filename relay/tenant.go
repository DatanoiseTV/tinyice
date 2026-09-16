package relay

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Tenant represents an isolated tenant in a multi-tenant deployment.
type Tenant struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Plan      string       `json:"plan"` // "free", "starter", "pro", "enterprise"
	Limits    TenantLimits `json:"limits"`
	Config    TenantConfig `json:"config"`
	CreatedAt time.Time    `json:"created_at"`

	// Runtime state (not persisted in JSON config)
	pipelines map[string]*Pipeline
	stats     TenantStats
	mu        sync.RWMutex
}

// TenantLimits defines resource limits for a tenant.
type TenantLimits struct {
	MaxStreams        int   `json:"max_streams"`         // 0 = unlimited
	MaxListeners      int   `json:"max_listeners"`       // Per stream
	MaxTotalListeners int   `json:"max_total_listeners"` // Across all streams
	MaxBitrateKbps    int   `json:"max_bitrate_kbps"`    // Max source bitrate
	MaxStorageMB      int   `json:"max_storage_mb"`      // For AutoDJ files
	AllowTranscoding  bool  `json:"allow_transcoding"`
	AllowRelay        bool  `json:"allow_relay"`
	AllowWebRTC       bool  `json:"allow_webrtc"`
	AllowRTMP         bool  `json:"allow_rtmp"`
	AllowSRT          bool  `json:"allow_srt"`
	AllowHLS          bool  `json:"allow_hls"`
	AllowVideo        bool  `json:"allow_video"`
	BandwidthLimitMB  int64 `json:"bandwidth_limit_mb"` // Monthly, 0 = unlimited
}

// TenantConfig holds per-tenant configuration overrides.
type TenantConfig struct {
	CustomDomain    string            `json:"custom_domain"`
	AccentColor     string            `json:"accent_color"`
	LogoPath        string            `json:"logo_path"`
	PageTitle       string            `json:"page_title"`
	SourcePasswords map[string]string `json:"source_passwords"` // mount -> password hash
}

// TenantStats tracks usage metrics for billing.
type TenantStats struct {
	BytesIn          int64 // atomic
	BytesOut         int64 // atomic
	CurrentListeners int32 // atomic
	PeakListeners    int32 // atomic
	StreamMinutes    int64 // atomic — accumulated streaming time
}

// APIKey represents an API key for tenant authentication.
type APIKey struct {
	ID        string    `json:"id"`
	KeyHash   string    `json:"key_hash"` // bcrypt hash
	Name      string    `json:"name"`
	TenantID  string    `json:"tenant_id"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at,omitempty"`
}

// GetStats returns a snapshot of the tenant's current stats.
func (t *Tenant) GetStats() TenantStats {
	return TenantStats{
		BytesIn:          atomic.LoadInt64(&t.stats.BytesIn),
		BytesOut:         atomic.LoadInt64(&t.stats.BytesOut),
		CurrentListeners: atomic.LoadInt32(&t.stats.CurrentListeners),
		PeakListeners:    atomic.LoadInt32(&t.stats.PeakListeners),
		StreamMinutes:    atomic.LoadInt64(&t.stats.StreamMinutes),
	}
}

// RecordBytesIn atomically adds to bytes in counter.
func (t *Tenant) RecordBytesIn(n int64) {
	atomic.AddInt64(&t.stats.BytesIn, n)
}

// RecordBytesOut atomically adds to bytes out counter.
func (t *Tenant) RecordBytesOut(n int64) {
	atomic.AddInt64(&t.stats.BytesOut, n)
}

// UpdateListenerCount atomically updates the current listener count and peak.
func (t *Tenant) UpdateListenerCount(delta int32) {
	current := atomic.AddInt32(&t.stats.CurrentListeners, delta)
	for {
		peak := atomic.LoadInt32(&t.stats.PeakListeners)
		if current <= peak {
			break
		}
		if atomic.CompareAndSwapInt32(&t.stats.PeakListeners, peak, current) {
			break
		}
	}
}

// StreamCount returns the number of active streams for this tenant.
func (t *Tenant) StreamCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.pipelines)
}

// CanCreateStream checks if the tenant can create another stream.
func (t *Tenant) CanCreateStream() bool {
	if t.Limits.MaxStreams == 0 {
		return true // unlimited
	}
	return t.StreamCount() < t.Limits.MaxStreams
}

// CanAcceptListener checks if the tenant can accept another listener.
func (t *Tenant) CanAcceptListener() bool {
	if t.Limits.MaxTotalListeners == 0 {
		return true
	}
	return int(atomic.LoadInt32(&t.stats.CurrentListeners)) < t.Limits.MaxTotalListeners
}

// CheckBandwidthLimit returns true if the tenant has exceeded their monthly bandwidth.
func (t *Tenant) CheckBandwidthLimit() bool {
	if t.Limits.BandwidthLimitMB == 0 {
		return false // unlimited
	}
	totalBytes := atomic.LoadInt64(&t.stats.BytesIn) + atomic.LoadInt64(&t.stats.BytesOut)
	limitBytes := t.Limits.BandwidthLimitMB * 1024 * 1024
	return totalBytes >= limitBytes
}

// DefaultTenantLimits returns limits suitable for an unlimited/admin tenant.
func DefaultTenantLimits() TenantLimits {
	return TenantLimits{
		MaxStreams:        0, // unlimited
		MaxListeners:      0,
		MaxTotalListeners: 0,
		AllowTranscoding:  true,
		AllowRelay:        true,
		AllowWebRTC:       true,
		AllowRTMP:         true,
		AllowSRT:          true,
		AllowHLS:          true,
		AllowVideo:        true,
		BandwidthLimitMB:  0, // unlimited
	}
}

// PlanLimits returns preset limits for a given plan name.
func PlanLimits(plan string) TenantLimits {
	switch plan {
	case "free":
		return TenantLimits{
			MaxStreams:        1,
			MaxListeners:      10,
			MaxTotalListeners: 10,
			MaxBitrateKbps:    128,
			MaxStorageMB:      100,
			AllowTranscoding:  false,
			AllowRelay:        false,
			AllowWebRTC:       true,
			AllowRTMP:         false,
			AllowSRT:          false,
			AllowHLS:          true,
			AllowVideo:        false,
			BandwidthLimitMB:  10240, // 10GB
		}
	case "starter":
		return TenantLimits{
			MaxStreams:        3,
			MaxListeners:      50,
			MaxTotalListeners: 100,
			MaxBitrateKbps:    320,
			MaxStorageMB:      1024,
			AllowTranscoding:  true,
			AllowRelay:        true,
			AllowWebRTC:       true,
			AllowRTMP:         true,
			AllowSRT:          false,
			AllowHLS:          true,
			AllowVideo:        false,
			BandwidthLimitMB:  102400, // 100GB
		}
	case "pro":
		return TenantLimits{
			MaxStreams:        10,
			MaxListeners:      500,
			MaxTotalListeners: 1000,
			MaxBitrateKbps:    0, // unlimited
			MaxStorageMB:      10240,
			AllowTranscoding:  true,
			AllowRelay:        true,
			AllowWebRTC:       true,
			AllowRTMP:         true,
			AllowSRT:          true,
			AllowHLS:          true,
			AllowVideo:        true,
			BandwidthLimitMB:  1048576, // 1TB
		}
	case "enterprise":
		return DefaultTenantLimits()
	default:
		return DefaultTenantLimits()
	}
}

// TenantManager manages all tenants.
type TenantManager struct {
	tenants map[string]*Tenant // key is tenant ID
	domains map[string]string  // custom domain -> tenant ID
	mu      sync.RWMutex
}

// NewTenantManager creates a new TenantManager.
func NewTenantManager() *TenantManager {
	return &TenantManager{
		tenants: make(map[string]*Tenant),
		domains: make(map[string]string),
	}
}

// CreateTenant creates a new tenant with the given plan.
func (tm *TenantManager) CreateTenant(id, name, plan string) *Tenant {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	t := &Tenant{
		ID:        id,
		Name:      name,
		Plan:      plan,
		Limits:    PlanLimits(plan),
		Config:    TenantConfig{SourcePasswords: make(map[string]string)},
		CreatedAt: time.Now(),
		pipelines: make(map[string]*Pipeline),
	}
	tm.tenants[id] = t
	return t
}

// GetTenant returns a tenant by ID, or nil if not found.
func (tm *TenantManager) GetTenant(id string) *Tenant {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return tm.tenants[id]
}

// GetTenantByDomain returns a tenant by custom domain, or nil.
func (tm *TenantManager) GetTenantByDomain(domain string) *Tenant {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	if id, ok := tm.domains[domain]; ok {
		return tm.tenants[id]
	}
	return nil
}

// SetCustomDomain maps a domain to a tenant.
func (tm *TenantManager) SetCustomDomain(tenantID, domain string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	tm.domains[domain] = tenantID
}

// RemoveTenant removes a tenant.
func (tm *TenantManager) RemoveTenant(id string) {
	tm.mu.Lock()
	defer tm.mu.Unlock()
	if t, ok := tm.tenants[id]; ok {
		// Remove domain mappings
		if t.Config.CustomDomain != "" {
			delete(tm.domains, t.Config.CustomDomain)
		}
		delete(tm.tenants, id)
	}
}

// ListTenants returns all tenant IDs.
func (tm *TenantManager) ListTenants() []string {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	ids := make([]string, 0, len(tm.tenants))
	for id := range tm.tenants {
		ids = append(ids, id)
	}
	return ids
}

// TenantCount returns the number of tenants.
func (tm *TenantManager) TenantCount() int {
	tm.mu.RLock()
	defer tm.mu.RUnlock()
	return len(tm.tenants)
}

// GetOrCreateDefaultTenant ensures a "default" tenant exists (for single-tenant mode).
func (tm *TenantManager) GetOrCreateDefaultTenant() *Tenant {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if t, ok := tm.tenants["default"]; ok {
		return t
	}

	t := &Tenant{
		ID:        "default",
		Name:      "Default",
		Plan:      "enterprise",
		Limits:    DefaultTenantLimits(),
		Config:    TenantConfig{SourcePasswords: make(map[string]string)},
		CreatedAt: time.Now(),
		pipelines: make(map[string]*Pipeline),
	}
	tm.tenants["default"] = t
	return t
}

// GenerateAPIKey creates a random API key string.
func GenerateAPIKey() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate API key: %w", err)
	}
	return "tinyice_" + hex.EncodeToString(bytes), nil
}
