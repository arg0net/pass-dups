package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/arg0net/pass-dups/internal/core"
	"github.com/arg0net/pass-dups/internal/demo"
	"github.com/arg0net/pass-dups/internal/passcli"
)

func key(m Model, k string) (Model, tea.Cmd) {
	var code rune
	switch k {
	case "enter":
		code = tea.KeyEnter
	case "esc":
		code = tea.KeyEscape
	case "space":
		code = tea.KeySpace
	case "down":
		code = tea.KeyDown
	case "up":
		code = tea.KeyUp
	case "left":
		code = tea.KeyLeft
	case "right":
		code = tea.KeyRight
	case "tab":
		code = tea.KeyTab
	case "pgdown":
		code = tea.KeyPgDown
	case "pgup":
		code = tea.KeyPgUp
	default:
		code = []rune(k)[0]
	}
	next, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return next.(Model), cmd
}

func reviewModel(t *testing.T) Model {
	t.Helper()
	c := passcli.Client{Runner: demo.New()}
	scan, err := core.ScanAllURLs(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	m := New(c, true, true)
	m.scan = scan
	m.groups = scan.Groups
	m.vault = core.Vault{Name: "Synthetic", ShareID: "demo"}
	for n, x := range scan.Groups {
		if len(x.Items) == 4 {
			m.openGroup(n)
			break
		}
	}
	m.refresh(true)
	return m
}

func entryIndex(m Model, id string) int {
	for n, i := range m.groups[m.group].Items {
		if i.ID == id {
			return n
		}
	}
	panic("missing demo entry")
}

func TestMaskingRevealResetAndControlSanitization(t *testing.T) {
	m := reviewModel(t)
	body := m.body()
	if strings.Contains(body, "SYNTHETIC-password") || strings.Contains(body, "SYNTHETIC-recovery-code") {
		t.Fatal("secret visible before reveal")
	}
	if !strings.Contains(body, "different") {
		t.Fatal("missing comparison status")
	}
	m, _ = key(m, "r")
	if !strings.Contains(m.body(), "SYNTHETIC-password") {
		t.Fatal("reveal failed")
	}
	m, _ = key(m, "]")
	if m.reveal || strings.Contains(m.body(), "SYNTHETIC-password") {
		t.Fatal("reveal carried to another group")
	}
	if got := Safe("x\x1b]52;c;clipboard\a\u202ey\n"); strings.ContainsAny(got, "\x1b\a\u202e\n") {
		t.Fatal("terminal controls survived sanitization")
	}
}

func TestStagingRequiresPreviewWhichAcknowledgesExtraData(t *testing.T) {
	m := reviewModel(t)
	target := entryIndex(m, "a")
	m.resetDraft(target)
	m.cursor = 1 + entryIndex(m, "b")
	m, _ = key(m, "space")
	m.draft.sources["password"] = entryIndex(m, "b")
	if _, err := m.makePlan(); !errors.Is(err, core.ErrExtrasUnacknowledged) {
		t.Fatal("draft plan was acknowledged before the preview page")
	}
	m, _ = key(m, "s")
	if m.screen != previewScreen || len(m.staged) != 0 {
		t.Fatal("staged before preview approval")
	}
	m, _ = key(m, "enter")
	if len(m.staged) != 1 || m.screen != groupScreen || !m.plans()[0].ExtrasAcknowledged {
		t.Fatal("stage from the preview page did not record acknowledgment")
	}
	if !strings.Contains(m.body(), "[Resolved]") || !strings.Contains(m.body(), "[Pending]") || !strings.Contains(m.body(), "1 resolved · 1 pending") {
		t.Fatal("selection list does not distinguish resolved groups")
	}
	if m.groups[m.visibleGroups()[m.cursor]].ID != m.groups[m.group].ID {
		t.Fatal("selection did not stay on the newly resolved group")
	}
	p := m.plans()[0]
	if p.TargetID != "a" || p.Fields["password"] != "SYNTHETIC-password-two" || len(p.TrashIDs) != 1 {
		t.Fatal("wrong staged result")
	}
	m, _ = key(m, "b")
	if m.screen != stagedScreen {
		t.Fatal("batch screen not reached")
	}
	if strings.Contains(m.body(), "SYNTHETIC-password-two") {
		t.Fatal("batch preview leaked password")
	}
	m, _ = key(m, "a")
	if m.confirm != "apply" || m.screen != stagedScreen {
		t.Fatal("batch approval not required")
	}
	m, _ = key(m, "n")
	if m.confirm != "" {
		t.Fatal("cancel approval failed")
	}
	m, _ = key(m, "esc")
	for row, n := range m.visibleGroups() {
		if m.groups[n].ID == p.GroupID {
			m.cursor = row
			break
		}
	}
	m, _ = key(m, "x")
	if strings.Contains(m.body(), "[Resolved]") || !strings.Contains(m.body(), "0 resolved · 2 pending") {
		t.Fatal("unstaging did not mark group pending again")
	}
}

func TestDraftRevisionPreservesChoicesAndDoesNotAlias(t *testing.T) {
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "a"))
	m.draft.trash["d"] = true
	m.draft.notes = []string{"short", "short\n\nlong"}
	m.draft.noteOn = map[string]bool{"short": false, "short\n\nlong": true}
	m, _ = key(m, "s")
	m, _ = key(m, "enter")
	g := m.group
	m.openGroup(g)
	if m.draft.noteOn["short"] || !m.draft.noteOn["short\n\nlong"] {
		t.Fatal("overlapping note blocks restored incorrectly")
	}
	m.draft.trash["d"] = false
	m.openGroup(g)
	if !m.draft.trash["d"] {
		t.Fatal("unstaged edits mutated saved choices")
	}
}

