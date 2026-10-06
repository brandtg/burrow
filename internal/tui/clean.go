// Package tui holds the Bubble Tea models. clean.go implements the preview and
// review screens for `burrow clean` (spec §16).
//
// Provenance: the categorized preview and the explicit review screen are
// behavioral ports of Mole's cleanup UX, reimplemented clean-room. No Mole
// source was copied.
package tui

import (
	"fmt"
	"io"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
)

// RenderPreview writes the grouped preview table.
func RenderPreview(w io.Writer, plan *engine.Plan, platform string, color bool) {
	if platform != "" {
		fmt.Fprintln(w, platform)
	}
	for _, g := range plan.GroupedByCategory() {
		fmt.Fprintf(w, "\u25b8 %s  %s / %d items\n", g.Title, config.FormatBytes(g.Bytes), len(g.Items))
		for _, it := range g.Items {
			fmt.Fprintf(w, "  %s\n", itemLine(it, color))
		}
	}
	t := plan.SelectedTotals()
	fmt.Fprintf(w, "Selected: %s across %d items\n", config.FormatBytes(t.Bytes), t.Count)
}

// RenderReview writes every exact argv/path that will run.
func RenderReview(w io.Writer, plan *engine.Plan) {
	fmt.Fprintln(w, "The following will run:")
	for _, it := range plan.Selected() {
		fmt.Fprintf(w, "  %s\n", reviewLine(it))
	}
}

// RenderSummary writes the per-item results, totals, and journal path.
func RenderSummary(w io.Writer, sum *engine.Summary, color bool) {
	for _, it := range sum.Executed() {
		fmt.Fprintf(w, "  %-28s %s%s\n", it.Target.Display, statusLabel(it, color), reasonSuffix(it))
	}
	freed := "unknown"
	if sum.FreedKnown || sum.Freed > 0 {
		freed = config.FormatBytes(sum.Freed)
	}
	fmt.Fprintf(w, "Freed %s (df: %s -> %s)\n",
		freed, config.FormatBytes(int64(sum.DFBefore)), config.FormatBytes(int64(sum.DFAfter)))
	fmt.Fprintf(w, "ok %d  skipped %d  failed %d\n", sum.OK, sum.Skipped, sum.Failed)
	if sum.JournalPath != "" {
		fmt.Fprintf(w, "journal: %s\n", sum.JournalPath)
	}
}

func reviewLine(it *engine.PlannedItem) string {
	if len(it.Argv) > 0 {
		return fmt.Sprintf("%s: %s", it.Target.ID, strings.Join(it.Argv, " "))
	}
	if it.Target.Path != "" {
		verb := "trash"
		if it.Target.Recovery == engine.RecoveryOwner {
			verb = "owner"
		}
		return fmt.Sprintf("%s: %s %s", it.Target.ID, verb, it.Target.Path)
	}
	return fmt.Sprintf("%s: (no path)", it.Target.ID)
}

func itemLine(it *engine.PlannedItem, color bool) string {
	check := "[ ]"
	if it.Selected {
		check = "[x]"
	}
	rec := "[owner]"
	if it.Target.Recovery == engine.RecoveryTrash {
		rec = "[trash]"
	}
	root := ""
	if it.Target.NeedsRoot {
		root = " (root)"
	}
	size := "unknown"
	if it.Target.SizeKnown {
		size = config.FormatBytes(it.Target.SizeBytes)
	}
	return fmt.Sprintf("%s %-26s %10s  %s%s %s", check, it.Target.Display, size, rec, root, riskLabel(it.Target.Risk, color))
}

func riskLabel(r engine.Risk, color bool) string {
	text := r.String()
	if !color {
		return text
	}
	switch r {
	case engine.RiskHigh:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(text)
	case engine.RiskMed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(text)
	default:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(text)
	}
}

