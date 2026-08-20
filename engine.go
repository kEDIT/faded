package main

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Keyboard maps
// ---------------------------------------------------------------------------

// adjacency lists physical QWERTY neighbours (US layout), lowercase.
var adjacency = map[rune]string{
	'q': "wa", 'w': "qeasd", 'e': "wrsdf", 'r': "etdfg", 't': "ryfgh",
	'y': "tughj", 'u': "yihjk", 'i': "uojkl", 'o': "ipkl", 'p': "ol",
	'a': "qwsz", 's': "qweadzx", 'd': "wersfxc", 'f': "ertdgcv",
	'g': "rtyfhvb", 'h': "tyugjbn", 'j': "yuihknm", 'k': "uiojlm",
	'l': "iopk", 'z': "asx", 'x': "zsdc", 'c': "xdfv", 'v': "cfgb",
	'b': "vghn", 'n': "bhjm", 'm': "njk",
	'1': "2q", '2': "13qw", '3': "24we", '4': "35er", '5': "46rt",
	'6': "57ty", '7': "68yu", '8': "79ui", '9': "80io", '0': "9op",
}

// shiftPairs maps a character to what you get with Shift toggled (both ways).
var shiftPairs = buildShiftPairs()

func buildShiftPairs() map[rune]rune {
	base := map[rune]rune{
		'1': '!', '2': '@', '3': '#', '4': '$', '5': '%', '6': '^', '7': '&',
		'8': '*', '9': '(', '0': ')', '-': '_', '=': '+', ';': ':', '\'': '"',
		',': '<', '.': '>', '/': '?', '`': '~',
	}
	m := make(map[rune]rune, len(base)*2)
	for k, v := range base {
		m[k] = v
		m[v] = k
	}
	return m
}

// leetMap holds the common letter->symbol substitutions.
var leetMap = map[rune]rune{
	'a': '@', 'e': '3', 'i': '1', 'o': '0', 's': '$', 't': '7', 'l': '1',
}

// ---------------------------------------------------------------------------
// Typo families
// ---------------------------------------------------------------------------

// typoFamilies is the set of short-names (the part after "typo:").
var typoFamilies = map[string]bool{
	"capslock": true, "shift-first": true, "shift-last": true,
	"transpose": true, "shift-symbol": true, "drop": true, "double": true,
	"adjacent": true, "trailing-space": true, "leading-space": true,
}

