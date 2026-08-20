package main

import (
	"crypto/subtle"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// The logo is intentionally plain ASCII so it renders in any terminal; color and
// the fade gradient are applied at draw time.
var logoLines = []string{
	"  __         _        _ ",
	" / _|__ _ __| |___ __| |",
	"|  _/ _` / _` / -_) _` |",
	"|_| \\__,_\\__,_\\___\\__,_|",
}

// ---------------------------------------------------------------------------
// styles
// ---------------------------------------------------------------------------

var (
	// A four-stop fade for the four logo rows: bright -> dim.
	fadeStops = []string{"#f5f5f5", "#b8a6d9", "#7c6ba0", "#4a4060"}

	dimText   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8a8a8a"))
	accent    = lipgloss.NewStyle().Foreground(lipgloss.Color("#b8a6d9"))
	okStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#7bd88f"))
	badStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#e88"))
	selBar    = lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1a1a")).Background(lipgloss.Color("#b8a6d9")).Bold(true)
	dimItem   = lipgloss.NewStyle().Foreground(lipgloss.Color("#666"))
	keyStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#b8a6d9")).Bold(true)
	titleBox  = lipgloss.NewStyle().Foreground(lipgloss.Color("#ddd")).Bold(true)
	borderTop = lipgloss.NewStyle().Foreground(lipgloss.Color("#3a3350"))
)

// ---------------------------------------------------------------------------
// model
// ---------------------------------------------------------------------------

type view int

const (
	viewList view = iota
	viewStatus
	viewHelp
	viewSave
)

type tuiModel struct {
	sess    *session
	profile string

	// ordered candidate list (untried first, then tried), rebuilt on change
	rows   []candRow
	cursor int
	top    int // scroll offset
	height int
	width  int

	view   string // "list" or "status"
	flash  string // transient message line
	solved string

	saveStage   int
	savePass    []rune
	saveConfirm []rune
}

type candRow struct {
	pw     string
	score  float64
	status string
}

func newTUIModel(s *session, profile string) *tuiModel {
	m := &tuiModel{sess: s, profile: profile, view: "list"}
	m.rebuild()
	return m
}

// rebuild sorts candidates: untried by score first, then tried, keeping the
// cursor on a sensible row.
func (m *tuiModel) rebuild() {
	rows := make([]candRow, 0, len(m.sess.state.Candidates))
	for pw, meta := range m.sess.state.Candidates {
		rows = append(rows, candRow{pw: pw, score: meta.Score, status: meta.Status})
	}
	sort.Slice(rows, func(i, j int) bool {
		ui, uj := rows[i].status == "untried", rows[j].status == "untried"
		if ui != uj {
			return ui // untried first
		}
		if rows[i].score != rows[j].score {
			return rows[i].score < rows[j].score
		}
		return rows[i].pw < rows[j].pw
	})
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	for pw, meta := range m.sess.state.Candidates {
		if meta.Status == "worked" {
			m.solved = pw
		}
	}
}

func (m *tuiModel) Init() tea.Cmd { return nil }

func (m *tuiModel) visibleRows() int {
	// height minus logo (4) minus header/help chrome (~7)
	h := m.height - len(logoLines) - 8
	if h < 3 {
		h = 3
	}
	return h
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.view == "save" {
			return m.updateSave(msg)
		}
		if m.view == "status" || m.view == "help" {
			// any key returns to the list
			switch msg.String() {
			case "q", "ctrl+c":
				return m, tea.Quit
			default:
				m.view = "list"
				return m, nil
			}
		}

		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.rows)-1 {
				m.cursor++
			}
		case "pgup":
			m.cursor -= m.visibleRows()
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "pgdown":
			m.cursor += m.visibleRows()
			if m.cursor > len(m.rows)-1 {
				m.cursor = len(m.rows) - 1
			}
		case "g", "home":
			m.cursor = 0
		case "G", "end":
			m.cursor = len(m.rows) - 1
		case "f":
			m.markCursor("failed")
		case "w", "enter":
			m.markCursor("worked")
		case "u":
			m.markCursor("untried")
		case "s":
			m.view = "status"
		case "h":
			m.view = "help"
		case "S":
			m.beginSave()
		case "tab":
			m.cycleProfile()
		}
		m.clampScroll()
	}
	return m, nil
}

