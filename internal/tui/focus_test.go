package tui

import (
	"fmt"
	"slices"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
	tea "github.com/charmbracelet/bubbletea"
)

func TestNumbersFollowSidebarThreadAndTextEntryFocus(t *testing.T) {
	for _, width := range []int{70, 140} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			m := loadedModel(t)
			mm, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 18})
			m = mm.(model)
			cmd := m.switchTo(core.Conversation{ID: "CGENERAL", Name: "general", Kind: "channel"})
			mm, _ = m.Update(cmd())
			m = mm.(model)
			m, _ = key(m, "g")
			m, _ = key(m, "j")
			m, _ = key(m, "j")
			assertMessageNumbers(t, m.main, []string{"2", "1", "3", "1", "2"})
			starts := slices.Clone(m.main.starts)
			offset := m.main.viewport.YOffset
			m, _ = key(m, "tab")
			assertNoMessageNumbers(t, m.main)
			if !slices.Equal(starts, m.main.starts) || m.main.viewport.YOffset != offset {
				t.Fatal("sidebar focus changed message wrapping or scroll position")
			}
			m, _ = key(m, "tab")
			assertMessageNumbers(t, m.main, []string{"2", "1", "3", "1", "2"})
			m, cmd = key(m, "enter")
			assertNoMessageNumbers(t, m.main) // Hide while the thread is loading too.
			mm, _ = m.Update(cmd())
			m = mm.(model)
			assertMessageNumbers(t, *m.thread, []string{"2", "1", "3"})
			assertNoMessageNumbers(t, m.main)

			starts = slices.Clone(m.thread.starts)
			offset = m.thread.viewport.YOffset
			m, _ = key(m, "tab")
			m, _ = key(m, "j")
			mm, _ = m.Update(m.pollCmd()())
			m = mm.(model)
			assertNoMessageNumbers(t, m.main)
			assertNoMessageNumbers(t, *m.thread)
			if !slices.Equal(starts, m.thread.starts) || m.thread.viewport.YOffset != offset {
				t.Fatal("sidebar focus or refresh moved the thread")
			}
			m, _ = key(m, "tab")
			assertMessageNumbers(t, *m.thread, []string{"2", "1", "3"})

			for _, entry := range []string{"i", "/", "ctrl+k", "r", "?"} {
				m, _ = key(m, entry)
				assertNoMessageNumbers(t, m.main)
				assertNoMessageNumbers(t, *m.thread)
				m, _ = key(m, "esc")
				assertMessageNumbers(t, *m.thread, []string{"2", "1", "3"})
				assertNoMessageNumbers(t, m.main)
			}
			m, _ = key(m, "esc")
			assertMessageNumbers(t, m.main, []string{"2", "1", "3", "1", "2"})
		})
	}
}

func TestSearchNumbersFollowFocusAndReturnToChannel(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "/")
	m, _ = key(m, "deploy")
	m, cmd := key(m, "enter")
	mm, _ := m.Update(cmd())
	m = mm.(model)
	assertMessageNumbers(t, m.search, []string{"1", "1"})
	assertNoMessageNumbers(t, m.main)
	m, cmd = key(m, "enter")
	mm, _ = m.Update(cmd())
	m = mm.(model)
	assertMessageNumbers(t, *m.thread, []string{"1"})
	assertNoMessageNumbers(t, m.search)
	m, _ = key(m, "esc")
	assertMessageNumbers(t, m.search, []string{"1", "1"})
	m, _ = key(m, "tab")
	assertNoMessageNumbers(t, m.search)
	m, _ = key(m, "esc")
	assertMessageNumbers(t, m.main, []string{"1", "2"})
	assertNoMessageNumbers(t, m.search)
}

func TestSendResultsRestoreNumbersOnlyWhenBrowsingResumes(t *testing.T) {
	m := loadedModel(t)
	m, _ = key(m, "i")
	m, _ = key(m, "a message")
	m, _ = key(m, "ctrl+s")
	assertNoMessageNumbers(t, m.main)
	mm, _ := m.Update(sentResult{err: fmt.Errorf("send failed")})
	m = mm.(model)
	assertNoMessageNumbers(t, m.main)
	m, cmd := key(m, "ctrl+s")
	mm, _ = m.Update(cmd())
	m = mm.(model)
	assertMessageNumbers(t, m.main, []string{"2", "1", "3"})
}
