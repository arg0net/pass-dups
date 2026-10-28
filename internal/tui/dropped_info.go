package tui

import (
	"fmt"
	"sort"

	"github.com/arg0net/pass-dups/internal/core"
)

func (m Model) droppedData() []core.DroppedEntry {
	g, d := m.groups[m.group], m.draft
	p := m.draftPlan()
	result := core.Item{}
	if active, _ := g.StateCounts(); active > 0 && !d.keepNone {
		result = g.Items[d.target]
		values, urls := m.mergedValues()
		for field, value := range values {
			result.Set(field, value)
		}
		// URLs are assigned directly: a live draft may not yet survive EncodeURLs.
		result.URLs = urls
	}
	return core.DroppedData(g.Items, p.TrashIDs, result)
}

// revealable reports whether the r key works where these entries are shown;
// pages without it mask values without suggesting the key.
func renderDroppedInfo(entries []core.DroppedEntry, reveal, revealable bool, line func(string)) {
	if len(entries) == 0 {
		return
	}
	line("")
	line(warn.Bold(true).Render("Not kept after merge"))
	line(dim.Render("These values are not retained in any kept Active entry."))
	for _, entry := range entries {
		action := "replaced on kept entry"
		if entry.Trashed {
			action = "entry moving to Trash"
		}
		line(warn.Render(fmt.Sprintf("#%d %s · %s", entry.Index+1, Safe(entry.Title), action)))
		for _, field := range []string{"username", "email", "password", "note"} {
			if value, ok := entry.Fields[field]; ok {
				line(warn.Render("  " + field + ": " + display(field, value, reveal)))
			}
		}
		for _, url := range entry.URLs {
			line(warn.Render("  URL: " + Safe(url)))
		}
		keys := make([]string, 0, len(entry.Extras))
		for key := range entry.Extras {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := displayExtra(entry.Extras[key], reveal)
			if !reveal && !revealable {
				value = "[masked]"
			}
			line(warn.Render("  " + Safe(key) + ": " + value))
		}
	}
}
