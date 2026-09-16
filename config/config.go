package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

// NewWebhookID returns a 12-char hex token used to address webhooks in
// the API and admin UI. Random rather than sequential so deletes don't
// invalidate cached references on the frontend.
func NewWebhookID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "wh-fallback"
	}
	return "wh_" + hex.EncodeToString(b)
}

const (
	RoleSuperAdmin = "superadmin"
	RoleAdmin      = "admin"
	RoleDJ         = "dj"
)

type PasskeyCredential struct {
	ID            string `json:"id"`
	RawCredential string `json:"raw_credential"`
	Name          string `json:"name"`
	CreatedAt     string `json:"created_at"`
	LastUsed      string `json:"last_used"`
}

type OIDCProvider struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	ClientID     string `json:"client_id"`
	ClientSecret string `json:"client_secret"`
	DiscoveryURL string `json:"discovery_url"`
	Icon         string `json:"icon"`
	Enabled      bool   `json:"enabled"`
}

type PendingUser struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	Name        string `json:"name"`
	Provider    string `json:"provider"`
	RequestedAt string `json:"requested_at"`
	DeniedAt    string `json:"denied_at,omitempty"`
}

type SMTPConfig struct {
	Enabled  bool   `json:"enabled"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	From     string `json:"from"`
	Username string `json:"username"`
	Password string `json:"password"`
}

type WebAuthnConfig struct {
	RPID      string   `json:"rp_id"`
	RPName    string   `json:"rp_name"`
	RPOrigins []string `json:"rp_origins"`
}

type User struct {
	Username     string               `json:"username"`
	Password     string               `json:"password"`
	Role         string               `json:"role"`
	Mounts       map[string]string    `json:"mounts"`
	Passkeys     []*PasskeyCredential `json:"passkeys,omitempty"`
	LinkedEmails []string             `json:"linked_emails,omitempty"`
}

type APIToken struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	TokenHash  string `json:"token_hash"`
	Username   string `json:"username"`
	Role       string `json:"role"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at"`
	LastUsedIP string `json:"last_used_ip"`
	ExpiresAt  string `json:"expires_at"`
}

type RelayConfig struct {
	URL       string `json:"url"`      // e.g. http://master:8000/stream
	Mount     string `json:"mount"`    // Local mount point
	Password  string `json:"password"` // If the master requires one
	BurstSize int    `json:"burst_size"`
	Enabled   bool   `json:"enabled"`
}

type MountSettings struct {
	Password  string `json:"password"` // Hashed
	BurstSize int    `json:"burst_size"`
	// NameOverride: when set, replaces the source's Ice-Name header on
	// metadata update. Lets ops rebrand a station server-side without
	// touching the encoder.
	NameOverride string `json:"name_override,omitempty"`
}

type TranscoderConfig struct {
	Name        string `json:"name"`
	InputMount  string `json:"input_mount"`
	OutputMount string `json:"output_mount"`
	Format      string `json:"format"` // "mp3" or "opus"
	Bitrate     int    `json:"bitrate"`
	Enabled     bool   `json:"enabled"`

	// Visibility controls whether the transcoded output appears in public
	// stream listings. Independent of access — an "unlisted" transcoded
	// mount is still streamable by URL, it just isn't advertised. Values:
	//   ""         — follow input mount visibility (default, legacy)
	//   "public"   — always listed
	//   "unlisted" — hidden from listings, still streamable
	Visibility string `json:"visibility,omitempty"`

	// Encoder tuning. Zero/empty values fall back to defaults so existing
	// configs continue to work without migration.
	SampleRate      int    `json:"sample_rate,omitempty"`        // Hz. 0 = follow input (MP3) / 48000 (Opus)
	Channels        int    `json:"channels,omitempty"`           // 1 or 2. 0 = 2 (stereo)
	OpusApplication string `json:"opus_application,omitempty"`   // "audio" | "voip" | "lowdelay"
	OpusVBR         *bool  `json:"opus_vbr,omitempty"`           // nil = VBR on (default)
	OpusComplexity  int    `json:"opus_complexity,omitempty"`    // 0-10, 0 = encoder default
	OpusFrameSizeMS int    `json:"opus_frame_size_ms,omitempty"` // 2.5/5/10/20/40/60, 0 = 20
}

