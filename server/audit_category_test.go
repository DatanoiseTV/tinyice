package server

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/DatanoiseTV/tinyice/relay"
)

// The audit-log category filter was a hand-written list of exact action
// names that had gone stale by thirteen actions: selecting "Streams" hid
// every mount_updated / mount_enabled / mount_disabled / kick ever
// recorded, and webhooks had no category at all. This scans the handlers
// for the action names actually passed to Audit and fails if any of them
// falls outside every category, so the next new action can't quietly drop
// out of the filter.
func TestEveryAuditedActionHasACategory(t *testing.T) {
	direct := regexp.MustCompile(`s\.Audit\(\s*r\s*,\s*"([a-z_]+)"`)
	// A handful of call sites choose the action in a variable first.
	indirect := regexp.MustCompile(`\baction\s*:?=\s*"([a-z_]+)"`)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]string{} // action -> file it was found in
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, re := range []*regexp.Regexp{direct, indirect} {
			for _, m := range re.FindAllStringSubmatch(string(src), -1) {
				actions[m[1]] = f
			}
		}
	}
	if len(actions) < 20 {
		t.Fatalf("only found %d audit actions in the handlers; the scan is broken", len(actions))
	}

	var orphans []string
	for a := range actions {
		if relay.AuditCategoryOf(a) == "" {
			orphans = append(orphans, a+" ("+actions[a]+")")
		}
	}
	sort.Strings(orphans)
	if len(orphans) > 0 {
		t.Errorf("audit actions that no category covers, so the filter hides them:\n  %s",
			strings.Join(orphans, "\n  "))
	}
}

// An unknown category must return nothing, not everything: the old filter
// fell through to an unfiltered query, so a typo in the query string
// looked like "this category contains the entire log".
func TestUnknownAuditCategoryMatchesNothing(t *testing.T) {
	if got := relay.AuditCategoryOf("definitely_not_an_action"); got != "" {
		t.Errorf("AuditCategoryOf = %q, want empty", got)
	}
	if _, ok := relay.AuditCategoryRules["nonsense"]; ok {
		t.Error("nonsense is a category")
	}
}
