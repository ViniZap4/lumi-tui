package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/vinizap/lumi/tui-client/theme"
)

func (m Model) renderWithSearchModal(base string) string {
	modalWidth := min(m.width-10, 100)

	var modal strings.Builder

	// Title
	modal.WriteString(lipgloss.NewStyle().
		Bold(true).
		Foreground(primaryColor).
		Render("  Find Notes"))
	modal.WriteString("\n\n")

	// Search input
	typeLabel := "Filename"
	if m.searchType == "content" {
		typeLabel = "Content"
	}

	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Padding(0, 1).
		Width(modalWidth - 8).
		Render(fmt.Sprintf("[%s] %s", typeLabel, m.searchQuery+"_"))

	modal.WriteString(inputBox)
	modal.WriteString("\n\n")

	if len(m.searchResults) == 0 {
		modal.WriteString(lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true).
			Render("  No results"))
		modal.WriteString("\n")
	} else {
		modal.WriteString(lipgloss.NewStyle().
			Foreground(mutedColor).
			Render(fmt.Sprintf("  Results (%d)", len(m.searchResults))))
		modal.WriteString("\n\n")

		// Split: results left, preview right
		leftWidth := (modalWidth - 8) / 2
		rightWidth := modalWidth - 8 - leftWidth - 3

		var resultsList strings.Builder
		maxResults := 12
		for i, item := range m.searchResults {
			if i >= maxResults {
				break
			}

			name := item.Name
			if len(name) > leftWidth-6 {
				name = name[:leftWidth-9] + "..."
			}

			if i == m.cursor {
				line := lipgloss.NewStyle().
					Foreground(theme.Current.Background).
					Background(accentColor).
					Bold(true).
					Width(leftWidth).
					Render(fmt.Sprintf(" > %s", name))
				resultsList.WriteString(line)
			} else {
				resultsList.WriteString(lipgloss.NewStyle().
					Width(leftWidth).
					Render(fmt.Sprintf("   %s", name)))
			}
			resultsList.WriteString("\n")
		}

		// Preview
		var previewBox strings.Builder
		if m.cursor >= 0 && m.cursor < len(m.searchResults) && m.searchResults[m.cursor].Note != nil {
			note := m.searchResults[m.cursor].Note

			previewBox.WriteString(lipgloss.NewStyle().
				Foreground(primaryColor).
				Bold(true).
				Render(note.Title))
			previewBox.WriteString("\n")
			previewBox.WriteString(strings.Repeat("-", rightWidth))
			previewBox.WriteString("\n")

			previewLines := strings.Split(note.Content, "\n")
			maxPreview := 10
			for i := 0; i < min(len(previewLines), maxPreview); i++ {
				line := previewLines[i]
				if len(line) > rightWidth {
					line = line[:rightWidth-3] + "..."
				}
				previewBox.WriteString(lipgloss.NewStyle().
					Foreground(mutedColor).
					Render(line))
				previewBox.WriteString("\n")
			}
		} else {
			previewBox.WriteString(lipgloss.NewStyle().
				Foreground(mutedColor).
				Italic(true).
				Render("No preview available"))
		}

		// Combine side by side
		resultsLines := strings.Split(resultsList.String(), "\n")
		previewLines := strings.Split(previewBox.String(), "\n")
		maxLines := max(len(resultsLines), len(previewLines))

		for i := 0; i < maxLines; i++ {
			if i < len(resultsLines) {
				modal.WriteString(resultsLines[i])
			} else {
				modal.WriteString(strings.Repeat(" ", leftWidth))
			}
			modal.WriteString(" | ")
			if i < len(previewLines) {
				modal.WriteString(previewLines[i])
			}
			modal.WriteString("\n")
		}
	}

	modal.WriteString("\n")
	modal.WriteString(HelpStyle.Render("ctrl+f=toggle | ↑↓=navigate | enter=open | esc=close"))

	modalBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(accentColor).
		Background(theme.Current.Background).
		Padding(1, 2).
		Width(modalWidth).
		Render(modal.String())

	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		modalBox,
		lipgloss.WithWhitespaceBackground(theme.Current.OverlayBg),
	)
}
