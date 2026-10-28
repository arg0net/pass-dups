package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"pass-dups/internal/core"
)

type screen int

const (
	vaultScreen screen = iota
	groupScreen
	reviewScreen
	previewScreen
	stagedScreen
	resultScreen
	busyScreen
	scanErrorScreen
	detectionScreen
)

type workMsg struct {
	kind   string
	vaults []core.Vault
	scan   core.ScanResult
	loaded core.Group
	result core.BatchResult
	err    error
}
type progressMsg core.Progress
type startMsg struct{}

type draft struct {
	target        int
	keepNone      bool
	trash         map[string]bool
	sources       map[string]int
	urls, notes   []string
	urlOn, noteOn map[string]bool
}

type Model struct {
	client         core.Client
	demo           bool
	allURLs        bool
	screen         screen
	vaults         []core.Vault
	vault          core.Vault
	groups         []core.Group
	scan           core.ScanResult
	group          int
	cursor         int
	draft          draft
	staged         map[string]core.Plan
	drafts         map[string]draft
	reveal         bool
	filter         textinput.Model
	filtering      bool
	viewport       viewport.Model
	mergePreview   viewport.Model
	previewFocused bool
	spinner        spinner.Model
	width, height  int
	focusLine      int
	status         string
	confirm        string
	progress       core.Progress
	result         core.BatchResult
	events         chan tea.Msg
	cancel         context.CancelFunc
	lastOp         string
}

var accent = lipgloss.NewStyle().Foreground(lipgloss.Color("110")).Bold(true)
var dim = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
var warn = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
var good = lipgloss.NewStyle().Foreground(lipgloss.Color("114"))

func New(c core.Client, demo bool, allURLs bool) Model {
	f := textinput.New()
	f.Prompt = "Filter: "
	f.Placeholder = "domain or entry title"
	f.CharLimit = 120
	v := viewport.New()
	v.SetWidth(76)
	v.SetHeight(15)
	v.SoftWrap = true
	p := viewport.New()
	p.SoftWrap = true
	m := Model{client: c, demo: demo, allURLs: allURLs, screen: busyScreen, staged: map[string]core.Plan{}, drafts: map[string]draft{}, filter: f, viewport: v, mergePreview: p, spinner: spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(accent)), width: 80, height: 24, progress: core.Progress{Phase: "Loading vaults"}}
	m.refresh(false)
	return m
}

func (m Model) Init() tea.Cmd {
	return func() tea.Msg { return startMsg{} }
}

func waitEvent(events <-chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-events } }