func (m *tuiModel) beginSave() {
	m.view = "save"
	m.saveStage = 1
	m.savePass = nil
	m.saveConfirm = nil
	m.flash = ""
}

func (m *tuiModel) updateSave(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" || msg.String() == "esc" {
		m.view = "list"
		m.savePass = nil
		m.saveConfirm = nil
		return m, nil
	}
	if msg.String() == "backspace" || msg.String() == "delete" {
		if m.saveStage == 1 && len(m.savePass) > 0 {
			m.savePass = m.savePass[:len(m.savePass)-1]
		} else if m.saveStage == 2 && len(m.saveConfirm) > 0 {
			m.saveConfirm = m.saveConfirm[:len(m.saveConfirm)-1]
		}
		return m, nil
	}
	if msg.String() == "enter" {
		if m.saveStage == 1 {
			if len(m.savePass) == 0 {
				m.flash = badStyle.Render("passphrase cannot be empty")
				return m, nil
			}
			m.saveStage = 2
			m.flash = ""
			return m, nil
		}
		if subtle.ConstantTimeCompare([]byte(string(m.savePass)), []byte(string(m.saveConfirm))) != 1 {
			m.saveStage = 1
			m.savePass = nil
			m.saveConfirm = nil
			m.flash = badStyle.Render("passphrases did not match")
			return m, nil
		}

		path := defaultEncryptedPath()
		err := m.sess.saveWithPassphrase(path, []byte(string(m.savePass)))
		m.savePass = nil
		m.saveConfirm = nil
		m.view = "list"
		if err != nil {
			m.flash = badStyle.Render("save failed: ") + err.Error()
		} else {
			m.sess.dirty = false
			m.flash = okStyle.Render("saved encrypted session: ") + path
		}
		return m, nil
	}
	if len(msg.Runes) > 0 {
		if m.saveStage == 1 {
			m.savePass = append(m.savePass, msg.Runes...)
		} else {
			m.saveConfirm = append(m.saveConfirm, msg.Runes...)
		}
	}
	return m, nil
}

func (m *tuiModel) markCursor(status string) {
	if len(m.rows) == 0 {
		return
	}
	pw := m.rows[m.cursor].pw
	meta, ok := m.sess.state.Candidates[pw]
	if !ok {
		return
	}
	if status == "untried" {
		meta.Status = "untried"
		meta.TriedAt = nil
		m.flash = dimText.Render("reset ") + pw
	} else {
		now := nowISO()
		meta.Status = status
		meta.TriedAt = &now
		if status == "worked" {
			m.flash = okStyle.Render("solved! ") + pw + dimText.Render("  — nothing was written to disk; press q to exit")
		} else {
			m.flash = badStyle.Render("failed ") + dimText.Render(pw)
		}
	}
	m.sess.dirty = true
	m.rebuild()
}

func (m *tuiModel) cycleProfile() {
	order := orderedProfiles()
	idx := 0
	for i, n := range order {
		if n == m.profile {
			idx = i
			break
		}
	}
	next := order[(idx+1)%len(order)]
	if len(m.sess.attempts) == 0 {
		m.flash = dimText.Render("no attempts loaded to regenerate from")
		return
	}
	added := m.sess.regenerate(profiles()[next])
	m.profile = next
	m.rebuild()
	m.flash = accent.Render("profile "+next) + dimText.Render(fmt.Sprintf("  (+%d new, %d total)", added, len(m.rows)))
}

func (m *tuiModel) clampScroll() {
	vis := m.visibleRows()
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+vis {
		m.top = m.cursor - vis + 1
	}
	if m.top < 0 {
		m.top = 0
	}
}

// ---------------------------------------------------------------------------
// view
// ---------------------------------------------------------------------------

func (m *tuiModel) renderLogo() string {
	var b strings.Builder
	for i, line := range logoLines {
		color := fadeStops[i%len(fadeStops)]
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(line))
		b.WriteByte('\n')
	}
	tag := dimText.Render("   a password you half-remember, recovered in memory")
	b.WriteString(tag)
	return b.String()
}

