package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Reasonable defaults: with these files present, bare commands just work.
const (
	defaultAttemptsFile = "attempts.txt"
	defaultSubsFile     = "subs.txt"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]
	args := os.Args[2:]

	var err error
	switch cmd {
	case "gen":
		err = cmdGen(args)
	case "session":
		err = cmdSession(args)
	case "tui":
		err = cmdTUI(args)
	case "compare":
		err = cmdCompare(args)
	case "next":
		err = cmdNext(args)
	case "mark":
		err = cmdMark(args)
	case "status":
		err = cmdStatus(args)
	case "forget":
		err = cmdForget(args)
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `faded — recover your own password from fragments you half-remember

Usage:
  faded tui     [--attempts FILE] [--subs FILE] [--profile NAME]
  faded compare [--attempts FILE] [--subs FILE] [--top N]
  faded gen     [--attempts FILE] [--subs FILE] [--profile NAME] [options]
  faded session [--attempts FILE] [--subs FILE] [--profile NAME] [--load FILE]
  faded next    [N]
  faded mark    PASSWORD (worked|failed)
  faded status
  faded forget  FILE [FILE ...]

For a full-screen interactive UI, run "faded tui". Like the session, it keeps
everything in memory and writes nothing to disk.

With no flags, faded looks for %q and %q in the current directory, so the
common case is just:
  faded compare        # see how the profiles differ, pick one
  faded gen            # generate and save ranked guesses
  faded next 15        # print the next batch to try
  faded mark 'guess' failed

Profiles: %s
Run "faded gen -h" for the full list of generation options.

State file defaults to $FADED_DIR or the OS temp dir (%s).
`, defaultAttemptsFile, defaultSubsFile, strings.Join(orderedProfiles(), ", "), os.TempDir())
}

// addCommonFlags registers --state and --dir on a flag set.
func addCommonFlags(fs *flag.FlagSet, state, dir *string) {
	fs.StringVar(state, "state", "", "explicit state file path (overrides --dir)")
	fs.StringVar(dir, "dir", "", "directory for the state file (default: $FADED_DIR or OS temp dir)")
}

// ---------------------------------------------------------------------------
// shared input loading
// ---------------------------------------------------------------------------

// gatherInputs reads attempts (with a friendly error if missing), loads subs if
// present, mines recurring substrings, and returns the merged set.
func gatherInputs(attemptsPath, subsPath string, subList []string) (attempts, substrings, mined, explicit []string, err error) {
	attempts, err = readLines(attemptsPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = fmt.Errorf("no attempts file at %q.\n"+
				"Create it with one near-miss guess per line, or pass --attempts FILE.\n"+
				"  e.g.  printf 'MyGuess1\\nMyGuess2\\n' > %s",
				attemptsPath, attemptsPath)
		}
		return
	}
	if len(attempts) == 0 {
		err = fmt.Errorf("attempts file %q is empty", attemptsPath)
		return
	}

	explicit = append(explicit, subList...)
	if subsPath != "" {
		if _, statErr := os.Stat(subsPath); statErr == nil {
			lines, e := readLines(subsPath)
			if e != nil {
				err = e
				return
			}
			explicit = append(explicit, lines...)
		}
	}
	mined = mineSubstrings(attempts, 3, 2, 12)
	substrings = dedup(append(append([]string{}, explicit...), mined...))
	return
}

func cfgFromProfile(p profile) genConfig {
	return genConfig{
		addTypos:    p.depth >= 1,
		addAffixes:  p.affixes,
		addLeet:     p.leet,
		cap:         p.cap,
		typoDepth:   p.depth,
		typoFams:    p.families,
		slipPenalty: p.slipPenalty,
		beam:        p.beam,
	}
}

// ---------------------------------------------------------------------------
// compare
// ---------------------------------------------------------------------------

func cmdCompare(args []string) error {
	fs := flag.NewFlagSet("compare", flag.ContinueOnError)
	var (
		attempts, subs string
		subList        multiFlag
		top            int
	)
	fs.StringVar(&attempts, "attempts", defaultAttemptsFile, "file of near-miss guesses (one per line)")
	fs.StringVar(&subs, "subs", defaultSubsFile, "optional file of known building-block substrings")
	fs.Var(&subList, "sub", "an explicit substring (repeatable)")
	fs.IntVar(&top, "top", 3, "how many sample top guesses to show per profile")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: faded compare [--attempts FILE] [--subs FILE] [--top N]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	attemptLines, substrings, _, _, err := gatherInputs(attempts, subs, subList)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Comparing %d profiles on %d attempts (substrings: %s).\n\n",
		len(orderedProfiles()), len(attemptLines), fmtList(substrings))
	fmt.Printf("%-14s %5s %11s   %s\n", "Profile", "Depth", "Candidates", "Sample top guesses")
	fmt.Printf("%-14s %5s %11s   %s\n", strings.Repeat("-", 14), "-----", "----------", "------------------")
	for _, name := range orderedProfiles() {
		p := profiles()[name]
		cands := buildCandidates(attemptLines, substrings, cfgFromProfile(p))
		samples := topCandidates(cands, top)
		fmt.Printf("%-14s %5d %11d   %s\n", name, p.depth, len(cands), strings.Join(samples, ", "))
	}
	fmt.Fprintln(os.Stderr, "\nTip: try the sample guesses from `conservative` first, then widen with")
	fmt.Fprintln(os.Stderr, "`faded gen --profile balanced` (or higher) if none land.")
	return nil
}

