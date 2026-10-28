package passcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"pass-dups/internal/core"
)

// Runner is the only boundary that may start a process. Tests and demo replace it.
type Runner interface {
	Run(context.Context, ...string) ([]byte, error)
}
type Client struct{ Runner Runner }

type ExecRunner struct {
	Binary  string
	Timeout time.Duration
}

type limitedBuffer struct {
	bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		return 0, errors.New("CLI output limit exceeded")
	}
	return b.Buffer.Write(p)
}

func (r ExecRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.Binary, args...)
	out, stderr := &limitedBuffer{max: 32 << 20}, &limitedBuffer{max: 64 << 10}
	cmd.Stdout = out
	cmd.Stderr = stderr
	// No shell, inherited terminal input, output streaming or command logging.
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, errors.New("pass-cli operation canceled or timed out")
		}
		s := strings.ToLower(stderr.String())
		switch {
		case errors.Is(err, exec.ErrNotFound), errors.Is(err, os.ErrNotExist):
			return nil, errors.New("pass-cli could not be started; check --pass-cli and PATH")
		case strings.Contains(s, "login"), strings.Contains(s, "log in"), strings.Contains(s, "session"), strings.Contains(s, "authenticat"):
			return nil, errors.New("pass-cli authentication failed; log in separately, then retry")
		case strings.Contains(s, "permission"), strings.Contains(s, "denied"), strings.Contains(s, "read-only"):
			return nil, errors.New("pass-cli access denied; check vault permissions")
		default:
			return nil, errors.New("pass-cli command failed; check CLI connectivity and compatibility, then retry")
		}
	}
	return out.Bytes(), nil
}

func decodeList[T any](b []byte, key string) ([]T, error) {
	var root map[string]json.RawMessage
	if json.Unmarshal(b, &root) != nil || root[key] == nil || string(root[key]) == "null" {
		return nil, core.ErrSchema
	}
	var values []T
	if json.Unmarshal(root[key], &values) != nil {
		return nil, core.ErrSchema
	}
	return values, nil
}

func (c Client) Vaults(ctx context.Context) ([]core.Vault, error) {
	b, err := c.Runner.Run(ctx, "vault", "list", "--output=json")
	if err != nil {
		return nil, err
	}
	vs, err := decodeList[core.Vault](b, "vaults")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, v := range vs {
		if v.ShareID == "" || seen[v.ShareID] {
			return nil, core.ErrSchema
		}
		seen[v.ShareID] = true
	}
	sort.Slice(vs, func(i, j int) bool {
		if vs[i].Name == vs[j].Name {
			return vs[i].ShareID < vs[j].ShareID
		}
		return vs[i].Name < vs[j].Name
	})
	return vs, nil
}

func (c Client) List(ctx context.Context, share string) ([]core.Summary, error) {
	b, err := c.Runner.Run(ctx, "item", "list", "--share-id", share, "--filter-type", "login", "--output=json")
	if err != nil {
		return nil, err
	}
	is, err := decodeList[core.Summary](b, "items")
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	logins := make([]core.Summary, 0, len(is))
	for _, i := range is {
		if i.ID == "" || i.ShareID != share || i.Type == "" || (i.State != "Active" && i.State != "Trashed") || seen[i.ID] {
			return nil, core.ErrSchema
		}
		seen[i.ID] = true
		if i.Type == "login" {
			logins = append(logins, i)
		}
	}
	return logins, nil
}

func (c Client) View(ctx context.Context, share, id string) (core.Item, error) {
	b, err := c.Runner.Run(ctx, "item", "view", "--share-id", share, "--item-id", id, "--output=json")
	if err != nil {
		return core.Item{}, err
	}
	i, err := core.ParseItem(b)
	if err != nil {
		return core.Item{}, err
	}
	if i.ID != id || i.ShareID != share {
		return core.Item{}, core.ErrSchema
	}
	return i, nil
}

func (c Client) Update(ctx context.Context, share, id string, fields map[string]string) error {
	args := []string{"item", "update", "--share-id", share, "--item-id", id}
	for _, f := range core.SortedFields(fields) {
		args = append(args, "--field", f+"="+fields[f])
	}
	_, err := c.Runner.Run(ctx, args...)
	return err
}

func (c Client) Trash(ctx context.Context, share, id string) error {
	_, err := c.Runner.Run(ctx, "item", "trash", "--share-id", share, "--item-id", id)
	return err
}