func (m *Model) start(kind string) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.events = make(chan tea.Msg, 16)
	ch := m.events
	m.screen = busyScreen
	// A new ID prevents a pending tick from an earlier operation starting a
	// second animation loop. Animation runs independently of CLI progress.
	m.spinner = spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(accent))
	m.lastOp = kind
	m.reveal = false
	m.previewFocused = false
	m.status = ""
	m.confirm = ""
	m.progress = core.Progress{Phase: "Loading vaults"}
	if kind == "scan" {
		m.progress.Phase = "Listing Active and Trashed logins"
	}
	if kind == "apply" {
		m.progress.Phase = "Validating staged changes"
	}
	if kind == "group" {
		m.progress.Phase = "Loading selected group"
	}
	client, share := m.client, m.vault.ShareID
	allURLs := m.allURLs
	var selected core.Group
	if kind == "group" {
		selected = m.groups[m.group]
	}
	plans := m.plans()
	progress := func(p core.Progress) {
		select {
		case ch <- progressMsg(p):
		default:
		}
	}
	go func() {
		defer cancel()
		msg := workMsg{kind: kind}
		switch kind {
		case "vaults":
			msg.vaults, msg.err = client.Vaults(ctx)
		case "scan":
			if allURLs {
				msg.scan, msg.err = core.ScanAllURLs(ctx, client, share, progress)
			} else {
				msg.scan, msg.err = core.Scan(ctx, client, share, progress)
			}
		case "group":
			msg.loaded, msg.err = core.LoadGroup(ctx, client, selected, progress)
		case "apply":
			msg.result = core.Apply(ctx, client, plans, progress)
			msg.err = msg.result.Err
		}
		ch <- msg
	}()
	return tea.Batch(waitEvent(ch), m.spinner.Tick)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch msg := msg.(type) {
	case startMsg:
		cmd = m.start("vaults")
	case spinner.TickMsg:
		if m.screen == busyScreen {
			m.spinner, cmd = m.spinner.Update(msg)
		}
	case tea.WindowSizeMsg:
		m.width = max(1, msg.Width)
		m.height = max(1, msg.Height)
		m.filter.SetWidth(max(8, m.width-16))
	case progressMsg:
		m.progress = core.Progress(msg)
		cmd = waitEvent(m.events)
	case workMsg:
		m.cancel = nil
		m.status = ""
		m.cursor = 0
		m.viewport.GotoTop()
		switch msg.kind {
		case "vaults":
			m.screen = vaultScreen
			m.vaults = msg.vaults
			m.lastOp = "vaults"
		case "scan":
			m.screen = groupScreen
			m.scan = msg.scan
			m.groups = msg.scan.Groups
			m.filter.SetValue("")
			m.staged = map[string]core.Plan{}
			m.drafts = map[string]draft{}
			m.lastOp = "scan"
			if msg.err != nil {
				m.screen = scanErrorScreen
			}
		case "group":
			if msg.err == nil {
				m.groups[m.group] = msg.loaded
				m.openGroup(m.group)
			} else {
				m.screen = groupScreen
				m.cursorToGroup(m.group)
			}
		case "apply":
			m.screen = resultScreen
			m.result = msg.result
			m.reveal = false
			// Every result requires a fresh scan; there is no blind retry of writes.
			m.staged = map[string]core.Plan{}
			m.drafts = map[string]draft{}
		}
		if msg.err != nil {
			m.status = msg.err.Error()
		}
	case tea.KeyPressMsg:
		key := msg.String()
		if m.screen == busyScreen {
			if key == "esc" || key == "ctrl+c" {
				if m.cancel != nil {
					m.cancel()
					m.status = "Canceling; waiting for the current operation to settle…"
				}
			}
			break
		}
		if m.confirm != "" {
			if key == "esc" || key == "n" {
				m.confirm = ""
				break
			}
			if key == "y" {
				action := m.confirm
				m.confirm = ""
				switch action {
				case "quit":
					return m, tea.Quit
				case "vaults":
					m.staged = map[string]core.Plan{}
					m.groups = nil
					cmd = m.start("vaults")
				case "scan":
					m.staged = map[string]core.Plan{}
					cmd = m.start("scan")
				case "apply":
					cmd = m.start("apply")
				}
			}
			break
		}
		if m.filtering {
			if key == "esc" || key == "enter" {
				m.filtering = false
				m.filter.Blur()
				m.cursor = 0
				break
			}
			m.filter, cmd = m.filter.Update(msg)
			m.cursor = 0
			break
		}
		if key == "q" || key == "ctrl+c" {
			if len(m.staged) > 0 {
				m.confirm = "quit"
			} else {
				return m, tea.Quit
			}
			break
		}
		if m.screen == reviewScreen && key == "tab" {
			m.previewFocused = !m.previewFocused
			m.refresh(false)
			return m, nil
		}
		if key == "pgup" || key == "pgdown" || key == "ctrl+u" || key == "ctrl+d" {
			if m.screen == reviewScreen && m.previewFocused {
				m.mergePreview, cmd = m.mergePreview.Update(msg)
			} else {
				m.viewport, cmd = m.viewport.Update(msg)
			}
			m.refresh(false)
			return m, cmd
		}
		// A focused preview passes only its footer's keys to the review handler,
		// so draft-changing keys cannot act while the choices are out of focus.
		if m.screen == reviewScreen && m.previewFocused && key != "r" && key != "s" && key != "esc" {
			m.mergePreview, cmd = m.mergePreview.Update(msg)
			m.refresh(false)
			return m, cmd
		}
		switch m.screen {
		case vaultScreen:
			switch key {
			case "up", "k":
				m.cursor = max(0, m.cursor-1)
			case "down", "j":
				m.cursor = min(max(0, len(m.vaults)-1), m.cursor+1)
			case "enter":
				if len(m.vaults) > 0 {
					m.vault = m.vaults[m.cursor]
					cmd = m.start("scan")
				}
			case "r":
				cmd = m.start("vaults")
			}
		case groupScreen:
			visible := m.visibleGroups()
			switch key {
			case "up", "k":
				m.cursor = max(0, m.cursor-1)
			case "down", "j":
				m.cursor = min(max(0, len(visible)-1), m.cursor+1)
			case "enter":
				if len(visible) > 0 {
					cmd = m.requestGroup(visible[m.cursor])
				}
			case "d":
				m.screen = detectionScreen
				m.viewport.GotoTop()
			case "/":
				m.filtering = true
				cmd = m.filter.Focus()
			case "esc":
				m.filter.SetValue("")
				m.cursor = 0
			case "b":
				m.screen = stagedScreen
				m.cursor = 0
				m.reveal = false
			case "x":
				if len(visible) > 0 {
					delete(m.staged, m.groups[visible[m.cursor]].ID)
					m.status = "Group marked pending; decision unstaged."
				}
			case "r":
				if len(m.staged) > 0 {
					m.confirm = "scan"
				} else {
					cmd = m.start("scan")
				}
			case "v":
				if len(m.staged) > 0 {
					m.confirm = "vaults"
				} else {
					m.groups = nil
					cmd = m.start("vaults")
				}
			}
		case reviewScreen:
			switch key {
			case "up", "k":
				m.cursor = max(0, m.cursor-1)
			case "down", "j":
				m.cursor = min(m.rowCount()-1, m.cursor+1)
			case "left", "h":
				m.change(-1)
			case "right", "l", "space", "enter":
				m.change(1)
			case "r":
				m.reveal = !m.reveal
			case "d":
				m.selectNone()
			case "s":
				if _, err := m.reviewedPlan(); err != nil {
					m.status = err.Error()
				} else {
					m.screen = previewScreen
					m.viewport.GotoTop()
					m.status = ""
					m.reveal = false
				}
			case "x":
				delete(m.staged, m.groups[m.group].ID)
				m.openGroup(m.group)
				m.status = "Unstaged this group"
			case "]", "n":
				cmd = m.requestGroup((m.group + 1) % len(m.groups))
			case "[":
				cmd = m.requestGroup((m.group + len(m.groups) - 1) % len(m.groups))
			case "esc":
				m.screen = groupScreen
				m.cursorToGroup(m.group)
				m.reveal = false
				m.status = ""
			}
		case previewScreen:
			switch key {
			case "esc":
				m.screen = reviewScreen
			case "enter":
				p, err := m.reviewedPlan()
				if err != nil {
					m.status = err.Error()
					break
				}
				m.staged[p.GroupID] = p
				m.drafts[p.GroupID] = cloneDraft(m.draft)
				m.screen = groupScreen
				m.cursorToGroup(m.group)
				m.status = "Group resolved; decision staged. Press b to review the batch."
				m.reveal = false
			}
		case stagedScreen:
			ps := m.plans()
			switch key {
			case "esc":
				m.screen = groupScreen
				m.cursor = 0
			case "up", "k":
				m.cursor = max(0, m.cursor-1)
			case "down", "j":
				m.cursor = min(max(0, len(ps)-1), m.cursor+1)
			case "enter":
				if len(ps) > 0 {
					for n, g := range m.groups {
						if g.ID == ps[m.cursor].GroupID {
							cmd = m.requestGroup(n)
							break
						}
					}
				}
			case "x":
				if len(ps) > 0 {
					delete(m.staged, ps[m.cursor].GroupID)
					m.cursor = max(0, m.cursor-1)
				}
			case "a":
				if len(ps) > 0 {
					m.confirm = "apply"
				}
			}
		case resultScreen:
			if key == "r" || key == "enter" {
				cmd = m.start("scan")
			}
		case scanErrorScreen:
			if key == "r" || key == "enter" {
				cmd = m.start("scan")
			}
			if key == "v" {
				cmd = m.start("vaults")
			}
		case detectionScreen:
			if key == "esc" || key == "d" {
				m.screen = groupScreen
				m.cursor = 0
				m.viewport.GotoTop()
			} else {
				m.viewport, cmd = m.viewport.Update(msg)
				m.refresh(false)
				return m, cmd
			}
		}
	}
	m.refresh(true)
	return m, cmd
}

