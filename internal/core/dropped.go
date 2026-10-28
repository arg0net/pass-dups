package core

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"reflect"
	"strings"
)

// DecodeExtra decodes one extra value, keeping numbers exact as json.Number.
func DecodeExtra(raw json.RawMessage) (any, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, false
	}
	return value, true
}

// retained reports whether every meaningful part of source also appears in kept.
// Objects match as subsets, list elements may appear anywhere in the kept list,
// and numbers compare by value rather than spelling.
func retained(source, kept any) bool {
	if !present(source) {
		return true
	}
	switch s := source.(type) {
	case map[string]any:
		k, ok := kept.(map[string]any)
		if !ok {
			return false
		}
		for key, value := range s {
			if !retained(value, k[key]) {
				return false
			}
		}
		return true
	case []any:
		k, ok := kept.([]any)
		if !ok {
			return false
		}
		for _, element := range s {
			if !containsElement(k, element) {
				return false
			}
		}
		return true
	case json.Number:
		k, ok := kept.(json.Number)
		if !ok {
			return false
		}
		a, okA := new(big.Rat).SetString(string(s))
		b, okB := new(big.Rat).SetString(string(k))
		return okA && okB && a.Cmp(b) == 0
	}
	return reflect.DeepEqual(source, kept)
}

func containsElement(list []any, element any) bool {
	if !present(element) {
		return true
	}
	for _, candidate := range list {
		if retained(element, candidate) {
			return true
		}
	}
	return false
}

// noteRetained matches whole note blocks, which merged notes join with a blank line.
func noteRetained(note, value string) bool {
	return strings.Contains("\n\n"+note+"\n\n", "\n\n"+value+"\n\n")
}

// DroppedEntry describes data that will not remain in any kept Active entry.
// Its values contain secrets; never log this structure.
type DroppedEntry struct {
	ID, Title string
	Index     int
	Trashed   bool
	Fields    map[string]string
	URLs      []string
	Extras    map[string]json.RawMessage
}

func (p Plan) DroppedData() []DroppedEntry {
	return DroppedData(p.Originals, p.TrashIDs, p.Result())
}

// DroppedData accepts the full merged result so live drafts can be inspected
// before URL encoding or other staging validation. A zero result means keep none.
func DroppedData(originals []Item, trashIDs []string, result Item) []DroppedEntry {
	selected := make(map[string]bool, len(trashIDs))
	for _, id := range trashIDs {
		selected[id] = true
	}
	kept := []Item{}
	for _, item := range originals {
		if item.State != "Active" || selected[item.ID] {
			continue
		}
		if item.ID == result.ID {
			item = result
		}
		kept = append(kept, item)
	}
	urls := map[string]bool{}
	for _, item := range kept {
		for _, u := range item.URLs {
			urls[u] = true
		}
		var rules []struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(item.Extras["login.autofill_urls"], &rules) == nil {
			for _, r := range rules {
				urls[r.URL] = true
			}
		}
	}
	// Each kept extra is decoded at most once per call.
	keptExtras := map[string][]any{}
	available := func(key string) []any {
		values, ok := keptExtras[key]
		if !ok {
			values = []any{}
			for _, item := range kept {
				if value, valid := DecodeExtra(item.Extras[key]); valid {
					values = append(values, value)
				}
			}
			keptExtras[key] = values
		}
		return values
	}
	out := []DroppedEntry{}
	for index, item := range originals {
		if item.State != "Active" || (!selected[item.ID] && item.ID != result.ID) {
			continue
		}
		entry := DroppedEntry{ID: item.ID, Title: item.Title, Index: index, Trashed: selected[item.ID], Fields: map[string]string{}, Extras: map[string]json.RawMessage{}}
		for _, field := range []string{"username", "email", "password", "note"} {
			value := item.Value(field)
			if value == "" {
				continue
			}
			found := false
			for _, other := range kept {
				if other.Value(field) == value || (field == "note" && noteRetained(other.Note, value)) {
					found = true
					break
				}
			}
			if !found {
				entry.Fields[field] = value
			}
		}
		seen := map[string]bool{}
		for _, u := range item.URLs {
			if u != "" && !urls[u] && !seen[u] {
				entry.URLs = append(entry.URLs, u)
				seen[u] = true
			}
		}
		for key, raw := range item.Extras {
			if !meaningful(raw) {
				continue
			}
			source, valid := DecodeExtra(raw)
			if !valid {
				entry.Extras[key] = raw
				continue
			}
			if list, ok := source.([]any); ok {
				missing := []any{}
				for _, element := range list {
					if !present(element) {
						continue
					}
					found := false
					for _, value := range available(key) {
						if values, ok := value.([]any); ok && containsElement(values, element) {
							found = true
							break
						}
					}
					if !found {
						missing = append(missing, element)
					}
				}
				if len(missing) > 0 {
					entry.Extras[key], _ = json.Marshal(missing)
				}
				continue
			}
			found := false
			for _, value := range available(key) {
				if retained(source, value) {
					found = true
					break
				}
			}
			if !found {
				entry.Extras[key] = raw
			}
		}
		if len(entry.Fields)+len(entry.URLs)+len(entry.Extras) > 0 {
			out = append(out, entry)
		}
	}
	return out
}
