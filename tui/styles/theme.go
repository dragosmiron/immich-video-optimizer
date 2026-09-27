package styles

import "github.com/charmbracelet/lipgloss"

var (
	Base = lipgloss.NewStyle().Padding(1, 2)

	Title = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFDF5")).
		Background(lipgloss.Color("#2563EB")).
		Padding(0, 1).
		Bold(true).
		MarginBottom(1)

	SectionHeader = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#38BDF8")).
		Bold(true).
		MarginTop(1).
		MarginBottom(1)

	Subtext = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))

	ErrorText = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true)
	SuccessText = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Bold(true)
	WarningText = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Bold(true)

	SummaryBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#64748B")).
		Padding(0, 1).
		MarginBottom(1)

	Button = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.Color("#334155")).
		Padding(0, 2).
		MarginTop(1)

	ActiveButton = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.Color("#38BDF8")).
		Padding(0, 2).
		Bold(true).
		MarginTop(1)

	Value = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8FAFC")).Bold(true)
)