func typoFamilyList() []string {
	out := make([]string, 0, len(typoFamilies))
	for k := range typoFamilies {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// shortLabel turns "typo:transpose" into "transpose".
func shortLabel(label string) string {
	if i := strings.IndexByte(label, ':'); i >= 0 {
		return label[i+1:]
	}
	return label
}

func swapCaseRune(r rune) rune {
	switch {
	case unicode.IsUpper(r):
		return unicode.ToLower(r)
	case unicode.IsLower(r):
		return unicode.ToUpper(r)
	default:
		return r
	}
}

func swapCase(s string) string {
	rs := []rune(s)
	for i, r := range rs {
		rs[i] = swapCaseRune(r)
	}
	return string(rs)
}

// adjacentKeys returns neighbour keys for a rune, preserving case.
func adjacentKeys(r rune) []rune {
	low := unicode.ToLower(r)
	neigh, ok := adjacency[low]
	if !ok {
		return nil
	}
	upper := unicode.IsUpper(r)
	out := make([]rune, 0, len(neigh))
	for _, n := range neigh {
		if upper {
			out = append(out, unicode.ToUpper(n))
		} else {
			out = append(out, n)
		}
	}
	return out
}

type typo struct {
	cand  string
	label string
	cost  float64
}

// typoVariants yields single-slip typos of pw, each tagged with a family label.
func typoVariants(pw string) []typo {
	rs := []rune(pw)
	n := len(rs)
	var out []typo
	seen := make(map[string]bool)

	add := func(cand, label string, cost float64) {
		if cand == "" || cand == pw {
			return
		}
		key := label + "\x00" + cand
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, typo{cand, label, cost})
	}

	// caps lock: whole-string case inversion
	add(swapCase(pw), "typo:capslock", 1.0)

	if n > 0 {
		add(string(swapCaseRune(rs[0]))+string(rs[1:]), "typo:shift-first", 1.2)
		add(string(rs[:n-1])+string(swapCaseRune(rs[n-1])), "typo:shift-last", 1.5)
	}

	// adjacent transposition
	for i := 0; i < n-1; i++ {
		swapped := make([]rune, n)
		copy(swapped, rs)
		swapped[i], swapped[i+1] = swapped[i+1], swapped[i]
		add(string(swapped), "typo:transpose", 1.8)
	}

	// number/symbol shift confusion
	for i, r := range rs {
		if alt, ok := shiftPairs[r]; ok {
			cand := string(rs[:i]) + string(alt) + string(rs[i+1:])
			add(cand, "typo:shift-symbol", 2.0)
		}
	}

	// dropped character
	for i := 0; i < n; i++ {
		add(string(rs[:i])+string(rs[i+1:]), "typo:drop", 2.4)
	}

	// doubled character
	for i, r := range rs {
		add(string(rs[:i+1])+string(r)+string(rs[i+1:]), "typo:double", 2.6)
	}

	// adjacent-key substitution
	for i, r := range rs {
		for _, nb := range adjacentKeys(r) {
			cand := string(rs[:i]) + string(nb) + string(rs[i+1:])
			add(cand, "typo:adjacent", 3.0)
		}
	}

	// stray whitespace
	add(pw+" ", "typo:trailing-space", 3.4)
	add(" "+pw, "typo:leading-space", 3.5)

	return out
}

// ---------------------------------------------------------------------------
// Stacked typos (multi-slip)
// ---------------------------------------------------------------------------

type stackNode struct {
	cost   float64
	labels []string
}

type stackResult struct {
	cand   string
	labels []string
	cost   float64
}

// stackedTypos applies up to depth typos in sequence. A beam limit keeps only
// the cheapest `beam` intermediates between layers so the count stays bounded.
// families is a set of allowed short-names, or nil for all.
func stackedTypos(base string, depth int, families map[string]bool, beam int) []stackResult {
	if depth < 1 {
		depth = 1
	}
	frontier := map[string]stackNode{base: {0, nil}}
	reached := map[string]stackNode{}

	for d := 0; d < depth; d++ {
		next := map[string]stackNode{}
		for cand, nd := range frontier {
			for _, t := range typoVariants(cand) {
				if families != nil && !families[shortLabel(t.label)] {
					continue
				}
				if t.cand == base {
					continue
				}
				nc := nd.cost + t.cost
				if prev, ok := next[t.cand]; !ok || nc < prev.cost {
					lbls := make([]string, 0, len(nd.labels)+1)
					lbls = append(lbls, nd.labels...)
					lbls = append(lbls, t.label)
					next[t.cand] = stackNode{nc, lbls}
				}
			}
		}
		for c2, nd := range next {
			if prev, ok := reached[c2]; !ok || nd.cost < prev.cost {
				reached[c2] = nd
			}
		}
		frontier = topNodes(next, beam)
		if len(frontier) == 0 {
			break
		}
	}

	out := make([]stackResult, 0, len(reached))
	for c, nd := range reached {
		out = append(out, stackResult{c, nd.labels, nd.cost})
	}
	return out
}

// topNodes deterministically keeps the `beam` cheapest entries (ties by key).
func topNodes(m map[string]stackNode, beam int) map[string]stackNode {
	type kv struct {
		k string
		v stackNode
	}
	items := make([]kv, 0, len(m))
	for k, v := range m {
		items = append(items, kv{k, v})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v.cost != items[j].v.cost {
			return items[i].v.cost < items[j].v.cost
		}
		return items[i].k < items[j].k
	})
	if beam > 0 && len(items) > beam {
		items = items[:beam]
	}
	out := make(map[string]stackNode, len(items))
	for _, it := range items {
		out[it.k] = it.v
	}
	return out
}

// ---------------------------------------------------------------------------
// Profiles
// ---------------------------------------------------------------------------

type profile struct {
	depth       int
	families    map[string]bool // nil == all
	leet        bool
	affixes     bool
	beam        int
	cap         int
	slipPenalty float64
}

func familiesExcept(excluded ...string) map[string]bool {
	skip := make(map[string]bool)
	for _, e := range excluded {
		skip[e] = true
	}
	out := make(map[string]bool)
	for k := range typoFamilies {
		if !skip[k] {
			out[k] = true
		}
	}
	return out
}

func profiles() map[string]profile {
	noAdj := familiesExcept("adjacent")
	return map[string]profile{
		"conservative": {depth: 1, families: noAdj, leet: true, affixes: true, beam: 40, cap: 200, slipPenalty: 1.6},
		"balanced":     {depth: 1, families: nil, leet: true, affixes: true, beam: 40, cap: 800, slipPenalty: 1.5},
		"aggressive":   {depth: 2, families: noAdj, leet: true, affixes: true, beam: 30, cap: 2500, slipPenalty: 1.6},
		"kitchen-sink": {depth: 2, families: nil, leet: true, affixes: true, beam: 45, cap: 6000, slipPenalty: 1.4},
	}
}