func TestFilterResizeAndDiscardPrompts(t *testing.T) {
	m := reviewModel(t)
	m.screen = groupScreen
	m.filter.SetValue("co.uk")
	if len(m.visibleGroups()) != 1 {
		t.Fatal("filter did not match")
	}
	m.filter.SetValue("nonexistent")
	if len(m.visibleGroups()) != 0 {
		t.Fatal("filter did not exclude")
	}
	next, _ := m.Update(tea.WindowSizeMsg{Width: 20, Height: 5})
	m = next.(Model)
	if !strings.Contains(m.View().Content, "Resize") {
		t.Fatal("tiny terminal not handled")
	}
	for _, size := range []tea.WindowSizeMsg{{Width: 50, Height: 12}, {Width: 80, Height: 24}, {Width: 160, Height: 40}} {
		next, _ = m.Update(size)
		m = next.(Model)
		_ = m.View()
	}
	m.filter.SetValue("")
	m.openGroup(0)
	m.draft.trash[m.groups[0].Items[1].ID] = true
	m, _ = key(m, "s")
	m, _ = key(m, "enter")
	if len(m.staged) != 1 {
		t.Fatal("setup failed")
	}
	m, _ = key(m, "q")
	if m.confirm != "quit" {
		t.Fatal("pending work discarded without confirmation")
	}
	m, _ = key(m, "n")
	m, _ = key(m, "r")
	if m.confirm != "scan" {
		t.Fatal("rescan discarded pending work without confirmation")
	}
}

// Execute commands only against demo.New(). No test calls a live CLI.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ch := make(chan tea.Msg, 32)
	pending := 0
	schedule := func(c tea.Cmd) {
		if c != nil {
			pending++
			go func() { ch <- c() }()
		}
	}
	schedule(cmd)
	for pending > 0 {
		select {
		case msg := <-ch:
			pending--
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					schedule(c)
				}
			} else {
				next, nextCmd := m.Update(msg)
				m = next.(Model)
				schedule(nextCmd)
			}
		case <-deadline:
			t.Fatal("offline operation did not finish")
		}
	}
	return m
}

func TestOfflineEndToEndBatch(t *testing.T) {
	m := New(passcli.Client{Runner: demo.New()}, true, false)
	m = settle(t, m, m.Init())
	if m.screen != vaultScreen || len(m.vaults) != 2 {
		t.Fatal("demo vault loading failed")
	}
	m, cmd := key(m, "enter")
	m = settle(t, m, cmd)
	if m.screen != groupScreen || len(m.groups) != 2 {
		t.Fatal("demo scan failed")
	}
	m, cmd = key(m, "enter")
	m = settle(t, m, cmd)
	g := m.groups[m.group]
	// Keep the selected target; trash one other synthetic entry.
	var id string
	for _, i := range g.Items {
		if i.ID != g.Items[m.draft.target].ID {
			id = i.ID
			break
		}
	}
	m.draft.trash[id] = true
	m, _ = key(m, "s")
	m, _ = key(m, "enter")
	m, _ = key(m, "b")
	m, _ = key(m, "a")
	m, cmd = key(m, "y")
	m = settle(t, m, cmd)
	if m.screen != resultScreen || m.result.Err != nil || len(m.result.Outcomes) != 1 || m.result.Outcomes[0].Status != "completed" {
		t.Fatal("offline apply failed")
	}
	if len(m.staged) != 0 {
		t.Fatal("applied batch remained retryable")
	}
	m, cmd = key(m, "r")
	m = settle(t, m, cmd)
	if m.screen != groupScreen {
		t.Fatal("post-batch rescan failed")
	}
}

