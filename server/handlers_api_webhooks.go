package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/DatanoiseTV/tinyice/config"
)

// ---------------------------------------------------------------------------
// Webhook CRUD API (admin UI)
// ---------------------------------------------------------------------------

// requireSuperAdminJSON consolidates the auth/CSRF guard used by every
// webhook endpoint. Returns false if the response has already been
// written, in which case the caller must just return.
func (s *Server) requireSuperAdminJSON(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodGet && !s.isCSRFSafe(r) {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return false
	}
	user, ok := s.checkAuth(r)
	if !ok {
		jsonError(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	if user.Role != config.RoleSuperAdmin {
		jsonError(w, "Forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// apiGetWebhookMeta serves the static metadata the admin UI needs to
// build the form: the canonical event list with sample payloads, the
// available placeholders per event, and the preset templates. One
// round-trip on page load means the editor doesn't need to hard-code
// anything that lives on the server.
func (s *Server) apiGetWebhookMeta(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuperAdminJSON(w, r) {
		return
	}

	type eventInfo struct {
		Name         string                 `json:"name"`
		Description  string                 `json:"description"`
		Sample       map[string]interface{} `json:"sample"`
		Placeholders []string               `json:"placeholders"`
	}

	events := []eventInfo{
		{
			Name:        "now_playing",
			Description: "A new track started playing on an AutoDJ mount.",
		},
		{
			Name:        "source_connect",
			Description: "An external source (broadcaster) connected to a mount.",
		},
		{
			Name:        "source_disconnect",
			Description: "An external source disconnected from a mount.",
		},
		{
			Name:        "metadata_update",
			Description: "Mount metadata (title/artist/song) changed.",
		},
		{
			Name:        "security_lockout",
			Description: "An IP was locked out for repeated auth failures.",
		},
	}
	for i := range events {
		sample := SampleEventData(events[i].Name)
		events[i].Sample = sample
		// Per-event placeholders are derived from the sample payload:
		// each key shows up CamelCased so users can write the same form
		// the renderer actually accepts ({{.DurationSeconds}}, not
		// {{.duration_seconds}} — even though both work).
		ph := []string{}
		for k := range sample {
			if k != "" {
				ph = append(ph, snakeToCamel(k))
			}
		}
		// When the payload carries a Mount, the dispatcher synthesises
		// MountURL / PlayerURL from the configured BaseURL. Surface them
		// so users discover the helpers in the editor.
		if _, ok := sample["mount"]; ok {
			ph = append(ph, "MountURL", "PlayerURL")
		}
		events[i].Placeholders = ph
	}

	jsonResponse(w, map[string]interface{}{
		"events":              events,
		"global_placeholders": WebhookGlobalPlaceholders,
		"presets":             builtinPresets,
		"funcs": []map[string]string{
			{"name": "urlencode", "description": "URL-encode a string for query params or URLs.", "example": "{{urlencode .Title}}"},
			{"name": "json", "description": "Marshal any value to a JSON literal.", "example": "{{json .}}"},
			{"name": "lower", "description": "Lowercase a string.", "example": "{{lower .Mount}}"},
			{"name": "upper", "description": "Uppercase a string.", "example": "{{upper .Event}}"},
		},
	})
}

func (s *Server) apiGetWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuperAdminJSON(w, r) {
		return
	}
	jsonResponse(w, s.Config.Webhooks)
}

type webhookBody struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	URL          string            `json:"url"`
	Method       string            `json:"method"`
	Events       []string          `json:"events"`
	Headers      map[string]string `json:"headers"`
	BodyTemplate string            `json:"body_template"`
	ContentType  string            `json:"content_type"`
	Enabled      bool              `json:"enabled"`
}

// validateWebhookBody enforces the invariants we need before persisting.
// URL is checked through the same SSRF guard used by relays so an admin
// can't quietly turn the webhook system into an internal-network probe.
func validateWebhookBody(b *webhookBody) error {
	b.URL = strings.TrimSpace(b.URL)
	if b.URL == "" {
		return errBadRequest("URL is required")
	}
	if err := validateOutboundURL(b.URL); err != nil {
		return errBadRequest("URL rejected: " + err.Error())
	}
	if len(b.Events) == 0 {
		return errBadRequest("at least one event is required")
	}
	known := map[string]bool{}
	for _, e := range WebhookEvents {
		known[e] = true
	}
	for _, e := range b.Events {
		if !known[e] {
			return errBadRequest("unknown event: " + e)
		}
	}
	if b.Method != "" {
		switch strings.ToUpper(b.Method) {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
		default:
			return errBadRequest("unsupported HTTP method: " + b.Method)
		}
	}
	return nil
}

// errBadRequest is a tiny sentinel for validateWebhookBody so callers
// can map its message straight onto a 400 response.
type errBadRequest string

func (e errBadRequest) Error() string { return string(e) }

func (s *Server) apiCreateWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuperAdminJSON(w, r) {
		return
	}
	var b webhookBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := validateWebhookBody(&b); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	wh := &config.WebhookConfig{
		ID:           "", // set below
		Name:         strings.TrimSpace(b.Name),
		URL:          b.URL,
		Method:       strings.ToUpper(strings.TrimSpace(b.Method)),
		Events:       b.Events,
		Headers:      b.Headers,
		BodyTemplate: b.BodyTemplate,
		ContentType:  strings.TrimSpace(b.ContentType),
		Enabled:      b.Enabled,
	}
	wh.ID = newWebhookIDForServer()
	s.Config.Webhooks = append(s.Config.Webhooks, wh)
	s.Config.SaveConfig()
	jsonResponse(w, wh)
	s.Audit(r, "webhook_created", "webhook", wh.ID, wh.URL)
}

func (s *Server) apiUpdateWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuperAdminJSON(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, "id query param is required", http.StatusBadRequest)
		return
	}
	var b webhookBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		jsonError(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if err := validateWebhookBody(&b); err != nil {
		jsonError(w, err.Error(), http.StatusBadRequest)
		return
	}
	for _, wh := range s.Config.Webhooks {
		if wh.ID == id {
			wh.Name = strings.TrimSpace(b.Name)
			wh.URL = b.URL
			wh.Method = strings.ToUpper(strings.TrimSpace(b.Method))
			wh.Events = b.Events
			wh.Headers = b.Headers
			wh.BodyTemplate = b.BodyTemplate
			wh.ContentType = strings.TrimSpace(b.ContentType)
			wh.Enabled = b.Enabled
			s.Config.SaveConfig()
			jsonResponse(w, wh)
			s.Audit(r, "webhook_updated", "webhook", wh.ID, wh.URL)
			return
		}
	}
	jsonError(w, "Webhook not found", http.StatusNotFound)
}

func (s *Server) apiDeleteWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.requireSuperAdminJSON(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		jsonError(w, "id query param is required", http.StatusBadRequest)
		return
	}
	out := s.Config.Webhooks[:0]
	deleted := ""
	for _, wh := range s.Config.Webhooks {
		if wh.ID != id {
			out = append(out, wh)
		} else {
			deleted = wh.URL
		}
	}
	if deleted == "" {
		jsonError(w, "Webhook not found", http.StatusNotFound)
		return
	}
	s.Config.Webhooks = out
	s.Config.SaveConfig()
	jsonResponse(w, map[string]string{"status": "deleted"})
	s.Audit(r, "webhook_deleted", "webhook", id, deleted)
}