func profileNames() []string {
	names := make([]string, 0)
	for k := range profiles() {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// orderedProfiles lists presets from least to most aggressive, for display.
func orderedProfiles() []string {
	return []string{"conservative", "balanced", "aggressive", "kitchen-sink"}
}

// ---------------------------------------------------------------------------
// Recombination
// ---------------------------------------------------------------------------

type sepCost struct {
	sep  string
	cost float64
}

var separators = []sepCost{
	{"", 0.0}, {"-", 0.1}, {"_", 0.1}, {".", 0.2}, {"!", 0.3},
}

type affix struct {
	text  string
	where string // "suf" or "pre"
	cost  float64
}

var affixes = []affix{
	{"!", "suf", 0.2}, {"1", "suf", 0.3}, {"123", "suf", 0.4},
	{"2026", "suf", 0.3}, {"2025", "suf", 0.4}, {"2024", "suf", 0.5},
	{"@", "suf", 0.4}, {"#", "suf", 0.4}, {"!", "pre", 0.5},
}

type scored struct {
	text string
	cost float64
}

// leetVariants returns the word unchanged, fully leeted, and each single swap.
func leetVariants(word string) []scored {
	rs := []rune(word)
	out := []scored{{word, 0.0}}
	seen := map[string]bool{word: true}

	// full leet
	full := make([]rune, len(rs))
	applicable := 0
	for i, r := range rs {
		if sub, ok := leetMap[unicode.ToLower(r)]; ok {
			full[i] = sub
			applicable++
		} else {
			full[i] = r
		}
	}
	if fs := string(full); fs != word && !seen[fs] {
		n := applicable
		if n > 4 {
			n = 4
		}
		out = append(out, scored{fs, 0.25 * float64(n)})
		seen[fs] = true
	}

	// single-char leet
	for i, r := range rs {
		if sub, ok := leetMap[unicode.ToLower(r)]; ok {
			cand := string(rs[:i]) + string(sub) + string(rs[i+1:])
			if !seen[cand] {
				out = append(out, scored{cand, 0.25})
				seen[cand] = true
			}
		}
	}
	return out
}

// permutations returns all ordered k-length arrangements of items.
func permutations(items []string, k int) [][]string {
	var result [][]string
	n := len(items)
	if k <= 0 || k > n {
		return result
	}
	used := make([]bool, n)
	cur := make([]string, 0, k)
	var rec func()
	rec = func() {
		if len(cur) == k {
			cp := make([]string, k)
			copy(cp, cur)
			result = append(result, cp)
			return
		}
		for i := 0; i < n; i++ {
			if used[i] {
				continue
			}
			used[i] = true
			cur = append(cur, items[i])
			rec()
			cur = cur[:len(cur)-1]
			used[i] = false
		}
	}
	rec()
	return result
}

// recombine orders substrings and joins them with separators.
func recombine(substrings []string, maxParts int) []scored {
	var subs []string
	for _, s := range substrings {
		if s != "" {
			subs = append(subs, s)
		}
	}
	var results []scored
	upto := maxParts
	if len(subs) < upto {
		upto = len(subs)
	}
	for k := 1; k <= upto; k++ {
		for _, combo := range permutations(subs, k) {
			for _, sc := range separators {
				joined := strings.Join(combo, sc.sep)
				baseCost := 2.0 + 0.3*float64(k-1) + sc.cost
				results = append(results, scored{joined, baseCost})
			}
		}
	}
	return results
}

// ---------------------------------------------------------------------------
// Candidate assembly
// ---------------------------------------------------------------------------

type buildMeta struct {
	score      float64
	strategies map[string]bool
}

type genConfig struct {
	addTypos    bool
	addAffixes  bool
	addLeet     bool
	cap         int
	typoDepth   int
	typoFams    map[string]bool
	slipPenalty float64
	beam        int
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }

// buildCandidates produces the ranked candidate set. Lower score == more likely.
func buildCandidates(attempts, substrings []string, cfg genConfig) map[string]*buildMeta {
	pool := make(map[string]*buildMeta)

	register := func(cand string, score float64, strategy string) {
		if cand == "" || utf8.RuneCountInString(cand) > 128 {
			return
		}
		e, ok := pool[cand]
		if !ok {
			pool[cand] = &buildMeta{score: score, strategies: map[string]bool{strategy: true}}
			return
		}
		if score < e.score {
			e.score = score
		}
		e.strategies[strategy] = true
	}

	// 0) verbatim attempts
	for _, a := range attempts {
		register(a, 0.5, "attempt-verbatim")
	}

	// 1) recombinations of building blocks
	bases := recombine(substrings, 3)
	for _, b := range bases {
		register(b.text, b.cost, "recombine")
	}

	// bases for the layers below: attempts + recombinations
	layerBases := make([]scored, 0, len(attempts)+len(bases))
	for _, a := range attempts {
		layerBases = append(layerBases, scored{a, 0.5})
	}
	layerBases = append(layerBases, bases...)

	// 2) leet on bases
	if cfg.addLeet {
		for _, b := range layerBases {
			for _, lv := range leetVariants(b.text) {
				if lv.text != b.text {
					register(lv.text, b.cost+lv.cost+0.2, "leet")
				}
			}
		}
	}

	// 3) affixes on bases
	if cfg.addAffixes {
		for _, b := range layerBases {
			for _, af := range affixes {
				var cand string
				if af.where == "pre" {
					cand = af.text + b.text
				} else {
					cand = b.text + af.text
				}
				register(cand, b.cost+af.cost, "affix")
			}
		}
	}

	// 4) fat-finger typos (stacked when depth >= 2)
	if cfg.addTypos && cfg.typoDepth >= 1 {
		typoTargets := append([]string{}, attempts...)
		extraLimit := 20
		if cfg.typoDepth >= 2 {
			extraLimit = 10
		}
		var extra []string
		for _, b := range bases {
			if b.cost <= 2.6 {
				extra = append(extra, b.text)
			}
		}
		if len(extra) > extraLimit {
			extra = extra[:extraLimit]
		}
		typoTargets = append(typoTargets, extra...)

		for _, base := range typoTargets {
			for _, sr := range stackedTypos(base, cfg.typoDepth, cfg.typoFams, cfg.beam) {
				slips := len(sr.labels)
				score := 1.0 + sr.cost*0.4 + cfg.slipPenalty*float64(slips-1)
				shorts := make([]string, len(sr.labels))
				for i, l := range sr.labels {
					shorts[i] = shortLabel(l)
				}
				var tag string
				if slips == 1 {
					tag = "typo:" + shorts[0]
				} else {
					tag = "typo" + itoa(slips) + ":" + strings.Join(shorts, "+")
				}
				register(sr.cand, score, tag)
			}
		}
	}

	// 5) gentle boost for candidates near a remembered attempt
	for cand, e := range pool {
		best := 99
		for _, a := range attempts {
			if d := levenshtein(cand, a); d < best {
				best = d
			}
		}
		if best == 1 {
			e.score -= 0.3
		} else if best == 2 {
			e.score -= 0.1
		}
	}

	// keep the most promising `cap` (deterministic: score then candidate)
	type entry struct {
		cand string
		meta *buildMeta
	}
	all := make([]entry, 0, len(pool))
	for c, m := range pool {
		all = append(all, entry{c, m})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].meta.score != all[j].meta.score {
			return all[i].meta.score < all[j].meta.score
		}
		return all[i].cand < all[j].cand
	})
	if cfg.cap > 0 && len(all) > cfg.cap {
		all = all[:cfg.cap]
	}
	out := make(map[string]*buildMeta, len(all))
	for _, e := range all {
		out[e.cand] = e.meta
	}
	return out
}