// ---------------------------------------------------------------------------
// gen
// ---------------------------------------------------------------------------

func cmdGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	var (
		attempts, subs, state, dir string
		subList                    multiFlag
		top                        int
		profileName                string
		depth, beam, capFlag       int
		slipPenalty                float64
		typos                      string
		noTypos, noLeet, noAffixes bool
		dryRun, fresh              bool
	)
	fs.StringVar(&attempts, "attempts", defaultAttemptsFile, "file of near-miss guesses (one per line)")
	fs.StringVar(&subs, "subs", defaultSubsFile, "optional file of known building-block substrings")
	fs.Var(&subList, "sub", "an explicit substring (repeatable)")
	fs.IntVar(&top, "top", 25, "how many candidates to print now")
	fs.StringVar(&profileName, "profile", "balanced", "aggressiveness preset: "+strings.Join(orderedProfiles(), ", "))
	fs.IntVar(&depth, "depth", 0, "max simultaneous typo slips (overrides profile)")
	fs.IntVar(&beam, "beam", 0, "beam width between typo layers (overrides profile)")
	fs.IntVar(&capFlag, "cap", 0, "max candidates to keep (overrides profile)")
	fs.Float64Var(&slipPenalty, "slip-penalty", 0, "score penalty per extra stacked slip (overrides profile)")
	fs.StringVar(&typos, "typos", "", "comma list of typo families to allow, or 'all'")
	fs.BoolVar(&noTypos, "no-typos", false, "disable typo generation entirely")
	fs.BoolVar(&noLeet, "no-leet", false, "disable leet substitutions")
	fs.BoolVar(&noAffixes, "no-affixes", false, "disable prefix/suffix affixes")
	fs.BoolVar(&dryRun, "dry-run", false, "preview counts/top list without writing state")
	fs.BoolVar(&fresh, "fresh", false, "clear previously stored candidates first")
	addCommonFlags(fs, &state, &dir)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: faded gen [--attempts FILE] [options]\n\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nTypo families: %s\n", strings.Join(typoFamilyList(), ", "))
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	provided := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { provided[f.Name] = true })

	prof, ok := profiles()[profileName]
	if !ok {
		return fmt.Errorf("unknown profile %q (choices: %s)", profileName, strings.Join(orderedProfiles(), ", "))
	}

	// apply per-knob overrides on top of the profile
	if provided["depth"] {
		prof.depth = depth
	}
	if provided["beam"] {
		prof.beam = beam
	}
	if provided["cap"] {
		prof.cap = capFlag
	}
	if provided["slip-penalty"] {
		prof.slipPenalty = slipPenalty
	}
	if noTypos {
		prof.depth = 0
	}
	if noLeet {
		prof.leet = false
	}
	if noAffixes {
		prof.affixes = false
	}
	if provided["typos"] {
		if strings.EqualFold(strings.TrimSpace(typos), "all") {
			prof.families = nil
		} else {
			chosen := map[string]bool{}
			var bad []string
			for _, t := range strings.Split(typos, ",") {
				t = strings.TrimSpace(t)
				if t == "" {
					continue
				}
				if !typoFamilies[t] {
					bad = append(bad, t)
				} else {
					chosen[t] = true
				}
			}
			if len(bad) > 0 {
				sort.Strings(bad)
				return fmt.Errorf("unknown typo families: %s\nvalid: %s",
					strings.Join(bad, ","), strings.Join(typoFamilyList(), ", "))
			}
			prof.families = chosen
		}
	}

	attemptLines, substrings, mined, explicit, err := gatherInputs(attempts, subs, subList)
	if err != nil {
		return err
	}

	famDesc := "all"
	if prof.families != nil {
		keys := make([]string, 0, len(prof.families))
		for k := range prof.families {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		famDesc = strings.Join(keys, ",")
	}
	fmt.Fprintf(os.Stderr, "Profile    : %s\n", profileName)
	fmt.Fprintf(os.Stderr, "Effective  : depth=%d beam=%d cap=%d slip_penalty=%.2f leet=%t affixes=%t\n",
		prof.depth, prof.beam, prof.cap, prof.slipPenalty, prof.leet, prof.affixes)
	fmt.Fprintf(os.Stderr, "Typo set   : %s\n", famDesc)
	fmt.Fprintf(os.Stderr, "Substrings : mined=%s", fmtList(mined))
	if len(explicit) > 0 {
		fmt.Fprintf(os.Stderr, "  explicit=%s", fmtList(explicit))
	}
	fmt.Fprintln(os.Stderr)

	candidates := buildCandidates(attemptLines, substrings, cfgFromProfile(prof))

	famCounts := map[string]int{}
	for _, m := range candidates {
		for s := range m.strategies {
			famCounts[s]++
		}
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "\n[dry-run] %d candidates generated; state NOT modified.\n", len(candidates))
		printTopCandidates(candidates, top)
		fmt.Fprintln(os.Stderr, "\nBy strategy:")
		printStrategyCounts(famCounts)
		return nil
	}

	statePath := resolveStatePath(state, dir)
	st, err := loadState(statePath)
	if err != nil {
		return err
	}
	if fresh {
		st.Candidates = map[string]*CandMeta{}
		fmt.Fprintln(os.Stderr, "(--fresh: cleared previous candidates)")
	}
	newCount := 0
	for cand, meta := range candidates {
		strat := sortedKeys(meta.strategies)
		if existing, ok := st.Candidates[cand]; ok {
			existing.Strategies = dedup(append(existing.Strategies, strat...))
			sort.Strings(existing.Strategies)
			if round3(meta.score) < existing.Score {
				existing.Score = round3(meta.score)
			}
		} else {
			st.Candidates[cand] = &CandMeta{
				Score:      round3(meta.score),
				Strategies: strat,
				Status:     "untried",
				TriedAt:    nil,
			}
			newCount++
		}
	}
	st.AttemptsCount = len(attemptLines)
	if err := saveState(statePath, st); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nAdded %d new candidates (%d total in %s).\n\n", newCount, len(st.Candidates), statePath)
	printUntried(st.Candidates, top, true)
	return nil
}

