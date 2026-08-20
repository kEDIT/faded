package main

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const stateFileName = "faded_state.json"

// CandMeta is the stored record for one candidate password.
type CandMeta struct {
	Score      float64  `json:"score"`
	Strategies []string `json:"strategies"`
	Status     string   `json:"status"` // untried | tried | failed | worked
	TriedAt    *string  `json:"tried_at"`
}

// State is the full persisted document.
type State struct {
	Created       string               `json:"created,omitempty"`
	Candidates    map[string]*CandMeta `json:"candidates"`
	AttemptsCount int                  `json:"attempts_count,omitempty"`
}

func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05Z07:00")
}

// resolveBaseDir picks the directory for the default state file:
// explicit --dir wins, then $FADED_DIR, then the OS temp dir (/tmp).
func resolveBaseDir(dirFlag string) string {
	if dirFlag != "" {
		return dirFlag
	}
	if env := os.Getenv("FADED_DIR"); env != "" {
		return env
	}
	return os.TempDir()
}

// resolveStatePath returns the state file path. An explicit --state path is
// used verbatim; otherwise it is <baseDir>/faded_state.json.
func resolveStatePath(stateFlag, dirFlag string) string {
	if stateFlag != "" {
		return stateFlag
	}
	return filepath.Join(resolveBaseDir(dirFlag), stateFileName)
}

func loadState(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &State{Created: nowISO(), Candidates: map[string]*CandMeta{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if st.Candidates == nil {
		st.Candidates = map[string]*CandMeta{}
	}
	return &st, nil
}

// writeAtomic writes data to path via a temp file + rename, with 0600 perms.
func writeAtomic(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// enforce owner-only perms even if the file pre-existed
	_ = os.Chmod(path, 0o600)
	return nil
}

func saveState(path string, st *State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// stateHandle carries a loaded state plus how it should be persisted. When
// encrypted is true, secret holds the passphrase used to seal it on save().
type stateHandle struct {
	path      string
	st        *State
	encrypted bool
	secret    []byte
}

// openState loads (and if needed decrypts) the state at path. wantEncrypt asks
// to *initiate* encryption on a new or plaintext file; an already-encrypted file
// is always kept encrypted regardless. Passphrase prompts happen here.
func openState(path string, wantEncrypt bool) (*stateHandle, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		st := &State{Created: nowISO(), Candidates: map[string]*CandMeta{}}
		if wantEncrypt {
			secret, perr := setNewPassphrase()
			if perr != nil {
				return nil, perr
			}
			return &stateHandle{path: path, st: st, encrypted: true, secret: secret}, nil
		}
		return &stateHandle{path: path, st: st}, nil
	}
	if err != nil {
		return nil, err
	}

	if isEncrypted(data) {
		secret, perr := readSecret("Passphrase for encrypted state: ")
		if perr != nil {
			return nil, perr
		}
		plain, derr := decryptBytes(data, secret)
		if derr != nil {
			return nil, fmt.Errorf("could not decrypt state (wrong passphrase or corrupted file)")
		}
		st, uerr := unmarshalState(plain)
		if uerr != nil {
			return nil, uerr
		}
		return &stateHandle{path: path, st: st, encrypted: true, secret: secret}, nil
	}

	st, uerr := unmarshalState(data)
	if uerr != nil {
		return nil, uerr
	}
	if wantEncrypt {
		secret, perr := setNewPassphrase()
		if perr != nil {
			return nil, perr
		}
		return &stateHandle{path: path, st: st, encrypted: true, secret: secret}, nil
	}
	return &stateHandle{path: path, st: st}, nil
}

func unmarshalState(data []byte) (*State, error) {
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parsing state: %w", err)
	}
	if st.Candidates == nil {
		st.Candidates = map[string]*CandMeta{}
	}
	return &st, nil
}

func (h *stateHandle) save() error {
	if h.encrypted {
		data, err := json.MarshalIndent(h.st, "", "  ")
		if err != nil {
			return err
		}
		env, err := encryptBytes(data, h.secret)
		if err != nil {
			return err
		}
		return writeAtomic(h.path, env)
	}
	return saveState(h.path, h.st)
}

// readLines returns non-blank lines with trailing CR/LF stripped.
func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r\n")
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

// forgetFile overwrites a file with random bytes (twice), syncs, then removes
// it. On SSDs this is best-effort, not a guaranteed forensic erase.
func forgetFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	size := info.Size()
	if size < 1 {
		size = 1
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	buf := make([]byte, size)
	for pass := 0; pass < 2; pass++ {
		if _, err := rand.Read(buf); err != nil {
			f.Close()
			return err
		}
		if _, err := f.Seek(0, 0); err != nil {
			f.Close()
			return err
		}
		if _, err := f.Write(buf); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
	}
	f.Close()
	return os.Remove(path)
}