// ---------------------------------------------------------------------------
// Substring mining
// ---------------------------------------------------------------------------

// mineSubstrings finds fragments recurring across >= minCount attempts.
func mineSubstrings(attempts []string, minLen, minCount, maxKeep int) []string {
	counts := make(map[string]int)
	for _, pw := range attempts {
		rs := []rune(pw)
		n := len(rs)
		seenHere := make(map[string]bool)
		for i := 0; i < n; i++ {
			for j := i + minLen; j <= n; j++ {
				sub := string(rs[i:j])
				if !seenHere[sub] {
					seenHere[sub] = true
					counts[sub]++
				}
			}
		}
	}

	var frequent []string
	for s, c := range counts {
		if c >= minCount {
			frequent = append(frequent, s)
		}
	}
	// longest first, then lexicographic for determinism
	sort.Slice(frequent, func(i, j int) bool {
		li, lj := utf8.RuneCountInString(frequent[i]), utf8.RuneCountInString(frequent[j])
		if li != lj {
			return li > lj
		}
		return frequent[i] < frequent[j]
	})

	var maximal []string
	for _, s := range frequent {
		contained := false
		for _, bigger := range maximal {
			if s != bigger && strings.Contains(bigger, s) {
				contained = true
				break
			}
		}
		if !contained {
			maximal = append(maximal, s)
		}
		if len(maximal) >= maxKeep {
			break
		}
	}
	return maximal
}

// ---------------------------------------------------------------------------
// Small helpers
// ---------------------------------------------------------------------------

func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if a == b {
		return 0
	}
	if len(ra) == 0 {
		return len(rb)
	}
	if len(rb) == 0 {
		return len(ra)
	}
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			sub := prev[j-1]
			if ra[i-1] != rb[j-1] {
				sub++
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, sub)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// itoa avoids importing strconv in a couple of hot spots.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