type trackingRunner struct {
	mu    sync.Mutex
	base  passcli.Runner
	views []string
	fail  string
}

func (r *trackingRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	if args[1] == "view" {
		for n, v := range args {
			if v == "--item-id" && n+1 < len(args) {
				r.views = append(r.views, args[n+1])
			}
		}
	}
	fail := args[0] == "item" && r.fail == args[1]
	r.mu.Unlock()
	if fail {
		return nil, errors.New("synthetic CLI failure")
	}
	return r.base.Run(ctx, args...)
}

func TestDefaultUIReadsDetailsOnlyOnOpen(t *testing.T) {
	r := &trackingRunner{base: demo.New()}
	m := New(passcli.Client{Runner: r}, true, false)
	m = settle(t, m, m.Init())
	m, cmd := key(m, "enter")
	m = settle(t, m, cmd)
	if len(r.views) != 0 || m.scan.WithDomains != 6 || len(m.scan.Entries) != 7 {
		t.Fatal("default scan read secrets or lost metadata counts")
	}
	if !strings.Contains(m.body(), "7 logins") || !strings.Contains(m.body(), "6 Active · 1 Trashed") {
		t.Fatal("missing scan diagnostics")
	}
	m, cmd = key(m, "d")
	m = settle(t, m, cmd)
	if m.screen != detectionScreen || len(r.views) != 0 || !strings.Contains(m.body(), "no recognized domain") {
		t.Fatal("domain inspection read secrets or omitted unmatched titles")
	}
	m, _ = key(m, "esc")
	wantIDs := map[string]bool{}
	for _, i := range m.groups[0].Items {
		wantIDs[i.ID] = true
	}
	m, cmd = key(m, "enter")
	if m.screen != busyScreen {
		t.Fatal("group did not enter loading state")
	}
	m = settle(t, m, cmd)
	if m.screen != reviewScreen || !m.groups[0].Loaded || len(r.views) != len(wantIDs) {
		t.Fatal("selected group was not loaded")
	}
	for _, id := range r.views {
		if !wantIDs[id] {
			t.Fatal("opening a group read an unrelated item")
		}
	}
	m, _ = key(m, "esc")
	m, cmd = key(m, "enter")
	if cmd != nil || len(r.views) != len(wantIDs) {
		t.Fatal("reopening cached group fetched details again")
	}
}

func TestOptionalFullScanAndFailures(t *testing.T) {
	t.Run("all URLs explicit opt-in", func(t *testing.T) {
		r := &trackingRunner{base: demo.New()}
		m := New(passcli.Client{Runner: r}, true, true)
		m = settle(t, m, m.Init())
		m, cmd := key(m, "enter")
		m = settle(t, m, cmd)
		if len(r.views) != 7 || m.scan.WithDomains != 7 || !m.groups[0].Loaded {
			t.Fatal("full scan did not include saved URL-only entry")
		}
	})
	t.Run("failed scan is not no duplicates", func(t *testing.T) {
		r := &trackingRunner{base: demo.New(), fail: "list"}
		m := New(passcli.Client{Runner: r}, true, false)
		m = settle(t, m, m.Init())
		m, cmd := key(m, "enter")
		m = settle(t, m, cmd)
		if m.screen != scanErrorScreen || !strings.Contains(m.body(), "Scan failed") || strings.Contains(m.body(), "No duplicate") {
			t.Fatal("failed scan was presented as an empty result")
		}
	})
	t.Run("failed group load retains discovery", func(t *testing.T) {
		r := &trackingRunner{base: demo.New(), fail: "view"}
		m := New(passcli.Client{Runner: r}, true, false)
		m = settle(t, m, m.Init())
		m, cmd := key(m, "enter")
		m = settle(t, m, cmd)
		m, cmd = key(m, "enter")
		m = settle(t, m, cmd)
		if m.screen != groupScreen || len(m.groups) != 2 || m.groups[0].Loaded || m.status == "" || len(m.staged) != 0 {
			t.Fatal("failed detail read erased metadata discovery or enabled review")
		}
	})
}

