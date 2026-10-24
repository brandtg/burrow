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

	"github.com/brandtg/burrow/internal/catalog/projects"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/whitelist"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// previewItemCap bounds the number of item rows printed per group by the
// non-interactive preview. Larger groups print a "+N more" line and point at
// --json for the full list.
const previewItemCap = 12

// reviewItemCap bounds the number of trash items listed individually by the
// review screen. Beyond it the review aggregates by group.
const reviewItemCap = 20

// collapseThreshold is the number of items at or above which a category or
// group starts collapsed in the TUI.
const collapseThreshold = 20

// RenderPreview writes the grouped preview table. Large groups are capped so a
// category with hundreds of similar entries does not swamp the output.
func RenderPreview(w io.Writer, plan *engine.Plan, platform string, color bool) {
	if platform != "" {
		fmt.Fprintln(w, platform)
	}
	unknown := 0
	for _, g := range plan.GroupedTree() {
		fmt.Fprintf(w, "\u25b8 %s  %s / %d items\n", g.Title, config.FormatBytes(g.Bytes), len(g.Items))
		for _, ig := range g.Groups {
			for _, it := range ig.Items {
				if !it.Target.SizeKnown {
					unknown++
				}
			}
			if ig.ID != "" {
				fmt.Fprintf(w, "  \u25b8 %s  %s / %d items\n", ig.Title, config.FormatBytes(ig.Bytes), len(ig.Items))
			}
			// Indistinguishable rows (for example one __pycache__ per project)
			// add nothing once the group header carries the count and size.
			if sameDisplay(ig.Items) && len(ig.Items) > 1 {
				continue
			}
			shown := 0
			for _, it := range ig.Items {
				if shown >= previewItemCap {
					break
				}
				indent := "  "
				if ig.ID != "" {
					indent = "    "
				}
				fmt.Fprintf(w, "%s%s\n", indent, itemLine(it, color))
				shown++
			}
			if extra := len(ig.Items) - shown; extra > 0 {
				fmt.Fprintf(w, "    \u2026 +%d more (use --json for the full list)\n", extra)
			}
		}
	}
	t := plan.SelectedTotals()
	fmt.Fprintf(w, "Selected: %s across %d items\n", config.FormatBytes(t.Bytes), t.Count)
	if unknown > 0 {
		fmt.Fprintf(w, "%d item(s) report size after the owner command runs (unknown now)\n", unknown)
	}
}

// RenderReview writes the exact argv/path that will run, aggregating large
// trash selections by group so the screen stays readable. The complete list is
// always available via --json.
func RenderReview(w io.Writer, plan *engine.Plan) {
	fmt.Fprint(w, reviewText(plan, false))
}

