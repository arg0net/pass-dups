package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type Vault struct {
	Name    string `json:"name"`
	ShareID string `json:"share_id"`
	VaultID string `json:"vault_id"`
}

type Summary struct {
	ID         string `json:"id"`
	ShareID    string `json:"share_id"`
	Title      string `json:"title"`
	Type       string `json:"item_type"`
	State      string `json:"state"`
	CreateTime string `json:"create_time"`
	ModifyTime string `json:"modify_time"`
}

// Item holds normalized review fields and a lossless copy of the CLI response.
// Do not log or format Item: it contains plaintext credentials.
type Item struct {
	ID, ShareID, State                     string
	CreateTime, ModifyTime                 string
	Title, Username, Email, Password, Note string
	URLs                                   []string
	WebsiteURLs                            []string
	Extras                                 map[string]json.RawMessage
	CustomNames                            []string
	Autofill                               bool
	Raw                                    json.RawMessage
}

func (i Item) Value(field string) string {
	switch field {
	case "title":
		return i.Title
	case "username":
		return i.Username
	case "email":
		return i.Email
	case "password":
		return i.Password
	case "note":
		return i.Note
	case "urls":
		return strings.Join(i.URLs, ", ")
	}
	return ""
}

// Set is the inverse of Value; it is the single place merge values enter an Item.
func (i *Item) Set(field, value string) {
	switch field {
	case "title":
		i.Title = value
	case "username":
		i.Username = value
	case "email":
		i.Email = value
	case "password":
		i.Password = value
	case "note":
		i.Note = value
	case "urls":
		i.URLs = DecodeURLs(value)
	}
}

var ScalarFields = []string{"title", "username", "email", "password"}

type Client interface {
	Vaults(context.Context) ([]Vault, error)
	List(context.Context, string) ([]Summary, error)
	View(context.Context, string, string) (Item, error)
	Update(context.Context, string, string, map[string]string) error
	Trash(context.Context, string, string) error
}

var ErrSchema = errors.New("unsupported or malformed pass-cli JSON")

// ErrExtrasUnacknowledged is always the last check in Validate, so a plan failing
// only with this error is otherwise valid.
var ErrExtrasUnacknowledged = errors.New("acknowledge extra data that will not be transferred from entries selected for Trash")

func object(b json.RawMessage) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	err := json.Unmarshal(b, &m)
	if err != nil || m == nil {
		return nil, ErrSchema
	}
	return m, nil
}

func meaningful(b json.RawMessage) bool {
	if len(strings.TrimSpace(string(b))) == 0 {
		return false
	}
	value, valid := DecodeExtra(b)
	return !valid || present(value)
}

func present(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case string:
		return v != ""
	case []any:
		for _, child := range v {
			if present(child) {
				return true
			}
		}
		return false
	case map[string]any:
		for _, child := range v {
			if present(child) {
				return true
			}
		}
		return false
	default:
		return true // Preserve scalar values, including false and zero.
	}
}

func (i Item) HasExtraData() bool {
	return len(i.ExtraKeys()) > 0
}