func TestMixedGroupStateDatesAndActiveDefaults(t *testing.T) {
	m := New(passcli.Client{Runner: demo.New()}, true, false)
	m = settle(t, m, m.Init())
	m, cmd := key(m, "enter")
	m = settle(t, m, cmd)
	if !strings.Contains(m.body(), "1 Active · 1 Trashed") {
		t.Fatal("group list does not show its state breakdown")
	}
	var n int
	for j, g := range m.groups {
		if g.Domains[0] == "example.co.uk" {
			n = j
			break
		}
	}
	cmd = m.requestGroup(n)
	m = settle(t, m, cmd)
	g := m.groups[m.group]
	if g.Items[m.draft.target].ID != "e" {
		t.Fatal("Trashed entry was chosen as default target")
	}
	for _, index := range m.draft.sources {
		if g.Items[index].State != "Active" {
			t.Fatal("Trashed scalar data selected by default")
		}
	}
	f := g.Items[entryIndex(m, "f")]
	if m.draft.noteOn[f.Note] {
		t.Fatal("Trashed note selected automatically")
	}
	for _, u := range f.URLs {
		if m.draft.urlOn[u] {
			t.Fatal("Trashed-only URL selected automatically")
		}
	}
	body := m.body()
	for _, text := range []string{"[TARGET · Active]", "[UNCHANGED · Trashed]", "Created: 2026-01-05T00:00:00", "Modified: 2026-01-06T00:00:00"} {
		if !strings.Contains(body, text) {
			t.Errorf("comparison missing %q", text)
		}
	}
	m.cursor = 0
	m, _ = key(m, "right")
	if !m.draft.keepNone {
		t.Fatal("target cycling did not offer keep none")
	}
	m, _ = key(m, "left")
	if m.draft.keepNone || g.Items[m.draft.target].State != "Active" {
		t.Fatal("target cycling did not return to an Active entry")
	}
	m.cursor = 1 + entryIndex(m, "f")
	m, _ = key(m, "space")
	if m.draft.trash["f"] {
		t.Fatal("already-Trashed entry selected for trash")
	}
	// Trashed data can still be selected deliberately as a source for an Active target.
	m.draft.noteOn[f.Note] = true
	p, err := m.makePlan()
	if err != nil {
		t.Fatal(err)
	}
	if p.TargetID != "e" || p.Fields["note"] != f.Note || len(p.TrashIDs) != 0 {
		t.Fatal("explicit copy from Trashed source produced wrong plan")
	}
	m, _ = key(m, "s")
	if !strings.Contains(m.body(), "[UNCHANGED · Trashed]") || !strings.Contains(m.body(), "Created:") {
		t.Fatal("staged preview lost states or dates")
	}
}

func TestTrashedOnlyUIIsComparisonOnly(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	if err := c.Trash(context.Background(), "demo", "e"); err != nil {
		t.Fatal(err)
	}
	m := New(c, true, false)
	m = settle(t, m, m.Init())
	m, cmd := key(m, "enter")
	m = settle(t, m, cmd)
	cmd = m.requestGroup(len(m.groups) - 1)
	m = settle(t, m, cmd)
	if !strings.Contains(m.body(), "comparison only") {
		t.Fatal("Trashed-only group not marked read-only")
	}
	m, _ = key(m, "right")
	m, _ = key(m, "s")
	if m.screen != reviewScreen || len(m.staged) != 0 || !strings.Contains(m.status, "no Active merge target") {
		t.Fatal("Trashed-only group allowed staging")
	}
}

func TestUnknownTimestampsAreExplicit(t *testing.T) {
	if got := entryDates("", ""); got != "Created: unknown · Modified: unknown" {
		t.Fatal("missing dates were not rendered as unknown")
	}
	if got := entryDates("2024-09-08T01:49:57-07:00", "2026-10-28T03:28:33Z"); !strings.Contains(got, "-07:00") || !strings.Contains(got, "33Z") {
		t.Fatal("display changed timestamp timezone")
	}
}

type blockedListingRunner struct {
	base    passcli.Runner
	started chan struct{}
}

