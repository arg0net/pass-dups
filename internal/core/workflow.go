package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

type Progress struct {
	Done, Total int
	Phase       string
}

// Detection contains only metadata, so scan diagnostics never require secret reads.
type Detection struct {
	ID, Title                     string
	State, CreateTime, ModifyTime string
	Domains                       []string
}

type ScanResult struct {
	Groups          []Group
	Entries         []Detection
	WithDomains     int
	IncludeURLs     bool
	Active, Trashed int
}

// Scan discovers collisions from list metadata without reading any item details.
func Scan(ctx context.Context, c Client, share string, progress func(Progress)) (ScanResult, error) {
	return scan(ctx, c, share, false, progress)
}

// ScanAllURLs is an explicit opt-in: saved URLs require reading every login.
func ScanAllURLs(ctx context.Context, c Client, share string, progress func(Progress)) (ScanResult, error) {
	return scan(ctx, c, share, true, progress)
}

func scan(ctx context.Context, c Client, share string, includeURLs bool, progress func(Progress)) (ScanResult, error) {
	summaries, err := c.List(ctx, share)
	if err != nil {
		return ScanResult{}, err
	}
	items := make([]Item, len(summaries))
	for n, s := range summaries {
		items[n] = Item{ID: s.ID, ShareID: s.ShareID, Title: s.Title, State: s.State, CreateTime: s.CreateTime, ModifyTime: s.ModifyTime}
	}
	if includeURLs {
		items, err = loadItems(ctx, c, items, progress)
		if err != nil {
			return ScanResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return ScanResult{}, err
	}
	result := ScanResult{Groups: Collisions(items), IncludeURLs: includeURLs}
	for _, i := range items {
		domains := TitleDomains(i.Title)
		if includeURLs {
			domains = Domains(i)
		}
		result.Entries = append(result.Entries, Detection{ID: i.ID, Title: i.Title, State: i.State, CreateTime: i.CreateTime, ModifyTime: i.ModifyTime, Domains: domains})
		if i.State == "Active" {
			result.Active++
		}
		if i.State == "Trashed" {
			result.Trashed++
		}
		if len(domains) > 0 {
			result.WithDomains++
		}
	}
	sort.Slice(result.Entries, func(a, b int) bool {
		if result.Entries[a].State != result.Entries[b].State {
			return result.Entries[a].State == "Active"
		}
		if result.Entries[a].Title == result.Entries[b].Title {
			return result.Entries[a].ID < result.Entries[b].ID
		}
		return result.Entries[a].Title < result.Entries[b].Title
	})
	progress(Progress{Done: len(items), Total: len(items), Phase: "Detected domains"})
	return result, nil
}

// LoadGroup is all-or-nothing. Until every member is loaded, no plan can be staged.
// Keep the discovery links intact: opening a group never expands a metadata scan
// into secret reads for other groups.
func LoadGroup(ctx context.Context, c Client, g Group, progress func(Progress)) (Group, error) {
	items, err := loadItems(ctx, c, g.Items, progress)
	if err != nil {
		return Group{}, err
	}
	g.Items = items
	g.Loaded = true
	return g, nil
}

func loadItems(ctx context.Context, c Client, originals []Item, progress func(Progress)) ([]Item, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	items := make([]Item, len(originals))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var first error
	done := 0
	progress(Progress{Total: len(items), Phase: "Reading login details"})
	for range min(4, len(items)) {
		wg.Go(func() {
			for n := range jobs {
				old := originals[n]
				i, e := c.View(ctx, old.ShareID, old.ID)
				mu.Lock()
				if e == nil && (i.ID != old.ID || i.ShareID != old.ShareID || i.State != old.State || i.Title != old.Title || len(i.Raw) == 0) {
					e = errors.New("an entry changed or details are incomplete; rescan the vault")
				}
				if e != nil && first == nil {
					first = e
					cancel()
				}
				if e == nil {
					items[n] = i
					done++
					progress(Progress{Done: done, Total: len(items), Phase: "Reading login details"})
				}
				mu.Unlock()
			}
		})
	}
send:
	for n := range originals {
		select {
		case jobs <- n:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	if first != nil {
		return nil, first
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return items, nil
}

type Outcome struct{ GroupID, ItemID, Action, Status string }
type BatchResult struct {
	Outcomes []Outcome
	Err      error
	// AttemptedWrite means the caller must rescan, even when a command failed.
	AttemptedWrite bool
	StaleGroups    []string
}

func Apply(ctx context.Context, c Client, plans []Plan, progress func(Progress)) BatchResult {
	r := BatchResult{}
	for _, p := range plans {
		if err := p.Validate(); err != nil {
			r.Err = err
			return r
		}
		if len(p.Fields) > 0 {
			r.Outcomes = append(r.Outcomes, Outcome{p.GroupID, p.TargetID, "update", "unattempted"})
		}
		for _, id := range p.TrashIDs {
			r.Outcomes = append(r.Outcomes, Outcome{p.GroupID, id, "trash", "unattempted"})
		}
	}
	// Reject overlapping plans before any read or write.
	seen := map[string]bool{}
	for _, p := range plans {
		for _, i := range p.Originals {
			key := i.ShareID + "\x00" + i.ID
			if seen[key] {
				r.Err = errors.New("staged groups overlap; review them again")
				return r
			}
			seen[key] = true
		}
	}
	total := 0
	for _, p := range plans {
		total += len(p.Originals)
	}
	done := 0
	for _, p := range plans {
		stale := false
		for _, old := range p.Originals {
			progress(Progress{done, total, "Checking staged entries"})
			fresh, err := c.View(ctx, old.ShareID, old.ID)
			if err != nil {
				r.Err = err
				return r
			}
			if fresh.Snapshot() != old.Snapshot() {
				stale = true
			}
			done++
		}
		if stale {
			r.StaleGroups = append(r.StaleGroups, p.GroupID)
		}
	}
	if len(r.StaleGroups) > 0 {
		r.Err = errors.New("staged entries changed; rescan and review before applying")
		return r
	}
	step := 0
	run := func(action string, fn func() error) bool {
		if ctx.Err() != nil {
			r.Err = errors.New("batch canceled; rescan to reconcile actual state")
			return false
		}
		progress(Progress{step, len(r.Outcomes), "Applying reviewed changes"})
		r.AttemptedWrite = true
		if err := fn(); err != nil {
			r.Outcomes[step].Status = "failed; state may have changed"
			r.Err = fmt.Errorf("%s failed; rescan to reconcile actual state: %w", action, err)
			return false
		}
		r.Outcomes[step].Status = "completed"
		step++
		return true
	}
	for _, p := range plans {
		target := p.Target()
		share := p.Originals[0].ShareID
		// Recheck immediately before each group: earlier groups may take time to apply.
		for _, old := range p.Originals {
			fresh, err := c.View(ctx, old.ShareID, old.ID)
			if err != nil {
				r.Err = err
				return r
			}
			if fresh.Snapshot() != old.Snapshot() {
				r.StaleGroups = append(r.StaleGroups, p.GroupID)
				r.Err = errors.New("an entry changed while applying the batch; remaining operations were stopped")
				return r
			}
		}
		if len(p.Fields) > 0 {
			if !run("update", func() error {
				if err := c.Update(ctx, target.ShareID, target.ID, p.Fields); err != nil {
					return err
				}
				fresh, err := c.View(ctx, target.ShareID, target.ID)
				if err != nil {
					return err
				}
				return VerifyUpdate(target, fresh, p.Fields)
			}) {
				return r
			}
		}
		for _, id := range p.TrashIDs {
			fresh, err := c.View(ctx, share, id)
			if err != nil {
				r.Err = err
				return r
			}
			for _, old := range p.Originals {
				if old.ID == id && old.Snapshot() != fresh.Snapshot() {
					r.Err = errors.New("a source entry changed before trashing; remaining operations were stopped")
					return r
				}
			}
			if !run("trash", func() error { return c.Trash(ctx, share, id) }) {
				return r
			}
		}
	}
	progress(Progress{step, len(r.Outcomes), "Complete"})
	return r
}
