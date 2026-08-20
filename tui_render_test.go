package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestModel(t *testing.T) *tuiModel {
	t.Helper()
	attempts := []string{"BlueSky1988", "Blu3Sky!", "SkyBlue88"}
	subs := []string{"Sky", "Blue"}
	sess := &session{profile: "balanced", attempts: attempts, substrings: subs}
	sess.state = stateFromCandidates(buildCandidates(attempts, subs, cfgFromProfile(profiles()["balanced"])), len(attempts))
	return newTUIModel(sess, "balanced")
}

func send(m tea.Model, msg tea.Msg) tea.Model {
	nm, _ := m.Update(msg)
	return nm
}

func TestTUIRenders(t *testing.T) {
	var m tea.Model = newTestModel(t)
	m = send(m, tea.WindowSizeMsg{Width: 72, Height: 30})
	out := m.View()
	if !strings.Contains(out, "candidates") {
		t.Errorf("list view missing header; got:\n%s", out)
	}
	// logo present (first glyph row)
	if !strings.Contains(out, logoLines[0]) {
		t.Errorf("logo not rendered")
	}
}

func TestTUIMarkFailedRemovesFromTop(t *testing.T) {
	tm := newTestModel(t)
	var m tea.Model = tm
	m = send(m, tea.WindowSizeMsg{Width: 72, Height: 30})
	first := tm.rows[0].pw
	// press 'f' to fail the top candidate
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f")})
	tm = m.(*tuiModel)
	if tm.sess.state.Candidates[first].Status != "failed" {
		t.Errorf("top candidate not marked failed")
	}
	// a failed item should no longer be the first untried row
	if len(tm.rows) > 0 && tm.rows[0].status == "untried" && tm.rows[0].pw == first {
		t.Errorf("failed candidate still sorted as top untried")
	}
}

func TestTUIProfileCycle(t *testing.T) {
	tm := newTestModel(t)
	var m tea.Model = tm
	m = send(m, tea.WindowSizeMsg{Width: 72, Height: 30})
	before := len(tm.rows)
	m = send(m, tea.KeyMsg{Type: tea.KeyTab})
	tm = m.(*tuiModel)
	if tm.profile == "balanced" {
		t.Errorf("profile did not change on tab")
	}
	if len(tm.rows) < before {
		t.Errorf("candidate count shrank after widening profile")
	}
}

func TestTUIStatusView(t *testing.T) {
	tm := newTestModel(t)
	var m tea.Model = tm
	m = send(m, tea.WindowSizeMsg{Width: 72, Height: 30})
	m = send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	out := m.View()
	if !strings.Contains(out, "scoreboard") {
		t.Errorf("status view not shown; got:\n%s", out)
	}
}

func TestTUIQuit(t *testing.T) {
	tm := newTestModel(t)
	var m tea.Model = tm
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Errorf("expected quit command on 'q'")
	}
}