// ResolveVisibility returns the explicit visibility for a transcoder
// output (true=public, false=unlisted) or false+false when the operator
// hasn't picked one and the runtime should keep following the input.
func (t *TranscoderConfig) ResolveVisibility() (visible bool, explicit bool) {
	switch t.Visibility {
	case "public":
		return true, true
	case "unlisted":
		return false, true
	}
	return false, false
}

// WebhookConfig describes one outbound HTTP webhook subscription.
//
// The minimal form (URL + Events + Enabled) is preserved for backwards
// compatibility with existing tinyice.json files written before templating
// was added. New deployments get an ID assigned at load time so the admin
// UI can address each entry uniquely even if URLs collide.
//
// BodyTemplate, when set, is rendered with Go text/template against a
// context that contains .Event, .Timestamp, .Hostname plus every key from
// the per-event payload promoted to the top level (so users can write
// {{.Artist}} for now_playing, {{.Mount}} for source_connect, etc.). When
// BodyTemplate is empty we fall back to the legacy JSON envelope, which
// keeps existing receivers working unchanged.
//
// Method defaults to POST, ContentType to application/json. Headers is a
// free-form map of extra request headers — useful for Authorization,
// X-Webhook-Token, custom routing keys for Slack, etc.
type WebhookConfig struct {
	ID           string            `json:"id"`
	Name         string            `json:"name,omitempty"`
	URL          string            `json:"url"`
	Method       string            `json:"method,omitempty"`
	Events       []string          `json:"events"`
	Headers      map[string]string `json:"headers,omitempty"`
	BodyTemplate string            `json:"body_template,omitempty"`
	ContentType  string            `json:"content_type,omitempty"`
	Enabled      bool              `json:"enabled"`
}

type AutoDJConfig struct {
	Name                 string   `json:"name"`
	Mount                string   `json:"mount"`
	MusicDir             string   `json:"music_dir"`
	Format               string   `json:"format"` // "mp3" or "opus"
	Bitrate              int      `json:"bitrate"`
	Enabled              bool     `json:"enabled"`
	Loop                 bool     `json:"loop"`
	InjectMetadata       bool     `json:"inject_metadata"`
	Playlist             []string `json:"playlist"`
	LastPlaylist         string   `json:"last_playlist"`
	MPDEnabled           bool     `json:"mpd_enabled"`
	MPDPort              string   `json:"mpd_port"`
	MPDPassword          string   `json:"mpd_password"`
	Visible              bool     `json:"visible"`
	SongCommand          string   `json:"song_command"`
	SongCommandTimeout   int      `json:"song_command_timeout"`
	OnPlayCommand        string   `json:"on_play_command,omitempty"`
	OnPlayCommandTimeout int      `json:"on_play_command_timeout,omitempty"`
}

type IngestConfig struct {
	RTMPEnabled bool   `json:"rtmp_enabled"`
	RTMPPort    string `json:"rtmp_port"`
	SRTEnabled  bool   `json:"srt_enabled"`
	SRTPort     string `json:"srt_port"`
	SRTLatency  int    `json:"srt_latency"` // ms
}

type MultiTenantConfig struct {
	Enabled       bool   `json:"enabled"`
	DefaultTenant string `json:"default_tenant"`
	TenantStore   string `json:"tenant_store"` // "config", "database"
}

// configSaveMu serialises SaveConfig across concurrent admin / API /
// background-task call sites. Without it, two simultaneous writes stomp the
// .tmp file and can leave a truncated JSON on disk, plus later writes can
// lose earlier mutations. It lives at package scope (rather than inside the
// Config struct) so json.Marshal never copies a lock value.
var configSaveMu sync.Mutex

