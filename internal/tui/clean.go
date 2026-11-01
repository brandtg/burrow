// Package tui holds the Bubble Tea models. clean.go implements the preview and
// review screens for `burrow clean`.
//
// Provenance: the categorized preview and the explicit review screen are
// behavioral ports of Mole's cleanup UX, reimplemented clean-room. No Mole
// source was copied.
package tui

import (
	"fmt"
	"io"
	"sort"
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

// RenderPreview writes the grouped preview table. The reclaimable total leads
// (one honest number, details below); large groups are capped so a category
// with hundreds of similar entries does not swamp the output.
func RenderPreview(w io.Writer, plan *engine.Plan, platform string, color bool) {
	if platform != "" {
		fmt.Fprintln(w, platform)
	}
	t := plan.SelectedTotals()
	fmt.Fprintf(w, "Reclaimable now: %s across %d items\n", config.FormatBytes(t.Bytes), t.Count)
	cw := planItemWidths(plan)
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
				fmt.Fprintf(w, "%s%s\n", indent, itemLine(it, cw, color))
				shown++
			}
			if extra := len(ig.Items) - shown; extra > 0 {
				fmt.Fprintf(w, "    \u2026 +%d more (use --json for the full list)\n", extra)
			}
		}
	}
	// Never-empty categories: a category that was scanned but found nothing
	// is visible as "nothing found" instead of vanishing, so "nothing
	// found", "not applicable", and "not scanned" stay distinguishable.
	withItems := map[string]bool{}
	for _, g := range plan.GroupedTree() {
		withItems[g.ID] = true
	}
	for _, ref := range plan.Scanned {
		if !withItems[ref.ID] {
			fmt.Fprintf(w, "\u25b8 %s  nothing found\n", ref.Title)
		}
	}
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