func (m *tuiModel) View() string {
	if m.width == 0 {
		return m.renderLogo() + "\n\nloading…"
	}
	if m.view == "status" {
		return m.renderStatusView()
	}
	if m.view == "help" {
		return m.renderHelpView()
	}
	if m.view == "save" {
		return m.renderSaveView()
	}
	return m.renderListView()
}

func countByStatus(rows []candRow) (untried, tried int) {
	for _, r := range rows {
		if r.status == "untried" {
			untried++
		} else {
			tried++
		}
	}
	return
}

func (m *tuiModel) renderListView() string {
	var b strings.Builder
	b.WriteString(m.renderLogo())
	b.WriteString("\n\n")

	untried, tried := countByStatus(m.rows)
	header := fmt.Sprintf("%s  %s   %s",
		titleBox.Render("candidates"),
		accent.Render("profile:"+m.profile),
		dimText.Render(fmt.Sprintf("%d untried · %d tried", untried, tried)),
	)
	b.WriteString(header + "\n")
	b.WriteString(borderTop.Render(strings.Repeat("─", min(m.width, 60))) + "\n")

	vis := m.visibleRows()
	end := m.top + vis
	if end > len(m.rows) {
		end = len(m.rows)
	}
	if len(m.rows) == 0 {
		b.WriteString(dimText.Render("  (no candidates)\n"))
	}
	for i := m.top; i < end; i++ {
		r := m.rows[i]
		marker := "  "
		label := r.pw
		switch r.status {
		case "failed":
			label = dimItem.Render(r.pw) + badStyle.Render("  ✗")
		case "worked":
			label = okStyle.Render(r.pw) + okStyle.Render("  ✓")
		case "tried":
			label = dimItem.Render(r.pw)
		}
		line := fmt.Sprintf("%s%s", marker, label)
		if i == m.cursor {
			// selection bar spans the visible width
			plain := fmt.Sprintf("%s%s", "› ", r.pw)
			switch r.status {
			case "failed":
				plain += "  ✗"
			case "worked":
				plain += "  ✓"
			}
			line = selBar.Render(padRight(plain, min(m.width-1, 58)))
		}
		b.WriteString(line + "\n")
	}

	// pad to keep the help bar anchored
	for i := end - m.top; i < vis; i++ {
		b.WriteString("\n")
	}

	if m.flash != "" {
		b.WriteString("\n" + m.flash + "\n")
	} else {
		b.WriteString("\n" + dimText.Render("nothing is written to disk — this session lives only in memory") + "\n")
	}
	b.WriteString(m.helpBar())
	return b.String()
}

func (m *tuiModel) renderStatusView() string {
	var b strings.Builder
	b.WriteString(m.renderLogo())
	b.WriteString("\n\n")
	b.WriteString(titleBox.Render("strategy scoreboard") + "\n")
	b.WriteString(borderTop.Render(strings.Repeat("─", min(m.width, 60))) + "\n")

	type stat struct{ total, failed, untried int }
	stats := map[string]*stat{}
	for _, meta := range m.sess.state.Candidates {
		for _, s := range meta.Strategies {
			d := stats[s]
			if d == nil {
				d = &stat{}
				stats[s] = d
			}
			d.total++
			switch meta.Status {
			case "failed":
				d.failed++
			case "untried":
				d.untried++
			}
		}
	}
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

	b.WriteString(dimText.Render(fmt.Sprintf("  %-22s %6s %7s %8s", "strategy", "total", "failed", "untried")) + "\n")
	limit := m.visibleRows()
	for i, s := range names {
		if i >= limit {
			b.WriteString(dimText.Render(fmt.Sprintf("  … %d more", len(names)-i)) + "\n")
			break
		}
		d := stats[s]
		b.WriteString(fmt.Sprintf("  %-22s %6d %7d %8d\n", s, d.total, d.failed, d.untried))
	}
	b.WriteString("\n" + dimText.Render("press any key to go back") + "\n")
	b.WriteString(m.helpBar())
	return b.String()
}

