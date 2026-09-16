package relay

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DatanoiseTV/tinyice/logger"
	"github.com/oschwald/maxminddb-golang"
)

// GeoIP — free, key-less city lookup for listener locations.
//
// We pull DB-IP's "ip-to-city-lite" database, published monthly
// under CC-BY-4.0 with no API key, no signup, and no per-request
// quota. The .mmdb file is ~250 MB uncompressed; we cache it under
// dataDir/dbip-city-lite.mmdb and refresh whenever it crosses 30
// days stale. City lookups give us per-listener lat/lon directly,
// which the dashboard plots as one bubble per city instead of one
// per country.
//
// Attribution requirement (CC-BY-4.0): the dashboard surfaces
// "GeoIP data: db-ip.com / CC BY 4.0" near the map.

const (
	dbipFilename     = "dbip-city-lite.mmdb"
	dbipMaxAge       = 30 * 24 * time.Hour
	dbipDownloadTmpl = "https://download.db-ip.com/free/dbip-city-lite-%s.mmdb.gz"
	dbipUserAgent    = "tinyice-geoip/1.0 (+https://github.com/DatanoiseTV/tinyice)"
)

// GeoInfo is the lookup result. Empty fields mean "unknown" — the
// city DB only resolves to country for some IP ranges (corporate
// allocations, CGNAT pools, etc.); we still surface those at the
// country centroid using the legacy CountryCentroids() table.
type GeoInfo struct {
	ISO  string  // ISO-3166-1 alpha-2 country code
	City string  // city common name, English
	Lat  float64 // city latitude
	Lon  float64 // city longitude
}

// GeoLookup is the read side of the geoip cache. Listener-connect
// hot path takes a read lock per Lookup; a periodic update goroutine
// takes the write lock to swap in a fresh mmdb. Lookup is allocation-
// free (the maxminddb library reads into the supplied struct).
type GeoLookup struct {
	mu     sync.RWMutex
	reader *maxminddb.Reader
	loaded time.Time
	dir    string
}

// NewGeoLookup loads dataDir/dbip-country-lite.mmdb if present and
// kicks off a background updater. Returns a non-nil GeoLookup even
// when the file is absent or corrupt — Lookup just returns "" until
// the first download lands. Pass an empty dir to disable persistence
// + auto-update entirely (useful in tests).
func NewGeoLookup(dataDir string) *GeoLookup {
	g := &GeoLookup{dir: dataDir}
	if dataDir == "" {
		return g
	}
	_ = os.MkdirAll(dataDir, 0o755)
	if err := g.tryLoad(); err != nil {
		logger.L.Infof("geoip: no usable database yet (%v); will fetch in background", err)
	}
	go g.updater()
	return g
}

func (g *GeoLookup) path() string { return filepath.Join(g.dir, dbipFilename) }

// tryLoad opens the cached mmdb under the data directory and swaps
// it in atomically.
func (g *GeoLookup) tryLoad() error {
	p := g.path()
	st, err := os.Stat(p)
	if err != nil {
		return err
	}
	rdr, err := maxminddb.Open(p)
	if err != nil {
		return err
	}
	g.mu.Lock()
	if g.reader != nil {
		_ = g.reader.Close()
	}
	g.reader = rdr
	g.loaded = st.ModTime()
	g.mu.Unlock()
	logger.L.Infow("geoip: database loaded",
		"path", p, "build", st.ModTime().UTC().Format(time.RFC3339), "size_kb", st.Size()/1024)
	return nil
}

