package tui

func newMessagesSeparator(width int) string {
	return accent.Bold(true).Render(labeledSeparator("New messages", width))
}

// Labels belong to the following message when revealing it or finding the end
// of the preceding message. New messages replaces Today at a shared boundary.
func (v *view) labelRows(index int) int {
	if index == v.todayIndex || index == v.newIndex {
		return 1
	}
	return 0
}

func (m *model) positionInitial(v *view) {
	if v.newIndex < 0 {
		v.viewport.GotoBottom()
		return
	}
	v.selected = v.newIndex
	m.rebuild(v, false)
	// Keep a little previous context when the pane has room for the labels,
	// author, and first line of the new message.
	labels := v.labelRows(v.selected)
	context := min(3, max(0, v.viewport.Height-labels-2))
	v.viewport.SetYOffset(max(0, v.starts[v.selected]-labels-context))
}