// ---------------------------------------------------------------------------
// next
// ---------------------------------------------------------------------------

func cmdNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	var state, dir string
	addCommonFlags(fs, &state, &dir)
	if err := fs.Parse(args); err != nil {
		return err
	}
	count := 15
	if rest := fs.Args(); len(rest) > 0 {
		if n, ok := atoi(rest[0]); ok {
			count = n
		}
	}
	statePath := resolveStatePath(state, dir)
	st, err := loadState(statePath)
	if err != nil {
		return err
	}
	printUntried(st.Candidates, count, true)
	return nil
}

// ---------------------------------------------------------------------------
// mark
// ---------------------------------------------------------------------------

func cmdMark(args []string) error {
	fs := flag.NewFlagSet("mark", flag.ContinueOnError)
	var state, dir string
	addCommonFlags(fs, &state, &dir)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: faded mark PASSWORD (worked|failed)")
	}
	password := rest[0]
	result := rest[1]
	if result != "worked" && result != "failed" {
		return fmt.Errorf("result must be 'worked' or 'failed', got %q", result)
	}

	statePath := resolveStatePath(state, dir)
	st, err := loadState(statePath)
	if err != nil {
		return err
	}
	entry, ok := st.Candidates[password]
	if !ok {
		entry = &CandMeta{Score: 0, Strategies: []string{"manual"}, Status: "untried"}
		st.Candidates[password] = entry
	}
	now := nowISO()
	entry.Status = result
	entry.TriedAt = &now
	if err := saveState(statePath, st); err != nil {
		return err
	}

	if result == "worked" {
		fmt.Fprintln(os.Stderr, "That's the one. Stop here, re-enable biometrics so you")
		fmt.Fprintln(os.Stderr, "don't have to type it again, then forget your temp files:")
		fmt.Fprintf(os.Stderr, "    faded forget %s <your-attempts-file>\n", statePath)
	} else {
		fmt.Fprintln(os.Stderr, "Marked failed. It won't be suggested again.")
	}
	return nil
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var state, dir string
	addCommonFlags(fs, &state, &dir)
	if err := fs.Parse(args); err != nil {
		return err
	}
	statePath := resolveStatePath(state, dir)
	st, err := loadState(statePath)
	if err != nil {
		return err
	}
	store := st.Candidates
	if len(store) == 0 {
		fmt.Fprintln(os.Stderr, "No candidates yet. Run `faded gen` first.")
		return nil
	}
	printStatus(store, "State file : "+statePath)
	return nil
}