// Lookup returns the city + country + lat/lon for the given IP,
// or a zero GeoInfo when the IP is private / loopback / not
// resolvable / the database isn't loaded. Safe to call
// concurrently. Falls back to the embedded CountryCentroids table
// when the MMDB record has a country code but no city/coords (a
// real DB-IP lite quirk for some CGNAT ranges).
func (g *GeoLookup) Lookup(ip string) GeoInfo {
	if g == nil || ip == "" {
		return GeoInfo{}
	}
	addr := net.ParseIP(stripPort(ip))
	if addr == nil || addr.IsLoopback() || addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
		return GeoInfo{}
	}
	g.mu.RLock()
	rdr := g.reader
	g.mu.RUnlock()
	if rdr == nil {
		return GeoInfo{}
	}
	var rec struct {
		Country struct {
			ISOCode string `maxminddb:"iso_code"`
		} `maxminddb:"country"`
		City struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"city"`
		Location struct {
			Latitude  float64 `maxminddb:"latitude"`
			Longitude float64 `maxminddb:"longitude"`
		} `maxminddb:"location"`
	}
	if err := rdr.Lookup(addr, &rec); err != nil {
		return GeoInfo{}
	}
	info := GeoInfo{
		ISO:  strings.ToUpper(rec.Country.ISOCode),
		City: rec.City.Names["en"],
		Lat:  rec.Location.Latitude,
		Lon:  rec.Location.Longitude,
	}
	// MMDB encoded an iso but no lat/lon — fall back to the country
	// centroid so we still place the marker SOMEWHERE meaningful.
	if info.ISO != "" && info.Lat == 0 && info.Lon == 0 {
		if meta, ok := countryCentroids[info.ISO]; ok {
			info.Lat = meta.Lat
			info.Lon = meta.Lon
			if info.City == "" {
				info.City = meta.Name
			}
		}
	}
	return info
}

// LookupCountry is a back-compat shim that returns just the ISO
// alpha-2 (or "" when unresolvable). Existing callers that don't
// need the city/lat/lon continue to compile.
func (g *GeoLookup) LookupCountry(ip string) string {
	return g.Lookup(ip).ISO
}

func stripPort(s string) string {
	if h, _, err := net.SplitHostPort(s); err == nil {
		return h
	}
	return s
}

// updater runs forever. Sleeps until the loaded mmdb crosses
// dbipMaxAge, then downloads the current month's release and
// reloads. Failures retry with a 30 min → 6 h capped backoff.
func (g *GeoLookup) updater() {
	if g.dir == "" {
		return
	}
	backoff := 30 * time.Minute
	for {
		g.mu.RLock()
		age := time.Since(g.loaded)
		hasReader := g.reader != nil
		g.mu.RUnlock()
		var delay time.Duration
		switch {
		case !hasReader:
			delay = 0
		case age >= dbipMaxAge:
			delay = 0
		default:
			delay = dbipMaxAge - age
		}
		if delay > 0 {
			time.Sleep(delay)
		}
		if err := g.fetchAndSwap(); err != nil {
			logger.L.Warnw("geoip: update failed; will retry", "error", err.Error(), "retry_in", backoff)
			time.Sleep(backoff)
			if backoff < 6*time.Hour {
				backoff *= 2
			}
			continue
		}
		backoff = 30 * time.Minute
	}
}

// fetchAndSwap downloads the current month's lite mmdb and atomically
// replaces the cached file + open reader. Tries the current month
// first; on 404 (early-month publishing window) falls back to the
// previous month.
func (g *GeoLookup) fetchAndSwap() error {
	now := time.Now().UTC()
	candidates := []string{
		now.Format("2006-01"),
		now.AddDate(0, -1, 0).Format("2006-01"),
	}
	var lastErr error
	for _, ym := range candidates {
		url := fmt.Sprintf(dbipDownloadTmpl, ym)
		if err := g.downloadTo(url, g.path()); err != nil {
			lastErr = err
			continue
		}
		return g.tryLoad()
	}
	if lastErr == nil {
		lastErr = errors.New("no candidate URL succeeded")
	}
	return lastErr
}

// geoipClient carries the outbound address policy; the 5 minute bound
// is on the request context, since the download can be large.
var geoipClient = NewOutboundHTTPClient(0)

func (g *GeoLookup) downloadTo(url, dest string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", dbipUserAgent)
	// The database URL is configurable, so the download goes through the
	// same dial-time address policy as every other outbound request.
	resp, err := geoipClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s for %s", resp.Status, url)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return fmt.Errorf("gunzip: %w", err)
	}
	defer gz.Close()
	tmp := dest + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, gz); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dest)
}