func (m Model) plans() []core.Plan {
	ps := []core.Plan{}
	for _, g := range m.groups {
		if p, ok := m.staged[g.ID]; ok {
			ps = append(ps, p)
		}
	}
	return ps
}

func (m Model) visibleGroups() []int {
	query := strings.ToLower(m.filter.Value())
	out := []int{}
	for n, g := range m.groups {
		text := strings.Join(g.Domains, " ")
		for _, i := range g.Items {
			text += " " + i.Title
		}
		if strings.Contains(strings.ToLower(text), query) {
			out = append(out, n)
		}
	}
	return out
}

// cursorToGroup selects group n in the list, or the top row if filtered out.
func (m *Model) cursorToGroup(n int) {
	m.cursor = 0
	for row, visible := range m.visibleGroups() {
		if visible == n {
			m.cursor = row
			return
		}
	}
}

func (m *Model) requestGroup(n int) tea.Cmd {
	if m.groups[n].Loaded {
		m.openGroup(n)
		return nil
	}
	m.group = n
	return m.start("group")
}

func (m *Model) openGroup(n int) {
	m.group = n
	m.screen = reviewScreen
	m.cursor = 0
	m.reveal = false
	m.previewFocused = false
	m.mergePreview.GotoTop()
	m.status = ""
	m.viewport.GotoTop()
	g := m.groups[n]
	target := 0
	for j, i := range g.Items {
		if i.State == "Active" {
			target = j
			break
		}
	}
	if p, ok := m.staged[g.ID]; ok {
		for j, i := range g.Items {
			if i.ID == p.TargetID {
				target = j
			}
		}
	}
	m.resetDraft(target)
	if _, ok := m.staged[g.ID]; ok {
		m.draft = cloneDraft(m.drafts[g.ID])
	}
}

