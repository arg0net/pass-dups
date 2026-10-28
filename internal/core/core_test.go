package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/arg0net/pass-dups/internal/core"
	"github.com/arg0net/pass-dups/internal/demo"
	"github.com/arg0net/pass-dups/internal/passcli"
)

func TestDomains(t *testing.T) {
	for input, want := range map[string]string{
		"https://LOGIN.Example.COM.:443/path": "example.com",
		"shop.example.co.uk":                  "example.co.uk",
		"https://bücher.de":                   "xn--bcher-kva.de",
		"alice.github.io":                     "alice.github.io",
		"bob.github.io":                       "bob.github.io",
		"co.uk":                               "", "com": "", "localhost": "", "127.0.0.1": "", "http://[::1]": "",
		"https://user:password@example.com": "", "http://bad.invalid": "", "-bad.com": "", "https://exa_mple.com": "",
		"https://example.com:notaport": "", "ftp://example.com": "",
	} {
		t.Run(input, func(t *testing.T) {
			if got := core.Domain(input); got != want {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
	for input, want := range map[string]string{"A login (shop.example.co.uk)": "example.co.uk", "Login — https://accounts.example.com/login": "example.com", "A name without domain": ""} {
		if got := core.TitleDomain(input); got != want {
			t.Errorf("title %q: %q", input, got)
		}
	}
}

func load(t *testing.T) (passcli.Client, []core.Group) {
	t.Helper()
	c := passcli.Client{Runner: demo.New()}
	g, err := core.ScanAllURLs(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	return c, g.Groups
}

func find(g []core.Group, domain string) core.Group {
	for _, x := range g {
		for _, d := range x.Domains {
			if d == domain {
				return x
			}
		}
	}
	panic("missing synthetic group")
}
func item(g core.Group, id string) core.Item {
	for _, i := range g.Items {
		if i.ID == id {
			return i
		}
	}
	panic("missing synthetic item")
}

func TestConnectedGroupsAndDiscovery(t *testing.T) {
	_, groups := load(t)
	if len(groups) != 2 {
		t.Fatalf("got %d groups", len(groups))
	}
	g := find(groups, "example.com")
	if len(g.Items) != 4 || !reflect.DeepEqual(g.Domains, []string{"example.com", "example.org"}) || len(g.Links["example.org"]) != 2 {
		t.Fatal("transitive domain group missing")
	}
	uk := find(groups, "example.co.uk")
	if len(uk.Items) != 3 {
		t.Fatal("autofill URL-only entry not discovered")
	}
	if len(item(g, "b").Extras) < 3 {
		t.Fatal("TOTP, custom fields or attachments missing")
	}
	for range 20 {
		shuffled := append([]core.Item(nil), g.Items...)
		for a, b := 0, len(shuffled)-1; a < b; a, b = a+1, b-1 {
			shuffled[a], shuffled[b] = shuffled[b], shuffled[a]
		}
		if !reflect.DeepEqual(core.Collisions(shuffled), []core.Group{g}) {
			t.Fatal("unstable grouping")
		}
	}
}

func plan(g core.Group) core.Plan {
	return core.Plan{GroupID: g.ID, TargetID: "a", Originals: g.Items, Fields: map[string]string{"password": "SYNTHETIC-password-two", "note": "merged synthetic note"}, TrashIDs: []string{"b"}, ExtrasAcknowledged: true}
}

func TestPlanValidation(t *testing.T) {
	_, groups := load(t)
	p := plan(find(groups, "example.com"))
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	p.ExtrasAcknowledged = false
	if p.Validate() == nil {
		t.Fatal("allowed unacknowledged extra data loss")
	}
	p.ExtrasAcknowledged = true
	p.TrashIDs = []string{"a"}
	if p.Validate() == nil {
		t.Fatal("allowed target trash")
	}
	p.TrashIDs = []string{"b"}
	p.Fields = map[string]string{"totp": "seed"}
	if p.Validate() == nil {
		t.Fatal("allowed unsupported update")
	}
	p.Fields = map[string]string{"password": "a\x00b"}
	if p.Validate() == nil {
		t.Fatal("allowed NUL argument")
	}
	p.Fields = map[string]string{"urls": "https://example.com/a,b"}
	if p.Validate() == nil {
		t.Fatal("allowed ambiguous URL list")
	}
	for _, u := range [][]string{{"https://example.com/a,b"}, {" https://example.com"}, {""}} {
		if _, err := core.EncodeURLs(u); err == nil {
			t.Fatal("allowed lossy URL encoding")
		}
	}
	p.Fields = map[string]string{"password": "new"}
	p.Originals = append([]core.Item(nil), p.Originals...)
	for n, i := range p.Originals {
		if i.ID == "a" {
			p.Originals[n].CustomNames = []string{"password"}
		}
	}
	if p.Validate() == nil {
		t.Fatal("allowed custom field shadowing")
	}
	uk := find(groups, "example.co.uk")
	p = core.Plan{GroupID: uk.ID, Originals: uk.Items, TargetID: "g", Fields: map[string]string{"urls": "https://example.co.uk"}}
	if p.Validate() == nil {
		t.Fatal("allowed advanced autofill overwrite")
	}
}

type wrappedRunner struct {
	base                passcli.Runner
	mu                  sync.Mutex
	calls               []string
	failAction          string
	corruptVerification bool
	updated             bool
	after               func([]string)
}

func (r *wrappedRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args[1])
	if args[1] == r.failAction {
		return nil, errors.New("synthetic failure")
	}
	b, err := r.base.Run(ctx, args...)
	if args[1] == "update" {
		r.updated = true
	}
	if r.after != nil {
		r.after(args)
	}
	if r.corruptVerification && r.updated && args[1] == "view" {
		var d map[string]any
		_ = json.Unmarshal(b, &d)
		d["item"].(map[string]any)["content"].(map[string]any)["content"].(map[string]any)["Login"].(map[string]any)["password"] = "SYNTHETIC-unexpected"
		b, _ = json.Marshal(d)
	}
	return b, err
}

func TestApplyUpdatesVerifiesThenTrashes(t *testing.T) {
	c, groups := load(t)
	p := plan(find(groups, "example.com"))
	r := &wrappedRunner{base: c.Runner}
	c.Runner = r
	result := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if len(result.Outcomes) != 2 || result.Outcomes[1].Status != "completed" {
		t.Fatal("incomplete batch")
	}
	update, trash := -1, -1
	for n, v := range r.calls {
		if v == "update" {
			update = n
		}
		if v == "trash" {
			trash = n
		}
	}
	if update < 0 || trash <= update+1 || r.calls[update+1] != "view" {
		t.Fatal("missing verification between update and trash")
	}
	a, _ := c.View(context.Background(), "demo", "a")
	b, _ := c.View(context.Background(), "demo", "b")
	other, _ := c.View(context.Background(), "demo", "c")
	if a.Password != p.Fields["password"] || b.State != "Trashed" || other.State != "Active" {
		t.Fatal("incorrect keep/merge/trash result")
	}
}

func TestStalePlanWritesNothing(t *testing.T) {
	c, groups := load(t)
	p := plan(find(groups, "example.com"))
	if err := c.Update(context.Background(), "demo", "b", map[string]string{"note": "concurrent synthetic edit"}); err != nil {
		t.Fatal(err)
	}
	r := &wrappedRunner{base: c.Runner}
	c.Runner = r
	result := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if result.Err == nil || result.AttemptedWrite || len(result.StaleGroups) != 1 {
		t.Fatal("stale preflight did not stop")
	}
	for _, v := range r.calls {
		if v == "update" || v == "trash" {
			t.Fatal("stale batch wrote data")
		}
	}
}

func TestBatchFailures(t *testing.T) {
	for _, tc := range []struct {
		name, fail  string
		corrupt     bool
		firstStatus string
	}{
		{"update failure", "update", false, "failed"}, {"verification failure", "", true, "failed"}, {"trash failure", "trash", false, "completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, g := load(t)
			p := plan(find(g, "example.com"))
			r := &wrappedRunner{base: c.Runner, failAction: tc.fail, corruptVerification: tc.corrupt}
			c.Runner = r
			res := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
			if res.Err == nil || !res.AttemptedWrite || !strings.HasPrefix(res.Outcomes[0].Status, tc.firstStatus) {
				t.Fatal("incorrect partial failure reporting")
			}
			if tc.fail != "trash" {
				for _, call := range r.calls {
					if call == "trash" {
						t.Fatal("source trashed after failed update or verification")
					}
				}
			}
		})
	}
}

func TestCancellationAndOverlap(t *testing.T) {
	c, g := load(t)
	p := plan(find(g, "example.com"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := core.Apply(ctx, c, []core.Plan{p}, func(core.Progress) {})
	if res.Err == nil || res.AttemptedWrite {
		t.Fatal("canceled batch wrote")
	}
	res = core.Apply(context.Background(), c, []core.Plan{p, p}, func(core.Progress) {})
	if res.Err == nil || res.AttemptedWrite {
		t.Fatal("overlapping batch accepted")
	}
	groups, err := core.Scan(ctx, c, "demo", func(core.Progress) {})
	if err == nil || groups.Groups != nil {
		t.Fatal("canceled scan returned actionable groups")
	}
}

func TestIncompleteScanHasNoGroups(t *testing.T) {
	r := &wrappedRunner{base: demo.New(), failAction: "view"}
	g, err := core.ScanAllURLs(context.Background(), passcli.Client{Runner: r}, "demo", func(core.Progress) {})
	if err == nil || g.Groups != nil {
		t.Fatal("partial scan exposed collision groups")
	}
}

func TestKeepOnlyAndEmptyVault(t *testing.T) {
	c, g := load(t)
	group := find(g, "example.com")
	p := core.Plan{GroupID: group.ID, TargetID: "a", Originals: group.Items, TrashIDs: []string{"d"}}
	r := &wrappedRunner{base: c.Runner}
	c.Runner = r
	res := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if res.Err != nil {
		t.Fatal(res.Err)
	}
	for _, s := range r.calls {
		if s == "update" {
			t.Fatal("keep-only updated target")
		}
	}
	empty, err := core.Scan(context.Background(), c, "empty", func(core.Progress) {})
	if err != nil || len(empty.Groups) != 0 || len(empty.Entries) != 0 {
		t.Fatal("empty vault scan failed")
	}
}

func TestVerificationPreservesUnselectedData(t *testing.T) {
	c, _ := load(t)
	before, err := c.View(context.Background(), "demo", "a")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		mutate    func(map[string]any)
		wantError bool
	}{
		{"only intended change", func(map[string]any) {}, false},
		{"unselected username", func(r map[string]any) { loginMap(r)["username"] = "unexpected" }, true},
		{"UUID", func(r map[string]any) { dataMap(r)["item_uuid"] = "unexpected" }, true},
		{"flags", func(r map[string]any) { r["item"].(map[string]any)["flags"] = []string{"unexpected"} }, true},
		{"future secret", func(r map[string]any) { loginMap(r)["future_secret"] = "SYNTHETIC-new-extra" }, true},
		{"attachment", func(r map[string]any) { r["attachments"] = []map[string]any{{"id": "unexpected"}} }, true},
		{"default autofill migration", func(r map[string]any) {
			loginMap(r)["autofill_urls"] = []map[string]any{{"url": "https://accounts.example.com", "mode": "Default"}}
		}, false},
		{"different autofill rule", func(r map[string]any) {
			loginMap(r)["autofill_urls"] = []map[string]any{{"url": "https://accounts.example.com", "mode": "Exact"}}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r map[string]any
			_ = json.Unmarshal(before.Raw, &r)
			loginMap(r)["password"] = "SYNTHETIC-selected"
			r["item"].(map[string]any)["modify_time"] = "a-new-revision"
			tc.mutate(r)
			b, _ := json.Marshal(r)
			after, err := core.ParseItem(b)
			if err != nil {
				t.Fatal(err)
			}
			err = core.VerifyUpdate(before, after, map[string]string{"password": "SYNTHETIC-selected"})
			if (err != nil) != tc.wantError {
				t.Fatalf("verification error=%v; wanted failure=%v", err, tc.wantError)
			}
		})
	}
}

func dataMap(r map[string]any) map[string]any {
	return r["item"].(map[string]any)["content"].(map[string]any)
}
func loginMap(r map[string]any) map[string]any {
	return dataMap(r)["content"].(map[string]any)["Login"].(map[string]any)
}

func TestConcurrentSourceEditAfterMergeStopsTrash(t *testing.T) {
	c, groups := load(t)
	p := plan(find(groups, "example.com"))
	base := c.Runner
	r := &wrappedRunner{base: base}
	updated, edited := false, false
	r.after = func(args []string) {
		if args[1] == "update" {
			updated = true
		}
		if args[1] == "view" && updated && !edited && strings.Contains(strings.Join(args, " "), "--item-id a ") {
			edited = true
			_, err := base.Run(context.Background(), "item", "update", "--share-id", "demo", "--item-id", "b", "--field", "note=concurrent synthetic edit")
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	c.Runner = r
	res := core.Apply(context.Background(), c, []core.Plan{p}, func(core.Progress) {})
	if !edited || res.Err == nil || res.Outcomes[0].Status != "completed" || res.Outcomes[1].Status != "unattempted" {
		t.Fatal("source edit was not caught after successful merge")
	}
	for _, v := range r.calls {
		if v == "trash" {
			t.Fatal("concurrently changed source was trashed")
		}
	}
}

func TestFailureLeavesLaterGroupsUnattempted(t *testing.T) {
	c, groups := load(t)
	first := plan(find(groups, "example.com"))
	uk := find(groups, "example.co.uk")
	second := core.Plan{GroupID: uk.ID, TargetID: "e", Originals: uk.Items, Fields: map[string]string{"password": "SYNTHETIC-merged"}, TrashIDs: []string{"g"}, ExtrasAcknowledged: true}
	r := &wrappedRunner{base: c.Runner, failAction: "update"}
	c.Runner = r
	res := core.Apply(context.Background(), c, []core.Plan{first, second}, func(core.Progress) {})
	if res.Err == nil || len(res.Outcomes) != 4 || res.Outcomes[2].Status != "unattempted" || res.Outcomes[3].Status != "unattempted" {
		t.Fatal("later group was not left unattempted")
	}
	updates := 0
	for _, v := range r.calls {
		if v == "update" {
			updates++
		}
	}
	if updates != 1 {
		t.Fatal("batch continued after first failure")
	}
}

func TestAcknowledgmentIsCheckedLast(t *testing.T) {
	_, groups := load(t)
	p := plan(find(groups, "example.com"))
	p.ExtrasAcknowledged = false
	if err := p.Validate(); !errors.Is(err, core.ErrExtrasUnacknowledged) {
		t.Fatalf("expected an acknowledgment error, got %v", err)
	}
	p.Fields["title"] = ""
	if err := p.Validate(); err == nil || errors.Is(err, core.ErrExtrasUnacknowledged) {
		t.Fatalf("acknowledgment error hid another validation error: %v", err)
	}
}
