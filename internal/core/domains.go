package core

import (
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

func Domain(value string) string {
	value = strings.TrimSpace(value)
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if net.ParseIP(host) != nil {
		return ""
	}
	host, err = idna.Lookup.ToASCII(host)
	if err != nil || len(host) > 253 || !strings.Contains(host, ".") {
		return ""
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return ""
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return ""
			}
		}
	}
	suffix, _ := publicsuffix.PublicSuffix(host)
	// Reject unknown/local suffixes, while preserving private suffix boundaries.
	_, icann := publicsuffix.PublicSuffix(suffix)
	if !icann && !strings.Contains(suffix, ".") {
		return ""
	}
	d, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return ""
	}
	return d
}

// Match complete domain/URL candidates rather than just the last whitespace token.
// Domain still validates the host and its public suffix. Boundaries prevent taking
// a domain from the middle of an email address, invalid host or non-web URL.
var titleWebsite = regexp.MustCompile(`(?i)(?:https?://)?(?:[\p{L}\p{N}][\p{L}\p{N}\p{M}-]*[.。．｡])+[\p{L}][\p{L}\p{N}\p{M}-]*(?::[0-9]+)?(?:[/?#][^\s<>"“”]*)?`)

func TitleDomains(title string) []string {
	set := map[string]bool{}
	for _, loc := range titleWebsite.FindAllStringIndex(title, -1) {
		if loc[0] > 0 {
			r, _ := utf8.DecodeLastRuneInString(title[:loc[0]])
			if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || strings.ContainsRune("@._/-", r) {
				continue
			}
		}
		if loc[1] < len(title) {
			r, _ := utf8.DecodeRuneInString(title[loc[1]:])
			if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) || strings.ContainsRune("@_:-", r) {
				continue
			}
		}
		if d := Domain(title[loc[0]:loc[1]]); d != "" {
			set[d] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func TitleDomain(title string) string {
	if ds := TitleDomains(title); len(ds) > 0 {
		return ds[0]
	}
	return ""
}

func Domains(i Item) []string {
	set := map[string]bool{}
	for _, d := range TitleDomains(i.Title) {
		set[d] = true
	}
	for _, u := range i.WebsiteURLs {
		if d := Domain(u); d != "" {
			set[d] = true
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func Collisions(items []Item) []Group {
	items = append([]Item(nil), items...)
	sort.Slice(items, func(a, b int) bool {
		if items[a].State != items[b].State {
			return items[a].State == "Active"
		}
		if items[a].Title == items[b].Title {
			return items[a].ID < items[b].ID
		}
		return items[a].Title < items[b].Title
	})
	parent := make([]int, len(items))
	for n := range parent {
		parent[n] = n
	}
	var root func(int) int
	root = func(n int) int {
		if parent[n] != n {
			parent[n] = root(parent[n])
		}
		return parent[n]
	}
	byDomain := map[string][]int{}
	for n, i := range items {
		for _, d := range Domains(i) {
			if prev := byDomain[d]; len(prev) > 0 {
				parent[root(n)] = root(prev[0])
			}
			byDomain[d] = append(byDomain[d], n)
		}
	}
	groups := map[int]*Group{}
	for n, i := range items {
		r := root(n)
		if groups[r] == nil {
			groups[r] = &Group{Links: map[string][]string{}}
		}
		groups[r].Items = append(groups[r].Items, i)
	}
	for d, ns := range byDomain {
		if len(ns) < 2 {
			continue
		}
		g := groups[root(ns[0])]
		g.Domains = append(g.Domains, d)
		for _, n := range ns {
			g.Links[d] = append(g.Links[d], items[n].ID)
		}
	}
	out := []Group{}
	for _, g := range groups {
		if len(g.Items) < 2 {
			continue
		}
		sort.Strings(g.Domains)
		ids := []string{}
		for _, i := range g.Items {
			ids = append(ids, i.ID)
		}
		sort.Strings(ids)
		g.ID = strings.Join(ids, "\x00")
		g.Loaded = true
		for _, i := range g.Items {
			if len(i.Raw) == 0 {
				g.Loaded = false
				break
			}
		}
		out = append(out, *g)
	}
	sort.Slice(out, func(a, b int) bool {
		priority := func(g Group) int {
			active, _ := g.StateCounts()
			if active >= 2 {
				return 0
			}
			if active == 1 {
				return 1
			}
			return 2
		}
		if pa, pb := priority(out[a]), priority(out[b]); pa != pb {
			return pa < pb
		}
		x, y := strings.Join(out[a].Domains, ", "), strings.Join(out[b].Domains, ", ")
		if x == y {
			return out[a].ID < out[b].ID
		}
		return x < y
	})
	return out
}