func statusLabel(it *engine.PlannedItem, color bool) string {
	text := it.Status
	if !color {
		return text
	}
	switch it.Status {
	case engine.StatusOK:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render(text)
	case engine.StatusFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render(text)
	case engine.StatusSkipped:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Render(text)
	}
	return text
}

func reasonSuffix(it *engine.PlannedItem) string {
	if it.Reason == "" {
		return ""
	}
	return "  (" + it.Reason + ")"
}

// --- interactive model ---

type rowKind int

const (
	rowGroup rowKind = iota
	rowItem
)

type row struct {
	kind  rowKind
	title string
	item  *engine.PlannedItem
}

type model struct {
	plan      *engine.Plan
	rows      []row
	cursor    int
	screen    int // 0 preview, 1 review
	width     int
	height    int
	color     bool
	confirmed bool
	aborted   bool
}

func newModel(plan *engine.Plan, color bool) *model {
	m := &model{plan: plan, color: color, width: 80, height: 24}
	m.rebuild()
	return m
}

func (m *model) rebuild() {
	m.rows = nil
	for _, g := range m.plan.GroupedByCategory() {
		m.rows = append(m.rows, row{
			kind:  rowGroup,
			title: fmt.Sprintf("\u25b8 %s  %s / %d items", g.Title, config.FormatBytes(g.Bytes), len(g.Items)),
		})
		for _, it := range g.Items {
			m.rows = append(m.rows, row{kind: rowItem, item: it})
		}
	}
	m.clampCursor()
}

func (m *model) clampCursor() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.aborted = true
			return m, tea.Quit
		case "esc":
			if m.screen == 1 {
				m.screen = 0
			} else {
				m.aborted = true
				return m, tea.Quit
			}
		case "up", "k":
			m.move(-1)
		case "down", "j":
			m.move(1)
		case " ":
			if it := m.current(); it != nil {
				it.Selected = !it.Selected
			}
		case "a":
			for _, it := range m.plan.Items {
				if it.Target.Risk != engine.RiskHigh {
					it.Selected = true
				}
			}
		case "n":
			for _, it := range m.plan.Items {
				it.Selected = false
			}
		case "r":
			m.screen = 1
		case "enter":
			if m.screen == 0 {
				m.screen = 1
			} else {
				m.confirmed = true
				return m, tea.Quit
			}
		case "y":
			m.confirmed = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *model) move(delta int) {
	for {
		next := m.cursor + delta
		if next < 0 || next >= len(m.rows) {
			return
		}
		m.cursor = next
		if m.rows[m.cursor].kind == rowItem {
			return
		}
	}
}

func (m *model) current() *engine.PlannedItem {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor].item
}

func (m *model) View() string {
	if m.screen == 1 {
		return m.reviewView()
	}
	return m.previewView()
}

func (m *model) previewView() string {
	var b strings.Builder
	b.WriteString("Preview \u2014 space toggle, a all-safe, enter review, q quit\n\n")
	for i, r := range m.rows {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		if r.kind == rowGroup {
			b.WriteString(cursor + r.title + "\n")
			continue
		}
		b.WriteString(cursor + itemLine(r.item, m.color) + "\n")
	}
	t := m.plan.SelectedTotals()
	fmt.Fprintf(&b, "\nSelected: %s across %d items\n", config.FormatBytes(t.Bytes), t.Count)
	return b.String()
}

func (m *model) reviewView() string {
	var b strings.Builder
	b.WriteString("Review \u2014 these exact commands will run. enter confirm, esc back, q quit\n\n")
	for _, it := range m.plan.Selected() {
		b.WriteString("  " + reviewLine(it) + "\n")
	}
	b.WriteString("\nenter confirm  esc back  q quit\n")
	return b.String()
}

// RunInteractive runs the preview/review screens and reports whether the user
// confirmed. On abort or error the caller must not execute.
func RunInteractive(plan *engine.Plan, color bool) (confirmed, aborted bool, err error) {
	m := newModel(plan, color)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		return false, false, err
	}
	return m.confirmed, m.aborted, nil
}