func cloneDraft(d draft) draft {
	d.trash = maps.Clone(d.trash)
	d.sources = maps.Clone(d.sources)
	d.urlOn = maps.Clone(d.urlOn)
	d.noteOn = maps.Clone(d.noteOn)
	return d
}

func (m *Model) resetDraft(target int) {
	g := m.groups[m.group]
	i := g.Items[target]
	d := draft{target: target, trash: map[string]bool{}, sources: map[string]int{}, urlOn: map[string]bool{}, noteOn: map[string]bool{}}
	for _, f := range core.ScalarFields {
		d.sources[f] = target
	}
	urlSeen, noteSeen := map[string]bool{}, map[string]bool{}
	d.urls = append(d.urls, i.URLs...)
	for _, u := range i.URLs {
		urlSeen[u] = true
	}
	// Target values lead the displayed order, making untouched drafts exact copies.
	ordered := append([]core.Item{i}, g.Items...)
	for _, item := range ordered {
		for _, u := range item.URLs {
			if !urlSeen[u] {
				d.urls = append(d.urls, u)
				urlSeen[u] = true
			}
		}
		if item.Note != "" && !noteSeen[item.Note] {
			d.notes = append(d.notes, item.Note)
			noteSeen[item.Note] = true
		}
	}
	for _, u := range i.URLs {
		d.urlOn[u] = true
	}
	if i.Note != "" {
		d.noteOn[i.Note] = true
	}
	m.draft = d
	m.mergePreview.GotoTop()
}

func (m Model) rowCount() int {
	count := 1 + len(m.groups[m.group].Items) + len(core.ScalarFields) + len(m.draft.urls) + len(m.draft.notes)
	return count
}

func (m Model) draftPlan() core.Plan {
	g, d := m.groups[m.group], m.draft
	p := core.Plan{GroupID: g.ID, KeepNone: d.keepNone, Originals: g.Items, Fields: map[string]string{}}
	for _, i := range g.Items {
		if d.trash[i.ID] {
			p.TrashIDs = append(p.TrashIDs, i.ID)
		}
	}
	return p
}

func (m *Model) selectNone() {
	g := m.groups[m.group]
	if active, _ := g.StateCounts(); active == 0 {
		m.status = "All entries are already Trashed; there is nothing to delete"
		return
	}
	m.draft.keepNone = true
	m.draft.trash = map[string]bool{}
	for _, i := range g.Items {
		if i.State == "Active" {
			m.draft.trash[i.ID] = true
		}
	}
	for u := range m.draft.urlOn {
		m.draft.urlOn[u] = false
	}
	for note := range m.draft.noteOn {
		m.draft.noteOn[note] = false
	}
	m.cursor = 0
	m.mergePreview.GotoTop()
	m.status = "Keep none selected. Press s to preview moving all Active entries to Trash."
}

func (m *Model) change(delta int) {
	g := m.groups[m.group]
	d := &m.draft
	row := m.cursor
	m.status = ""
	if active, _ := g.StateCounts(); active == 0 {
		m.status = "Trashed-only group: comparison only; no Active merge target"
		return
	}
	if row == 0 {
		targets := []int{}
		position := 0
		for n, i := range g.Items {
			if i.State == "Active" {
				if n == d.target {
					position = len(targets)
				}
				targets = append(targets, n)
			}
		}
		if d.keepNone {
			position = len(targets)
		}
		position = (position + delta + len(targets) + 1) % (len(targets) + 1)
		if position == len(targets) {
			m.selectNone()
			return
		}
		m.resetDraft(targets[position])
		m.status = "Target changed; field choices reset. Other Active entries remain kept; Trashed entries stay unchanged."
		return
	}
	if d.keepNone {
		m.status = "Keep none selected; choose an Active target to edit merge choices"
		return
	}
	row--
	if row < len(g.Items) {
		i := g.Items[row]
		if i.State == "Trashed" {
			m.status = "This entry is already Trashed and will stay unchanged"
			return
		}
		if row != d.target {
			d.trash[i.ID] = !d.trash[i.ID]
		}
		return
	}
	row -= len(g.Items)
	if row < len(core.ScalarFields) {
		f := core.ScalarFields[row]
		d.sources[f] = (d.sources[f] + delta + len(g.Items)) % len(g.Items)
		return
	}
	row -= len(core.ScalarFields)
	if row < len(d.urls) {
		u := d.urls[row]
		d.urlOn[u] = !d.urlOn[u]
		return
	}
	row -= len(d.urls)
	if row < len(d.notes) {
		note := d.notes[row]
		d.noteOn[note] = !d.noteOn[note]
		return
	}
}