// RenderSummary writes the per-item results, totals, and journal path. The
// display column is width-aligned across the actual results (not a fixed
// width) so one long display name cannot desync the table.
func RenderSummary(w io.Writer, sum *engine.Summary, color bool) {
	items := sum.Executed()
	width := 0
	for _, it := range items {
		if n := displayWidth(it.Target.Display); n > width {
			width = n
		}
	}
	for _, it := range items {
		fmt.Fprintf(w, "  %-*s %s%s\n", width, it.Target.Display, statusLabel(it, color), reasonSuffix(it))
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

// colWidths carries the per-column paddings so item rows align vertically.
// Widths are computed once from every item in the plan, not per row.
type colWidths struct {
	display int
	size    int
	tag     int
	regen   int
}

func itemWidths(items []*engine.PlannedItem) colWidths {
	var cw colWidths
	for _, it := range items {
		if n := displayWidth(it.Target.Display); n > cw.display {
			cw.display = n
		}
		if n := len(sizeField(it.Target)); n > cw.size {
			cw.size = n
		}
		if n := len(tagField(it.Target)); n > cw.tag {
			cw.tag = n
		}
		if n := len(regenField(it.Target)); n > cw.regen {
			cw.regen = n
		}
	}
	return cw
}

// displayWidth is the rune count of a display name (display names are plain
// text; no ANSI escapes reach the Target).
func displayWidth(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func sizeField(t engine.Target) string {
	if !t.SizeKnown {
		return "unknown"
	}
	return config.FormatBytes(t.SizeBytes)
}

func tagField(t engine.Target) string {
	tag := "[trash]"
	if t.Recovery == engine.RecoveryOwner {
		tag = "[owner]"
	}
	if t.NeedsRoot {
		tag += " (root)"
	}
	return tag
}

func regenField(t engine.Target) string {
	regen := t.Regen.String()
	if t.Gated {
		regen += ",active"
	}
	return regen
}

// itemCountSuffix renders the count shown after an aggregate name.
func itemCountSuffix(n int) string {
	if n == 1 {
		return "(1 item)"
	}
	return fmt.Sprintf("(%d items)", n)
}

// headerField renders an aggregate row's name as "▾ title (N items)" padded
// to content+2 columns (the arrow and its space add two), so the aggregate
// size lands in the same column as item sizes across every indent level. The
// count is faint when color is on; padding is computed on plain text so
// styling cannot skew alignment.
func headerField(arrow, title string, count, content int, color bool) string {
	suffix := itemCountSuffix(count)
	gap := content - displayWidth(title) - displayWidth(suffix)
	if gap < 1 {
		gap = 1
	}
	if color {
		suffix = lipgloss.NewStyle().Faint(true).Render(suffix)
	}
	return arrow + " " + title + strings.Repeat(" ", gap) + suffix
}

// aggregateSize renders a folder or category total right-aligned in the size
// column, faint when color is on so sums read differently from file sizes.
func aggregateSize(bytes int64, width int, color bool) string {
	s := fmt.Sprintf("%*s", width, config.FormatBytes(bytes))
	if color {
		return lipgloss.NewStyle().Faint(true).Render(s)
	}
	return s
}

func itemLine(it *engine.PlannedItem, cw colWidths, color bool) string {
	check := "[ ]"
	switch {
	case it.Kept:
		check = "[kept]"
	case it.Selected:
		check = "[x]"
	}
	line := fmt.Sprintf("%s %-*s  %*s  %-*s %-*s %s",
		check, cw.display, it.Target.Display,
		cw.size, sizeField(it.Target),
		cw.tag, tagField(it.Target),
		cw.regen, regenField(it.Target),
		riskLabel(it.Target.Risk, color))
	return line
}

// planItemWidths sizes the columns from every item plus the aggregate rows:
// folder and category totals can be wider than any single item's size, and a
// long project title must not push its aggregate out of the size column.
func planItemWidths(plan *engine.Plan) colWidths {
	cw := itemWidths(plan.Items)
	fold := func(title string, count int, bytes int64) {
		if n := displayWidth(title) + 1 + len(itemCountSuffix(count)); n > cw.display {
			cw.display = n
		}
		if n := len(config.FormatBytes(bytes)); n > cw.size {
			cw.size = n
		}
	}
	for _, cat := range plan.GroupedTree() {
		fold(cat.Title, len(cat.Items), cat.Bytes)
		for _, g := range cat.Groups {
			fold(g.Title, len(g.Items), g.Bytes)
		}
	}
	return cw
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
	sortMode     sortMode

	filter    textinput.Model
	filtering bool

	screen    int // 0 preview, 1 review
	confirmed bool
	aborted   bool
	status    string

	// reviewOffset is the first visible line of the review screen's text.
	reviewOffset int

	help   help.Model
	keys   cleanKeyMap
	styles styles

	widths colWidths
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
		widths:       planItemWidths(cfg.Plan),
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
	if helpKey(&m.help, msg) {
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
		case "down", "j":
			m.reviewOffset++
		case "up", "k":
			m.reviewOffset--
		case "pgdown":
			m.reviewOffset += m.reviewHeight()
		case "pgup":
			m.reviewOffset -= m.reviewHeight()
		case "g", "home":
			m.reviewOffset = 0
		case "G", "end":
			// Clamped against the line count when rendered.
			m.reviewOffset = 1 << 30
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
	case key.Matches(msg, m.keys.PageUp):
		m.page(-1)
	case key.Matches(msg, m.keys.PageDown):
		m.page(1)
	case key.Matches(msg, m.keys.Top):
		m.setCursor(0)
	case key.Matches(msg, m.keys.Bottom):
		m.setCursor(len(m.rows) - 1)
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
	case key.Matches(msg, m.keys.SortName):
		m.toggleSort(sortName)
	case key.Matches(msg, m.keys.SortSize):
		m.toggleSort(sortSize)
	case key.Matches(msg, m.keys.Keep):
		m.keep()
	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		m.filter.Focus()
	case key.Matches(msg, m.keys.Review):
		m.screen = 1
		m.reviewOffset = 0
	}
	return m, nil
}

func (m *CleanModel) move(delta int) {
	m.setCursor(m.cursor + delta)
}

// setCursor jumps to an absolute row and keeps it visible.
func (m *CleanModel) setCursor(idx int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor = idx
	m.clampCursor()
	m.ensureVisible()
}

// page scrolls by one viewport height.
func (m *CleanModel) page(delta int) {
	m.setCursor(m.cursor + delta*m.listHeight())
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

// toggleSort applies the requested ordering; pressing the active sort's key
// again returns to the plan's discovery order.
func (m *CleanModel) toggleSort(mode sortMode) {
	if m.sortMode == mode {
		m.sortMode = sortDefault
	} else {
		m.sortMode = mode
	}
	m.rebuildRows()
}

// sortMode is the preview's row ordering. sortDefault keeps the plan's
// discovery order (categories in scan order, projects by path); the other
// modes re-sort every level of the tree.
type sortMode int

const (
	sortDefault sortMode = iota
	sortName
	sortSize
)

// itemSize orders items by measured bytes; unknown sizes sort last.
func itemSize(it *engine.PlannedItem) int64 {
	if !it.Target.SizeKnown {
		return -1
	}
	return it.Target.SizeBytes
}

func (m *CleanModel) rebuildRows() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	prev, hadPrev := m.currentRow()
	catOrder := make([]int, len(m.cats))
	for i := range catOrder {
		catOrder[i] = i
	}
	switch m.sortMode {
	case sortName:
		sort.SliceStable(catOrder, func(i, j int) bool {
			return strings.ToLower(m.cats[catOrder[i]].Title) < strings.ToLower(m.cats[catOrder[j]].Title)
		})
	case sortSize:
		sort.SliceStable(catOrder, func(i, j int) bool {
			return m.cats[catOrder[i]].Bytes > m.cats[catOrder[j]].Bytes
		})
	}
	m.rows = nil
	for _, ci := range catOrder {
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
		switch m.sortMode {
		case sortName:
			sort.SliceStable(vgs, func(i, j int) bool {
				return strings.ToLower(vgs[i].group.Title) < strings.ToLower(vgs[j].group.Title)
			})
			for _, v := range vgs {
				sort.SliceStable(v.items, func(i, j int) bool {
					return strings.ToLower(v.items[i].Target.Display) < strings.ToLower(v.items[j].Target.Display)
				})
			}
		case sortSize:
			sort.SliceStable(vgs, func(i, j int) bool {
				return vgs[i].group.Bytes > vgs[j].group.Bytes
			})
			for _, v := range vgs {
				sort.SliceStable(v.items, func(i, j int) bool {
					return itemSize(v.items[i]) > itemSize(v.items[j])
				})
			}
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
	if hadPrev {
		for i, r := range m.rows {
			if sameRow(r, prev) {
				m.cursor = i
				break
			}
		}
	}
	m.clampCursor()
	m.ensureVisible()
}

// currentRow returns the row under the cursor, if any.
func (m *CleanModel) currentRow() (cleanRow, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return cleanRow{}, false
	}
	return m.rows[m.cursor], true
}

// sameRow reports whether two rows point at the same plan entry.
func sameRow(a, b cleanRow) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case rowCategory:
		return a.cat == b.cat
	case rowGroup:
		return a.cat == b.cat && a.group == b.group
	default:
		return a.item == b.item
	}
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
	// Two header lines, a blank line before the list, a blank line before the
	// footer, and the footer (plus the status line when there is one).
	overhead := 4 + len(m.footerLines())
	if m.status != "" {
		overhead++
	}
	h := m.height - overhead
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
	sortLabel := "default"
	switch m.sortMode {
	case sortName:
		sortLabel = "name"
	case sortSize:
		sortLabel = "size"
	}
	line2 := m.styles.subtle.Render(fmt.Sprintf(
		"%d category(ies) \u00b7 %d item(s) \u00b7 selected %s across %d \u00b7 sort %s%s",
		len(m.cats), len(m.plan.Items), config.FormatBytes(t.Bytes), t.Count, sortLabel, mode))
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
		line = fmt.Sprintf("%s [%s] %s  %s", marker, selectionMark(r.cat.Items),
			headerField(arrow, r.cat.Title, len(r.cat.Items), m.widths.display+2, m.color),
			aggregateSize(r.cat.Bytes, m.widths.size, m.color))
	case rowGroup:
		arrow := "\u25be"
		if m.collapsedGrp[groupKey(r.cat.ID, r.group.ID)] {
			arrow = "\u25b8"
		}
		line = fmt.Sprintf("%s   [%s] %s  %s", marker, selectionMark(r.group.Items),
			headerField(arrow, r.group.Title, len(r.group.Items), m.widths.display, m.color),
			aggregateSize(r.group.Bytes, m.widths.size, m.color))
	case rowItem:
		indent := "    "
		w := m.widths
		if r.group != nil && r.group.ID == "" {
			indent = "  "
			// One indent level shallower than grouped rows: widen the name
			// field by the same two columns so every size shares one column.
			w.display += 2
		}
		line = marker + " " + indent + itemLine(r.item, w, m.color)
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
	lines := strings.Split(reviewText(m.plan, m.dryRun), "\n")
	height := m.reviewHeight()
	m.clampReviewOffset(len(lines), height)
	start, end := m.reviewOffset, m.reviewOffset+height
	if end > len(lines) {
		end = len(lines)
	}
	var b strings.Builder
	if m.platform != "" {
		b.WriteString(m.styles.header.Render(m.platform) + "\n\n")
	}
	for _, l := range lines[start:end] {
		b.WriteString(l + "\n")
	}
	foot := "enter confirm  esc back  q quit"
	if height < len(lines) {
		foot = fmt.Sprintf("%d\u2013%d of %d \u00b7 j/k scroll \u00b7 %s", start+1, end, len(lines), foot)
	}
	b.WriteString("\n" + m.styles.subtle.Render(foot))
	return b.String()
}

// reviewHeight is the number of review lines that fit between the platform
// header and the footer.
func (m *CleanModel) reviewHeight() int {
	h := m.height - 6
	if h < 3 {
		h = 3
	}
	return h
}

func (m *CleanModel) clampReviewOffset(n, height int) {
	if m.reviewOffset > n-height {
		m.reviewOffset = n - height
	}
	if m.reviewOffset < 0 {
		m.reviewOffset = 0
	}
}

func (m *CleanModel) footer() string {
	out := strings.Join(m.footerLines(), "\n")
	if m.status != "" {
		out += "\n" + m.styles.subtle.Render(m.status)
	}
	return out
}

// footerLines renders the short help as at most two lines so the list keeps a
// predictable height.
func (m *CleanModel) footerLines() []string {
	if m.filtering {
		return []string{m.filter.View()}
	}
	lines := renderKeyHelp(m.keys.shortHelp(), m.width)
	for i := range lines {
		lines[i] = m.styles.subtle.Render(lines[i])
	}
	return lines
}

type cleanKeyMap struct {
	Up        key.Binding
	Down      key.Binding
	PageUp    key.Binding
	PageDown  key.Binding
	Top       key.Binding
	Bottom    key.Binding
	Toggle    key.Binding
	Expand    key.Binding
	Collapse  key.Binding
	SelectAll key.Binding
	Clear     key.Binding
	SortName  key.Binding
	SortSize  key.Binding
	Keep      key.Binding
	Filter    key.Binding
	Review    key.Binding
	Help      key.Binding
	Quit      key.Binding
}

// Shared keymap rationale: enter acts on the selected row (here: opens the
// review screen) and →/← navigate the tree in both models. n means sort by name
// and s means sort by size in both models, so select-none moved to N.
func defaultCleanKeys() cleanKeyMap {
	return cleanKeyMap{
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("\u2191/k", "up")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("\u2193/j", "down")),
		PageUp:    key.NewBinding(key.WithKeys("pgup", "ctrl+b"), key.WithHelp("pgup/^b", "page up")),
		PageDown:  key.NewBinding(key.WithKeys("pgdown", "ctrl+f"), key.WithHelp("pgdn/^f", "page down")),
		Top:       key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g/home", "top")),
		Bottom:    key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G/end", "bottom")),
		Toggle:    key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "toggle")),
		Expand:    key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("\u2192/l", "expand")),
		Collapse:  key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("\u2190/h", "collapse")),
		SelectAll: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "all safe")),
		Clear:     key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "none")),
		SortName:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort name")),
		SortSize:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort size")),
		// Shift-K: lowercase k is the vim "up" binding, so the whitelist key
		// must be distinct or it is unreachable.
		Keep:   key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "keep")),
		Filter: key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Review: key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "review")),
		Help:   key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "all keys")),
		Quit:   key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// shortHelp lists the most-used bindings compactly. renderKeyHelp wraps it to
// two lines; when it cannot fit, the footer says "? all keys", which lists the
// rest (page keys, g/G) in the full help view.
func (k cleanKeyMap) shortHelp() []key.Binding {
	return []key.Binding{
		k.Up, k.Down, k.Toggle, k.Expand, k.Collapse,
		k.SortName, k.SortSize, k.SelectAll, k.Clear, k.Keep, k.Filter,
		k.Review, k.Help, k.Quit,
	}
}

func (k cleanKeyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
		{k.Expand, k.Collapse, k.Toggle, k.SelectAll, k.Clear},
		{k.SortName, k.SortSize, k.Keep, k.Filter, k.Review, k.Help, k.Quit},
	}
}

// RunClean runs the preview/review screens and reports whether the user
// confirmed. On abort or error the caller must not execute.
func RunClean(cfg CleanConfig) (confirmed, aborted bool, err error) {
	m := NewCleanModel(cfg)
	return m.Run()
}
