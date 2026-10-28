package tui

import (
	"reflect"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/arg0net/pass-dups/internal/core"
)

func TestLivePreviewMatchesMergedFieldSelections(t *testing.T) {
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "a"))
	g := m.groups[m.group]
	source := g.Items[entryIndex(m, "b")]
	work := g.Items[entryIndex(m, "c")]
	m.draft.sources["title"] = entryIndex(m, "b")
	m.draft.sources["username"] = entryIndex(m, "c")
	m.draft.sources["email"] = entryIndex(m, "c")
	m.draft.sources["password"] = entryIndex(m, "b")
	m.draft.trash["b"] = true
	for _, u := range source.URLs {
		m.draft.urlOn[u] = true
	}
	m.draft.noteOn[source.Note] = true
	m.refresh(false)
	if _, err := m.reviewedPlan(); err != nil {
		t.Fatal(err)
	}
	values, urls := m.mergedValues()
	if values["password"] != source.Password || values["username"] != work.Username || values["email"] != work.Email {
		t.Fatal("preview did not use selected scalar sources")
	}
	target := g.Items[entryIndex(m, "a")]
	if values["note"] != target.Note+"\n\n"+source.Note {
		t.Fatal("preview did not preserve combined note order")
	}
	if !reflect.DeepEqual(urls, []string{"https://accounts.example.com", "https://example.org/login", "https://shop.example.com"}) {
		t.Fatal("preview did not preserve selected URL order")
	}
	m.mergePreview.SetWidth(200)
	body := m.mergePreviewBody(m.droppedData())
	if strings.Contains(body, source.Password) || !strings.Contains(body, "••••••••") {
		t.Fatal("preview exposed password while hidden")
	}
	for _, want := range []string{source.Title, work.Username, work.Email, target.Note, source.Note, "Move to Trash (1)"} {
		if !strings.Contains(body, want) {
			t.Errorf("preview missing %q", want)
		}
	}
	m.reveal = true
	if !strings.Contains(m.mergePreviewBody(m.droppedData()), source.Password) {
		t.Fatal("preview did not honor reveal")
	}
	p, err := m.reviewedPlan()
	if err != nil {
		t.Fatal(err)
	}
	for field, value := range values {
		actual := target.Value(field)
		if selected, ok := p.Fields[field]; ok {
			actual = selected
		}
		if actual != value {
			t.Errorf("preview and staged plan disagree on %s", field)
		}
	}
	encoded, err := core.EncodeURLs(urls)
	if err != nil || p.Fields["urls"] != encoded {
		t.Fatal("preview and staged plan disagree on URLs")
	}
}

func TestLivePreviewMasksRetainedExtraData(t *testing.T) {
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "b"))
	m.mergePreview.SetWidth(200)
	before := m.mergePreviewBody(m.droppedData())
	if !strings.Contains(before, "Retained extra data") || strings.Contains(before, "SYNTHETIC-recovery-code") || strings.Contains(before, "otpauth://") {
		t.Fatal("retained extra data was missing or revealed by default")
	}
	m.reveal = true
	if got := m.mergePreviewBody(m.droppedData()); !strings.Contains(got, "SYNTHETIC-recovery-code") || !strings.Contains(got, "otpauth://") {
		t.Fatal("extra data did not honor reveal")
	}
	m.reveal = false
	if strings.Contains(m.mergePreviewBody(m.droppedData()), "SYNTHETIC-recovery-code") {
		t.Fatal("extra data stayed visible after hiding")
	}
}

func TestPreviewPaneIsOnRightAndFitsTerminal(t *testing.T) {
	for _, width := range []int{80, 100, 160, 240} {
		t.Run(strconv.Itoa(width), func(t *testing.T) {
			m := reviewModel(t)
			next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 30})
			m = next.(Model)
			view := m.View().Content
			found := false
			for _, line := range strings.Split(view, "\n") {
				if lipgloss.Width(line) > width {
					t.Errorf("rendered line exceeds terminal width: got %d, want <=%d", lipgloss.Width(line), width)
				}
				if at := strings.Index(line, "Merged result"); at >= 0 {
					found = true
					if column := lipgloss.Width(line[:at]); column != 2+m.viewport.Width()+3 {
						t.Errorf("preview heading column=%d; expected right pane at %d", column, 2+m.viewport.Width()+3)
					}
				}
			}
			if !found {
				t.Fatal("right-hand merged preview missing")
			}
		})
	}
}

