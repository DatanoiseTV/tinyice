package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// The `stream` SSE frame was a hand-listed subset of the stats entry and
// had drifted from it: artist was hardcoded empty and the entire
// transcode group never left the server, so the dashboard's
// "<src-format> -> <out-format>" display had nothing to render. Every
// field of streamEventInfo must reach the frame.
func TestNamedStreamEventCarriesEveryStatsField(t *testing.T) {
	entry := streamEventInfo{
		Mount: "/out", Name: "Station", Listeners: 3, Viewers: 2,
		Bitrate: "320", Uptime: "1m", ContentType: "audio/mpeg", SourceIP: "10.0.0.1",
		BytesIn: 10, BytesOut: 20, BytesDropped: 1, CurrentSong: "Artist - Title",
		Health: 0.9, IsTranscoded: true,
		SourceMount: "/in", SourceContentType: "audio/ogg", SourceBitrate: "160",
		VideoWidth: 1280, VideoHeight: 720, VideoFPS: 25, VideoGOP: 2, VideoKbps: 2500,
	}
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var stMap map[string]interface{}
	if err := json.Unmarshal(raw, &stMap); err != nil {
		t.Fatal(err)
	}

	ev := namedStreamEvent(stMap)

	rt := reflect.TypeOf(entry)
	for i := 0; i < rt.NumField(); i++ {
		tag := strings.Split(rt.Field(i).Tag.Get("json"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		got, ok := ev[tag]
		if !ok {
			t.Errorf("field %q (%s) is missing from the stream event", tag, rt.Field(i).Name)
			continue
		}
		if !reflect.DeepEqual(got, stMap[tag]) {
			t.Errorf("field %q = %v, want %v", tag, got, stMap[tag])
		}
	}

	// The three fields the frontend names differently.
	for name, want := range map[string]interface{}{
		"format": stMap["type"], "title": stMap["song"], "artist": stMap["name"],
	} {
		if ev[name] != want {
			t.Errorf("%s = %v, want %v", name, ev[name], want)
		}
	}
}
