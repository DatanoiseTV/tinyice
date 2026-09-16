package config

import "testing"

// The admin UI offered an "OGG" format while every encoder path is
// `Format == "opus"` with MP3 as the fallback, so an AutoDJ configured as
// OGG streamed MP3 and advertised audio/mpeg. The stored value has to
// collapse onto what the encoder actually produces, or the config keeps
// describing a stream that was never emitted.
func TestNormalizeAutoDJFormat(t *testing.T) {
	for in, want := range map[string]string{
		"opus":   "opus",
		"Opus":   "opus",
		" OPUS":  "opus",
		"mp3":    "mp3",
		"MP3":    "mp3",
		"ogg":    "mp3", // never implemented; MP3 is what was really streamed
		"vorbis": "mp3",
		"":       "mp3",
		"flac":   "mp3",
	} {
		if got := NormalizeAutoDJFormat(in); got != want {
			t.Errorf("NormalizeAutoDJFormat(%q) = %q, want %q", in, got, want)
		}
	}
}

// An existing config carrying the format that never worked is rewritten
// on load, so the admin UI stops showing a setting that has no effect.
// The rewrite cannot change any listener's stream: MP3 is what the
// encoder was already producing for that value.
func TestLoadRewritesAnUnsupportedAutoDJFormat(t *testing.T) {
	c := &Config{AutoDJs: []*AutoDJConfig{
		{Mount: "/a", Format: "ogg"},
		{Mount: "/b", Format: "opus"},
		{Mount: "/c", Format: ""},
		nil,
	}}
	normalizeAutoDJFormats(c)

	if c.AutoDJs[0].Format != "mp3" {
		t.Errorf("/a format = %q, want mp3", c.AutoDJs[0].Format)
	}
	if c.AutoDJs[1].Format != "opus" {
		t.Errorf("/b format = %q, want opus untouched", c.AutoDJs[1].Format)
	}
	if c.AutoDJs[2].Format != "mp3" {
		t.Errorf("/c format = %q, want mp3", c.AutoDJs[2].Format)
	}
}
