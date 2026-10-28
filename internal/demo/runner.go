// Package demo is an in-memory, synthetic pass-cli. It never starts a process.
package demo

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
)

//go:embed testdata/vault.json
var fixture []byte

type Runner struct {
	mu       sync.Mutex
	details  map[string]map[string]any
	revision int
}

func New() *Runner {
	var f struct {
		Details []map[string]any `json:"details"`
	}
	if err := json.Unmarshal(fixture, &f); err != nil {
		panic("invalid synthetic demo fixture")
	}
	r := &Runner{details: map[string]map[string]any{}}
	for _, d := range f.Details {
		item := d["item"].(map[string]any)
		r.details[item["id"].(string)] = d
	}
	return r
}

func (r *Runner) Run(ctx context.Context, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(args) < 2 {
		return nil, errors.New("unsupported demo command")
	}
	arg := func(name string) string {
		for n := 2; n+1 < len(args); n++ {
			if args[n] == name {
				return args[n+1]
			}
		}
		return ""
	}
	if args[0] == "vault" && args[1] == "list" {
		return []byte(`{"vaults":[{"name":"Demo • synthetic accounts","share_id":"demo","vault_id":"demo-vault"},{"name":"Empty demo vault","share_id":"empty","vault_id":"empty-vault"}]}`), nil
	}
	if args[0] != "item" {
		return nil, errors.New("unsupported demo command")
	}
	if args[1] == "list" {
		items := []map[string]any{}
		for _, d := range r.details {
			i := d["item"].(map[string]any)
			if i["share_id"] == arg("--share-id") {
				summary := map[string]any{"id": i["id"], "share_id": i["share_id"], "vault_id": i["vault_id"], "flags": i["flags"], "state": i["state"], "create_time": i["create_time"], "modify_time": i["modify_time"], "title": i["content"].(map[string]any)["title"], "item_type": "login"}
				if folder, ok := i["folder_id"]; ok {
					summary["folder_id"] = folder
				}
				items = append(items, summary)
			}
		}
		return json.Marshal(map[string]any{"items": items})
	}
	d, ok := r.details[arg("--item-id")]
	if !ok {
		return nil, errors.New("demo item missing")
	}
	i := d["item"].(map[string]any)
	if i["share_id"] != arg("--share-id") {
		return nil, errors.New("demo vault mismatch")
	}
	switch args[1] {
	case "view":
		return json.Marshal(d)
	case "trash":
		i["state"] = "Trashed"
	case "update":
		data := i["content"].(map[string]any)
		login := data["content"].(map[string]any)["Login"].(map[string]any)
		for n := 2; n+1 < len(args); n++ {
			if args[n] != "--field" {
				continue
			}
			pair := strings.SplitN(args[n+1], "=", 2)
			if len(pair) != 2 {
				return nil, errors.New("invalid demo field")
			}
			key, val := pair[0], pair[1]
			switch key {
			case "title", "note":
				data[key] = val
			case "urls":
				urls := []string{}
				for _, s := range strings.Split(val, ",") {
					if s = strings.TrimSpace(s); s != "" {
						urls = append(urls, s)
					}
				}
				login[key] = urls
			case "username", "email", "password":
				login[key] = val
			default:
				return nil, errors.New("unsupported demo field")
			}
		}
	default:
		return nil, errors.New("unsupported demo command")
	}
	r.revision++
	i["modify_time"] = time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(r.revision) * time.Second).Format("2006-01-02T15:04:05")
	return []byte("Demo change completed"), nil
}
