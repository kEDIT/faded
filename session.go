package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// A session holds all candidate state in memory. Nothing is written to disk
// unless the user explicitly `save`s an encrypted snapshot.
type session struct {
	attempts   []string
	substrings []string
	profile    string
	state      *State
	dirty      bool // in-memory changes not yet saved
}

func defaultEncryptedPath() string {
	return filepath.Join(resolveBaseDir(""), "faded_session.enc")
}

// isEncryptedFile reports whether the file at path is a faded encrypted envelope.
func isEncryptedFile(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return isEncrypted(data)
}

// stateFromCandidates converts a freshly built candidate pool into a State.
func stateFromCandidates(c map[string]*buildMeta, attemptsCount int) *State {
	st := &State{Created: nowISO(), Candidates: map[string]*CandMeta{}, AttemptsCount: attemptsCount}
	for cand, meta := range c {
		st.Candidates[cand] = &CandMeta{
			Score:      round3(meta.score),
			Strategies: sortedKeys(meta.strategies),
			Status:     "untried",
		}
	}
	return st
}

// regenerate rebuilds candidates at a new profile, preserving existing marks.
func (s *session) regenerate(prof profile) int {
	fresh := buildCandidates(s.attempts, s.substrings, cfgFromProfile(prof))
	added := 0
	for cand, meta := range fresh {
		if e, ok := s.state.Candidates[cand]; ok {
			e.Strategies = dedup(append(e.Strategies, sortedKeys(meta.strategies)...))
			sort.Strings(e.Strategies)
			if round3(meta.score) < e.Score {
				e.Score = round3(meta.score)
			}
		} else {
			s.state.Candidates[cand] = &CandMeta{
				Score:      round3(meta.score),
				Strategies: sortedKeys(meta.strategies),
				Status:     "untried",
			}
			added++
		}
	}
	return added
}

func cmdSession(args []string) error {
	fs := flag.NewFlagSet("session", flag.ContinueOnError)
	var (
		attempts, subs, load, profileName string
		subList                           multiFlag
	)
	fs.StringVar(&attempts, "attempts", defaultAttemptsFile, "file of near-miss guesses (one per line)")
	fs.StringVar(&subs, "subs", defaultSubsFile, "optional file of known building-block substrings")
	fs.Var(&subList, "sub", "an explicit substring (repeatable)")
	fs.StringVar(&profileName, "profile", "balanced", "aggressiveness preset: "+strings.Join(orderedProfiles(), ", "))
	fs.StringVar(&load, "load", "", "unlock and resume an encrypted session file at start")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: faded session [--attempts FILE] [--subs FILE] [--profile NAME] [--load FILE]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	if _, ok := profiles()[profileName]; !ok {
		return fmt.Errorf("unknown profile %q (choices: %s)", profileName, strings.Join(orderedProfiles(), ", "))
	}

	s := &session{profile: profileName}

	if load != "" {
		pass, err := readSecret(fmt.Sprintf("Passphrase to unlock %s: ", load))
		if err != nil {
			return err
		}
		blob, err := os.ReadFile(load)
		if err != nil {
			return err
		}
		plain, err := decryptBytes(blob, pass)
		if err != nil {
			return err
		}
		var st State
		if err := json.Unmarshal(plain, &st); err != nil {
			return err
		}
		if st.Candidates == nil {
			st.Candidates = map[string]*CandMeta{}
		}
		s.state = &st
		fmt.Fprintf(os.Stderr, "Unlocked %d candidates from %s.\n", len(st.Candidates), load)
		// pull attempts/subs too, if present, so `profile` can regenerate
		if a, ss, _, _, e := gatherInputs(attempts, subs, subList); e == nil {
			s.attempts, s.substrings = a, ss
		}
	} else {
		a, ss, mined, _, err := gatherInputs(attempts, subs, subList)
		if err != nil {
			return err
		}
		s.attempts, s.substrings = a, ss
		s.state = stateFromCandidates(buildCandidates(a, ss, cfgFromProfile(profiles()[profileName])), len(a))
		fmt.Fprintf(os.Stderr, "Built %d candidates in memory (profile %s). Substrings mined: %s\n",
			len(s.state.Candidates), profileName, fmtList(mined))
	}

	fmt.Fprintln(os.Stderr, "\nInteractive session — nothing is written to disk unless you `save`.")
	printSessionHelp()
	return s.repl()
}

