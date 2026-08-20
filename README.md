# faded

A small, dependency-free Go tool for recovering **your own** password when it's
gone a bit *faded* in memory — you recall the *ingredients* but not the exact
assembly. The classic case: "I built my master password out of a few substrings,
I'm not sure which arrangement I used, and I might have fat-fingered it too."

It takes a short list of near-miss guesses you provide, mines the substrings you
reuse, recombines them the way people actually build passwords (orderings,
separators, leet, affixes), layers common typing slips on top (optionally
*stacked* for two-error mistypes), and **ranks** the results most-likely-first.
Persistent state means it never re-suggests something you've already ruled out,
and a scoreboard tells you which slip pattern is still worth chasing.

Everything runs locally. Nothing is sent anywhere.

> **Scope.** This is a self-recovery aid for accounts you own. It generates
> candidates for *you* to type at your own prompt; it does not talk to any
> login system, bypass lockouts, or crack hashes.

---

## Contents

- [Install](#install)
- [Quick start](#quick-start)
- [Concepts](#concepts)
- [Where files are written](#where-files-are-written)
- [Commands](#commands)
- [Generation options](#generation-options)
- [Profiles](#profiles)
- [Typo families](#typo-families)
- [How ranking works](#how-ranking-works)
- [Worked examples](#worked-examples)
- [Important cautions](#important-cautions)

---

## Install

Requires Go 1.22+ to build. No third-party dependencies.

```sh
cd faded
go build -o faded .
```

That produces a single self-contained binary named `faded`. Put it anywhere on
your `PATH`, or run it as `./faded`.

To cross-compile (e.g. an Apple-Silicon Mac from a Linux box):

```sh
GOOS=darwin GOARCH=arm64 go build -o faded .
```

---

## Quick start

By default, `faded` prompts for near-miss guesses in the terminal, so no
attempts file is required. Enter one guess per line and press Enter on a blank
line to start generating candidates. Use `--attempts FILE` when you prefer a
file, and `subs.txt` is still read from the current directory when present.

1. Start generation and enter your near-miss guesses when prompted:

   ```sh
   faded gen
   ```

   ```text
   Enter near-miss guesses, one per line. Press Enter on a blank line when done:
   BlueSky1988
   Blu3Sky!
   SkyBlue88

   ```

   Optionally list the reusable building blocks you know you use in `subs.txt`
   (one per line), or pass them with `--sub`. If you skip this, the tool infers
   them from your attempts, but giving it the real fragments sharpens the
   ranking a lot.

   ```
   Sky
   Blue
   ```

2. See how the profiles differ and pick one:

   ```sh
   faded compare
   ```

3. Generate ranked candidates and save them:

   ```sh
   faded gen
   ```

4. Try the top ones at your login. When one fails, tell the tool so it stops
   suggesting it and updates the scoreboard:

   ```sh
   faded mark 'Blu3Sky!88' failed
   ```

5. See what's next and which strategies still have juice:

   ```sh
   faded next 15
   faded status
   ```

6. When one works (or you're done), wipe the sensitive files:

   ```sh
   faded mark 'Blu3Sky!' worked
   faded forget /tmp/faded_state.json
   ```

---

## Interactive UI

```sh
faded tui
```

A full-screen [Bubble Tea](https://github.com/charmbracelet/bubbletea) interface:

```
  __         _        _
 / _|__ _ __| |___ __| |
|  _/ _` / _` / -_) _` |
|_| \__,_\__,_\___\__,_|
   a password you half-remember, recovered in memory

candidates  profile:balanced   800 untried · 0 tried
────────────────────────────────────────────────────
› Blu3Sky!!
  BlueSky1988!
  SkyBlue88!
  ...
────────────────────────────────────────────────────
↑/↓ move · w worked · f failed · u reset · tab profile · s stats · q quit
```

Keys: `↑/↓` (or `j/k`) move, `PgUp/PgDn`, `g/G` jump to top/bottom; `w`/Enter mark
worked, `f` mark failed, `u` reset a mark; `tab` cycles the aggressiveness profile
(regenerating while keeping your marks); `s` toggles the strategy scoreboard;
`h` opens the generation-options help screen; uppercase `S` saves an encrypted
snapshot after prompting for a passphrase; `q` quits. Like the session,
**everything lives in memory** until you explicitly save.

The TUI is the only part of `faded` with third-party dependencies (Bubble Tea and
Lipgloss). They're **vendored** into the repo, so it still builds offline with no
`go get` — see [Development](#development).

---



- **Attempts** — the handful of passwords you think are "close." These seed both
   the substring miner and the typo layers. By default they are entered in the
   terminal; `--attempts FILE` reads them from a file instead.
- **Substrings** — the reusable chunks of your master password. Supplied
  explicitly (`--subs` / `--sub`) and/or mined automatically. Default file:
  `subs.txt`.
- **Candidates** — the generated, ranked guesses. Lower score = more likely.
- **Strategy** — the technique that produced a candidate (`recombine`, `leet`,
  `affix`, `typo:transpose`, `typo2:transpose+capslock`, …). Tracked so the
  scoreboard can show which family still has untried guesses.
- **State file** — a JSON record of every candidate and its status
  (`untried` / `failed` / `worked`). Lets you stop and resume without repeating
  yourself, and never re-suggests a ruled-out guess.

---

## Where files are written

Nothing is hard-coded to a fixed project directory. The state file location is
resolved in this order:

1. `--state <path>` — an explicit file path, used verbatim.
2. `--dir <dir>` — a directory; the state file becomes `<dir>/faded_state.json`.
3. `FADED_DIR` environment variable — same effect as `--dir`.
4. **Default:** your OS temp directory (`/tmp` on Linux/macOS) —
   `/tmp/faded_state.json`.

Examples:

```sh
faded gen                                    # -> /tmp/faded_state.json
faded gen --dir ./work                       # -> ./work/faded_state.json
faded gen --state /run/user/1000/pw.json
FADED_DIR=~/.cache/faded faded gen
```

The state file is created with owner-only permissions (`0600`). It still
contains candidate passwords in **plaintext** — see [cautions](#important-cautions).

---

## Commands

| Command   | Purpose |
|-----------|---------|
| `tui`     | Full-screen interactive UI (Bubble Tea). In-memory only — writes nothing to disk. |
| `compare` | Show candidate counts and sample top guesses for every profile, side by side. |
| `gen`     | Generate ranked candidates from your attempts and store them. |
| `session` | Interactive REPL that keeps everything **in memory** — nothing on disk unless you `save`. |
| `next N`  | Print the next `N` untried candidates (default 15). |
| `mark`    | Record a candidate's outcome (`worked` / `failed`). |
| `status`  | Show progress and the per-strategy scoreboard. |
| `forget`  | Best-effort wipe (overwrite + delete) of sensitive files. |

`--state` and `--dir` are accepted by `gen`, `next`, `mark`, and `status`.

```
faded compare [--attempts FILE] [--subs FILE] [--top N]
faded gen     [--attempts FILE] [--subs FILE] [--profile NAME] [options]
faded next    [N] [--state FILE] [--dir DIR]
faded mark    PASSWORD (worked|failed) [--state FILE] [--dir DIR]
faded status  [--state FILE] [--dir DIR]
faded forget  FILE [FILE ...]
```

---

## Generation options

Run `faded gen -h` for the authoritative list. Summary:

| Flag             | Meaning |
|------------------|---------|
| `--attempts FILE`| File of near-miss guesses, one per line. Default: prompt in the terminal. |
| `--subs FILE`    | File of known building-block substrings. Default `subs.txt` (used if present). |
| `--sub STRING`   | An explicit substring; repeatable (`--sub Sky --sub Blue`). |
| `--top N`        | How many candidates to print now (default 25; `0` prints none). |
| `--profile NAME` | Aggressiveness preset (default `balanced`). |
| `--depth N`      | Max simultaneous typo slips (1 = single, 2 = double, …). Overrides profile. |
| `--beam N`       | Beam width kept between typo layers. Higher = more thorough, slower. |
| `--cap N`        | Max candidates to keep after ranking. |
| `--slip-penalty F` | Score penalty per extra stacked slip. Higher pushes multi-slip guesses lower. |
| `--typos LIST`   | Comma list of typo families to allow, or `all`. |
| `--no-typos`     | Disable typo generation entirely. |
| `--no-leet`      | Disable leet substitutions. |
| `--no-affixes`   | Disable prefix/suffix affixes. |
| `--dry-run`      | Preview counts and the top list **without** writing state. |
| `--fresh`        | Clear previously stored candidates before writing. |

Per-knob flags override the chosen profile, so you can take `aggressive` but dial
its cap down: `--profile aggressive --cap 800`.

---

## Profiles

Presets that set depth, allowed typo families, beam, cap, and slip penalty.
Override any individual knob on top of them. `faded compare` shows them all at
once.

| Profile        | Depth | Adjacent-key typos | Approx. size | Use it for |
|----------------|:-----:|:------------------:|:------------:|------------|
| `conservative` |   1   | off                | ~200         | Your first pass — the most likely guesses only. |
| `balanced`     |   1   | on                 | ~800         | Default. Single slips, all families. |
| `aggressive`   |   2   | off                | ~2500        | Two stacked slips, minus the noisy adjacent-key layer. |
| `kitchen-sink` |   2   | on                 | ~6000        | Everything. Last resort only. |

The sizes are what the tool caps to; the real signal is in the **top ~20**
regardless of profile.

---

## Typo families

Each single-slip typo is tagged with a family (the part after `typo:`):

| Family           | Models |
|------------------|--------|
| `capslock`       | Caps Lock left on (whole-string case inversion). |
| `shift-first`    | Shift slip on the first character. |
| `shift-last`     | Shift slip on the last character. |
| `transpose`      | Two neighbouring characters swapped. |
| `shift-symbol`   | Number/symbol confusion (`1`↔`!`, `3`↔`#`, …). |
| `drop`           | A character that didn't register. |
| `double`         | A held/bounced key typed twice. |
| `adjacent`       | Hitting a physically neighbouring key. |
| `leading-space`  | A stray space at the start. |
| `trailing-space` | A stray space at the end. |

Filter with `--typos`, e.g. `--typos capslock,transpose,drop`, or allow every
family with `--typos all`. Stacked (depth ≥ 2) candidates get a combined tag such
as `typo2:transpose+capslock`, and the scoreboard tracks each combination.

---

## How ranking works

Every candidate gets a numeric score; **lower is more likely**, and output is
sorted ascending. Scores are rough priors, not probabilities:

- Verbatim attempts start very low (you might just need a clean re-type).
- Recombinations cost more as they use more parts or unusual separators.
- Leet and affixes add a small amount on top of their base.
- Typos add their family cost; **stacking a second slip adds a penalty**, so
  two-error mistypes sit below single-error ones.
- Candidates within an edit-distance of 1–2 of one of your remembered attempts
  get a small boost toward the top.

Output ordering is deterministic (ties broken by the candidate string), so the
same inputs and options always produce the same list.

---

## Worked examples

**Compare aggressiveness at a glance** (this is the built-in replacement for
looping over profiles by hand):

```sh
faded compare
faded compare --top 5          # show more sample guesses per profile
```

**Tune before you commit.** `--dry-run` never touches saved state:

```sh
faded gen --profile balanced --dry-run --top 25
```

**Commit a fresh run** (clears any prior candidates first):

```sh
faded gen --profile balanced --fresh --top 25
```

**Chase a specific slip pattern only, with two stacked slips:**

```sh
faded gen --depth 2 --typos capslock,transpose --top 30
```

**Take a profile but rein it in:**

```sh
faded gen --profile aggressive --cap 800 --slip-penalty 2.0
```

**The try/mark loop:**

```sh
faded next 12                       # print a dozen to try
faded mark 'Blu3Sky!88' failed      # retire the ones that miss
faded status                        # see which families still have untried guesses
```

**Keep experiments isolated** by giving each its own state file:

```sh
faded gen --state /tmp/exp_conservative.json --profile conservative
faded gen --state /tmp/exp_aggressive.json  --profile aggressive
faded status --state /tmp/exp_aggressive.json
```

**Clean up when done:**

```sh
faded forget /tmp/faded_state.json subs.txt
```

---

## Handling secrets

Candidate passwords are sensitive. `faded` gives you three postures, from most
convenient to most private:

1. **Plaintext state (default for `gen`/`next`/`mark`/`status`).** Convenient and
   resumable, but the JSON state file holds candidates in the clear. Created
   `0600`; `forget` it when done. Best when the machine is trusted and you value
   convenience over secrecy.

2. **In-memory session (`faded session`) — recommended when secrecy matters.**
   Builds and tracks everything in RAM and writes **nothing** to disk. When you
   `quit`, the candidates are gone — there's no file to wipe, which sidesteps the
   whole "overwrite isn't a guaranteed erase" problem. This is strictly stronger
   than shredding a plaintext file.

3. **Encrypted snapshot (`save` inside a session).** When you want to stop and
   resume without leaving plaintext behind, `save` writes an authenticated,
   encrypted file (AES-256-GCM; key via PBKDF2-HMAC-SHA256, 600k iterations).
   Resume with `faded session --load FILE`. As you noted, this trades the
   original password problem for a passphrase — pick one you won't also forget.

```sh
faded session                      # in-memory; nothing hits disk
# faded> next 15
# faded> mark failed Blu3Sky!88
# faded> status
# faded> profile aggressive        # regenerate, keeping your marks
# faded> save ~/faded.enc          # optional: prompts for a passphrase (twice)
# faded> quit                      # in-memory state discarded

faded session --load ~/faded.enc   # resume later; prompts to unlock
```

**Passphrase input.** Prompts read with terminal echo disabled (via `stty` on
macOS/Linux) and never take the passphrase on the command line, so it doesn't
land in your shell history or the process list. For automation you can set
`FADED_PASSPHRASE`, but that exposes it to the environment — avoid it for real
secrets. Note that Go's garbage collector means in-memory secrets can't be
reliably zeroed; the guarantee here is "never written to disk," not "scrubbed
from RAM."

**`forget` is a fallback, not a first resort.** It overwrites a file with random
bytes and deletes it, which is fine for a temp file — but on SSDs
(wear-levelling, copy-on-write filesystems) overwrite-in-place is **not** a
guaranteed forensic erase. If secrecy matters, prefer the in-memory session so
there's no plaintext to wipe in the first place.

---



- **Login screens and password managers throttle or lock out** after a handful
  of wrong tries. Do **not** paste in dozens of these. Try the top ~10–15
  deliberately; if none land, that's your signal it's a support/IT reset, not a
  guessing problem. This matters doubly for a password manager — some wipe or
  hard-throttle after enough failures.
- The profiles generate hundreds to thousands of candidates. That volume is for
  **browsing and comparing strategies**, not for typing. The real answer is
  almost always in the top 20 of a `conservative` or `balanced` run; the deep
  tail (especially `adjacent` and depth-2 stacks) is a long-shot safety net.
- The **state file stores candidate passwords in plaintext**. It's created
  `0600`, but keep it in a directory only you can read and `forget` it when done.
- `forget` overwrites in place and deletes, which is fine for a temp file — but on
  SSDs (wear-levelling, copy-on-write filesystems) overwrite-in-place is **not**
  a guaranteed forensic erase.

---

## Development

A `Makefile` wraps the common tasks. Run `make help` to list them.

```sh
make            # build ./faded for your machine
make test       # run the unit tests
make cover      # tests with a coverage summary
make race       # tests under the race detector
make vet fmt    # go vet / gofmt
```

**Dependencies.** The core tool is standard-library-only. The `tui` command adds
Bubble Tea and Lipgloss, which are **vendored** under `vendor/`, so the whole
repo builds offline with `-mod=vendor` (the Makefile sets this by default) — no
`go get` or network needed. The `golang.org/x/*` modules are pinned via local
`replace` directives to checkouts under `third_party/`; if you re-run
`go mod tidy` yourself, keep those replaces (or fetch the upstream modules
normally on a machine with network access).

**Cross-compiling.** `make cross` builds a static, stripped binary for every
platform in `PLATFORMS` into `dist/`:

```sh
make cross
# dist/faded_linux_amd64, dist/faded_darwin_arm64, dist/faded_windows_amd64.exe, ...
```

Build a single target, or override the platform list:

```sh
make dist/faded_darwin_arm64
make cross PLATFORMS="linux/amd64 windows/amd64"
```

The default set is `linux/{amd64,arm64}`, `darwin/{amd64,arm64}`, and
`windows/{amd64,arm64}`. Builds use `CGO_ENABLED=0`, so the binaries are
statically linked and dependency-free at runtime.

**Tests** live in `engine_test.go` (typo generation, stacking, recombination,
substring mining, ranking), `support_test.go` (state round-trip, file
permissions, path resolution, line reading, forget, and CLI helpers),
`crypto_test.go` (PBKDF2 test vectors, encryption round-trip, and rejection of
wrong passphrases and tampered ciphertext), and `tui_render_test.go` (drives the
Bubble Tea model without a terminal: rendering, marking, profile cycling, the
status view, and quit). `TestProfileSizes` doubles as a regression guard on the
whole generation pipeline by locking the candidate counts for a known fixture.

## Project layout

```
faded/
├── go.mod            module (stdlib core; Bubble Tea/Lipgloss for the TUI)
├── Makefile          build / test / cross-compile targets (uses -mod=vendor)
├── main.go           CLI dispatch, flag parsing, command handlers, printing
├── engine.go         typos, stacking, recombination, candidate building, mining
├── state.go          state persistence, path resolution, line reading, forget
├── crypto.go         PBKDF2 + AES-256-GCM envelope, no-echo passphrase input
├── session.go        interactive in-memory REPL with encrypted save/load
├── tui.go            Bubble Tea full-screen UI (logo, list, marks, scoreboard)
├── engine_test.go    tests for the generation engine
├── support_test.go   tests for state and CLI helpers
├── crypto_test.go    tests for PBKDF2 vectors and encryption round-trips
├── tui_render_test.go  tests that drive the Bubble Tea model without a TTY
├── third_party/      pinned checkouts of golang.org/x/* (via replace)
└── vendor/           vendored dependencies (offline builds)
```