// reviewedPlan is the plan as staged from the preview page. That page ends by
// highlighting every value that will not be kept, including extra data on
// entries moving to Trash, so pressing Enter there is the acknowledgment.
func (m Model) reviewedPlan() (core.Plan, error) {
	p, err := m.makePlan()
	if errors.Is(err, core.ErrExtrasUnacknowledged) {
		p.ExtrasAcknowledged = true
		err = p.Validate()
	}
	return p, err
}

func (m Model) makePlan() (core.Plan, error) {
	g := m.groups[m.group]
	if active, _ := g.StateCounts(); active == 0 {
		return core.Plan{}, errors.New("Trashed-only group: comparison only; no Active merge target")
	}
	d := m.draft
	p := m.draftPlan()
	if d.keepNone {
		return p, p.Validate()
	}
	target := g.Items[d.target]
	p.TargetID = target.ID
	values, urls := m.mergedValues()
	for f, v := range values {
		if v != target.Value(f) {
			p.Fields[f] = v
		}
	}
	// Compare slices first: an unchanged legacy URL with a comma is safe to retain.
	if !equalStrings(urls, target.URLs) {
		v, err := core.EncodeURLs(urls)
		if err != nil {
			return p, err
		}
		p.Fields["urls"] = v
	}
	return p, p.Validate()
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

// Safe makes externally supplied text inert inside a terminal, including bidi controls.
func Safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return '↵'
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return '�'
		}
		return r
	}, s)
}

func display(field, value string, reveal bool) string {
	if value == "" {
		return "∅"
	}
	if field == "password" && !reveal {
		return "••••••••"
	}
	return Safe(value)
}

func (m *Model) refresh(focus bool) {
	m.sizePanes()
	// Dropped data is computed once per refresh and shared by both panes.
	var dropped []core.DroppedEntry
	if m.screen == reviewScreen {
		m.cursor = min(m.cursor, max(0, m.rowCount()-1))
		dropped = m.droppedData()
	}
	m.focusLine = -1
	content := m.bodyWith(dropped)
	m.viewport.SetContent(content)
	if m.screen != reviewScreen {
		m.previewFocused = false
	}
	if m.screen == reviewScreen && (m.width >= splitPreviewWidth || m.previewFocused) {
		m.mergePreview.SetContent(m.mergePreviewBody(dropped))
	} else {
		m.mergePreview.SetContent("")
	}
	if focus && !m.previewFocused && m.focusLine >= 0 {
		if m.focusLine < m.viewport.YOffset() {
			m.viewport.SetYOffset(m.focusLine)
		}
		if m.focusLine >= m.viewport.YOffset()+m.viewport.Height() {
			m.viewport.SetYOffset(m.focusLine - m.viewport.Height() + 1)
		}
	}
}

func (m *Model) body() string {
	var dropped []core.DroppedEntry
	if m.screen == reviewScreen {
		dropped = m.droppedData()
	}
	return m.bodyWith(dropped)
}

