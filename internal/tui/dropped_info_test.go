package tui

import (
	"strings"
	"testing"
)

func TestDroppedInformationIsHighlightedAndAcknowledgedOnPreview(t *testing.T) {
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "a"))
	if strings.Contains(m.body(), "Not kept after merge") {
		t.Fatal("unchanged draft shows dropped data")
	}
	rows := m.rowCount()
	m.cursor = 1 + entryIndex(m, "b")
	m, _ = key(m, "space")
	if m.rowCount() != rows || strings.Contains(m.body(), "I acknowledge") {
		t.Fatal("trashing an entry with extra data added an acknowledgment control")
	}
	if !strings.Contains(m.body(), "Not kept after merge") || !strings.Contains(m.mergePreviewBody(m.droppedData()), "Not kept after merge") {
		t.Fatal("dropped data missing from source or live preview")
	}
	if _, err := m.reviewedPlan(); err != nil {
		t.Fatal(err)
	}
	m, _ = key(m, "s")
	if m.screen != previewScreen || !strings.Contains(m.body(), "Not kept after merge") {
		t.Fatal("group preview does not highlight dropped data")
	}
	m, _ = key(m, "enter")
	m, _ = key(m, "b")
	if m.screen != stagedScreen || !strings.Contains(m.body(), "Not kept after merge") {
		t.Fatal("batch summary does not highlight dropped data")
	}
	if strings.Contains(m.body(), "SYNTHETIC-password-two") || strings.Contains(m.body(), "SYNTHETIC-recovery-code") {
		t.Fatal("batch summary exposed discarded secrets")
	}
}

func TestDroppedSecretsHonorRevealAndAreHiddenBeforeStaging(t *testing.T) {
	m := reviewModel(t)
	m, _ = key(m, "d")
	m.mergePreview.SetWidth(200)
	if strings.Contains(m.mergePreviewBody(m.droppedData()), "SYNTHETIC-password") || strings.Contains(m.mergePreviewBody(m.droppedData()), "SYNTHETIC-recovery-code") {
		t.Fatal("discarded secrets were visible while hidden")
	}
	m, _ = key(m, "r")
	m.mergePreview.SetWidth(200)
	if !strings.Contains(m.mergePreviewBody(m.droppedData()), "SYNTHETIC-password-two") || !strings.Contains(m.mergePreviewBody(m.droppedData()), "SYNTHETIC-recovery-code") {
		t.Fatal("reveal did not expose highlighted values")
	}
	m, _ = key(m, "s")
	if m.screen != previewScreen || strings.Contains(m.body(), "SYNTHETIC-password-two") || strings.Contains(m.body(), "SYNTHETIC-recovery-code") {
		t.Fatal("staging preview did not re-hide discarded secrets")
	}
}

func TestDroppedHighlightsClearWhenEntryIsKeptAgain(t *testing.T) {
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "a"))
	m.cursor = 1 + entryIndex(m, "b")
	m, _ = key(m, "space")
	if !strings.Contains(m.body(), "Not kept after merge") {
		t.Fatal("missing dropped-data highlight")
	}
	m, _ = key(m, "space")
	if strings.Contains(m.body(), "Not kept after merge") || strings.Contains(m.mergePreviewBody(m.droppedData()), "Some data will not be kept") {
		t.Fatal("highlight remained after entry was kept again")
	}
}