func (r blockedListingRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if args[0] == "item" && args[1] == "list" {
		close(r.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.base.Run(ctx, args...)
}

func TestListingSpinnerAnimatesWithoutProgressAndStopsAfterCancellation(t *testing.T) {
	r := blockedListingRunner{base: demo.New(), started: make(chan struct{})}
	c := passcli.Client{Runner: r}
	m := New(c, true, false)
	m = settle(t, m, m.Init())
	m, cmd := key(m, "enter")
	defer m.cancel()
	select {
	case <-r.started:
	case <-time.After(time.Second):
		t.Fatal("synthetic listing did not start")
	}
	if m.screen != busyScreen || m.progress.Total != 0 || !strings.Contains(m.body(), "Listing") {
		t.Fatal("listing did not render busy state")
	}
	for range 3 {
		before := m.spinner.View()
		next, tick := m.Update(m.spinner.Tick())
		m = next.(Model)
		if tick == nil || m.spinner.View() == before || !strings.Contains(m.body(), m.spinner.View()) {
			t.Fatal("spinner did not advance while listing had no progress events")
		}
	}
	m, _ = key(m, "esc")
	m = settle(t, m, cmd)
	if m.screen != scanErrorScreen {
		t.Fatal("cancellation did not settle the pending listing")
	}
	before := m.spinner.View()
	lateTick := m.spinner.Tick()
	next, tick := m.Update(lateTick)
	m = next.(Model)
	if tick != nil || m.spinner.View() != before {
		t.Fatal("spinner kept running after leaving the busy screen")
	}
	// A tick from an old operation must not start a duplicate animation loop.
	m.spinner = New(c, true, false).spinner
	m.screen = busyScreen
	before = m.spinner.View()
	next, tick = m.Update(lateTick)
	m = next.(Model)
	if tick != nil || m.spinner.View() != before {
		t.Fatal("stale operation tick was accepted")
	}
}

func TestKeepNonePreviewStagingAndRevision(t *testing.T) {
	m := reviewModel(t)
	m, _ = key(m, "d")
	if !m.draft.keepNone || !strings.Contains(m.body(), "Keep: none") || strings.Contains(m.body(), "[TARGET ·") {
		t.Fatal("keep-none choice was not rendered")
	}
	m, _ = key(m, "s")
	if m.screen != previewScreen || len(m.staged) != 0 || !strings.Contains(m.body(), "No merge target") || !strings.Contains(m.body(), "accepting that the highlighted data") {
		t.Fatal("keep-none bypassed the preview")
	}
	m, _ = key(m, "enter")
	p := m.plans()[0]
	if !p.KeepNone || p.TargetID != "" || len(p.Fields) != 0 || len(p.TrashIDs) != len(p.Originals) {
		t.Fatal("wrong keep-none plan staged")
	}
	if !strings.Contains(m.body(), "[Resolved · keep none]") {
		t.Fatal("group list lost the keep-none action label")
	}
	m, _ = key(m, "b")
	if !strings.Contains(m.body(), "Keep none — entire group") {
		t.Fatal("batch review has no keep-none label")
	}
	m, _ = key(m, "enter")
	if !m.draft.keepNone {
		t.Fatal("revising lost the keep-none choice")
	}
	m.cursor = 0
	m, _ = key(m, "left")
	if m.draft.keepNone || len(m.draft.trash) != 0 {
		t.Fatal("choosing a target did not reset keep-none")
	}
	if !m.plans()[0].KeepNone {
		t.Fatal("unstaged edits modified the staged keep-none plan")
	}
}

func TestKeepNoneOfflineBatch(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	m := New(c, true, false)
	m = settle(t, m, m.Init())
	m, cmd := key(m, "enter")
	m = settle(t, m, cmd)
	m, cmd = key(m, "enter")
	m = settle(t, m, cmd)
	group := m.groups[m.group]
	m, _ = key(m, "d")
	m, _ = key(m, "s")
	m, _ = key(m, "enter")
	m, _ = key(m, "b")
	m, _ = key(m, "a")
	if m.confirm != "apply" || m.screen != stagedScreen {
		t.Fatal("keep-none batch did not require final approval")
	}
	m, cmd = key(m, "y")
	m = settle(t, m, cmd)
	if m.result.Err != nil || m.screen != resultScreen || len(m.result.Outcomes) != len(group.Items) {
		t.Fatal("keep-none offline batch failed")
	}
	for _, o := range m.result.Outcomes {
		if o.Action != "trash" || o.Status != "completed" {
			t.Fatal("unexpected keep-none batch operation")
		}
	}
	for _, i := range group.Items {
		current, err := c.View(context.Background(), i.ShareID, i.ID)
		if err != nil || current.State != "Trashed" {
			t.Fatal("keep-none left a group member Active")
		}
	}
}

func TestPreviewPageEndsWithExtraDataLoss(t *testing.T) {
	const marker = "extra data not transferred"
	m := reviewModel(t)
	m.resetDraft(entryIndex(m, "a"))
	m.cursor = 1 + entryIndex(m, "d")
	m, _ = key(m, "space")
	rows := m.rowCount()
	if strings.Contains(m.body(), marker) {
		t.Fatal("entry without extra data was marked")
	}
	m, _ = key(m, "s")
	if m.screen != previewScreen || strings.Contains(m.body(), "Extra data not transferred") {
		t.Fatal("clean entry preview reported extra data loss")
	}
	m, _ = key(m, "esc")
	m.cursor = 1 + entryIndex(m, "b")
	m, _ = key(m, "space")
	b := m.groups[m.group].Items[entryIndex(m, "b")]
	label := fmt.Sprintf("#%d %s", entryIndex(m, "b")+1, b.Title)
	m.viewport.SetWidth(200)
	if m.rowCount() != rows || strings.Count(m.body(), marker) != 1 || strings.Contains(m.body(), "I acknowledge") {
		t.Fatal("review should mark the risky entry without an acknowledgment control")
	}
	m, _ = key(m, "s")
	body := m.body()
	loss := strings.Index(body, "Extra data not transferred from entries moving to Trash")
	if m.screen != previewScreen || loss < 0 || !strings.Contains(body[loss:], label+":") {
		t.Fatal("preview page does not name the entry losing extra data")
	}
	if strings.Contains(body, "r reveals") || !strings.Contains(body, "[masked]") {
		t.Fatal("preview page suggests a reveal key it does not support")
	}
	if loss < strings.LastIndex(body, "[TRASH ·") || loss > strings.Index(body, "Not kept after merge") || !strings.Contains(body[loss:], "accepting that the highlighted data") {
		t.Fatal("data loss is not highlighted at the end of the preview page")
	}
	m, _ = key(m, "esc")
	m.cursor = 1 + entryIndex(m, "b")
	m, _ = key(m, "space")
	p, err := m.reviewedPlan()
	if err != nil {
		t.Fatal(err)
	}
	if p.ExtrasAcknowledged {
		t.Fatal("unnecessary acknowledgment recorded in plan")
	}
}

func TestKeepNoneDoesNotHighlightAlreadyTrashedExtraData(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	// The only entry with extras is already in Trash, so its data is untouched.
	if err := c.Trash(context.Background(), "demo", "b"); err != nil {
		t.Fatal(err)
	}
	scan, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	m := New(c, true, false)
	m.groups = scan.Groups
	m.scan = scan
	var group int
	for n, g := range m.groups {
		if g.Domains[0] == "example.com" {
			group = n
			break
		}
	}
	cmd := m.requestGroup(group)
	m = settle(t, m, cmd)
	m, _ = key(m, "d")
	if strings.Contains(m.body(), "I acknowledge extra data") {
		t.Fatal("already-Trashed extra data triggered checkbox in keep-none mode")
	}
	m, _ = key(m, "s")
	if m.screen != previewScreen {
		t.Fatal("keep-none required acknowledgment for entries without extras")
	}
	m, _ = key(m, "enter")
	if len(m.staged) != 1 || m.plans()[0].ExtrasAcknowledged {
		t.Fatal("clean keep-none decision was not staged without acknowledgment")
	}
}

func TestReturningToGroupListKeepsSelectedGroup(t *testing.T) {
	m := reviewModel(t)
	if len(m.groups) < 2 {
		t.Fatal("synthetic scan needs at least two groups")
	}
	last := len(m.groups) - 1
	m.openGroup(last)
	m, _ = key(m, "esc")
	if m.screen != groupScreen || m.visibleGroups()[m.cursor] != last {
		t.Fatalf("cursor on row %d after leaving group %d", m.cursor, last)
	}
	m, _ = key(m, "enter")
	m, _ = key(m, "]")
	m, _ = key(m, "esc")
	if m.visibleGroups()[m.cursor] != 0 {
		t.Fatal("cursor did not follow group navigation within review")
	}
	m.filter.SetValue("no group matches this")
	m.openGroup(last)
	m, _ = key(m, "esc")
	if m.cursor != 0 {
		t.Fatal("cursor not reset when the group is filtered out")
	}
}

func TestFieldComparisonStatesAreColored(t *testing.T) {
	m := reviewModel(t)
	m.viewport.SetWidth(200)
	body := m.body()
	for _, f := range core.ScalarFields {
		if !strings.Contains(body, f+" ["+good.Render("equal")) && !strings.Contains(body, f+" ["+warn.Render("different")) {
			t.Errorf("%s comparison state is not colored", f)
		}
	}
}