func (m *Model) bodyWith(dropped []core.DroppedEntry) string {
	var b strings.Builder
	width := m.viewport.Width()
	line := func(s string) {
		b.WriteString(lipgloss.NewStyle().Width(width).Render(s))
		b.WriteByte('\n')
	}
	row := 0
	add := func(s string) {
		prefix := "  "
		if row == m.cursor {
			prefix = "› "
			m.focusLine = strings.Count(b.String(), "\n")
		}
		wrapped := lipgloss.NewStyle().Width(max(10, width-2)).Render(s)
		line(prefix + strings.ReplaceAll(wrapped, "\n", "\n  "))
		row++
	}
	switch m.screen {
	case vaultScreen:
		line(accent.Render("Choose a vault"))
		line("")
		for _, v := range m.vaults {
			add(Safe(v.Name) + dim.Render("  ["+Safe(v.ShareID)+"]"))
		}
		if len(m.vaults) == 0 {
			line("No vaults available. Log in with pass-cli separately, then press r.")
		}
	case busyScreen:
		line(m.spinner.View() + " " + accent.Render(Safe(m.progress.Phase)))
		if m.progress.Total > 0 {
			line(fmt.Sprintf("\n%d / %d", m.progress.Done, m.progress.Total))
		}
		line("\nEsc cancels. Changes require staged batch approval.")
	case scanErrorScreen:
		line(warn.Render("Scan failed — no results available"))
		line("")
		line(Safe(m.status))
		line("\nPress r to retry, or v to choose another vault.")
	case groupScreen:
		line(accent.Render(fmt.Sprintf("%d collision groups · %d resolved · %d pending", len(m.groups), len(m.staged), len(m.groups)-len(m.staged))))
		line(fmt.Sprintf("%d logins · %d Active · %d Trashed · %d with domains", len(m.scan.Entries), m.scan.Active, m.scan.Trashed, m.scan.WithDomains))
		if m.allURLs {
			line(dim.Render("Matching names and saved URLs"))
		} else {
			line(dim.Render("Matching item names · details load when a group is opened"))
		}
		if m.status != "" {
			line(warn.Render(Safe(m.status)))
		}
		line(dim.Render("Candidates may be different accounts. Review before changing anything."))
		line(dim.Render("Resolved decisions await batch approval (b)."))
		line("")
		for _, n := range m.visibleGroups() {
			g := m.groups[n]
			badge := dim.Render("[Pending]")
			if p, ok := m.staged[g.ID]; ok {
				label := "[Resolved]"
				if p.KeepNone {
					label = "[Resolved · keep none]"
				}
				badge = accent.Render(label)
			}
			add(fmt.Sprintf("%s %s · %s", badge, Safe(strings.Join(g.Domains, ", ")), groupState(g)))
		}
		if len(m.visibleGroups()) == 0 {
			if m.filter.Value() != "" {
				line("No groups match this filter.")
			} else {
				line("No duplicate domains found in this scan.")
				line("Press d to inspect the domains detected for each item.")
				if !m.allURLs {
					line("Use --all-urls to also search saved website URLs.")
				}
			}
		}
	case detectionScreen:
		if m.allURLs {
			line(accent.Render("Detected domains · names and saved URLs"))
		} else {
			line(accent.Render("Detected domains · item-list metadata"))
		}
		line(dim.Render("Esc returns · arrows / PgUp / PgDn scroll"))
		line("")
		for _, entry := range m.scan.Entries {
			domains := strings.Join(entry.Domains, ", ")
			if domains == "" {
				domains = "no recognized domain"
			}
			line("[" + Safe(entry.State) + "] " + Safe(entry.Title) + " → " + Safe(domains))
			line(dim.Render("  [" + Safe(entry.ID) + "]"))
			line(dim.Render("  " + entryDates(entry.CreateTime, entry.ModifyTime)))
		}
	case reviewScreen:
		g := m.groups[m.group]
		d := m.draft
		p := m.draftPlan()
		risky := map[string]bool{}
		for _, i := range p.ExtraDataTrashed() {
			risky[i.ID] = true
		}
		line(accent.Render(fmt.Sprintf("Group %d/%d · %s", m.group+1, len(m.groups), Safe(strings.Join(g.Domains, ", ")))))
		line(groupState(g))
		line(dim.Render("←/→ target or keep none · d keep none · s previews decision"))
		line("")
		active, _ := g.StateCounts()
		if active == 0 {
			add("Merge target: unavailable · Trashed-only group is comparison only")
		} else if d.keepNone {
			add("Keep: none — move all Active entries in this group to Trash")
		} else {
			add("Merge target: " + Safe(g.Items[d.target].Title) + " [Active]")
		}
		for n, i := range g.Items {
			action := "KEEP"
			if d.trash[i.ID] {
				action = "TRASH"
			}
			if n == d.target && !d.keepNone {
				action = "TARGET"
			}
			if i.State == "Trashed" {
				action = "UNCHANGED"
			}
			entry := fmt.Sprintf("[%s · %s] #%d %s  [%s]", action, Safe(i.State), n+1, Safe(i.Title), Safe(i.ID))
			if risky[i.ID] {
				entry += warn.Render("  ⚠ extra data not transferred")
			}
			add(entry)
			line(dim.Render("    " + entryDates(i.CreateTime, i.ModifyTime)))
		}
		renderDroppedInfo(dropped, m.reveal, true, line)
		line("")
		for _, f := range core.ScalarFields {
			values := map[string]bool{}
			missing := false
			for _, i := range g.Items {
				values[i.Value(f)] = true
				if i.Value(f) == "" {
					missing = true
				}
			}
			state := good.Render("equal")
			if len(values) > 1 {
				state = warn.Render("different")
			}
			if missing {
				state += " · missing value"
			}
			idx := d.sources[f]
			if d.keepNone {
				add(fmt.Sprintf("%s [%s] · comparison only", f, state))
			} else {
				add(fmt.Sprintf("%s [%s] → #%d [%s]: %s", f, state, idx+1, Safe(g.Items[idx].State), display(f, g.Items[idx].Value(f), m.reveal)))
			}
			// A compact matrix at wide widths, stacked source rows in narrower terminals.
			cells := []string{}
			for n, i := range g.Items {
				cells = append(cells, fmt.Sprintf("#%d [%s] %s", n+1, Safe(i.State), display(f, i.Value(f), m.reveal)))
			}
			if width >= 120 && f != "title" {
				line("    " + Safe(strings.Join(cells, " │ ")))
			} else {
				for _, c := range cells {
					line("    " + c)
				}
			}
		}
		line("")
		line(accent.Render("URLs · selected values keep this displayed order"))
		for _, u := range d.urls {
			mark := " "
			if d.urlOn[u] {
				mark = "x"
			}
			add("[" + mark + "] " + Safe(u) + "  " + sources(g.Items, func(i core.Item) bool {
				for _, v := range i.URLs {
					if v == u {
						return true
					}
				}
				return false
			}))
		}
		line("")
		line(accent.Render("Notes · selected blocks join with a blank line"))
		for _, s := range d.notes {
			mark := " "
			if d.noteOn[s] {
				mark = "x"
			}
			add("[" + mark + "] " + Safe(s) + "  " + sources(g.Items, func(i core.Item) bool { return i.Note == s }))
		}
		line("")
		line(accent.Render("Domain links"))
		for _, domain := range g.Domains {
			line("  " + Safe(domain) + " → " + Safe(strings.Join(g.Links[domain], ", ")))
		}
		line("")
		line(accent.Render("Additional data · comparison only"))
		for n, i := range g.Items {
			line(fmt.Sprintf("#%d [%s] %s", n+1, Safe(i.State), Safe(i.Title)))
			keys := make([]string, 0, len(i.Extras))
			for k := range i.Extras {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) == 0 {
				line("  none")
			}
			for _, k := range keys {
				value := displayExtra(i.Extras[k], m.reveal)
				line("  " + Safe(k) + " [" + extraStatus(g.Items, k) + "]: " + value)
			}
		}
	case previewScreen:
		line(accent.Render("Review this decision before staging"))
		line("")
		p, err := m.reviewedPlan()
		if err != nil {
			line(warn.Render(Safe(err.Error())))
		} else {
			m.planBody(&b, p)
			if len(p.ExtraDataTrashed())+len(p.DroppedData()) > 0 {
				line(warn.Render("\nEnter stages this decision, accepting that the highlighted data will not be kept."))
				line("No vault changes happen yet.")
				break
			}
		}
		line("\nEnter stages this decision. No vault changes happen yet.")
	case stagedScreen:
		line(accent.Render("Staged batch"))
		line(dim.Render("Enter revises · x unstages · a opens final approval"))
		line("")
		ps := m.plans()
		if len(ps) == 0 {
			line("No staged decisions. Esc returns to collision groups.")
		}
		for n, p := range ps {
			label := p.Target().Title
			if p.KeepNone {
				label = "Keep none — entire group"
			}
			add(fmt.Sprintf("Decision %d · %s", n+1, Safe(label)))
			m.planBody(&b, p)
			line("")
		}
	case resultScreen:
		line(accent.Render("Batch results"))
		line("")
		if m.result.Err == nil {
			line("Reviewed operations completed.")
		} else if !m.result.AttemptedWrite {
			line("Batch stopped before any write.")
		} else {
			line("Batch stopped. Some operations may have completed.")
		}
		for _, o := range m.result.Outcomes {
			line(fmt.Sprintf("  %s %s — %s", Safe(o.Action), Safe(o.ItemID), Safe(o.Status)))
		}
		line("\nPress r to rescan and reconcile actual state before further cleanup.")
	}
	return b.String()
}