// ExtraKeys returns the sorted keys of extras holding meaningful data.
func (i Item) ExtraKeys() []string {
	keys := []string{}
	for key, value := range i.Extras {
		if meaningful(value) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func ParseItem(b []byte) (Item, error) {
	var i Item
	root, err := object(b)
	if err != nil {
		return i, err
	}
	m, err := object(root["item"])
	if err != nil {
		return i, err
	}
	d, err := object(m["content"])
	if err != nil {
		return i, err
	}
	c, err := object(d["content"])
	if err != nil {
		return i, err
	}
	login, err := object(c["Login"])
	if err != nil || len(c) != 1 {
		return i, ErrSchema
	}
	read := func(m map[string]json.RawMessage, k string, dst any, required bool) bool {
		b, ok := m[k]
		if !ok {
			return !required
		}
		return json.Unmarshal(b, dst) == nil && string(b) != "null"
	}
	if !read(m, "id", &i.ID, true) || !read(m, "share_id", &i.ShareID, true) || !read(m, "state", &i.State, true) ||
		!read(d, "title", &i.Title, true) || !read(d, "note", &i.Note, true) ||
		!read(login, "username", &i.Username, true) || !read(login, "email", &i.Email, true) ||
		!read(login, "password", &i.Password, true) || !read(login, "urls", &i.URLs, true) ||
		i.ID == "" || i.ShareID == "" || (i.State != "Active" && i.State != "Trashed") {
		return Item{}, ErrSchema
	}
	for name, dst := range map[string]*string{"create_time": &i.CreateTime, "modify_time": &i.ModifyTime} {
		if meaningful(m[name]) && !read(m, name, dst, false) {
			return Item{}, ErrSchema
		}
	}
	i.Extras = make(map[string]json.RawMessage)
	add := func(prefix string, m map[string]json.RawMessage, known ...string) {
		for k, b := range m {
			found := false
			for _, v := range known {
				if k == v {
					found = true
					break
				}
			}
			if !found && meaningful(b) {
				i.Extras[prefix+k] = append(json.RawMessage(nil), b...)
			}
		}
	}
	add("login.", login, "username", "email", "password", "urls")
	add("data.", d, "title", "note", "content", "item_uuid")
	add("detail.", root, "item")
	add("item.", m, "id", "share_id", "vault_id", "content", "state", "flags", "create_time", "modify_time", "folder_id")
	var extras []struct {
		Name string `json:"name"`
	}
	if b, ok := d["extra_fields"]; ok {
		if json.Unmarshal(b, &extras) != nil {
			return Item{}, ErrSchema
		}
		for _, e := range extras {
			i.CustomNames = append(i.CustomNames, strings.ToLower(e.Name))
		}
	}
	i.WebsiteURLs = append([]string(nil), i.URLs...)
	if b, ok := login["autofill_urls"]; ok {
		var urls []struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(b, &urls) != nil {
			return Item{}, ErrSchema
		}
		i.Autofill = len(urls) > 0
		for _, u := range urls {
			i.WebsiteURLs = append(i.WebsiteURLs, u.URL)
		}
	}
	i.Raw = append(json.RawMessage(nil), b...)
	return i, nil
}

// Snapshot ignores read bookkeeping but includes content, state, metadata and attachments.
func (i Item) Snapshot() [32]byte {
	var root map[string]any
	_ = json.Unmarshal(i.Raw, &root)
	if m, ok := root["item"].(map[string]any); ok {
		delete(m, "read_time")
		delete(m, "last_used_time")
	}
	delete(root, "read_time")
	delete(root, "last_used_time")
	b, _ := json.Marshal(root)
	return sha256.Sum256(b)
}

type Group struct {
	ID      string
	Domains []string
	Items   []Item
	// Loaded is false for groups discovered from metadata-only item listings.
	Loaded bool
	// Links includes only domains shared by two or more entries.
	Links map[string][]string
}

func (g Group) StateCounts() (active, trashed int) {
	for _, i := range g.Items {
		switch i.State {
		case "Active":
			active++
		case "Trashed":
			trashed++
		}
	}
	return
}

type Plan struct {
	GroupID            string
	TargetID           string
	KeepNone           bool
	Originals          []Item
	Fields             map[string]string
	TrashIDs           []string
	ExtrasAcknowledged bool
}

// ExtraDataTrashed lists Active entries selected for Trash that hold extra data,
// such as TOTP seeds, passkeys, attachments or custom fields.
func (p Plan) ExtraDataTrashed() []Item {
	selected := make(map[string]bool, len(p.TrashIDs))
	for _, id := range p.TrashIDs {
		selected[id] = true
	}
	out := []Item{}
	for _, i := range p.Originals {
		if i.State == "Active" && selected[i.ID] && i.HasExtraData() {
			out = append(out, i)
		}
	}
	return out
}

func (p Plan) NeedsExtrasAcknowledgment() bool {
	return len(p.ExtraDataTrashed()) > 0
}

// EntryLabels names entries by their position in Originals and their title.
func (p Plan) EntryLabels(items []Item) string {
	labels := []string{}
	for _, i := range items {
		for n, o := range p.Originals {
			if o.ID == i.ID {
				labels = append(labels, fmt.Sprintf("#%d %s", n+1, i.Title))
			}
		}
	}
	return strings.Join(labels, ", ")
}

func (p Plan) Target() Item {
	for _, i := range p.Originals {
		if i.ID == p.TargetID {
			return i
		}
	}
	return Item{}
}

// Result is the target as it will look after the plan's field updates.
// Keep-none plans have no result and return the zero Item.
func (p Plan) Result() Item {
	if p.KeepNone {
		return Item{}
	}
	result := p.Target()
	for field, value := range p.Fields {
		result.Set(field, value)
	}
	return result
}

func EncodeURLs(urls []string) (string, error) {
	for _, u := range urls {
		if strings.Contains(u, ",") || strings.TrimSpace(u) != u || u == "" {
			return "", errors.New("selected URLs cannot be preserved by pass-cli's comma-separated update format")
		}
	}
	return strings.Join(urls, ", "), nil
}

// DecodeURLs reverses EncodeURLs.
func DecodeURLs(v string) []string {
	if v == "" {
		return []string{}
	}
	return strings.Split(v, ", ")
}

func (p Plan) Validate() error {
	if len(p.Originals) == 0 {
		return errors.New("empty collision group")
	}
	target := p.Target()
	if !p.KeepNone && (target.ID == "" || target.State != "Active") {
		return errors.New("choose an active merge target")
	}
	if p.KeepNone && (p.TargetID != "" || len(p.Fields) > 0) {
		return errors.New("keep none cannot include a merge target or field updates")
	}
	share := p.Originals[0].ShareID
	activeCount := 0
	known := map[string]bool{}
	active := map[string]bool{}
	seen := map[string]bool{}
	for _, i := range p.Originals {
		if len(i.Raw) == 0 {
			return errors.New("load all group details before staging changes")
		}
		if i.ID == "" || share == "" || i.ShareID != share || (i.State != "Active" && i.State != "Trashed") || known[i.ID] {
			return errors.New("invalid collision group")
		}
		known[i.ID] = true
		active[i.ID] = i.State == "Active"
		if active[i.ID] {
			activeCount++
		}
	}
	for _, id := range p.TrashIDs {
		if !known[id] || !active[id] || seen[id] || id == target.ID {
			return errors.New("invalid trash selection")
		}
		seen[id] = true
	}
	if p.KeepNone && (activeCount == 0 || len(p.TrashIDs) != activeCount) {
		return errors.New("keep none must select every Active entry for trash")
	}
	for f, v := range p.Fields {
		switch f {
		case "title", "username", "email", "password", "note", "urls":
		default:
			return errors.New("unsupported merge field")
		}
		if strings.ContainsRune(v, 0) {
			return errors.New("pass-cli cannot update a field containing a NUL character")
		}
		if f == "title" && v == "" {
			return errors.New("title cannot be empty")
		}
		if f != "title" && f != "note" {
			for _, name := range target.CustomNames {
				if name == f {
					return errors.New("a custom field shadows a standard field; this change requires another Proton Pass client")
				}
			}
		}
		if f == "urls" {
			if target.Autofill {
				return errors.New("this app does not support merging this entry's URL settings; keep the target's URLs unchanged or edit them in Proton Pass")
			}
			encoded, err := EncodeURLs(DecodeURLs(v))
			if err != nil || encoded != v {
				return errors.New("URL change cannot round-trip through pass-cli")
			}
		}
	}
	if len(p.Fields) == 0 && len(p.TrashIDs) == 0 {
		return errors.New("choose a field change or an entry to trash, or skip this group")
	}
	if !p.ExtrasAcknowledged && p.NeedsExtrasAcknowledgment() {
		return fmt.Errorf("%w: %s", ErrExtrasUnacknowledged, p.EntryLabels(p.ExtraDataTrashed()))
	}
	return nil
}

func SortedFields(fields map[string]string) []string {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func VerifyUpdate(before, after Item, fields map[string]string) error {
	if before.ID != after.ID || before.ShareID != after.ShareID || after.State != "Active" {
		return errors.New("target identity or state changed")
	}
	for _, f := range []string{"title", "username", "email", "password", "note", "urls"} {
		want := before.Value(f)
		if v, ok := fields[f]; ok {
			want = v
		}
		if after.Value(f) != want {
			return fmt.Errorf("target verification failed for %s", f)
		}
	}
	// New CLI versions can generate default autofill rules from legacy URLs.
	extras := func(i Item) map[string]json.RawMessage {
		m := map[string]json.RawMessage{}
		for k, v := range i.Extras {
			m[k] = v
		}
		return m
	}
	a, b := extras(before), extras(after)
	if !before.Autofill && after.Autofill {
		var rules []struct{ URL, Mode string }
		if json.Unmarshal(b["login.autofill_urls"], &rules) == nil && len(rules) == len(after.URLs) {
			ok := true
			for n, r := range rules {
				if r.URL != after.URLs[n] || r.Mode != "Default" {
					ok = false
				}
			}
			if ok {
				delete(b, "login.autofill_urls")
			}
		}
	}
	var normalize = func(m map[string]json.RawMessage) string {
		b, _ := json.Marshal(m)
		var v any
		_ = json.Unmarshal(b, &v)
		b, _ = json.Marshal(v)
		return string(b)
	}
	if normalize(a) != normalize(b) {
		return errors.New("target extra data changed unexpectedly; no source entries were trashed")
	}
	identity := func(i Item) any {
		var r map[string]any
		_ = json.Unmarshal(i.Raw, &r)
		return r["item"].(map[string]any)["content"].(map[string]any)["item_uuid"]
	}
	if !reflect.DeepEqual(identity(before), identity(after)) {
		return errors.New("target item UUID changed unexpectedly")
	}
	// Also verify target metadata is preserved (modify_time necessarily changes).
	meta := func(i Item) map[string]any {
		var r map[string]any
		_ = json.Unmarshal(i.Raw, &r)
		m, _ := r["item"].(map[string]any)
		for _, k := range []string{"content", "modify_time", "read_time", "last_used_time"} {
			delete(m, k)
		}
		return m
	}
	if !reflect.DeepEqual(meta(before), meta(after)) {
		return errors.New("target metadata changed unexpectedly")
	}
	return nil
}
