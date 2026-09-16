package relay

import (
	"strings"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HistoryItem represents a song played on a specific mount.
type HistoryItem struct {
	ID        uint   `gorm:"primaryKey"`
	Mount     string `gorm:"index"`
	Song      string
	Timestamp time.Time `gorm:"index"`
}

// UserAgent tracks listener and source client software counts.
type UserAgent struct {
	UA    string `gorm:"primaryKey"`
	Type  string `gorm:"primaryKey"`
	Count int    `gorm:"default:1"`
}

// ListenerHistory records periodic listener counts and bandwidth usage.
type ListenerHistory struct {
	ID        uint   `gorm:"primaryKey"`
	Mount     string `gorm:"index"`
	Listeners int
	BytesIn   int64
	BytesOut  int64
	Timestamp time.Time `gorm:"index"`
}

// AuditEntry records a single admin action for the audit log.
type AuditEntry struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	Timestamp    time.Time `gorm:"index" json:"timestamp"`
	Username     string    `gorm:"index" json:"username"`
	Action       string    `gorm:"index" json:"action"`
	ResourceType string    `json:"resource_type"`
	ResourceID   string    `json:"resource_id"`
	Detail       string    `json:"detail"`
	IP           string    `json:"ip"`
}

// HistoryManager coordinates all historical data persistence using GORM.
type HistoryManager struct {
	db *gorm.DB
}

// NewHistoryManager initializes the GORM database connection and performs auto-migration.
// It uses the non-CGO SQLite driver for maximum portability.
//
// PRAGMA tuning:
//   - journal_mode=WAL — writers no longer fsync the whole DB file
//     and readers don't block writers. Without this, concurrent
//     RecordUA calls under burst load were serialising at >1 s each
//     and starving the HTTPS handler pool (login, listener handlers,
//     etc.) on the shared write lock.
//   - synchronous=NORMAL — fsync only on checkpoint, fine for
//     non-financial dashboard data; the win is ~10x write latency.
//   - busy_timeout=5000 — wait up to 5 s on the lock instead of
//     returning SQLITE_BUSY immediately, so a brief contention spike
//     doesn't bubble up as a user-visible error.
func NewHistoryManager(path string) (*HistoryManager, error) {
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: nil, // We'll handle logging via zap
	})
	if err != nil {
		return nil, err
	}

	// Perform auto-migration to ensure schema is up-to-date
	err = db.AutoMigrate(&HistoryItem{}, &UserAgent{}, &ListenerHistory{}, &AuditEntry{})
	if err != nil {
		return nil, err
	}

	hm := &HistoryManager{db: db}
	hm.migrateOldData()
	return hm, nil
}

// migrateOldData moves data from legacy tables to the new GORM-managed structure.
func (hm *HistoryManager) migrateOldData() {
	// 1. Migrate 'history' -> 'history_items'
	if hm.db.Migrator().HasTable("history") {
		logger.L.Info("Migrating legacy 'history' table...")
		err := hm.db.Exec(`INSERT INTO history_items (mount, song, timestamp) 
			SELECT mount, song, timestamp FROM history`).Error
		if err == nil {
			hm.db.Migrator().DropTable("history")
			logger.L.Info("Legacy 'history' table migrated and dropped")
		} else {
			logger.L.Errorf("Failed to migrate legacy 'history' table: %v", err)
		}
	}

	// 2. Migrate 'listener_history' -> 'listener_histories'
	if hm.db.Migrator().HasTable("listener_history") {
		logger.L.Info("Migrating legacy 'listener_history' table...")
		err := hm.db.Exec(`INSERT INTO listener_histories (mount, listeners, bytes_in, bytes_out, timestamp) 
			SELECT mount, listeners, bytes_in, bytes_out, timestamp FROM listener_history`).Error
		if err == nil {
			hm.db.Migrator().DropTable("listener_history")
			logger.L.Info("Legacy 'listener_history' table migrated and dropped")
		} else {
			logger.L.Errorf("Failed to migrate legacy 'listener_history' table: %v", err)
		}
	}
}

// RecordStats persists periodic metrics for a specific mount.
func (hm *HistoryManager) RecordStats(mount string, listeners int, bi, bo int64) {
	item := ListenerHistory{
		Mount:     mount,
		Listeners: listeners,
		BytesIn:   bi,
		BytesOut:  bo,
		Timestamp: time.Now(),
	}
	if err := hm.db.Create(&item).Error; err != nil {
		logger.L.Errorf("Failed to record historical stats: %v", err)
	}
}

