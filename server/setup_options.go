package server

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/DatanoiseTV/tinyice/config"
)

// setupOptions are the optional answers the first-run wizard collects. Every
// group is a pointer: omitted means "keep the default", and the response tells
// the client which groups were actually stored.
type setupOptions struct {
	Station *struct {
		Title      string `json:"title"`
		Subtitle   string `json:"subtitle"`
		Location   string `json:"location"`
		AdminEmail string `json:"admin_email"`
	} `json:"station"`
	Address *struct {
		Hostname string `json:"hostname"`
		BaseURL  string `json:"base_url"`
	} `json:"address"`
	Appearance *struct {
		AccentColor string `json:"accent_color"`
	} `json:"appearance"`
	Audience *struct {
		MaxListeners     *int  `json:"max_listeners"`
		DirectoryListing *bool `json:"directory_listing"`
	} `json:"audience"`
	Mount *struct {
		Path string `json:"path"`
	} `json:"mount"`
}

// minSetupPasswordLen is enforced server-side; the wizard also checks it, but
// the API must not depend on the client.
const minSetupPasswordLen = 8

var (
	mountPathRe = regexp.MustCompile(`^/[A-Za-z0-9._-]{1,64}$`)
	hostnameRe  = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	accentRe    = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

	// First path segments that the router owns. A mount under one of these
	// could never be reached, or would shadow a page.
	reservedMountNames = map[string]bool{
		"admin": true, "api": true, "assets": true, "auth": true, "branding": true,
		"developers": true, "embed": true, "events": true, "explore": true, "kiosk": true,
		"login": true, "logout": true, "metrics": true, "player": true, "player-webrtc": true,
		"setup": true, "status-json.xsl": true, "webrtc": true,
	}
)

// validateMountPath checks a mount name a human typed.
func validateMountPath(p string) error {
	if !mountPathRe.MatchString(p) {
		return fmt.Errorf("must start with / followed by 1 to 64 letters, digits, dot, dash or underscore")
	}
	if reservedMountNames[strings.ToLower(p[1:])] {
		return fmt.Errorf("%s is used by the server itself, pick another name", p)
	}
	return nil
}

func plainText(field, v string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if len([]rune(v)) > max {
		return "", fmt.Errorf("%s: must be %d characters or fewer", field, max)
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return "", fmt.Errorf("%s: must not contain control characters", field)
		}
	}
	return v, nil
}

// setupValues is the validated, normalised form of setupOptions.
type setupValues struct {
	station                               bool
	title, subtitle, location, adminEmail string
	address                               bool
	hostname, baseURL                     string
	appearance                            bool
	accent                                string
	audience                              bool
	maxListeners                          *int
	directoryListing                      *bool
	mount                                 string
}

// validateSetupOptions checks every supplied group before anything is stored,
// so a bad answer never leaves a half-configured server. Errors are prefixed
// with the field path the wizard jumps back to.
func validateSetupOptions(o *setupOptions) (*setupValues, error) {
	v := &setupValues{mount: "/live"}
	var err error

	if o.Station != nil {
		v.station = true
		if v.title, err = plainText("station.title", o.Station.Title, 80); err != nil {
			return nil, err
		}
		if v.subtitle, err = plainText("station.subtitle", o.Station.Subtitle, 160); err != nil {
			return nil, err
		}
		if v.location, err = plainText("station.location", o.Station.Location, 80); err != nil {
			return nil, err
		}
		if v.adminEmail, err = plainText("station.admin_email", o.Station.AdminEmail, 120); err != nil {
			return nil, err
		}
		if v.adminEmail != "" {
			a, perr := mail.ParseAddress(v.adminEmail)
			if perr != nil || a.Address != v.adminEmail {
				return nil, fmt.Errorf("station.admin_email: enter a plain email address such as name@example.org")
			}
		}
	}

	if o.Address != nil {
		v.address = true
		h := strings.TrimSpace(o.Address.Hostname)
		if h != "" && net.ParseIP(strings.Trim(h, "[]")) == nil && !hostnameRe.MatchString(h) {
			return nil, fmt.Errorf("address.hostname: use a host name or IP address only, without http:// or a port")
		}
		v.hostname = h
		if b := strings.TrimSpace(o.Address.BaseURL); b != "" {
			u, perr := url.Parse(b)
			if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
				u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
				return nil, fmt.Errorf("address.base_url: use the address listeners open, like https://radio.example.org (no path)")
			}
			v.baseURL = u.Scheme + "://" + u.Host
		}
	}

	if o.Appearance != nil {
		v.appearance = true
		a := strings.TrimSpace(o.Appearance.AccentColor)
		if a != "" {
			if !accentRe.MatchString(a) {
				return nil, fmt.Errorf("appearance.accent_color: use a hex colour like #ff6a00")
			}
			v.accent = strings.ToLower(a)
		}
	}

	if o.Audience != nil {
		v.audience = true
		if n := o.Audience.MaxListeners; n != nil {
			if *n < 0 || *n > config.MaxListenersCeiling {
				return nil, fmt.Errorf("audience.max_listeners: use a whole number from 0 (unlimited) to %d", config.MaxListenersCeiling)
			}
			v.maxListeners = n
		}
		v.directoryListing = o.Audience.DirectoryListing
	}

	if o.Mount != nil && strings.TrimSpace(o.Mount.Path) != "" {
		p := strings.TrimSpace(o.Mount.Path)
		if verr := validateMountPath(p); verr != nil {
			return nil, fmt.Errorf("mount.path: %v", verr)
		}
		v.mount = p
	}

	// A group counts as applied only if it carries a value that will be stored,
	// so the response never claims a setting that was not changed.
	v.station = v.title != "" || v.subtitle != "" || v.location != "" || v.adminEmail != ""
	v.address = v.hostname != "" || v.baseURL != ""
	v.appearance = v.accent != ""
	v.audience = v.maxListeners != nil || v.directoryListing != nil
	return v, nil
}
