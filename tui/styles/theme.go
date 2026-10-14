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
		Foreground(lipgloss.AdaptiveColor{Light: "#0284C7", Dark: "#38BDF8"}).
		Bold(true).
		MarginTop(1).
		MarginBottom(1)

	Subtext = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#64748B", Dark: "#94A3B8"})

	ErrorText = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#DC2626", Dark: "#EF4444"}).
		Bold(true)

	SuccessText = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#16A34A", Dark: "#22C55E"}).
		Bold(true)

	WarningText = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#D97706", Dark: "#F59E0B"}).
		Bold(true)

	SummaryBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#94A3B8", Dark: "#64748B"}).
		Padding(0, 1).
		MarginBottom(1)

	Button = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(lipgloss.AdaptiveColor{Light: "#475569", Dark: "#334155"}).
		Padding(0, 2).
		MarginTop(1)

	ActiveButton = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#000000")).
		Background(lipgloss.AdaptiveColor{Light: "#38BDF8", Dark: "#38BDF8"}).
		Padding(0, 2).
		Bold(true).
		MarginTop(1)

	Value = lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#0F172A", Dark: "#F8FAFC"}).
		Bold(true)
)