func TestPreviewFocusScrollingAndNarrowLayout(t *testing.T) {
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "a"))
	next, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 18})
	m = next.(Model)
	before := cloneDraft(m.draft)
	leftOffset := m.viewport.YOffset()
	m, _ = key(m, "tab")
	m, _ = key(m, "pgdown")
	if !m.previewFocused || m.mergePreview.YOffset() == 0 || m.viewport.YOffset() != leftOffset {
		t.Fatal("preview did not scroll independently")
	}
	m, _ = key(m, "right")
	m, _ = key(m, "space")
	if !reflect.DeepEqual(m.draft, before) {
		t.Fatal("preview navigation changed merge selections")
	}
	m, _ = key(m, "r")
	m.mergePreview.SetWidth(200)
	if !m.reveal || !strings.Contains(m.mergePreviewBody(m.droppedData()), "SYNTHETIC-password-one") {
		t.Fatal("preview reveal shortcut failed")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = next.(Model)
	m.mergePreview.GotoTop()
	if !strings.Contains(m.View().Content, "Merged result") {
		t.Fatal("narrow terminal did not show focused preview")
	}
	m, _ = key(m, "tab")
	if m.previewFocused || strings.Contains(m.View().Content, "Merged result") {
		t.Fatal("narrow terminal did not return to merge choices")
	}
	m, _ = key(m, "tab")
	m.reveal = true
	m.openGroup((m.group + 1) % len(m.groups))
	if m.previewFocused || m.reveal || m.mergePreview.YOffset() != 0 {
		t.Fatal("new group inherited preview focus, reveal or scroll")
	}
}

func TestFocusedPreviewIgnoresDraftChangingKeys(t *testing.T) {
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "a"))
	m.cursor = 1 + entryIndex(m, "b")
	m, _ = key(m, "space")
	before, group := cloneDraft(m.draft), m.group
	m, _ = key(m, "tab")
	for _, k := range []string{"x", "d", "n", "]", "[", "h", "l", "space", "enter"} {
		m, _ = key(m, k)
		if m.screen != reviewScreen || m.group != group || !reflect.DeepEqual(m.draft, before) {
			t.Fatalf("focused preview let %q change the draft or group", k)
		}
	}
	m, _ = key(m, "s")
	if m.screen != previewScreen {
		t.Fatal("s did not reach the review handler from the focused preview")
	}
}

func TestDroppedDataMatchesBetweenLiveDraftAndStagedPlan(t *testing.T) {
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "a"))
	m.draft.sources["password"] = entryIndex(m, "b")
	m.draft.sources["username"] = entryIndex(m, "c")
	m.draft.trash["d"] = true
	for note := range m.draft.noteOn {
		m.draft.noteOn[note] = false
	}
	p, err := m.makePlan()
	if err != nil {
		t.Fatal(err)
	}
	if live := m.droppedData(); len(live) == 0 || !reflect.DeepEqual(live, p.DroppedData()) {
		t.Fatal("live preview and staged plan disagree on dropped data")
	}
}

func TestPreviewIsBuiltOnlyWhenVisible(t *testing.T) {
	m := reviewModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 24})
	m = next.(Model)
	if m.mergePreview.TotalLineCount() > 1 {
		t.Fatal("hidden preview was rendered")
	}
	m, _ = key(m, "tab")
	if m.mergePreview.TotalLineCount() <= 1 {
		t.Fatal("focused preview was not rendered")
	}
}

func TestKeepNoneLivePreviewShowsTrashActionsOnly(t *testing.T) {
	m := reviewModel(t)
	m, _ = key(m, "d")
	m.mergePreview.SetWidth(200)
	preview := m.mergePreviewBody(m.droppedData())
	if !strings.Contains(preview, "Keep none — no merged entry") || !strings.Contains(preview, "Move to Trash (4)") {
		t.Fatal("keep-none preview missing action summary")
	}
	if strings.Contains(preview, "Password:") || strings.Contains(preview, "Target: entry") {
		t.Fatal("keep-none preview shows a merged entry")
	}
	for _, i := range m.groups[m.group].Items {
		if !strings.Contains(preview, i.Title) {
			t.Fatal("keep-none preview omitted a selected entry")
		}
	}
}
