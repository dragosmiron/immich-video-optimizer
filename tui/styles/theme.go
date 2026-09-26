package styles

import "github.com/charmbracelet/lipgloss"

var (
	Base = lipgloss.NewStyle().Padding(1, 2)
	Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFDF5")).
		Background(lipgloss.Color("#25A065")).
		Padding(0, 1).
		MarginBottom(1)

	Text = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	Subtext = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))

	SelectedRow = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	NormalRow = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	
	ErrorText = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF0000"))
	SuccessText = lipgloss.NewStyle().Foreground(lipgloss.Color("#00FF00"))
	WarningText = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFF00"))
)
