package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"pass-dups/internal/core"
	"pass-dups/internal/demo"
	"pass-dups/internal/passcli"
)

func TestExtraDataIgnoresEmptyContainers(t *testing.T) {
	for _, tc := range []struct {
		name, json string
		want       bool
	}{
		{"null", " null ", false},
		{"empty text", `""`, false},
		{"formatted empty array", "[\n  ]", false},
		{"formatted empty object", "{\n  }", false},
		{"empty platform defaults", `{"android":{"allowed_apps":[]},"other":null}`, false},
		{"empty nested arrays", `[{},null,[],""]`, false},
		{"hidden value", `{"Hidden":"SYNTHETIC-secret"}`, true},
		{"false is data", `false`, true},
		{"zero is data", `0`, true},
		{"unknown nonempty field", `{"future_data":"synthetic"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := core.Item{Extras: map[string]json.RawMessage{"synthetic": json.RawMessage(tc.json)}}
			if got := i.HasExtraData(); got != tc.want {
				t.Fatalf("got presence=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestEmptyCLIExtraMetadataDoesNotRequireAcknowledgment(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	target, err := c.View(context.Background(), "demo", "a")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal(target.Raw, &root)
	root["item"].(map[string]any)["id"] = "synthetic-source"
	dataMap(root)["platform_specific"] = map[string]any{"android": map[string]any{"allowed_apps": []any{}}}
	b, _ := json.MarshalIndent(root, "", "  ")
	// Empty containers need not be serialized with the exact bytes [] or {}.
	b = bytes.ReplaceAll(b, []byte("[]"), []byte("[\n ]"))
	source, err := core.ParseItem(b)
	if err != nil {
		t.Fatal(err)
	}
	if source.HasExtraData() || len(source.Extras) != 0 {
		t.Fatal("empty CLI metadata was classified as extra data")
	}
	p := core.Plan{TargetID: target.ID, Originals: []core.Item{target, source}, TrashIDs: []string{source.ID}}
	if p.NeedsExtrasAcknowledgment() {
		t.Fatal("empty metadata triggered acknowledgment")
	}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestAcknowledgmentDependsOnSelectedActiveEntries(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	a, err := c.View(context.Background(), "demo", "a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.View(context.Background(), "demo", "b")
	if err != nil {
		t.Fatal(err)
	}
	if !b.HasExtraData() {
		t.Fatal("synthetic risky entry has no extra data")
	}
	p := core.Plan{TargetID: b.ID, Originals: []core.Item{a, b}, TrashIDs: []string{a.ID}}
	if p.NeedsExtrasAcknowledgment() || p.Validate() != nil {
		t.Fatal("retained extra data triggered acknowledgment")
	}
	p.TargetID = a.ID
	p.TrashIDs = []string{b.ID}
	if !p.NeedsExtrasAcknowledgment() || p.Validate() == nil {
		t.Fatal("selected extra data was not protected")
	}
	if err := c.Trash(context.Background(), "demo", "b"); err != nil {
		t.Fatal(err)
	}
	b, err = c.View(context.Background(), "demo", "b")
	if err != nil {
		t.Fatal(err)
	}
	p = core.Plan{TargetID: a.ID, Originals: []core.Item{a, b}, Fields: map[string]string{"note": "synthetic edit"}}
	if p.NeedsExtrasAcknowledgment() || p.Validate() != nil {
		t.Fatal("already-Trashed extra data triggered acknowledgment")
	}
}
