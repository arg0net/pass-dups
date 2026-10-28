package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"pass-dups/internal/core"
	"pass-dups/internal/demo"
	"pass-dups/internal/passcli"
)

func TestMixedStatesPrioritizeActiveEntriesAndGroups(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	scan, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if scan.Active != 6 || scan.Trashed != 1 {
		t.Fatal("scan did not include both states")
	}
	if scan.Groups[0].Domains[0] != "example.com" {
		t.Fatal("group with multiple Active entries was not prioritized")
	}
	g := find(scan.Groups, "example.co.uk")
	active, trashed := g.StateCounts()
	if active != 1 || trashed != 1 {
		t.Fatal("wrong group state counts")
	}
	if g.Items[0].ID != "e" || g.Items[1].ID != "f" {
		t.Fatal("alphabetically earlier Trashed entry was not deprioritized")
	}
	if g.Items[0].CreateTime != "2026-01-05T00:00:00" || g.Items[1].ModifyTime != "2026-01-06T00:00:00" {
		t.Fatal("metadata scan lost entry dates")
	}
	loaded, err := core.LoadGroup(context.Background(), c, g, func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Items[1].State != "Trashed" || loaded.Items[1].CreateTime != "2026-01-06T00:00:00" {
		t.Fatal("Trashed details or timestamps were lost")
	}
}

func TestTrashedSourceMayBeComparedButIsNeverWritten(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	scan, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	g, err := core.LoadGroup(context.Background(), c, find(scan.Groups, "example.co.uk"), func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	p := core.Plan{GroupID: g.ID, TargetID: "e", Originals: g.Items, Fields: map[string]string{"note": item(g, "f").Note}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	before := item(g, "f")
	r := &wrappedRunner{base: c.Runner}
	c.Runner = r
	res := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if res.Err != nil || len(res.Outcomes) != 1 || res.Outcomes[0].ItemID != "e" {
		t.Fatalf("explicit copy from Trashed source failed: %v", res.Err)
	}
	after, err := c.View(context.Background(), "demo", "f")
	if err != nil {
		t.Fatal(err)
	}
	if before.Snapshot() != after.Snapshot() {
		t.Fatal("Trashed source was changed")
	}
	for _, cmd := range r.calls {
		if cmd == "trash" {
			t.Fatal("already-Trashed entry was trashed again")
		}
	}
	p.TargetID = "f"
	if p.Validate() == nil {
		t.Fatal("Trashed merge target allowed")
	}
	p.TargetID = "e"
	p.TrashIDs = []string{"f"}
	if p.Validate() == nil {
		t.Fatal("redundant trash operation allowed")
	}
}

func TestTrashedOnlyGroupsAreLastAndReadOnly(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	if err := c.Trash(context.Background(), "demo", "e"); err != nil {
		t.Fatal(err)
	}
	scan, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	g := scan.Groups[len(scan.Groups)-1]
	if active, trashed := g.StateCounts(); active != 0 || trashed != 2 {
		t.Fatal("Trashed-only group was not ordered last")
	}
	g, err = core.LoadGroup(context.Background(), c, g, func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	p := core.Plan{GroupID: g.ID, TargetID: g.Items[0].ID, Originals: g.Items, Fields: map[string]string{"note": "synthetic"}}
	if p.Validate() == nil {
		t.Fatal("Trashed-only group allowed mutation")
	}
}

func TestStateChangesInvalidateLazyLoad(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	scan, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	g := find(scan.Groups, "example.co.uk")
	if err := c.Trash(context.Background(), "demo", "e"); err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadGroup(context.Background(), c, g, func(core.Progress) {}); err == nil {
		t.Fatal("state change after listing was not detected")
	}
}

func TestDatesAreOptionalAndPreserveTimezone(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	i, err := c.View(context.Background(), "demo", "a")
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	_ = json.Unmarshal(i.Raw, &raw)
	item := raw["item"].(map[string]any)
	item["create_time"] = "2024-09-08T01:49:57-07:00"
	item["modify_time"] = "2026-10-28T03:28:33.123456Z"
	b, _ := json.Marshal(raw)
	parsed, err := core.ParseItem(b)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.CreateTime != item["create_time"] || parsed.ModifyTime != item["modify_time"] {
		t.Fatal("timestamp precision or timezone changed")
	}
	delete(item, "create_time")
	item["modify_time"] = nil
	b, _ = json.Marshal(raw)
	parsed, err = core.ParseItem(b)
	if err != nil || parsed.CreateTime != "" || parsed.ModifyTime != "" {
		t.Fatal("missing timestamps should be unknown, not fabricated")
	}
}
