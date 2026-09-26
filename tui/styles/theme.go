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

	Text = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	Subtext = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8"))

	SelectedRow = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E"))
	NormalRow = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	
	ErrorText = lipgloss.NewStyle().Foreground(lipgloss.Color("#EF4444")).Bold(true)
	SuccessText = lipgloss.NewStyle().Foreground(lipgloss.Color("#22C55E")).Bold(true)
	WarningText = lipgloss.NewStyle().Foreground(lipgloss.Color("#F59E0B")).Bold(true)

	Box = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#3B82F6")).
		Padding(1, 2).
		MarginBottom(1)

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

	KeyBadge = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#E2E8F0")).
		Background(lipgloss.Color("#1E293B")).
		Padding(0, 1).
		Bold(true)

	Label = lipgloss.NewStyle().Foreground(lipgloss.Color("#94A3B8")).Width(20)
	Value = lipgloss.NewStyle().Foreground(lipgloss.Color("#F8FAFC")).Bold(true)
)