func reviewText(plan *engine.Plan, dryRun bool) string {
	var b strings.Builder
	if dryRun {
		b.WriteString("Review \u2014 dry run: nothing will run.\n\n")
	} else {
		b.WriteString("Review \u2014 these exact commands will run.\n\n")
	}
	selected := plan.Selected()

	var owners, trashes []*engine.PlannedItem
	for _, it := range selected {
		if len(it.Argv) > 0 {
			owners = append(owners, it)
		} else {
			trashes = append(trashes, it)
		}
	}
	for _, it := range owners {
		b.WriteString("  " + reviewLine(it) + "\n")
	}

	if len(trashes) <= reviewItemCap {
		for _, it := range trashes {
			b.WriteString("  " + reviewLine(it) + "\n")
		}
	} else {
		for _, g := range plan.GroupedTree() {
			var items []*engine.PlannedItem
			for _, it := range g.Items {
				if it.Selected && len(it.Argv) == 0 {
					items = append(items, it)
				}
			}
			if len(items) == 0 {
				continue
			}
			var bytes int64
			for _, it := range items {
				if it.Target.SizeKnown {
					bytes += it.Target.SizeBytes
				}
			}
			line := fmt.Sprintf("  trash %d item(s), %s \u2014 %s", len(items), config.FormatBytes(bytes), g.Title)
			b.WriteString(line + "\n")
		}
	}

	t := plan.SelectedTotals()
	fmt.Fprintf(&b, "\nSelected: %s across %d items\n", config.FormatBytes(t.Bytes), t.Count)
	return b.String()
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

// sameDisplay reports whether every item in the group shares one display name,
// so listing them individually would be pure repetition.
func sameDisplay(items []*engine.PlannedItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, it := range items[1:] {
		if it.Target.Display != items[0].Target.Display {
			return false
		}
	}
	return true
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
	switch {
	case it.Kept:
		check = "[kept]"
	case it.Selected:
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
	size := "unknown (owner-reported)"
	if it.Target.SizeKnown {
		size = config.FormatBytes(it.Target.SizeBytes)
	}
	regen := it.Target.Regen.String()
	if it.Target.Gated {
		regen += ",active"
	}
	line := fmt.Sprintf("%s %-26s %10s  %s%s %-9s %s", check, it.Target.Display, size, rec, root, regen, riskLabel(it.Target.Risk, color))
	// Unselected rows state why, so a left-on item is a decision, not a
	// surprise (for example "dev caches are not pre-selected by default").
	if !it.Selected && len(it.Target.Evidence) > 0 {
		line += "  # " + it.Target.Evidence[0]
	}
	return line
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

type cleanRowKind int

const (
	rowCategory cleanRowKind = iota
	rowGroup
	rowItem
)

type cleanRow struct {
	kind  cleanRowKind
	cat   *engine.CategoryGroup
	group *engine.ItemGroup
	item  *engine.PlannedItem
}

// CleanConfig carries the dependencies the clean model needs. Plan is the
// already-built, read-only plan (invariant I1). Whitelist, when non-nil, is a
// loaded manager that backs the interactive keep key.
type CleanConfig struct {
	Plan      *engine.Plan
	Platform  string
	Color     bool
	DryRun    bool
	Whitelist *whitelist.Manager
}

// CleanModel is the Bubble Tea model for `burrow clean`.
type CleanModel struct {
	plan      *engine.Plan
	cats      []engine.CategoryGroup
	rows      []cleanRow
	cursor    int
	offset    int
	width     int
	height    int
	color     bool
	dryRun    bool
	platform  string
	whitelist *whitelist.Manager

	collapsedCat map[string]bool
	collapsedGrp map[string]bool

	filter    textinput.Model
	filtering bool

	screen    int // 0 preview, 1 review
	confirmed bool
	aborted   bool
	status    string

	help   help.Model
	keys   cleanKeyMap
	styles styles
}

// NewCleanModel builds the clean model from a plan.
func NewCleanModel(cfg CleanConfig) *CleanModel {
	filter := textinput.New()
	filter.Placeholder = "search"
	filter.Prompt = "/"
	m := &CleanModel{
		plan:         cfg.Plan,
		platform:     cfg.Platform,
		color:        cfg.Color,
		dryRun:       cfg.DryRun,
		whitelist:    cfg.Whitelist,
		cats:         cfg.Plan.GroupedTree(),
		collapsedCat: map[string]bool{},
		collapsedGrp: map[string]bool{},
		filter:       filter,
		help:         help.New(),
		keys:         defaultCleanKeys(),
		styles:       newStyles(cfg.Color),
		height:       24,
		width:        80,
	}
	for i := range m.cats {
		if len(m.cats[i].Items) > collapseThreshold {
			m.collapsedCat[m.cats[i].ID] = true
		}
		for j := range m.cats[i].Groups {
			// Project artifact groups always start collapsed: one row per
			// project (name + total artifact size), expandable — a monorepo is
			// one row, not thirty.
			if m.cats[i].ID == projects.ID || len(m.cats[i].Groups[j].Items) > collapseThreshold {
				m.collapsedGrp[groupKey(m.cats[i].ID, m.cats[i].Groups[j].ID)] = true
			}
		}
	}
	m.rebuildRows()
	return m
}

func groupKey(catID, groupID string) string { return catID + "\x00" + groupID }

// Run starts the Bubble Tea program.
func (m *CleanModel) Run() (confirmed, aborted bool, err error) {
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return false, false, err
	}
	return m.confirmed, m.aborted, nil
}

// Init implements tea.Model.
func (m *CleanModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *CleanModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		return m, nil
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *CleanModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.help.ShowAll {
		if msg.String() == "?" || msg.String() == "esc" || msg.String() == "q" {
			m.help.ShowAll = false
		}
		return m, nil
	}
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filter.SetValue("")
			m.filter.Blur()
			m.filtering = false
			m.rebuildRows()
			return m, nil
		case "enter":
			m.filter.Blur()
			m.filtering = false
			m.rebuildRows()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.rebuildRows()
		return m, cmd
	}
	if m.screen == 1 {
		switch msg.String() {
		case "enter":
			m.confirmed = true
			return m, tea.Quit
		case "esc":
			m.screen = 0
		case "q", "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		}
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		m.aborted = true
		return m, tea.Quit
	case key.Matches(msg, m.keys.Help):
		m.help.ShowAll = true
	case key.Matches(msg, m.keys.Up):
		m.move(-1)
	case key.Matches(msg, m.keys.Down):
		m.move(1)
	case key.Matches(msg, m.keys.Toggle):
		m.toggle()
	case key.Matches(msg, m.keys.Expand):
		m.expand()
	case key.Matches(msg, m.keys.Collapse):
		m.collapse()
	case key.Matches(msg, m.keys.SelectAll):
		m.selectAll()
	case key.Matches(msg, m.keys.Clear):
		m.clearAll()
	case key.Matches(msg, m.keys.Keep):
		m.keep()
	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		m.filter.Focus()
	case key.Matches(msg, m.keys.Review):
		m.screen = 1
	}
	return m, nil
}

func (m *CleanModel) move(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor += delta
	m.clampCursor()
	m.ensureVisible()
}

func (m *CleanModel) clampCursor() {
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

func (m *CleanModel) toggle() {
	if m.cursor >= len(m.rows) {
		return
	}
	row := m.rows[m.cursor]
	switch row.kind {
	case rowItem:
		row.item.Selected = !row.item.Selected
	case rowGroup:
		toggleSet(row.group.Items)
	case rowCategory:
		toggleSet(row.cat.Items)
	}
}

func toggleSet(items []*engine.PlannedItem) {
	members := selectable(items)
	if len(members) == 0 {
		return
	}
	all := true
	for _, it := range members {
		if !it.Selected {
			all = false
			break
		}
	}
	for _, it := range members {
		it.Selected = !all
	}
}

func (m *CleanModel) expand() {
	if m.cursor >= len(m.rows) {
		return
	}
	row := m.rows[m.cursor]
	switch row.kind {
	case rowCategory:
		delete(m.collapsedCat, row.cat.ID)
	case rowGroup:
		delete(m.collapsedGrp, groupKey(row.cat.ID, row.group.ID))
	}
	m.rebuildRows()
}

func (m *CleanModel) collapse() {
	if m.cursor >= len(m.rows) {
		return
	}
	row := m.rows[m.cursor]
	switch row.kind {
	case rowItem:
		if row.group.ID != "" {
			m.collapsedGrp[groupKey(row.cat.ID, row.group.ID)] = true
			m.rebuildRows()
			m.focusHeader(row.cat.ID, row.group.ID)
			return
		}
		m.collapsedCat[row.cat.ID] = true
		m.rebuildRows()
		m.focusHeader(row.cat.ID, "")
	case rowGroup:
		m.collapsedGrp[groupKey(row.cat.ID, row.group.ID)] = true
		m.rebuildRows()
	case rowCategory:
		m.collapsedCat[row.cat.ID] = true
		m.rebuildRows()
	}
}

func (m *CleanModel) focusHeader(catID, groupID string) {
	for i, r := range m.rows {
		if r.cat == nil || r.cat.ID != catID {
			continue
		}
		if groupID == "" && r.kind == rowCategory {
			m.cursor = i
			return
		}
		if r.kind == rowGroup && r.group != nil && r.group.ID == groupID {
			m.cursor = i
			return
		}
	}
	m.clampCursor()
}

func (m *CleanModel) selectAll() {
	for _, it := range m.plan.Items {
		if it.Target.AutoSelectable() {
			it.Selected = true
		}
	}
}

func (m *CleanModel) clearAll() {
	for _, it := range m.plan.Items {
		it.Selected = false
	}
}

// keep whitelists the paths under the cursor and marks them kept, so a cache a
// person wants to preserve is protected on this and future runs. Group and
// category rows keep every member path.
func (m *CleanModel) keep() {
	if m.cursor >= len(m.rows) {
		return
	}
	row := m.rows[m.cursor]
	var items []*engine.PlannedItem
	switch row.kind {
	case rowItem:
		items = []*engine.PlannedItem{row.item}
	case rowGroup:
		items = row.group.Items
	case rowCategory:
		items = row.cat.Items
	}
	var paths []string
	for _, it := range items {
		if it.Target.Path != "" {
			paths = append(paths, it.Target.Path)
		}
	}
	if len(paths) == 0 {
		m.status = "nothing to keep (item has no path)"
		return
	}
	if m.whitelist == nil {
		m.status = "whitelist unavailable; cannot keep"
		return
	}
	for _, p := range paths {
		if _, err := m.whitelist.Add(p); err != nil {
			m.status = "whitelist error: " + err.Error()
			return
		}
	}
	if err := m.whitelist.Save(); err != nil {
		m.status = "whitelist save failed: " + err.Error()
		return
	}
	for _, it := range items {
		if it.Target.Path == "" {
			continue
		}
		it.Kept = true
		it.Selected = false
	}
	m.status = fmt.Sprintf("kept %d path(s) in the whitelist", len(paths))
}

func selectable(items []*engine.PlannedItem) []*engine.PlannedItem {
	var out []*engine.PlannedItem
	for _, it := range items {
		if it.Target.AutoSelectable() {
			out = append(out, it)
		}
	}
	return out
}

func matchCleanItem(it *engine.PlannedItem, q string) bool {
	return strings.Contains(strings.ToLower(it.Target.Display), q) ||
		strings.Contains(strings.ToLower(it.Target.ID), q) ||
		strings.Contains(strings.ToLower(it.Target.Path), q)
}

func (m *CleanModel) rebuildRows() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	m.rows = nil
	for ci := range m.cats {
		cat := &m.cats[ci]
		type visibleGroup struct {
			group *engine.ItemGroup
			items []*engine.PlannedItem
		}
		var vgs []visibleGroup
		for gi := range cat.Groups {
			g := &cat.Groups[gi]
			var items []*engine.PlannedItem
			for _, it := range g.Items {
				if q == "" || matchCleanItem(it, q) || strings.Contains(strings.ToLower(g.Title), q) {
					items = append(items, it)
				}
			}
			if len(items) == 0 {
				continue
			}
			vgs = append(vgs, visibleGroup{group: g, items: items})
		}
		if len(vgs) == 0 {
			continue
		}
		m.rows = append(m.rows, cleanRow{kind: rowCategory, cat: cat})
		if m.collapsedCat[cat.ID] && q == "" {
			continue
		}
		for _, v := range vgs {
			if v.group.ID == "" {
				for _, it := range v.items {
					m.rows = append(m.rows, cleanRow{kind: rowItem, cat: cat, group: v.group, item: it})
				}
				continue
			}
			m.rows = append(m.rows, cleanRow{kind: rowGroup, cat: cat, group: v.group})
			if m.collapsedGrp[groupKey(cat.ID, v.group.ID)] && q == "" {
				continue
			}
			for _, it := range v.items {
				m.rows = append(m.rows, cleanRow{kind: rowItem, cat: cat, group: v.group, item: it})
			}
		}
	}
	m.clampCursor()
	m.ensureVisible()
}

func (m *CleanModel) ensureVisible() {
	rows := m.listHeight()
	if rows <= 0 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+rows {
		m.offset = m.cursor - rows + 1
	}
	if maxOffset := len(m.rows) - rows; m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *CleanModel) listHeight() int {
	h := m.height - 6
	if h < 1 {
		h = 1
	}
	return h
}

// View implements tea.Model.
func (m *CleanModel) View() string {
	if m.help.ShowAll {
		return m.help.FullHelpView(m.keys.fullHelp())
	}
	if m.screen == 1 {
		return m.reviewView()
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.list())
	b.WriteString("\n")
	b.WriteString(m.footer())
	return b.String()
}

func (m *CleanModel) header() string {
	line1 := m.styles.header.Render("clean \u2014 categorized cleanup")
	if m.platform != "" {
		line1 = m.styles.header.Render(m.platform)
	}
	t := m.plan.SelectedTotals()
	mode := ""
	if m.dryRun {
		mode = " \u00b7 dry-run"
	}
	line2 := m.styles.subtle.Render(fmt.Sprintf(
		"%d category(ies) \u00b7 %d item(s) \u00b7 selected %s across %d%s",
		len(m.cats), len(m.plan.Items), config.FormatBytes(t.Bytes), t.Count, mode))
	return line1 + "\n" + line2
}

func (m *CleanModel) list() string {
	if len(m.rows) == 0 {
		return m.styles.subtle.Render("  (nothing matched)")
	}
	rows := m.listHeight()
	end := m.offset + rows
	if end > len(m.rows) {
		end = len(m.rows)
	}
	var b strings.Builder
	for i := m.offset; i < end; i++ {
		b.WriteString(m.row(m.rows[i], i == m.cursor))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *CleanModel) row(r cleanRow, cursor bool) string {
	marker := " "
	if cursor {
		marker = ">"
	}
	var line string
	switch r.kind {
	case rowCategory:
		arrow := "\u25be"
		if m.collapsedCat[r.cat.ID] {
			arrow = "\u25b8"
		}
		line = fmt.Sprintf("%s [%s] %s %s  %s / %d items", marker, selectionMark(r.cat.Items), arrow,
			r.cat.Title, config.FormatBytes(r.cat.Bytes), len(r.cat.Items))
	case rowGroup:
		arrow := "\u25be"
		if m.collapsedGrp[groupKey(r.cat.ID, r.group.ID)] {
			arrow = "\u25b8"
		}
		line = fmt.Sprintf("%s   [%s] %s %s  %s / %d items", marker, selectionMark(r.group.Items), arrow,
			r.group.Title, config.FormatBytes(r.group.Bytes), len(r.group.Items))
	case rowItem:
		indent := "    "
		if r.group != nil && r.group.ID == "" {
			indent = "  "
		}
		line = marker + " " + indent + itemLine(r.item, m.color)
	}
	if cursor {
		return m.styles.cursor.Render(line)
	}
	return line
}

func selectionMark(items []*engine.PlannedItem) string {
	members := selectable(items)
	if len(members) == 0 {
		return " "
	}
	selected := 0
	for _, it := range members {
		if it.Selected {
			selected++
		}
	}
	switch {
	case selected == 0:
		return " "
	case selected == len(members):
		return "x"
	default:
		return "~"
	}
}

func (m *CleanModel) reviewView() string {
	var b strings.Builder
	b.WriteString(reviewText(m.plan, m.dryRun))
	b.WriteString("\nenter confirm  esc back  q quit\n")
	return b.String()
}

func (m *CleanModel) footer() string {
	if m.filtering {
		return m.filter.View()
	}
	line := m.help.ShortHelpView(m.keys.shortHelp())
	if m.status != "" {
		return line + "\n" + m.styles.subtle.Render(m.status)
	}
	return line
}

type cleanKeyMap struct {
	Up        key.Binding
	Down      key.Binding
	Toggle    key.Binding
	Expand    key.Binding
	Collapse  key.Binding
	SelectAll key.Binding
	Clear     key.Binding
	Keep      key.Binding
	Filter    key.Binding
	Review    key.Binding
	Help      key.Binding
	Quit      key.Binding
}

func defaultCleanKeys() cleanKeyMap {
	return cleanKeyMap{
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("\u2191/k", "up")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("\u2193/j", "down")),
		Toggle:    key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
		Expand:    key.NewBinding(key.WithKeys("right", "l", "tab"), key.WithHelp("\u2192/l", "expand")),
		Collapse:  key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("\u2190/h", "collapse")),
		SelectAll: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all-safe")),
		Clear:     key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "none")),
		Keep:      key.NewBinding(key.WithKeys("k"), key.WithHelp("k", "keep")),
		Filter:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Review:    key.NewBinding(key.WithKeys("enter", "r"), key.WithHelp("enter", "review")),
		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:      key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k cleanKeyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Toggle, k.SelectAll, k.Keep, k.Filter, k.Review, k.Help, k.Quit}
}

func (k cleanKeyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Expand, k.Collapse},
		{k.Toggle, k.SelectAll, k.Clear, k.Filter},
		{k.Keep, k.Review, k.Help, k.Quit},
	}
}

// RunClean runs the preview/review screens and reports whether the user
// confirmed. On abort or error the caller must not execute.
func RunClean(cfg CleanConfig) (confirmed, aborted bool, err error) {
	m := NewCleanModel(cfg)
	return m.Run()
}
