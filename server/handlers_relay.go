package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/DatanoiseTV/tinyice/config"
	"github.com/DatanoiseTV/tinyice/relay"
)

func (s *Server) handleAddRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if ok && user.Role == config.RoleSuperAdmin {
		u, m, pw, bs := r.FormValue("url"), r.FormValue("mount"), r.FormValue("password"), r.FormValue("burst_size")
		if u != "" && m != "" {
			if err := validateOutboundURL(u); err != nil {
				http.Error(w, "Upstream URL rejected: "+err.Error(), http.StatusBadRequest)
				return
			}
			if m[0] != '/' {
				m = "/" + m
			}
			burst := 20
			fmt.Sscanf(bs, "%d", &burst)

			found := false
			for _, rc := range s.Config.Relays {
				if rc.Mount == m {
					rc.URL = u
					rc.Password = pw
					rc.BurstSize = burst
					found = true
					break
				}
			}

			if !found {
				rc := &config.RelayConfig{URL: u, Mount: m, Password: pw, BurstSize: burst, Enabled: true}
				s.Config.Relays = append(s.Config.Relays, rc)
			}

			s.Config.SaveConfig()
			s.RelayM.StartRelay(u, m, pw, burst, s.Config.VisibleMounts[m])
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleToggleRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		for _, rc := range s.Config.Relays {
			if rc.Mount == mount {
				rc.Enabled = !rc.Enabled
				if rc.Enabled {
					s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[mount])
				} else {
					s.RelayM.StopRelay(mount)
				}
				s.Config.SaveConfig()
				break
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleDeleteRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		for i, rc := range s.Config.Relays {
			if rc.Mount == mount {
				s.Config.Relays = append(s.Config.Relays[:i], s.Config.Relays[i+1:]...)
				s.Config.SaveConfig()
				s.RelayM.StopRelay(mount)
				break
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleRestartRelay(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	mount := r.FormValue("mount")
	if ok && user.Role == config.RoleSuperAdmin {
		for _, rc := range s.Config.Relays {
			if rc.Mount == mount {
				s.RelayM.StartRelay(rc.URL, rc.Mount, rc.Password, rc.BurstSize, s.Config.VisibleMounts[mount])
				break
			}
		}
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (s *Server) handleAddTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	name := r.FormValue("name")
	input := r.FormValue("input")
	output := r.FormValue("output")
	format := r.FormValue("format")
	visibility := normalizeTranscoderVisibility(r.FormValue("visibility"))
	var bitrate int
	fmt.Sscanf(r.FormValue("bitrate"), "%d", &bitrate)

	tc := &config.TranscoderConfig{
		Name:        name,
		InputMount:  input,
		OutputMount: output,
		Format:      format,
		Bitrate:     bitrate,
		Enabled:     true,
		Visibility:  visibility,
	}

	s.Config.Transcoders = append(s.Config.Transcoders, tc)
	s.applyTranscoderVisibilityToMap(tc)
	s.Config.SaveConfig()
	s.TranscoderM.StartTranscoder(tc)

	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

// normalizeTranscoderVisibility coerces user input to one of the three
// supported values, defaulting unknown / empty input to "" (follow input).
func normalizeTranscoderVisibility(v string) string {
	switch v {
	case "public", "unlisted":
		return v
	default:
		return ""
	}
}

// applyTranscoderVisibilityToMap mirrors a transcoder's explicit visibility
// choice into Config.VisibleMounts so the admin toggle button, public
// listings and source-metadata path all see the same state. When the
// transcoder is set to follow the input, we drop the override so the input
// remains authoritative.
func (s *Server) applyTranscoderVisibilityToMap(tc *config.TranscoderConfig) {
	if tc == nil || tc.OutputMount == "" {
		return
	}
	if s.Config.VisibleMounts == nil {
		s.Config.VisibleMounts = make(map[string]bool)
	}
	switch tc.Visibility {
	case "public":
		s.Config.VisibleMounts[tc.OutputMount] = true
	case "unlisted":
		s.Config.VisibleMounts[tc.OutputMount] = false
	default:
		delete(s.Config.VisibleMounts, tc.OutputMount)
	}
}

func (s *Server) handleToggleTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	name := r.FormValue("name")
	for _, tc := range s.Config.Transcoders {
		if tc.Name == name {
			tc.Enabled = !tc.Enabled
			if tc.Enabled {
				s.TranscoderM.StartTranscoder(tc)
			} else {
				s.TranscoderM.StopTranscoder(tc.OutputMount)
			}
			break
		}
	}
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

func (s *Server) handleDeleteTranscoder(w http.ResponseWriter, r *http.Request) {
	if !s.isCSRFSafe(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	user, ok := s.checkAuth(r)
	if !ok || user.Role != config.RoleSuperAdmin {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	name := r.FormValue("name")
	newTCs := []*config.TranscoderConfig{}
	for _, tc := range s.Config.Transcoders {
		if tc.Name != name {
			newTCs = append(newTCs, tc)
		} else {
			s.TranscoderM.StopTranscoder(tc.OutputMount)
			if tc.Visibility != "" {
				delete(s.Config.VisibleMounts, tc.OutputMount)
			}
		}
	}
	s.Config.Transcoders = newTCs
	s.Config.SaveConfig()
	http.Redirect(w, r, "/admin#tab-transcoding", http.StatusSeeOther)
}

func (s *Server) handleTranscoderStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkAuth(r); !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var stats []relay.TranscoderStats
	for _, tc := range s.Config.Transcoders {
		inst := s.TranscoderM.GetInstance(tc.OutputMount)
		uptime := "OFF"
		var frames, bytes int64
		active := false
		if inst != nil {
			active = true
			uptime = time.Since(inst.StartTime).Round(time.Second).String()
			frames = atomic.LoadInt64(&inst.FramesProcessed)
			bytes = atomic.LoadInt64(&inst.BytesEncoded)
		}
		stats = append(stats, relay.TranscoderStats{
			Name:            tc.Name,
			Input:           tc.InputMount,
			Output:          tc.OutputMount,
			Format:          tc.Format,
			Bitrate:         tc.Bitrate,
			Active:          active,
			FramesProcessed: frames,
			BytesEncoded:    bytes,
			Uptime:          uptime,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}