type Config struct {
	BindHost              string            `json:"bind_host"`
	Port                  string            `json:"port"`
	DefaultSourcePassword string            `json:"default_source_password"`
	Mounts                map[string]string `json:"mounts"` // Legacy global mounts

	// New Advanced Settings
	AdvancedMounts map[string]*MountSettings `json:"advanced_mounts"`
	Relays         []*RelayConfig            `json:"relays"`
	Transcoders    []*TranscoderConfig       `json:"transcoders"`
	// AutoTranscodeMP3Bitrates spawns ephemeral mp3 transcoders for any
	// non-mp3 source mount as soon as it connects. Output mounts are
	// named `<input>-mp3-<bitrate>` and don't get persisted to config —
	// they live for the lifetime of the source. Empty / nil = off.
	AutoTranscodeMP3Bitrates []int            `json:"auto_transcode_mp3_bitrates,omitempty"`
	Webhooks                 []*WebhookConfig `json:"webhooks"`
	BannedIPs                []string         `json:"banned_ips"`
	WhitelistedIPs           []string         `json:"whitelisted_ips"`
	// TrustedProxies is a list of reverse-proxy peer addresses (exact IPs or
	// CIDRs) whose X-Forwarded-For / X-Real-IP headers we will honour for
	// scan-detection, bans and audit logging. Leave empty when the server
	// faces the internet directly. When populated, loopback is no longer
	// auto-whitelisted, so ops behind a reverse proxy see real client IPs.
	TrustedProxies []string `json:"trusted_proxies"`

	AdminPassword  string            `json:"admin_password"`
	AdminUser      string            `json:"admin_user"`
	Location       string            `json:"location"`
	AdminEmail     string            `json:"admin_email"`
	BaseURL        string            `json:"base_url"` // e.g. https://radio.example.com
	HostName       string            `json:"hostname"`
	ConfigPath     string            `json:"-"`
	LowLatencyMode bool              `json:"low_latency_mode"`
	MaxListeners   int               `json:"max_listeners"`
	DisabledMounts map[string]bool   `json:"disabled_mounts"`
	VisibleMounts  map[string]bool   `json:"visible_mounts"`  // map[mount]is_visible
	FallbackMounts map[string]string `json:"fallback_mounts"` // map[source]fallback
	// VariantGroups declares ABR simulcast ladders. The key is the primary
	// mount (the URL viewers navigate to); the value is the ordered list
	// of member mount paths that form the ladder — lowest rendition to
	// highest. The primary must also appear in the list. Each member is
	// a separately-published mount (OBS "Multiple Outputs" plugin, etc.)
	// with its own HLS segments; the server emits a master.m3u8 at
	// /{primary}/master.m3u8 that lists all members.
	VariantGroups map[string][]string `json:"variant_groups,omitempty"`

	// UI Customization
	PageTitle    string `json:"page_title"`
	PageSubtitle string `json:"page_subtitle"`

	// Branding
	AccentColor     string `json:"accent_color"`
	LogoPath        string `json:"logo_path"`
	LandingMarkdown string `json:"landing_markdown"`

	// HTTPS Configuration
	UseHTTPS         bool     `json:"use_https"`
	AutoHTTPS        bool     `json:"auto_https"` // ACME
	HTTPSPort        string   `json:"https_port"`
	CertFile         string   `json:"cert_file"`
	KeyFile          string   `json:"key_file"`
	ACMEEmail        string   `json:"acme_email"`
	ACMEDirectoryURL string   `json:"acme_directory_url"` // Support for custom CAs (Step-CA, etc)
	Domains          []string `json:"domains"`

	// Directory Listing (YP)
	DirectoryListing bool   `json:"directory_listing"`
	DirectoryServer  string `json:"directory_server"`

	// Internal Streamer (AutoDJ)
	AutoDJs []*AutoDJConfig `json:"autodjs"`

	// Ingest (RTMP/SRT)
	Ingest *IngestConfig `json:"ingest"`

	// Multi-tenant
	MultiTenant *MultiTenantConfig `json:"multi_tenant"`
	Users       map[string]*User   `json:"users"`
	APITokens   []*APIToken        `json:"api_tokens,omitempty"`

	// Audit logging
	AuditEnabled bool `json:"audit_enabled"`

	// Auth: Setup & Onboarding
	SetupComplete bool `json:"setup_complete"`

	// Auth: OIDC
	OIDCProviders []*OIDCProvider `json:"oidc_providers,omitempty"`
	PendingUsers  []*PendingUser  `json:"pending_users,omitempty"`

	// Auth: WebAuthn
	WebAuthn *WebAuthnConfig `json:"webauthn,omitempty"`

	// Auth: Email Notifications
	SMTP *SMTPConfig `json:"smtp,omitempty"`
}