// printStatus renders the progress summary and per-strategy scoreboard. The
// header line is shown verbatim (a file path, or "(in-memory session)").
func printStatus(store map[string]*CandMeta, header string) {
	total := len(store)
	tried := 0
	var worked []string
	for c, m := range store {
		if m.Status != "untried" {
			tried++
		}
		if m.Status == "worked" {
			worked = append(worked, c)
		}
	}

	type stat struct{ total, failed, untried int }
	stats := map[string]*stat{}
	for _, m := range store {
		for _, s := range m.Strategies {
			d := stats[s]
			if d == nil {
				d = &stat{}
				stats[s] = d
			}
			d.total++
			switch m.Status {
			case "failed":
				d.failed++
			case "untried":
				d.untried++
			}
		}
	}

	fmt.Println(header)
	fmt.Printf("Candidates : %d   tried: %d   remaining: %d\n", total, tried, total-tried)
	if len(worked) > 0 {
		fmt.Printf("SOLVED     : %q\n", worked[0])
	}
	fmt.Println("\nStrategy scoreboard (which slip pattern still has untried guesses):")
	fmt.Printf("  %-24s %6s %7s %8s\n", "strategy", "total", "failed", "untried")

	names := make([]string, 0, len(stats))
	for s := range stats {
		names = append(names, s)
	}
	sort.Slice(names, func(i, j int) bool {
		if stats[names[i]].untried != stats[names[j]].untried {
			return stats[names[i]].untried > stats[names[j]].untried
		}
		return names[i] < names[j]
	})
	for _, s := range names {
		d := stats[s]
		fmt.Printf("  %-24s %6d %7d %8d\n", s, d.total, d.failed, d.untried)
	}
	fmt.Fprintln(os.Stderr, "\nStrategies with untried candidates are still worth a look; all-failed ones are exhausted.")
}

// ---------------------------------------------------------------------------
// forget
// ---------------------------------------------------------------------------

func cmdForget(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: faded forget FILE [FILE ...]")
	}
	for _, path := range args {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "skip (missing): %s\n", path)
			continue
		}
		enc := isEncryptedFile(path)
		if err := forgetFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "failed on %s: %v\n", path, err)
		} else if enc {
			fmt.Fprintf(os.Stderr, "forgot: %s (was encrypted — the ciphertext was already opaque)\n", path)
		} else {
			fmt.Fprintf(os.Stderr, "forgot: %s\n", path)
		}
	}
	fmt.Fprintln(os.Stderr, "Note: on SSDs, overwrite-in-place isn't a guaranteed erase. For plaintext")
	fmt.Fprintln(os.Stderr, "secrets, prefer `faded session` (kept in memory) or an encrypted `save`,")
	fmt.Fprintln(os.Stderr, "so there's no sensitive plaintext on disk to wipe in the first place.")
	return nil
}

// ---------------------------------------------------------------------------
// printing helpers
// ---------------------------------------------------------------------------

func printUntried(store map[string]*CandMeta, limit int, numbered bool) {
	type kv struct {
		cand string
		meta *CandMeta
	}
	var items []kv
	for c, m := range store {
		if m.Status == "untried" {
			items = append(items, kv{c, m})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].meta.Score != items[j].meta.Score {
			return items[i].meta.Score < items[j].meta.Score
		}
		return items[i].cand < items[j].cand
	})
	if len(items) == 0 {
		fmt.Fprintln(os.Stderr, "(nothing untried left — regenerate or move to an IT reset)")
		return
	}
	if limit > len(items) {
		limit = len(items)
	}
	for i := 0; i < limit; i++ {
		if numbered {
			fmt.Printf("%3d  %s\n", i+1, items[i].cand)
		} else {
			fmt.Println(items[i].cand)
		}
	}
}

func printTopCandidates(store map[string]*buildMeta, limit int) {
	for i, cand := range topCandidates(store, limit) {
		fmt.Printf("%3d  %s\n", i+1, cand)
	}
}

// topCandidates returns the n lowest-scoring (most likely) candidate strings.
func topCandidates(store map[string]*buildMeta, n int) []string {
	type kv struct {
		cand  string
		score float64
	}
	items := make([]kv, 0, len(store))
	for c, m := range store {
		items = append(items, kv{c, m.score})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score < items[j].score
		}
		return items[i].cand < items[j].cand
	})
	if n > len(items) {
		n = len(items)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, items[i].cand)
	}
	return out
}

func printStrategyCounts(counts map[string]int) {
	type kv struct {
		name string
		n    int
	}
	items := make([]kv, 0, len(counts))
	for s, n := range counts {
		items = append(items, kv{s, n})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].n != items[j].n {
			return items[i].n > items[j].n
		}
		return items[i].name < items[j].name
	})
	for _, it := range items {
		fmt.Fprintf(os.Stderr, "  %-24s %5d\n", it.name, it.n)
	}
}

// ---------------------------------------------------------------------------
// misc helpers
// ---------------------------------------------------------------------------

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func dedup(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fmtList(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return "[" + strings.Join(items, " ") + "]"
}

func atoi(s string) (int, bool) {
	n := 0
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
