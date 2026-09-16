package relay

import "testing"

func TestResolveRTMPPath(t *testing.T) {
	cases := []struct {
		name      string
		app       string
		publish   string
		wantMount string
		wantPass  string
	}{
		// OBS default layout — Server "rtmp://host/foo", Key "pass".
		{"obs-server-path-as-mount", "foo", "pass", "/foo", "pass"},
		{"obs-nested-app", "live/cam", "pass", "/live/cam", "pass"},
		{"obs-app-with-leading-slash", "/foo", "pass", "/foo", "pass"},

		// Classic tinyice — Server "rtmp://host/", Key "mount?key=pass".
		{"classic-legacy", "", "mount?key=pass", "/mount", "pass"},
		{"classic-no-password", "", "mount", "/mount", ""},

		// Hybrid — Server "rtmp://host/foo", Key "stream?key=pass".
		// The ?key= wins; app joins onto the mount path.
		{"hybrid-query-wins", "foo", "stream?key=pass", "/foo/stream", "pass"},
		{"hybrid-query-wins-no-key", "foo", "stream?bitrate=hi", "/foo/stream", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mount, pw := resolveRTMPPath(tc.app, tc.publish)
			if mount != tc.wantMount || pw != tc.wantPass {
				t.Errorf("resolveRTMPPath(%q, %q) = (%q, %q); want (%q, %q)",
					tc.app, tc.publish, mount, pw, tc.wantMount, tc.wantPass)
			}
		})
	}
}
