package ui

import (
	"unicode"
)

// --- Cursor movement helpers ---

// applyDesiredCol sets colCursor from desiredCol, clamping to line length.
func (m *Model) applyDesiredCol() {
	if len(m.contentLines) == 0 {
		return
	}
	lineLen := len(m.contentLines[m.lineCursor])
	if lineLen == 0 {
		m.colCursor = 0
	} else if m.desiredCol >= lineLen {
		m.colCursor = lineLen - 1
	} else {
		m.colCursor = m.desiredCol
	}
}

// moveDown moves cursor down by n lines.
func (m *Model) moveDown(n int) {
	m.lineCursor += n
	if m.lineCursor >= len(m.contentLines) {
		m.lineCursor = len(m.contentLines) - 1
	}
	m.applyDesiredCol()
	m.updateVisualEnd()
}

// moveUp moves cursor up by n lines.
func (m *Model) moveUp(n int) {
	m.lineCursor -= n
	if m.lineCursor < 0 {
		m.lineCursor = 0
	}
	m.applyDesiredCol()
	m.updateVisualEnd()
}

// moveToFirstNonBlank moves to the first non-whitespace character.
func (m *Model) moveToFirstNonBlank() {
	if len(m.contentLines) == 0 {
		return
	}
	line := m.contentLines[m.lineCursor]
	for i, ch := range line {
		if !unicode.IsSpace(ch) {
			m.colCursor = i
			m.desiredCol = i
			return
		}
	}
	m.colCursor = 0
	m.desiredCol = 0
}

// moveToLineEnd moves to the last character of the line.
func (m *Model) moveToLineEnd() {
	if len(m.contentLines) == 0 {
		return
	}
	lineLen := len(m.contentLines[m.lineCursor])
	if lineLen > 0 {
		m.colCursor = lineLen - 1
	} else {
		m.colCursor = 0
	}
	m.desiredCol = m.colCursor
}

// viewportHeight returns usable lines for note content.
func (m *Model) viewportHeight() int {
	h := m.contentHeight() - 2 // title + rule; legend/status are chrome
	if h < 1 {
		h = 1
	}
	return h
}

// --- Visual mode ---

// updateVisualEnd updates the visual selection endpoint.
func (m *Model) updateVisualEnd() {
	if m.visualMode != VisualNone {
		m.visualEnd = m.lineCursor
		m.visualEndCol = m.colCursor
	}
}
