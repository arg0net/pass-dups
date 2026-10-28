package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"pass-dups/internal/core"
)

const splitPreviewWidth = 80

func (m *Model) sizePanes() {
	width := max(16, m.width-4)
	height := max(1, m.height-9)
	left, right := width, width
	if m.screen == reviewScreen && m.width >= splitPreviewWidth {
		right = max(28, width*2/5)
		left = width - right - 3
	}
	m.viewport.SetWidth(left)
	m.viewport.SetHeight(height)
	m.mergePreview.SetWidth(right)
	m.mergePreview.SetHeight(height)
}

// Both the live preview and the write plan derive their result from these values.
// This works even while the draft still needs URL validation.
func (m Model) mergedValues() (map[string]string, []string) {
	g, d := m.groups[m.group], m.draft
	values := map[string]string{}
	for _, field := range core.ScalarFields {
		values[field] = g.Items[d.sources[field]].Value(field)
	}
	urls := []string{}
	for _, u := range d.urls {
		if d.urlOn[u] {
			urls = append(urls, u)
		}
	}
	notes := []string{}
	for _, note := range d.notes {
		if d.noteOn[note] {
			notes = append(notes, note)
		}
	}
	values["note"] = strings.Join(notes, "\n\n")
	return values, urls
}

func displayExtra(raw json.RawMessage, reveal bool) string {
	if !reveal {
		return "[masked; r reveals]"
	}
	value, valid := core.DecodeExtra(raw)
	if !valid {
		return "[unavailable]"
	}
	b, _ := json.Marshal(value)
	return Safe(string(b))
}

func (m Model) mergePreviewBody(dropped []core.DroppedEntry) string {
	var b strings.Builder
	line := func(s string) {
		b.WriteString(lipgloss.NewStyle().Width(max(1, m.mergePreview.Width())).Render(s))
		b.WriteByte('\n')
	}
	heading := "Merged result"
	if m.previewFocused {
		heading += " · focused"
	}
	line(accent.Render(heading))
	if m.reveal {
		line(dim.Render("Live draft · r hides secrets"))
	} else {
		line(dim.Render("Live draft · r shows secrets"))
	}
	line("")
	g, d := m.groups[m.group], m.draft
	p := m.draftPlan()
	if len(dropped) > 0 {
		line(warn.Render("Some data will not be kept"))
		line("")
	}
	active, _ := g.StateCounts()
	if active == 0 {
		line("All entries are Trashed.")
		line("Comparison only; no merged entry or changes.")
		return b.String()
	}
	if d.keepNone {
		line(warn.Render("Keep none — no merged entry"))
	} else {
		target := g.Items[d.target]
		line(dim.Render(fmt.Sprintf("Target: entry #%d (Active)", d.target+1)))
		line("")
		values, urls := m.mergedValues()
		for _, field := range core.ScalarFields {
			label := strings.ToUpper(field[:1]) + field[1:]
			line(accent.Render(label+": ") + display(field, values[field], m.reveal))
		}
		line("")
		line(accent.Render("URLs"))
		if len(urls) == 0 {
			line("∅")
		}
		for _, u := range urls {
			line("• " + Safe(u))
		}
		if target.Autofill {
			line(dim.Render("Existing URL matching settings are retained."))
		}
		line("")
		line(accent.Render("Notes"))
		if values["note"] == "" {
			line("∅")
		} else {
			for _, s := range strings.Split(values["note"], "\n") {
				line(Safe(s))
			}
		}
		if keys := target.ExtraKeys(); len(keys) > 0 {
			line("")
			line(accent.Render("Retained extra data"))
			for _, k := range keys {
				line(Safe(k) + ": " + displayExtra(target.Extras[k], m.reveal))
			}
		}
	}
	renderDroppedInfo(dropped, m.reveal, true, line)
	line("")
	trash := map[string]bool{}
	for _, id := range p.TrashIDs {
		trash[id] = true
	}
	risky := map[string]bool{}
	for _, i := range p.ExtraDataTrashed() {
		risky[i.ID] = true
	}
	line(accent.Render(fmt.Sprintf("Move to Trash (%d)", len(p.TrashIDs))))
	if len(p.TrashIDs) == 0 {
		line("None")
	}
	for _, i := range g.Items {
		if trash[i.ID] {
			entry := "• " + Safe(i.Title) + " [" + Safe(i.ID) + "]"
			if risky[i.ID] {
				entry += warn.Render(" ⚠ extra data not transferred")
			}
			line(entry)
		}
	}
	kept := []string{}
	for n, i := range g.Items {
		if i.State == "Active" && !trash[i.ID] && (d.keepNone || n != d.target) {
			kept = append(kept, i.Title)
		}
	}
	if len(kept) > 0 {
		line("")
		line(accent.Render("Other entries kept"))
		for _, title := range kept {
			line("• " + Safe(title))
		}
	}
	_, trashed := g.StateCounts()
	if trashed > 0 {
		line("")
		line(dim.Render(fmt.Sprintf("%d already Trashed; unchanged", trashed)))
	}
	return b.String()
}
