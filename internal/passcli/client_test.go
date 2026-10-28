package passcli

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arg0net/pass-dups/internal/core"
	"github.com/arg0net/pass-dups/internal/demo"
)

type canned struct {
	data []byte
	err  error
	args []string
}

func (r *canned) Run(_ context.Context, args ...string) ([]byte, error) {
	r.args = append([]string(nil), args...)
	return r.data, r.err
}

func TestJSONValidationAndCommandArguments(t *testing.T) {
	r := &canned{data: []byte(`{"vaults":[{"name":"Synthetic","share_id":"v","vault_id":"id"}]}`)}
	c := Client{r}
	if vs, err := c.Vaults(context.Background()); err != nil || len(vs) != 1 {
		t.Fatal("vault JSON rejected")
	}
	if !reflect.DeepEqual(r.args, []string{"vault", "list", "--output=json"}) {
		t.Fatal("incorrect vault arguments")
	}
	for _, s := range []string{`{`, `{}`, `{"vaults":null}`, `{"vaults":[{}]}`, `{"vaults":[{"share_id":"a"},{"share_id":"a"}]}`} {
		r.data = []byte(s)
		if _, err := c.Vaults(context.Background()); err == nil {
			t.Fatal("accepted malformed vault response")
		}
	}
	r.data = []byte(`{"items":[{"id":"i","share_id":"v","title":"demo","state":"Active","item_type":"login"}]}`)
	if _, err := c.List(context.Background(), "v"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.args, " "), "--filter-type login") || strings.Contains(strings.Join(r.args, " "), "--filter-state") {
		t.Fatal("scan must include both Active and Trashed logins")
	}
	if _, err := c.List(context.Background(), "wrong-vault"); err == nil {
		t.Fatal("accepted wrong-vault entries")
	}
	if err := c.Update(context.Background(), "v", "i", map[string]string{"password": "SYNTHETIC=a$()\nvalue", "note": "synthetic"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.args, []string{"item", "update", "--share-id", "v", "--item-id", "i", "--field", "note=synthetic", "--field", "password=SYNTHETIC=a$()\nvalue"}) {
		t.Fatal("field values were altered or shell-encoded")
	}
}

func TestMetadataFixtureMatchesReportedCLIShape(t *testing.T) {
	// Mirrors the user's metadata shape, with synthetic IDs and titles. No secrets.
	b, err := os.ReadFile("testdata/item-list.json")
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Items []map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, item := range raw.Items {
		for _, key := range []string{"id", "share_id", "vault_id", "state", "flags", "create_time", "modify_time", "title", "item_type"} {
			if _, ok := item[key]; !ok {
				t.Fatalf("fixture missing CLI metadata field %s", key)
			}
		}
		for _, key := range []string{"password", "content", "note"} {
			if _, ok := item[key]; ok {
				t.Fatalf("metadata fixture contains secret-bearing field %s", key)
			}
		}
	}
	c := Client{&canned{data: b}}
	items, err := c.List(context.Background(), "synthetic-share-id")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].State != "Trashed" || items[0].Type != "login" {
		t.Fatal("mixed metadata did not retain Trashed login and exclude alias")
	}
	if items[0].CreateTime != "2026-10-28T03:16:06" || items[0].ModifyTime != "2026-10-28T03:28:33" {
		t.Fatal("timestamps were lost or converted")
	}
}

func TestParserRejectsMalformedWithoutEchoingData(t *testing.T) {
	for _, s := range []string{`{"password":"SYNTHETIC-private"}`, `{"item":{}}`, `{"item":{"content":{"content":{"Note":{}}}}}`} {
		_, err := core.ParseItem([]byte(s))
		if err == nil || strings.Contains(err.Error(), "SYNTHETIC") {
			t.Fatal("schema error leaked data or succeeded")
		}
	}
	c := Client{demo.New()}
	i, err := c.View(context.Background(), "demo", "a")
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	_ = json.Unmarshal(i.Raw, &root)
	root["item"].(map[string]any)["content"].(map[string]any)["content"].(map[string]any)["Login"].(map[string]any)["password"] = 123
	b, _ := json.Marshal(root)
	if _, err := core.ParseItem(b); err == nil {
		t.Fatal("accepted wrongly typed secret")
	}
}

// The subprocess is this Go test binary, never pass-cli or a shell.
func TestExecRunnerSanitizesAndCancels(t *testing.T) {
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := ExecRunner{Binary: bin, Timeout: time.Second}
	_, err = r.Run(context.Background(), "-test.run=TestSyntheticSubprocess", "--", "fail")
	if err == nil || strings.Contains(err.Error(), "SYNTHETIC-private") {
		t.Fatal("raw subprocess error exposed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = r.Run(ctx, "-test.run=TestSyntheticSubprocess", "--", "sleep")
	if err == nil {
		t.Fatal("canceled process succeeded")
	}
	r.Timeout = 20 * time.Millisecond
	_, err = r.Run(context.Background(), "-test.run=TestSyntheticSubprocess", "--", "sleep")
	if err == nil {
		t.Fatal("process did not time out")
	}
	_, err = (ExecRunner{Binary: "/nonexistent/synthetic-pass-cli"}).Run(context.Background(), "--help")
	if err == nil {
		t.Fatal("missing binary succeeded")
	}
}

func TestSyntheticSubprocess(t *testing.T) {
	if len(os.Args) < 2 {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "fail" {
		_, _ = os.Stderr.WriteString("SYNTHETIC-private-password; authentication failure")
		os.Exit(2)
	}
	if mode == "sleep" {
		time.Sleep(time.Second)
		os.Exit(0)
	}
}