func (c *Config) IsWhitelisted(ip string) bool {
	for _, existingIP := range c.WhitelistedIPs {
		if existingIP == ip {
			return true
		}
	}
	return false
}

func (c *Config) AddWhitelistedIP(ip string) {
	if !c.IsWhitelisted(ip) {
		c.WhitelistedIPs = append(c.WhitelistedIPs, ip)
	}
}

func (c *Config) RemoveWhitelistedIP(ip string) {
	for i, existingIP := range c.WhitelistedIPs {
		if existingIP == ip {
			c.WhitelistedIPs = append(c.WhitelistedIPs[:i], c.WhitelistedIPs[i+1:]...)
			return
		}
	}
}

func (c *Config) IsBanned(ip string) bool {
	for _, existingIP := range c.BannedIPs {
		if existingIP == ip {
			return true
		}
	}
	return false
}

func (c *Config) AddBannedIP(ip string) {
	if !c.IsBanned(ip) {
		c.BannedIPs = append(c.BannedIPs, ip)
	}
}

func (c *Config) RemoveBannedIP(ip string) {
	for i, existingIP := range c.BannedIPs {
		if existingIP == ip {
			c.BannedIPs = append(c.BannedIPs[:i], c.BannedIPs[i+1:]...)
			return
		}
	}
}

func HashPassword(p string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(p), 12)
	return string(bytes), err
}

func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

func LoadConfig(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	decoder := json.NewDecoder(file)
	config := &Config{}
	err = decoder.Decode(config)
	if err != nil {
		return nil, err
	}

	config.ConfigPath = path
	config.setDefaults()

	return config, nil
}

func (config *Config) setDefaults() {
	config.setBasicDefaults()
	config.initMapsAndArrays()
	config.handleMigrations()
}

func (config *Config) setBasicDefaults() {
	if config.BindHost == "" {
		config.BindHost = "0.0.0.0"
	}
	if config.Port == "" {
		config.Port = "8000"
	}
	if config.AdminUser == "" {
		config.AdminUser = "admin"
	}
	if config.MaxListeners == 0 {
		config.MaxListeners = 100
	}
	if config.PageTitle == "" {
		config.PageTitle = "TinyIce"
	}
	if config.PageSubtitle == "" {
		config.PageSubtitle = "Live Streaming Server powered by Go"
	}
	if config.AccentColor == "" {
		config.AccentColor = "#ff6600"
	}
	if config.HTTPSPort == "" {
		config.HTTPSPort = "443"
	}
	if config.DirectoryServer == "" {
		config.DirectoryServer = "http://dir.xiph.org/cgi-bin/yp-cgi"
	}
	// Existing configs are already set up
	if config.AdminPassword != "" && !config.SetupComplete {
		config.SetupComplete = true
	}
}