func (m Model) planBody(b *strings.Builder, p core.Plan) {
	line := func(s string) {
		b.WriteString(lipgloss.NewStyle().Width(m.viewport.Width()).Render(s))
		b.WriteByte('\n')
	}
	target := p.Target()
	if p.KeepNone {
		line(warn.Render("  Keep none: move all Active entries to Trash. No merge target."))
	} else {
		line(fmt.Sprintf("  Target: %s [%s]", Safe(target.Title), Safe(target.ID)))
	}
	for _, f := range core.SortedFields(p.Fields) {
		line(fmt.Sprintf("  %s: %s → %s", f, display(f, target.Value(f), false), display(f, p.Fields[f], false)))
	}
	for _, i := range p.Originals {
		action := "KEEP"
		if i.State == "Trashed" {
			action = "UNCHANGED"
		}
		for _, id := range p.TrashIDs {
			if id == i.ID {
				action = "TRASH"
			}
		}
		line(fmt.Sprintf("  [%s · %s] %s [%s]", action, Safe(i.State), Safe(i.Title), Safe(i.ID)))
		line("    " + entryDates(i.CreateTime, i.ModifyTime))
	}
	// The loss summary comes last so it is the final thing read before staging.
	if trashed := p.ExtraDataTrashed(); len(trashed) > 0 {
		line("")
		line(warn.Bold(true).Render("Extra data not transferred from entries moving to Trash"))
		for _, i := range trashed {
			line(warn.Render("  " + Safe(p.EntryLabels([]core.Item{i})) + ": " + Safe(strings.Join(i.ExtraKeys(), ", "))))
		}
	}
	renderDroppedInfo(p.DroppedData(), false, false, line)
}

