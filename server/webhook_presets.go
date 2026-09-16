package server

// WebhookPreset is a ready-to-use webhook template for a specific
// third-party service. Presets are advertised through the API so the
// admin UI can offer a dropdown that pre-fills method / headers /
// body template, leaving only the URL (and the event subscription)
// to the operator.
//
// Each preset matches the field names, content type and HTTP method
// documented by the receiver at the time of writing. The URLHint guides
// the operator to the right credential page; some presets have
// placeholder tokens (<TOKEN>, <CHAT_ID>, …) the operator must
// replace before saving.
type WebhookPreset struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	URLHint     string            `json:"url_hint"`
	Events      []string          `json:"events"`
	Method      string            `json:"method"`
	ContentType string            `json:"content_type"`
	Headers     map[string]string `json:"headers,omitempty"`
	Body        string            `json:"body"`
}

// builtinPresets is the list shipped with tinyice. Order matters — it's
// the order shown in the admin dropdown, with the most common
// integrations first.
var builtinPresets = []WebhookPreset{
	{
		ID:          "generic_json",
		Name:        "Generic JSON envelope",
		Description: "Default tinyice envelope with event, timestamp, hostname and payload — works with anything that accepts JSON.",
		URLHint:     "https://example.com/webhooks/tinyice",
		Events:      []string{"now_playing", "source_connect", "source_disconnect", "metadata_update", "security_lockout"},
		Method:      "POST",
		ContentType: "application/json",
		Body:        "", // empty body uses the legacy envelope path in dispatchWebhook
	},
	{
		ID:          "discord",
		Name:        "Discord channel webhook",
		Description: "Posts a one-line message to a Discord channel using an incoming webhook. Create the webhook in: Server Settings → Integrations → Webhooks.",
		URLHint:     "https://discord.com/api/webhooks/<channel-id>/<token>",
		Events:      []string{"now_playing"},
		Method:      "POST",
		ContentType: "application/json",
		Body: `{
  "username": "TinyIce",
  "content": ":musical_note: Now playing on **{{.Mount}}**: **{{.Artist}} – {{.Title}}**{{if .MountURL}} — [Listen]({{.MountURL}}){{end}}"
}`,
	},
	{
		ID:          "slack",
		Name:        "Slack incoming webhook",
		Description: "Posts to a Slack channel via an Incoming Webhook app. The text field is required; blocks/attachments can be added.",
		URLHint:     "https://hooks.slack.com/services/T.../B.../...",
		Events:      []string{"now_playing", "source_connect", "source_disconnect"},
		Method:      "POST",
		ContentType: "application/json",
		Body: `{
  "text": ":musical_note: *{{.Mount}}* — {{.Artist}} – {{.Title}}{{if .MountURL}} (<{{.MountURL}}|Listen>){{end}}"
}`,
	},
	{
		ID:          "mattermost",
		Name:        "Mattermost incoming webhook",
		Description: "Same payload shape as Slack. Configure under Integrations → Incoming Webhooks in Mattermost.",
		URLHint:     "https://mattermost.example.com/hooks/<id>",
		Events:      []string{"now_playing"},
		Method:      "POST",
		ContentType: "application/json",
		Body: `{
  "username": "TinyIce",
  "text": "🎵 **{{.Mount}}** — {{.Artist}} – {{.Title}}"
}`,
	},
	{
		ID:          "msteams",
		Name:        "Microsoft Teams (MessageCard)",
		Description: "Posts a legacy MessageCard via an Incoming Webhook connector. Works with classic Teams channel webhooks.",
		URLHint:     "https://outlook.office.com/webhook/...",
		Events:      []string{"now_playing"},
		Method:      "POST",
		ContentType: "application/json",
		Body: `{
  "@type": "MessageCard",
  "@context": "https://schema.org/extensions",
  "summary": "TinyIce now playing",
  "themeColor": "0078D4",
  "title": "Now playing on {{.Mount}}",
  "text": "**{{.Artist}}** – {{.Title}}"
}`,
	},
	{
		ID:          "telegram",
		Name:        "Telegram bot — sendMessage",
		Description: "Sends a message via a Telegram bot. URL must include your bot token. Replace <CHAT_ID> with the target chat.",
		URLHint:     "https://api.telegram.org/bot<TOKEN>/sendMessage",
		Events:      []string{"now_playing"},
		Method:      "POST",
		ContentType: "application/json",
		Body: `{
  "chat_id": "<CHAT_ID>",
  "text": "🎵 {{.Mount}} — {{.Artist}} – {{.Title}}",
  "parse_mode": "Markdown"
}`,
	},
	{
		ID:          "ntfy",
		Name:        "ntfy.sh push notification",
		Description: "Publishes a push notification to an ntfy topic. Plain-text body; topic is the URL path.",
		URLHint:     "https://ntfy.sh/<your-topic>",
		Events:      []string{"now_playing"},
		Method:      "POST",
		ContentType: "text/plain",
		Headers: map[string]string{
			"Title":    "TinyIce — {{.Mount}}",
			"Tags":     "musical_note",
			"Priority": "default",
		},
		Body: `{{.Artist}} – {{.Title}}`,
	},
	{
		ID:          "pushover",
		Name:        "Pushover — messages.json",
		Description: "Sends a Pushover notification. Replace <APP_TOKEN> and <USER_KEY> with the values from your Pushover console.",
		URLHint:     "https://api.pushover.net/1/messages.json",
		Events:      []string{"now_playing"},
		Method:      "POST",
		ContentType: "application/x-www-form-urlencoded",
		Body:        `token=<APP_TOKEN>&user=<USER_KEY>&title={{urlencode .Mount}}&message={{urlencode .Artist}}+-+{{urlencode .Title}}`,
	},
	{
		ID:          "tunein_air",
		Name:        "TuneIn AIR — Playing.ashx",
		Description: "Reports the now-playing track to TuneIn AIR. Replace <PARTNER_ID>, <PARTNER_KEY> and <STATION_ID> with your partner credentials.",
		URLHint:     "https://air.radiotime.com/Playing.ashx",
		Events:      []string{"now_playing"},
		Method:      "GET",
		ContentType: "application/x-www-form-urlencoded",
		Body:        `partnerId=<PARTNER_ID>&partnerKey=<PARTNER_KEY>&id=<STATION_ID>&title={{urlencode .Title}}&artist={{urlencode .Artist}}`,
	},
	{
		ID:          "webhook_site",
		Name:        "webhook.site (debug echo)",
		Description: "Sends the full JSON payload to a webhook.site URL — useful for debugging your template before pointing it at a real receiver.",
		URLHint:     "https://webhook.site/<your-uuid>",
		Events:      []string{"now_playing", "source_connect", "source_disconnect", "metadata_update", "security_lockout"},
		Method:      "POST",
		ContentType: "application/json",
		Body: `{
  "event": "{{.Event}}",
  "timestamp": "{{.Timestamp}}",
  "hostname": "{{.Hostname}}",
  "payload": {{json .}}
}`,
	},
}
