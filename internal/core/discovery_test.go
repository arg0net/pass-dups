package core_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"pass-dups/internal/core"
	"pass-dups/internal/demo"
	"pass-dups/internal/passcli"
)

// These are public hostnames from the reported example; IDs are synthetic.
// Detail reads deliberately fail so this regression cannot pass by eager loading.
type wolframRunner struct{ views atomic.Int32 }

func (r *wolframRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	if args[0] == "item" && args[1] == "list" {
		return []byte(`{"items":[{"id":"synthetic-wolfram-a","share_id":"synthetic-personal","title":"user.wolfram.com","state":"Active","item_type":"login"},{"id":"synthetic-wolfram-b","share_id":"synthetic-personal","title":"wolfram.com","state":"Active","item_type":"login"}]}`), nil
	}
	r.views.Add(1)
	return nil, errors.New("detail access is disabled in this synthetic regression")
}

func TestWolframDetectedWithoutReadingSecrets(t *testing.T) {
	r := &wolframRunner{}
	result, err := core.Scan(context.Background(), passcli.Client{Runner: r}, "synthetic-personal", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if r.views.Load() != 0 {
		t.Fatal("metadata discovery attempted detail access")
	}
	if result.WithDomains != 2 || len(result.Entries) != 2 || len(result.Groups) != 1 {
		t.Fatalf("expected both logins in one domain group; got %d matched names and %d groups", result.WithDomains, len(result.Groups))
	}
	g := result.Groups[0]
	if !reflect.DeepEqual(g.Domains, []string{"wolfram.com"}) || len(g.Items) != 2 || g.Loaded {
		t.Fatal("incorrect Wolfram collision group")
	}
	for _, i := range g.Items {
		if i.Password != "" || len(i.Raw) != 0 {
			t.Fatal("metadata group contains full details")
		}
	}
	if _, err := core.LoadGroup(context.Background(), passcli.Client{Runner: r}, g, func(core.Progress) {}); err == nil {
		t.Fatal("opening a group did not request its details")
	}
}

func TestTitleDomainFormats(t *testing.T) {
	for title, want := range map[string][]string{
		"user.wolfram.com": {"wolfram.com"},
		"wolfram.com":      {"wolfram.com"},
		"Wolfram (https://user.wolfram.com/login) — Personal": {"wolfram.com"},
		"Personal—user.wolfram.com":                           {"wolfram.com"},
		"Wolfram: user.wolfram.com (copy 2)":                  {"wolfram.com"},
		"Site https://USER.WOLFRAM.COM:443/login?next=1":      {"wolfram.com"},
		"Site WWW.WOLFRAM.COM.":                               {"wolfram.com"},
		"Quoted “https://user.wolfram.com/login”":             {"wolfram.com"},
		"Two sites: example.com, example.org":                 {"example.com", "example.org"},
		"IDN https://bücher.de/login":                         {"xn--bcher-kva.de"},
		"Unicode dots user。wolfram。com":                       {"wolfram.com"},
		"Email alex@wolfram.com":                              {},
		"Email first.name@wolfram.com":                        {},
		"Unknown https://example.invalid":                     {},
		"Bad https://exa_mple.com":                            {},
		"Bad http://example.com:notaport":                     {},
		"Other ftp://example.com":                             {},
		"Credentials https://user:password@example.com":       {},
		"Local https://localhost and https://127.0.0.1":       {},
	} {
		t.Run(title, func(t *testing.T) {
			got := core.TitleDomains(title)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("got %q, want %q", got, want)
			}
		})
	}
}

func TestOnlyOpenedGroupDetailsAreRead(t *testing.T) {
	r := &wrappedRunner{base: demo.New()}
	c := passcli.Client{Runner: r}
	result, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.calls) != 1 || r.calls[0] != "list" {
		t.Fatal("default scan read details")
	}
	if len(result.Entries) != 7 || result.WithDomains != 6 || len(result.Groups) != 2 {
		t.Fatal("incorrect scan statistics")
	}
	g := find(result.Groups, "example.co.uk")
	if len(g.Items) != 2 {
		t.Fatal("default scan unexpectedly used advanced URL-only entry")
	}
	loaded, err := core.LoadGroup(context.Background(), c, g, func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Loaded || len(r.calls) != 3 {
		t.Fatal("loading a group read entries outside that group")
	}
	for _, i := range loaded.Items {
		if len(i.Raw) == 0 {
			t.Fatal("loaded member missing details")
		}
	}
	if result.Groups[0].Loaded {
		t.Fatal("loading mutated original metadata groups")
	}
	p := core.Plan{GroupID: g.ID, TargetID: g.Items[0].ID, Originals: g.Items, TrashIDs: []string{g.Items[1].ID}}
	if err := p.Validate(); err == nil || !strings.Contains(err.Error(), "details") {
		t.Fatal("metadata-only plan allowed")
	}
}

func TestLoadFailureDoesNotExposePartialGroup(t *testing.T) {
	r := &wrappedRunner{base: demo.New()}
	c := passcli.Client{Runner: r}
	result, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	g := find(result.Groups, "example.com")
	r.failAction = "view"
	loaded, err := core.LoadGroup(context.Background(), c, g, func(core.Progress) {})
	if err == nil || loaded.Loaded || len(loaded.Items) != 0 {
		t.Fatal("failed group load exposed partial review data")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := core.LoadGroup(ctx, c, g, func(core.Progress) {}); err == nil {
		t.Fatal("canceled group load succeeded")
	}
}

func TestRenamedGroupMemberRequiresRescan(t *testing.T) {
	c := passcli.Client{Runner: demo.New()}
	result, err := core.Scan(context.Background(), c, "demo", func(core.Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	g := find(result.Groups, "example.com")
	if err := c.Update(context.Background(), "demo", g.Items[0].ID, map[string]string{"title": "Renamed — example.org"}); err != nil {
		t.Fatal(err)
	}
	if _, err := core.LoadGroup(context.Background(), c, g, func(core.Progress) {}); err == nil {
		t.Fatal("renamed member was accepted in old group")
	}
}