func sources(items []core.Item, matches func(core.Item) bool) string {
	labels := []string{}
	for n, i := range items {
		if matches(i) {
			labels = append(labels, fmt.Sprintf("#%d %s", n+1, Safe(i.State)))
		}
	}
	return "[" + strings.Join(labels, ", ") + "]"
}

func groupState(g core.Group) string {
	active, trashed := g.StateCounts()
	label := fmt.Sprintf("%d Active · %d Trashed", active, trashed)
	if active == 0 {
		label += " · comparison only"
	}
	return label
}

func entryDates(created, modified string) string {
	stamp := func(value string) string {
		if value == "" {
			return "unknown"
		}
		return Safe(value)
	}
	return "Created: " + stamp(created) + " · Modified: " + stamp(modified)
}

func extraStatus(items []core.Item, key string) string {
	values := map[string]bool{}
	missing := false
	for _, i := range items {
		b, ok := i.Extras[key]
		if !ok {
			missing = true
			values[""] = true
			continue
		}
		v, _ := core.DecodeExtra(b)
		b, _ = json.Marshal(v)
		values[string(b)] = true
	}
	status := "equal"
	if len(values) > 1 {
		status = "different"
	}
	if missing {
		status += " · missing"
	}
	return status
}

func (m Model) View() tea.View {
	if m.width < 50 || m.height < 12 {
		v := tea.NewView("Resize to at least 50 columns × 12 rows.\nq quits; Esc cancels an operation.")
		v.AltScreen = true
		return v
	}
	mode := ""
	if m.demo {
		mode = " · OFFLINE DEMO"
	}
	header := accent.Render("PASS DUPS" + mode)
	if m.vault.Name != "" && m.screen != vaultScreen {
		header += "  " + Safe(m.vault.Name)
	}
	footer := "↑/↓ or j/k navigate · PgUp/PgDn scroll · q quit"
	switch m.screen {
	case vaultScreen:
		footer += " · Enter select · r retry"
	case groupScreen:
		footer += " · / filter · d domains · b batch · r rescan · v vaults"
	case scanErrorScreen:
		footer = "r retry scan · v choose vault · q quit"
	case detectionScreen:
		footer = "↑/↓ / PgUp / PgDn scroll · Esc back · q quit"
	case reviewScreen:
		if m.previewFocused {
			footer = "Tab choices · ↑/↓ / PgUp/PgDn scroll preview · r hide/reveal · s preview · Esc back"
		} else {
			footer += " · Tab live preview · s preview · d keep none · r reveal · [/] previous/next · Esc back"
		}
	case previewScreen:
		footer = "Enter stage · Esc revise · PgUp/PgDn scroll"
	case stagedScreen:
		footer = "a apply · Enter revise · x unstage · Esc back · PgUp/PgDn scroll"
	case resultScreen:
		footer = "r rescan · PgUp/PgDn scroll · q quit"
	case busyScreen:
		footer = "Esc / Ctrl+C cancel operation"
	}
	status := Safe(m.status)
	if m.confirm != "" {
		switch m.confirm {
		case "apply":
			status = "Apply this entire staged batch to the vault? y confirms · n/Esc cancels"
		case "quit":
			status = "Discard staged work and quit? y confirms · n/Esc cancels"
		case "scan":
			status = "Discard staged work and rescan? y confirms · n/Esc cancels"
		case "vaults":
			status = "Discard staged work and switch vaults? y confirms · n/Esc cancels"
		}
	}
	filter := ""
	if m.screen == groupScreen {
		filter = m.filter.View()
	}
	body := m.viewport.View()
	if m.screen == reviewScreen {
		if m.width >= splitPreviewWidth {
			divider := dim.Render(strings.TrimSuffix(strings.Repeat(" │ \n", m.viewport.Height()), "\n"))
			body = lipgloss.JoinHorizontal(lipgloss.Top, body, divider, m.mergePreview.View())
		} else if m.previewFocused {
			body = m.mergePreview.View()
		}
	}
	text := header + "\n" + dim.Render(strings.Repeat("─", max(1, m.width-4))) + "\n" + body + "\n" + filter + "\n" + warn.Render(status) + "\n" + dim.Render(footer)
	v := tea.NewView(lipgloss.NewStyle().Padding(0, 2).Width(m.width).Render(text))
	v.AltScreen = true
	return v
}