// apiTestWebhook fires a sample payload for the requested event so the
// operator can verify their template + URL before relying on a real
// track change to trigger it. Uses the canonical sample data that
// SampleEventData advertises through the meta endpoint, so what they
// see in the placeholder reference is exactly what they receive.
func (s *Server) apiTestWebhook(w http.ResponseWriter, r *http.Request) {
	// Sends an outbound request — a side effect, so not on GET.
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.requireSuperAdminJSON(w, r) {
		return
	}
	id := r.URL.Query().Get("id")
	event := r.URL.Query().Get("event")
	if event == "" {
		event = "now_playing"
	}
	if id == "" {
		jsonError(w, "id query param is required", http.StatusBadRequest)
		return
	}
	for _, wh := range s.Config.Webhooks {
		if wh.ID == id {
			data := SampleEventData(event)
			// Run inline (not via dispatchWebhook) so subscription gating
			// doesn't suppress the test — the operator explicitly asked
			// for this event regardless of the webhook's Events list.
			go s.deliverWebhook(wh, event, data)
			jsonResponse(w, map[string]string{"status": "queued", "event": event})
			s.Audit(r, "webhook_tested", "webhook", id, event)
			return
		}
	}
	jsonError(w, "Webhook not found", http.StatusNotFound)
}

// newWebhookIDForServer delegates to the canonical generator in config
// so this file doesn't need its own crypto/rand import.
func newWebhookIDForServer() string {
	return config.NewWebhookID()
}