// RecordUA tracks or updates usage counts for specific User-Agent
// strings. Fire-and-forget: the actual SQLite INSERT runs on its own
// goroutine so a slow disk / lock contention never blocks the
// listener-connect or source-connect handler. Caller bears no
// latency cost beyond the goroutine spawn (sub-microsecond).
func (hm *HistoryManager) RecordUA(ua, uaType string) {
	if ua == "" {
		ua = "Unknown"
	}
	go func() {
		err := hm.db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "ua"}, {Name: "type"}},
			DoUpdates: clause.Assignments(map[string]interface{}{"count": gorm.Expr("user_agents.count + 1")}),
		}).Create(&UserAgent{UA: ua, Type: uaType, Count: 1}).Error
		if err != nil {
			logger.L.Errorf("Failed to record User-Agent: %v", err)
		}
	}()
}

type UAStat struct {
	UA    string `json:"ua"`
	Count int    `json:"count"`
}

// GetTopUAs retrieves the most frequent User-Agents of a specific type.
func (hm *HistoryManager) GetTopUAs(uaType string, limit int) []UAStat {
	var stats []UAStat
	err := hm.db.Model(&UserAgent{}).
		Where("type = ?", uaType).
		Order("count DESC").
		Limit(limit).
		Find(&stats).Error
	if err != nil {
		logger.L.Errorf("Failed to get top UAs: %v", err)
		return nil
	}
	return stats
}

type HistoricalStat struct {
	Timestamp time.Time `json:"timestamp"`
	Listeners int       `json:"listeners"`
	BytesIn   int64     `json:"bytes_in"`
	BytesOut  int64     `json:"bytes_out"`
}

// TrafficTotals captures the total bytes transferred across all mounts
// within a single time window.
type TrafficTotals struct {
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
	Mounts   int   `json:"mounts"` // distinct mounts that produced any traffic in the window
}

// GetTrafficTotals walks listener_histories within the window and sums
// the per-sample deltas of BytesIn / BytesOut, handling stream restarts
// (where the cumulative counter drops to a smaller value) by treating
// the new sample's whole value as fresh traffic. The first sample of
// each mount in-window is treated as the baseline (delta zero) so we
// don't count bytes that accumulated before the window started.
//
// Returns a single aggregated TrafficTotals across all mounts.
func (hm *HistoryManager) GetTrafficTotals(window time.Duration) TrafficTotals {
	if hm == nil || hm.db == nil {
		return TrafficTotals{}
	}
	cutoff := time.Now().Add(-window)
	if window <= 0 {
		// Sentinel: "all time"
		cutoff = time.Time{}
	}

	type row struct {
		Mount    string
		BytesIn  int64
		BytesOut int64
	}
	var rows []row
	q := hm.db.Model(&ListenerHistory{}).Order("mount ASC, timestamp ASC")
	if !cutoff.IsZero() {
		q = q.Where("timestamp > ?", cutoff)
	}
	if err := q.Find(&rows).Error; err != nil {
		logger.L.Errorf("Failed to load traffic totals: %v", err)
		return TrafficTotals{}
	}

	var totals TrafficTotals
	mountsSeen := make(map[string]bool)

	var curMount string
	var prevIn, prevOut int64
	first := true
	for _, r := range rows {
		if r.Mount != curMount {
			curMount = r.Mount
			prevIn = r.BytesIn
			prevOut = r.BytesOut
			first = true
		}
		if first {
			first = false
			continue // baseline; no delta to add
		}
		if r.BytesIn >= prevIn {
			totals.BytesIn += r.BytesIn - prevIn
		} else {
			// counter reset (stream restarted) — credit the full new value
			totals.BytesIn += r.BytesIn
		}
		if r.BytesOut >= prevOut {
			totals.BytesOut += r.BytesOut - prevOut
		} else {
			totals.BytesOut += r.BytesOut
		}
		if r.BytesIn > 0 || r.BytesOut > 0 {
			mountsSeen[r.Mount] = true
		}
		prevIn = r.BytesIn
		prevOut = r.BytesOut
	}
	totals.Mounts = len(mountsSeen)
	return totals
}

// GetAllHistoricalStats retrieves metrics for all mounts within a specific duration.
func (hm *HistoryManager) GetAllHistoricalStats(duration time.Duration) map[string][]HistoricalStat {
	var results []struct {
		Mount string
		HistoricalStat
	}
	err := hm.db.Model(&ListenerHistory{}).
		Where("timestamp > ?", time.Now().Add(-duration)).
		Order("timestamp ASC").
		Find(&results).Error
	if err != nil {
		logger.L.Errorf("Failed to fetch all historical stats: %v", err)
		return nil
	}

	stats := make(map[string][]HistoricalStat)
	for _, r := range results {
		stats[r.Mount] = append(stats[r.Mount], r.HistoricalStat)
	}
	return stats
}

