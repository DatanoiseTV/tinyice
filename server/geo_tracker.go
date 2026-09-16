package server

import (
	"sort"
	"sync"

	"github.com/DatanoiseTV/tinyice/relay"
)

// GeoTracker keeps a live count of listeners per (city, country,
// mount). handleListener calls Add when a listener subscribes and
// Remove when the handler returns. /admin/geo + the dashboard SSE
// 'geo' event read Snapshot.
//
// We aggregate at city granularity (not per-IP) so concurrent
// listeners from the same city collapse into one bigger bubble on
// the map, and so the snapshot stays small even on a busy night
// (cities are bounded; raw IPs are not).
type GeoTracker struct {
	mu     sync.Mutex
	geo    *relay.GeoLookup
	counts map[geoKey]int // (city, mount) -> count
}

type geoKey struct {
	ISO   string
	City  string
	Lat   int32 // lat * 1e3 — keeps map keys hashable + safe across float jitter
	Lon   int32
	Mount string
}

func encodeCoord(v float64) int32 { return int32(v * 1e3) }
func decodeCoord(v int32) float64 { return float64(v) / 1e3 }

func NewGeoTracker(g *relay.GeoLookup) *GeoTracker {
	return &GeoTracker{
		geo:    g,
		counts: make(map[geoKey]int),
	}
}

// addedCity is what Remove needs to undo a previous Add — Add
// returns it so callers can pass the same value back regardless of
// whether the underlying GeoIP DB updates between Add and Remove.
type addedCity struct {
	ISO  string
	City string
	Lat  int32
	Lon  int32
}

// Add looks up `ip` and increments its count for `mount`. Returns
// the resolved city handle so Remove can undo this exact entry,
// even if the database swaps under us. A zero return is a no-op
// for Remove.
func (t *GeoTracker) Add(ip, mount string) addedCity {
	if t == nil || t.geo == nil {
		return addedCity{}
	}
	info := t.geo.Lookup(ip)
	if info.ISO == "" || (info.Lat == 0 && info.Lon == 0) {
		// No resolvable location — skip, don't pin to (0,0) which
		// would cluster every unknown listener at the equator.
		return addedCity{}
	}
	a := addedCity{
		ISO:  info.ISO,
		City: info.City,
		Lat:  encodeCoord(info.Lat),
		Lon:  encodeCoord(info.Lon),
	}
	t.mu.Lock()
	t.counts[geoKey{a.ISO, a.City, a.Lat, a.Lon, mount}]++
	t.mu.Unlock()
	return a
}

// Remove decrements the count for the listener that previously
// resolved to `a` on `mount`. zero addedCity is a no-op.
func (t *GeoTracker) Remove(a addedCity, mount string) {
	if t == nil || a.ISO == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	k := geoKey{a.ISO, a.City, a.Lat, a.Lon, mount}
	if t.counts[k] <= 1 {
		delete(t.counts, k)
		return
	}
	t.counts[k]--
}

// GeoCity is one row of the /admin/geo + SSE 'geo' response.
type GeoCity struct {
	ISO       string         `json:"iso"`
	Country   string         `json:"country,omitempty"` // human-readable name
	City      string         `json:"city,omitempty"`
	Lat       float64        `json:"lat"`
	Lon       float64        `json:"lon"`
	Listeners int            `json:"listeners"` // total across all mounts
	Mounts    map[string]int `json:"mounts,omitempty"`
}

// Snapshot returns a per-city aggregated view, sorted by total
// listeners DESC. mountFilter == "" includes all mounts; otherwise
// counts are restricted to the named mount.
func (t *GeoTracker) Snapshot(mountFilter string) []GeoCity {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	type acc struct {
		iso    string
		city   string
		lat    int32
		lon    int32
		total  int
		mounts map[string]int
	}
	byCity := make(map[geoKey]*acc) // key WITHOUT mount field
	for k, v := range t.counts {
		if mountFilter != "" && k.Mount != mountFilter {
			continue
		}
		// Strip Mount from the aggregation key.
		ck := geoKey{k.ISO, k.City, k.Lat, k.Lon, ""}
		a, ok := byCity[ck]
		if !ok {
			a = &acc{iso: k.ISO, city: k.City, lat: k.Lat, lon: k.Lon, mounts: map[string]int{}}
			byCity[ck] = a
		}
		a.total += v
		a.mounts[k.Mount] += v
	}
	t.mu.Unlock()

	out := make([]GeoCity, 0, len(byCity))
	for _, a := range byCity {
		var country string
		if meta, ok := relay.CountryCentroids()[a.iso]; ok {
			country = meta.Name
		}
		out = append(out, GeoCity{
			ISO:       a.iso,
			Country:   country,
			City:      a.city,
			Lat:       decodeCoord(a.lat),
			Lon:       decodeCoord(a.lon),
			Listeners: a.total,
			Mounts:    a.mounts,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Listeners != out[j].Listeners {
			return out[i].Listeners > out[j].Listeners
		}
		if out[i].ISO != out[j].ISO {
			return out[i].ISO < out[j].ISO
		}
		return out[i].City < out[j].City
	})
	return out
}
