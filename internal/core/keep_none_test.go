package core_test

import (
	"context"
	"strings"
	"testing"

	"pass-dups/internal/core"
	"pass-dups/internal/demo"
	"pass-dups/internal/passcli"
)

func keepNone(g core.Group) core.Plan {
	p := core.Plan{GroupID: g.ID, KeepNone: true, Originals: g.Items, ExtrasAcknowledged: true}
	for _, i := range g.Items {
		if i.State == "Active" {
			p.TrashIDs = append(p.TrashIDs, i.ID)
		}
	}
	return p
}

func TestKeepNoneValidation(t *testing.T) {
	_, groups := load(t)
	g := find(groups, "example.com")
	if err := keepNone(g).Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*core.Plan)
	}{
		{"must be explicit", func(p *core.Plan) { p.KeepNone = false }},
		{"cannot keep a target", func(p *core.Plan) { p.TargetID = g.Items[0].ID }},
		{"cannot update fields", func(p *core.Plan) { p.Fields = map[string]string{"note": "synthetic"} }},
		{"must trash every Active entry", func(p *core.Plan) { p.TrashIDs = p.TrashIDs[1:] }},
		{"cannot duplicate operations", func(p *core.Plan) { p.TrashIDs = append(p.TrashIDs, p.TrashIDs[0]) }},
		{"must acknowledge extras", func(p *core.Plan) { p.ExtrasAcknowledged = false }},
		{"must have members", func(p *core.Plan) { p.Originals = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := keepNone(g)
			tc.change(&p)
			if p.Validate() == nil {
				t.Fatal("invalid keep-none plan accepted")
			}
		})
	}
}

func TestKeepNoneAppliesOnlyTrashOperations(t *testing.T) {
	c, groups := load(t)
	g := find(groups, "example.com")
	p := keepNone(g)
	r := &wrappedRunner{base: c.Runner}
	c.Runner = r
	res := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	if len(res.Outcomes) != len(p.TrashIDs) {
		t.Fatal("wrong number of trash operations")
	}
	for _, o := range res.Outcomes {
		if o.Action != "trash" || o.Status != "completed" {
			t.Fatal("keep-none produced a non-trash or incomplete operation")
		}
	}
	for _, cmd := range r.calls {
		if cmd == "update" {
			t.Fatal("keep-none attempted to update a merge target")
		}
	}
	for _, id := range p.TrashIDs {
		i, err := c.View(context.Background(), "demo", id)
		if err != nil || i.State != "Trashed" {
			t.Fatal("group member was not trashed")
		}
	}
	i, err := c.View(context.Background(), "demo", "e")
	if err != nil || i.State != "Active" {
		t.Fatal("unrelated group was changed")
	}
}

func TestKeepNoneLeavesAlreadyTrashedEntriesUntouched(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	scan, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	g, err := core.LoadGroup(context.Background(), c, find(scan.Groups, "example.co.uk"), func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	before := item(g, "f")
	p := keepNone(g)
	if len(p.TrashIDs) != 1 || p.TrashIDs[0] != "e" {
		t.Fatal("already-Trashed member was selected")
	}
	res := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	after, err := c.View(context.Background(), "demo", "f")
	if err != nil || before.Snapshot() != after.Snapshot() {
		t.Fatal("already-Trashed member was changed")
	}
	p.TrashIDs = append(p.TrashIDs, "f")
	if p.Validate() == nil {
		t.Fatal("redundant trash was accepted")
	}
	scan, err = core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	g, err = core.LoadGroup(context.Background(), c, find(scan.Groups, "example.co.uk"), func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if keepNone(g).Validate() == nil {
		t.Fatal("already-Trashed group allowed an empty action")
	}
}

func TestKeepNoneStopsOnPartialFailure(t *testing.T) {
	c, groups := load(t)
	p := keepNone(find(groups, "example.com"))
	r := &wrappedRunner{base: c.Runner}
	r.after = func(args []string) {
		if args[1] == "trash" {
			r.failAction = "trash"
		}
	}
	c.Runner = r
	res := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if res.Err == nil || !res.AttemptedWrite || res.Outcomes[0].Status != "completed" || !strings.HasPrefix(res.Outcomes[1].Status, "failed") {
		t.Fatal("partial failure was not reported")
	}
	for _, o := range res.Outcomes[2:] {
		if o.Status != "unattempted" {
			t.Fatal("keep-none continued after failure")
		}
	}
}

func TestKeepNoneChecksForStaleEntriesBeforeWriting(t *testing.T) {
	c, groups := load(t)
	p := keepNone(find(groups, "example.com"))
	if err := c.Update(context.Background(), "demo", p.Originals[0].ID, map[string]string{"note": "concurrent synthetic edit"}); err != nil {
		t.Fatal(err)
	}
	r := &wrappedRunner{base: c.Runner}
	c.Runner = r
	res := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if res.Err == nil || res.AttemptedWrite {
		t.Fatal("stale keep-none batch wrote to vault")
	}
	for _, cmd := range r.calls {
		if cmd == "trash" {
			t.Fatal("stale entry was trashed")
		}
	}
}