func (m *tuiModel) renderHelpView() string {
	var b strings.Builder
	b.WriteString(m.renderLogo())
	b.WriteString("\n\n")
	b.WriteString(titleBox.Render("generation options") + "\n")
	b.WriteString(borderTop.Render(strings.Repeat("─", min(m.width, 60))) + "\n")
	b.WriteString(fmt.Sprintf("  %-18s %s\n", "profile", "tab cycles conservative -> balanced -> aggressive -> kitchen-sink"))
	b.WriteString(fmt.Sprintf("  %-18s %s\n", "depth", "max stacked typo slips (profile default; 1 or 2 are practical)"))
	b.WriteString(fmt.Sprintf("  %-18s %s\n", "beam", "keeps the cheapest typo paths between layers"))
	b.WriteString(fmt.Sprintf("  %-18s %s\n", "cap", "maximum candidates retained after ranking"))
	b.WriteString(fmt.Sprintf("  %-18s %s\n", "typos", "filter families such as capslock, transpose, drop, adjacent"))
	b.WriteString(fmt.Sprintf("  %-18s %s\n", "leet / affixes", "symbol swaps and common prefixes or suffixes"))
	b.WriteString("\n" + dimText.Render("CLI-only tuning: faded gen -h shows --depth, --beam, --cap, --typos, --no-leet, --no-affixes, --dry-run, and --fresh.") + "\n")
	b.WriteString("\n" + dimText.Render("press any key to go back") + "\n")
	b.WriteString(m.helpBar())
	return b.String()
}

func (m *tuiModel) renderSaveView() string {
	var b strings.Builder
	b.WriteString(m.renderLogo())
	b.WriteString("\n\n")
	b.WriteString(titleBox.Render("save encrypted session") + "\n")
	b.WriteString(borderTop.Render(strings.Repeat("─", min(m.width, 60))) + "\n\n")
	label := "Passphrase:"
	value := m.savePass
	if m.saveStage == 2 {
		label = "Confirm passphrase:"
		value = m.saveConfirm
	}
	b.WriteString("  " + label + " " + strings.Repeat("•", len(value)) + "\n")
	b.WriteString(dimText.Render("  saved to "+defaultEncryptedPath()) + "\n")
	if m.flash != "" {
		b.WriteString("\n  " + m.flash + "\n")
	}
	b.WriteString("\n" + dimText.Render("enter to continue · esc to cancel · backspace to edit") + "\n")
	b.WriteString(m.helpBar())
	return b.String()
}

func (m *tuiModel) helpBar() string {
	keys := []struct{ k, d string }{
		{"↑/↓", "move"}, {"w", "worked"}, {"f", "failed"}, {"u", "reset"},
		{"tab", "profile"}, {"s", "stats"}, {"h", "help"}, {"S", "save"}, {"q", "quit"},
	}
	var parts []string
	for _, e := range keys {
		parts = append(parts, keyStyle.Render(e.k)+" "+dimText.Render(e.d))
	}
	return borderTop.Render(strings.Repeat("─", min(m.width, 60))) + "\n" + strings.Join(parts, dimText.Render("  ·  "))
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func padRight(s string, n int) string {
	if lipgloss.Width(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-lipgloss.Width(s))
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// command entry point
// ---------------------------------------------------------------------------

func cmdTUI(args []string) error {
	fs := flag.NewFlagSet("tui", flag.ContinueOnError)
	var (
		attempts, subs, profileName string
		subList                     multiFlag
	)
	fs.StringVar(&attempts, "attempts", "", "file of near-miss guesses (default: prompt in the terminal)")
	fs.StringVar(&subs, "subs", defaultSubsFile, "optional file of known building-block substrings")
	fs.Var(&subList, "sub", "an explicit substring (repeatable)")
	fs.StringVar(&profileName, "profile", "balanced", "aggressiveness preset: "+strings.Join(orderedProfiles(), ", "))
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: faded tui [--attempts FILE] [--subs FILE] [--profile NAME]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, ok := profiles()[profileName]; !ok {
		return fmt.Errorf("unknown profile %q (choices: %s)", profileName, strings.Join(orderedProfiles(), ", "))
	}

	a, ss, _, _, err := gatherInputs(attempts, subs, subList)
	if err != nil {
		return err
	}
	sess := &session{profile: profileName, attempts: a, substrings: ss}
	sess.state = stateFromCandidates(buildCandidates(a, ss, cfgFromProfile(profiles()[profileName])), len(a))

	p := tea.NewProgram(newTUIModel(sess, profileName), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