func (config *Config) initMapsAndArrays() {
	if config.Mounts == nil {
		config.Mounts = make(map[string]string)
	}
	if config.DisabledMounts == nil {
		config.DisabledMounts = make(map[string]bool)
	}
	if config.VisibleMounts == nil {
		config.VisibleMounts = make(map[string]bool)
	}
	if config.FallbackMounts == nil {
		config.FallbackMounts = make(map[string]string)
	}
	if config.Users == nil {
		config.Users = make(map[string]*User)
	}
	if config.AdvancedMounts == nil {
		config.AdvancedMounts = make(map[string]*MountSettings)
	}
	if config.Relays == nil {
		config.Relays = make([]*RelayConfig, 0)
	}
	if config.Transcoders == nil {
		config.Transcoders = make([]*TranscoderConfig, 0)
	}
	if config.Webhooks == nil {
		config.Webhooks = make([]*WebhookConfig, 0)
	}
	// Backfill IDs for legacy webhooks (pre-templating). Done in-place so
	// the next SaveConfig persists them; without this the admin UI would
	// have no stable handle for edit/delete on existing entries.
	for _, wh := range config.Webhooks {
		if wh.ID == "" {
			wh.ID = NewWebhookID()
		}
	}
	if config.BannedIPs == nil {
		config.BannedIPs = make([]string, 0)
	}
	if config.WhitelistedIPs == nil {
		config.WhitelistedIPs = make([]string, 0)
	}
	if config.AutoDJs == nil {
		config.AutoDJs = make([]*AutoDJConfig, 0)
	}
	if config.OIDCProviders == nil {
		config.OIDCProviders = make([]*OIDCProvider, 0)
	}
	if config.PendingUsers == nil {
		config.PendingUsers = make([]*PendingUser, 0)
	}
	if config.Ingest == nil {
		config.Ingest = &IngestConfig{
			RTMPPort:   "1935",
			SRTPort:    "9000",
			SRTLatency: 120,
		}
	}
	if config.MultiTenant == nil {
		config.MultiTenant = &MultiTenantConfig{
			DefaultTenant: "default",
			TenantStore:   "config",
		}
	}
}

func (config *Config) handleMigrations() {
	// Migration/Backward compatibility — lift a legacy top-level
	// AdminUser/AdminPassword pair into the unified Users map.
	// Only when there are no users at all. Lifting whenever the legacy
	// name was missing meant a superadmin who had deleted the original
	// account (or renamed it) got it silently recreated on every restart,
	// with the original password from the legacy admin_password field.
	if config.AdminUser != "" && len(config.Users) == 0 {
		config.Users[config.AdminUser] = &User{
			Username: config.AdminUser,
			Password: config.AdminPassword,
			Role:     RoleSuperAdmin,
			Mounts:   make(map[string]string),
		}
	}
	// Note: we used to unconditionally `r.Enabled = true` on every relay
	// here which silently reverted the per-relay toggle on every restart.
	// That line is gone — the Enabled flag is now honoured as persisted.
}

// configMapMu guards every map inside Config (Users, Mounts, VisibleMounts,
// DisabledMounts, AdvancedMounts, FallbackMounts and each User's Mounts)
// against concurrent iteration and mutation. SaveConfig marshals the live
// struct — reflect walks those maps — and it runs from a background timer
// (API-token last-used tracking) as well as from handlers, so any handler
// mutating a map at that moment hit Go's fatal "concurrent map iteration
// and map write", which is not a panic and ends the process. Mutators
// take Lock() around the write; SaveConfig takes the read side. Never
// call SaveConfig while holding Lock().
var configMapMu sync.RWMutex

// LockMaps / UnlockMaps bracket a mutation of any Config map. (Not
// named Lock/Unlock: that would make Config a sync.Locker in vet's eyes
// and flag every by-value use of the struct.)
func (c *Config) LockMaps()   { configMapMu.Lock() }
func (c *Config) UnlockMaps() { configMapMu.Unlock() }

func (c *Config) SaveConfig() error {
	configSaveMu.Lock()
	defer configSaveMu.Unlock()
	configMapMu.RLock()
	data, err := json.MarshalIndent(c, "", "    ")
	configMapMu.RUnlock()
	if err != nil {
		return err
	}
	tmpPath := c.ConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return err
	}
	return os.Rename(tmpPath, c.ConfigPath)
}