func printSessionHelp() {
	fmt.Fprint(os.Stderr, `
Commands:
  next [N]                 show the next N untried guesses (default 15)
  mark worked|failed PW    record an outcome for password PW
  status                   show progress and the strategy scoreboard
  profile NAME             regenerate at a different aggressiveness, keeping marks
  save [FILE]              write an encrypted snapshot (prompts for a passphrase)
  help                     show this help
  quit                     end the session and discard in-memory state
`)
}

func (s *session) repl() error {
	for {
		fmt.Fprint(os.Stderr, "\nfaded> ")
		line, err := stdinReader.ReadString('\n')
		if line == "" && err != nil {
			fmt.Fprintln(os.Stderr)
			return nil
		}
		raw := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(raw)
		if trimmed != "" {
			if quit := s.dispatch(raw, trimmed); quit {
				return nil
			}
		}
		if err != nil {
			return nil
		}
	}
}

// dispatch runs one REPL command; it returns true if the session should end.
func (s *session) dispatch(raw, trimmed string) (quit bool) {
	fields := strings.Fields(trimmed)
	switch fields[0] {
	case "next":
		n := 15
		if len(fields) > 1 {
			if v, ok := atoi(fields[1]); ok {
				n = v
			}
		}
		printUntried(s.state.Candidates, n, true)

	case "status":
		printStatus(s.state.Candidates, "(in-memory session)")

	case "mark":
		// Parse from the raw line so passwords may contain spaces
		// (e.g. leading/trailing-space typo candidates).
		l := strings.TrimLeft(raw, " ")
		parts := strings.SplitN(l, " ", 3)
		if len(parts) < 3 {
			fmt.Fprintln(os.Stderr, "usage: mark worked|failed <password>")
			return false
		}
		result, pw := parts[1], parts[2]
		if result != "worked" && result != "failed" {
			fmt.Fprintln(os.Stderr, "result must be 'worked' or 'failed'")
			return false
		}
		e, ok := s.state.Candidates[pw]
		if !ok {
			e = &CandMeta{Strategies: []string{"manual"}}
			s.state.Candidates[pw] = e
		}
		now := nowISO()
		e.Status = result
		e.TriedAt = &now
		s.dirty = true
		if result == "worked" {
			fmt.Fprintln(os.Stderr, "That's the one — nice. Re-enable biometrics so you don't retype it.")
			fmt.Fprintln(os.Stderr, "Nothing is on disk; `quit` to discard, or `save` if you want an encrypted record.")
		} else {
			fmt.Fprintln(os.Stderr, "Marked failed; it won't be suggested again.")
		}

	case "profile":
		if len(fields) < 2 {
			fmt.Fprintf(os.Stderr, "usage: profile <%s>\n", strings.Join(orderedProfiles(), " | "))
			return false
		}
		prof, ok := profiles()[fields[1]]
		if !ok {
			fmt.Fprintf(os.Stderr, "unknown profile %q\n", fields[1])
			return false
		}
		if len(s.attempts) == 0 {
			fmt.Fprintln(os.Stderr, "no attempts available to regenerate from (start a session without --load)")
			return false
		}
		added := s.regenerate(prof)
		s.profile = fields[1]
		s.dirty = true
		fmt.Fprintf(os.Stderr, "Regenerated at profile %s (+%d new, %d total).\n", fields[1], added, len(s.state.Candidates))

	case "save":
		path := defaultEncryptedPath()
		if len(fields) > 1 {
			path = fields[1]
		}
		if err := s.save(path); err != nil {
			fmt.Fprintln(os.Stderr, "save failed:", err)
		} else {
			s.dirty = false
		}

	case "help", "?":
		printSessionHelp()

	case "quit", "exit", "q":
		if s.dirty {
			fmt.Fprintln(os.Stderr, "Session ended; in-memory state discarded (nothing was saved).")
		} else {
			fmt.Fprintln(os.Stderr, "Session ended.")
		}
		return true

	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (try `help`)\n", fields[0])
	}
	return false
}

func (s *session) save(path string) error {
	pass, err := setNewPassphrase()
	if err != nil {
		return err
	}
	plain, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	blob, err := encryptBytes(plain, pass)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, blob, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Saved encrypted snapshot to %s (AES-256-GCM). Resume later with:\n  faded session --load %s\n", path, path)
	return nil
}
