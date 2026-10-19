package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Jarsefax/slakt/internal/core"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/muesli/termenv"
)

func TestMessageFormattingSurvivesWrappingAndSelection(t *testing.T) {
	previous := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	sample := cellbuf.NewBuffer(1, 1)
	cellbuf.SetContent(sample, selectedStyle.Render("x"))
	selectionBackground := sample.Cell(0, 0).Style.Bg
	cellbuf.SetContent(sample, messageStyle.Foreground(lipgloss.Color("#FCD34D")).Render("x"))
	codeForeground := sample.Cell(0, 0).Style.Fg

	for _, width := range []int{45, 140} {
		for _, selected := range []int{0, 1} {
			t.Run(fmt.Sprintf("width=%d/selection=%d", width, selected), func(t *testing.T) {
				m := loadedModel(t)
				m.width = width
				m.main.messages = []core.Message{
					{User: "author", Timestamp: "1.000001", Text: "*" + strings.Repeat("B", 70) + "* _" + strings.Repeat("I", 70) + "_ ~" + strings.Repeat("S", 70) + "~ `" + strings.Repeat("C", 70) + "` P"},
					{User: "author", Timestamp: "2.000001", Text: "ordinary"},
				}
				m.main.selected = selected
				m.rebuild(&m.main, false)
				m.main.viewport.Height = m.main.viewport.TotalLineCount()
				m.main.viewport.SetYOffset(0)
				frame := m.main.viewport.View()
				if strings.ContainsAny(ansi.Strip(frame), "*_~`") {
					t.Fatalf("formatting markers remain: %s", ansi.Strip(frame))
				}
				for _, line := range strings.Split(frame, "\n") {
					if ansi.StringWidth(line) > m.main.viewport.Width {
						t.Fatalf("row exceeds viewport width: %q", line)
					}
				}
				buffer := cellbuf.NewBuffer(m.main.viewport.Width, m.main.viewport.Height)
				cellbuf.SetContent(buffer, frame)
				seen := map[rune]int{}
				for y := m.main.starts[0] + 1; y < m.main.starts[1]-1; y++ {
					for x := 0; x < buffer.Width(); x++ {
						cell := buffer.Cell(x, y)
						if cell == nil {
							continue
						}
						if x < 6 {
							if cell.Style.Attrs&(cellbuf.ItalicAttr|cellbuf.StrikethroughAttr) != 0 || cell.Style.UlStyle != cellbuf.NoUnderline {
								t.Fatalf("formatting leaked into gutter at (%d,%d): %+v", x, y, cell.Style)
							}
							continue
						}
						if selected == 0 && cell.Style.Bg != selectionBackground {
							t.Fatalf("selection background lost at (%d,%d), %q: %+v", x, y, cell.String(), cell.Style)
						}
						want := cellbuf.ResetAttr
						switch cell.Rune {
						case 'B':
							want = cellbuf.BoldAttr
						case 'I':
							want = cellbuf.ItalicAttr
						case 'S':
							want = cellbuf.StrikethroughAttr
						case 'C', 'P':
						default:
							continue
						}
						seen[cell.Rune]++
						if cell.Style.Attrs != want {
							t.Fatalf("wrong style at (%d,%d), %q: got %v, want %v", x, y, cell.String(), cell.Style.Attrs, want)
						}
						if cell.Rune == 'C' {
							if cell.Style.Fg != codeForeground {
								t.Fatalf("code lost its foreground: %+v", cell.Style)
							}
							if selected == 0 && cell.Style.UlStyle != cellbuf.SingleUnderline {
								t.Fatal("selected inline code lost its underline")
							}
						} else if cell.Style.UlStyle != cellbuf.NoUnderline {
							t.Fatalf("code underline leaked into %q", cell.String())
						}
					}
				}
				for _, letter := range "BISC" {
					if seen[letter] != 70 {
						t.Fatalf("lost wrapped %c characters: got %d, want 70", letter, seen[letter])
					}
				}
				if seen['P'] != 1 {
					t.Fatal("plain text after formatting is missing")
				}
			})
		}
	}
}