// Add records a new song entry in the history, avoiding duplicates and pruning old entries.
func (hm *HistoryManager) Add(mount, song string) {
	if song == "" || song == "N/A" || song == "-" {
		return
	}

	// Check last entry to avoid immediate duplicates
	var last HistoryItem
	err := hm.db.Where("mount = ?", mount).Order("id DESC").First(&last).Error
	if err == nil && last.Song == song {
		return
	}

	item := HistoryItem{
		Mount:     mount,
		Song:      song,
		Timestamp: time.Now(),
	}
	if err := hm.db.Create(&item).Error; err != nil {
		logger.L.Errorf("Failed to save history: %v", err)
		return
	}

	// Prune history to keep only the latest 100 entries per mount
	var count int64
	hm.db.Model(&HistoryItem{}).Where("mount = ?", mount).Count(&count)
	if count > 100 {
		var oldest HistoryItem
		hm.db.Where("mount = ?", mount).Order("id DESC").Offset(100).First(&oldest)
		if oldest.ID > 0 {
			hm.db.Where("mount = ? AND id <= ?", mount, oldest.ID).Delete(&HistoryItem{})
		}
	}
}

// Get retrieves the most recent song history for a mount.
func (hm *HistoryManager) Get(mount string) []HistoryItem {
	var items []HistoryItem
	err := hm.db.Where("mount = ?", mount).
		Order("id DESC").
		Limit(100).
		Find(&items).Error
	if err != nil {
		logger.L.Warnf("Failed to get history: %v", err)
		return nil
	}
	return items
}

// RecordAudit persists a single audit log entry.
func (hm *HistoryManager) RecordAudit(user, action, resourceType, resourceID, detail, ip string) {
	hm.db.Create(&AuditEntry{
		Timestamp:    time.Now(),
		Username:     user,
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Detail:       detail,
		IP:           ip,
	})
}

// AuditCategoryRule describes which audit actions belong to one UI filter
// category. Prefixes exist so that a newly added action (mount_paused,
// relay_reset, ...) lands in its category automatically: the previous
// filter was a hand-written list of exact action names that had gone
// thirteen actions stale, so selecting "Streams" hid every mount_updated,
// mount_enabled, mount_disabled and kick that had ever been recorded.
type AuditCategoryRule struct {
	Exact    []string
	Prefixes []string
}

// AuditCategoryRules maps a filter category to the actions it covers.
// Every action passed to Server.Audit must be covered by exactly one
// category; relay/history_test.go pins that.
var AuditCategoryRules = map[string]AuditCategoryRule{
	"auth": {Exact: []string{"login", "logout", "login_failed",
		"setup_complete", "passkey_registered"}, Prefixes: []string{"token_"}},
	"streams":     {Exact: []string{"stream_kicked", "listeners_kicked_all"}, Prefixes: []string{"mount_"}},
	"autodj":      {Prefixes: []string{"autodj_"}},
	"relays":      {Prefixes: []string{"relay_"}},
	"transcoders": {Prefixes: []string{"transcoder_"}},
	"webhooks":    {Prefixes: []string{"webhook_"}},
	"users":       {Prefixes: []string{"user_", "pending_"}},
	"security":    {Prefixes: []string{"ip_"}},
	"settings":    {Prefixes: []string{"settings_", "branding_", "logo_"}},
}

// AuditCategoryOf returns the filter category an action belongs to, or ""
// if no category covers it.
func AuditCategoryOf(action string) string {
	for name, rule := range AuditCategoryRules {
		for _, a := range rule.Exact {
			if a == action {
				return name
			}
		}
		for _, p := range rule.Prefixes {
			if strings.HasPrefix(action, p) {
				return name
			}
		}
	}
	return ""
}

// GetAuditLog retrieves paginated audit log entries, optionally filtered by
// category. An unrecognised category returns nothing rather than silently
// falling through to "everything".
func (hm *HistoryManager) GetAuditLog(page, limit int, category string) ([]AuditEntry, int64) {
	var entries []AuditEntry
	var total int64

	q := hm.db.Model(&AuditEntry{})
	if category != "" {
		rule, ok := AuditCategoryRules[category]
		if !ok {
			return []AuditEntry{}, 0
		}
		conds := make([]string, 0, 1+len(rule.Prefixes))
		args := make([]interface{}, 0, 1+len(rule.Prefixes))
		if len(rule.Exact) > 0 {
			conds = append(conds, "action IN ?")
			args = append(args, rule.Exact)
		}
		for _, p := range rule.Prefixes {
			conds = append(conds, "action LIKE ?")
			args = append(args, p+"%")
		}
		q = q.Where(strings.Join(conds, " OR "), args...)
	}

	// Count and Find must not share a statement: GORM carries the built
	// clauses (including the count selector) forward on a reused *gorm.DB.
	q.Session(&gorm.Session{}).Count(&total)
	q.Session(&gorm.Session{}).Order("timestamp DESC").
		Offset((page - 1) * limit).Limit(limit).Find(&entries)
	return entries, total
}
