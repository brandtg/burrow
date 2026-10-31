// Package tui holds burrow's Bubble Tea models. This file is the read-only
// analyze explorer: a navigable tree. It never mutates the filesystem; the
// only deletion path in burrow is the clean flow's review screen.
//
// Provenance: the UI shape and keybindings mirror Mole's analyze TUI
// behaviourally; this is an independent implementation (Mole is GPL-3.0, no
// Mole source was copied).
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/scan"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sys/unix"
)

// SortMode selects the ordering of the visible list.
type SortMode int

const (
	// SortBySize orders largest first (default).
	SortBySize SortMode = iota
	// SortByName orders case-insensitively by name.
	SortByName
	// SortByMtime orders most recent first.
	SortByMtime
	// SortByCount orders most entries first.
	SortByCount
)

func (s SortMode) String() string {
	switch s {
	case SortByName:
		return "name"
	case SortByMtime:
		return "mtime"
	case SortByCount:
		return "count"
	default:
		return "size"
	}
}

// LoadFunc scans the analyze roots, reporting progress until it finishes. It
// runs on a background goroutine owned by the model.
type LoadFunc func(ctx context.Context, progress func(scan.Progress)) (*scan.Node, error)

// AnalyzeConfig carries the dependencies the analyze model needs. Root is the
// already-scanned tree; the model never mutates it.
//
// Root and Load are mutually exclusive: when Root is nil the model starts in a
// loading state and calls Load, rendering progress until the tree is ready.
type AnalyzeConfig struct {
	Root *scan.Node
	Load LoadFunc

	Apparent bool
	Color    bool

	CategoryRoots []string
	// RootSelectors maps a CategoryRoots entry to the --only selector that
	// scopes a clean run to it. When a cursor path is inside a root and has
	// a selector, the view names the exact `burrow clean --only <sel>`.
	RootSelectors map[string]string
}

type progressMsg scan.Progress

type loadDoneMsg struct {
	root *scan.Node
	err  error
}

// AnalyzeModel is the Bubble Tea model for `burrow analyze`.
type AnalyzeModel struct {
	cfg  AnalyzeConfig
	root *scan.Node

	dir     *scan.Node
	parents map[*scan.Node]*scan.Node
	entries []*scan.Node
	unknown int

	cursor int
	offset int
	sort   SortMode

	filter    textinput.Model
	filtering bool

	loading     bool
	loadErr     error
	progress    scan.Progress
	hasProgress bool
	spin        spinner.Model
	events      chan tea.Msg
	cancel      context.CancelFunc

	width  int
	height int

	help help.Model
	keys keyMap

	styles styles
}

// NewAnalyzeModel builds the analyze model from a scanned tree.
func NewAnalyzeModel(cfg AnalyzeConfig) *AnalyzeModel {
	filter := textinput.New()
	filter.Placeholder = "filter"
	filter.Prompt = "/"

	m := &AnalyzeModel{
		cfg:     cfg,
		root:    cfg.Root,
		dir:     cfg.Root,
		parents: map[*scan.Node]*scan.Node{},
		sort:    SortBySize,
		filter:  filter,
		help:    help.New(),
		keys:    defaultKeys(),
		styles:  newStyles(cfg.Color),
		spin:    spinner.New(spinner.WithSpinner(spinner.Dot)),
		height:  24,
		width:   80,
	}
	if cfg.Root != nil {
		m.indexParents(cfg.Root)
		m.rebuildEntries()
		m.unknown = scan.UnknownCount(cfg.Root)
	}
	return m
}

func (m *AnalyzeModel) indexParents(n *scan.Node) {
	if n == nil {
		return
	}
	for _, c := range n.Children {
		m.parents[c] = n
		m.indexParents(c)
	}
}

// Run starts the Bubble Tea program.
func (m *AnalyzeModel) Run() error {
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	if m.cancel != nil {
		m.cancel()
	}
	if err != nil {
		return err
	}
	return m.loadErr
}

// Init implements tea.Model. When the tree was not pre-scanned it starts the
// background load and returns a command that waits for the first event.
func (m *AnalyzeModel) Init() tea.Cmd {
	if m.root != nil || m.cfg.Load == nil {
		return nil
	}
	m.loading = true
	m.events = make(chan tea.Msg, 64)
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	return tea.Batch(m.startLoad(ctx), m.spin.Tick, waitForEvent(m.events))
}

// startLoad runs the scan on a goroutine, forwarding progress and a final
// done message over m.events.
func (m *AnalyzeModel) startLoad(ctx context.Context) tea.Cmd {
	return func() tea.Msg {
		go func() {
			root, err := m.cfg.Load(ctx, func(p scan.Progress) {
				select {
				case m.events <- progressMsg(p):
				case <-ctx.Done():
				}
			})
			select {
			case m.events <- loadDoneMsg{root: root, err: err}:
			case <-ctx.Done():
			}
		}()
		return nil
	}
}

func waitForEvent(ch <-chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

// Update implements tea.Model.
func (m *AnalyzeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.help.Width = msg.Width
		return m, nil
	case progressMsg:
		m.progress = scan.Progress(msg)
		m.hasProgress = true
		return m, waitForEvent(m.events)
	case loadDoneMsg:
		return m.finishLoad(msg)
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		if m.loading {
			switch msg.String() {
			case "q", "ctrl+c", "esc":
				if m.cancel != nil {
					m.cancel()
				}
				return m, tea.Quit
			}
			return m, nil
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *AnalyzeModel) finishLoad(msg loadDoneMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		m.loadErr = msg.err
		return m, nil
	}
	m.root = msg.root
	m.dir = msg.root
	m.indexParents(m.root)
	m.rebuildEntries()
	m.unknown = scan.UnknownCount(m.root)
	return m, nil
}

func (m *AnalyzeModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filter.SetValue("")
			m.filter.Blur()
			m.filtering = false
			m.rebuildEntries()
			return m, nil
		case "enter":
			m.filter.Blur()
			m.filtering = false
			m.rebuildEntries()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.rebuildEntries()
		return m, cmd
	}
	if helpKey(&m.help, msg) {
		return m, nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
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
		m.setCursor(len(m.entries) - 1)
	case key.Matches(msg, m.keys.Enter):
		m.enter()
	case key.Matches(msg, m.keys.Parent):
		m.parent()
	case key.Matches(msg, m.keys.SortSize):
		m.setSort(SortBySize)
	case key.Matches(msg, m.keys.SortName):
		m.setSort(SortByName)
	case key.Matches(msg, m.keys.SortMtime):
		m.setSort(SortByMtime)
	case key.Matches(msg, m.keys.SortCount):
		m.setSort(SortByCount)
	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		m.filter.Focus()
	}
	return m, nil
}

func (m *AnalyzeModel) move(delta int) {
	m.setCursor(m.cursor + delta)
}

// setCursor jumps to an absolute row and keeps it visible.
func (m *AnalyzeModel) setCursor(idx int) {
	if len(m.entries) == 0 {
		return
	}
	m.cursor = idx
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
	m.ensureVisible()
}

// page scrolls by one viewport height.
func (m *AnalyzeModel) page(delta int) {
	m.setCursor(m.cursor + delta*m.listHeight())
}

func (m *AnalyzeModel) enter() {
	if m.cursor >= len(m.entries) {
		return
	}
	n := m.entries[m.cursor]
	if !n.IsDir || len(n.Children) == 0 {
		return
	}
	m.dir = n
	m.cursor = 0
	m.offset = 0
	m.rebuildEntries()
}

func (m *AnalyzeModel) parent() {
	if m.dir == m.root {
		return
	}
	p, ok := m.parents[m.dir]
	if !ok {
		return
	}
	child := m.dir
	m.dir = p
	m.offset = 0
	m.rebuildEntries()
	// Place the cursor on the directory we came out of. This must happen after
	// rebuildEntries, which re-sorts m.entries; indexing into p.Children (the
	// unsorted slice) would land on an arbitrary row.
	for i, c := range m.entries {
		if c == child {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}

// setSort re-sorts the visible entries while keeping the cursor on the entry it
// was on before the sort, so a sort never teleports the selection.
func (m *AnalyzeModel) setSort(s SortMode) {
	if m.sort == s {
		return
	}
	var cur *scan.Node
	if m.cursor >= 0 && m.cursor < len(m.entries) {
		cur = m.entries[m.cursor]
	}
	m.sort = s
	m.rebuildEntries()
	if cur == nil {
		return
	}
	for i, n := range m.entries {
		if n == cur {
			m.cursor = i
			break
		}
	}
	m.ensureVisible()
}

func (m *AnalyzeModel) rebuildEntries() {
	if m.dir == nil {
		m.entries = nil
		return
	}
	var out []*scan.Node
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	for _, c := range m.dir.Children {
		if q != "" && !strings.Contains(strings.ToLower(c.Name), q) {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch m.sort {
		case SortByName:
			return strings.ToLower(a.Name) < strings.ToLower(b.Name)
		case SortByMtime:
			if !a.Mtime.Equal(b.Mtime) {
				return a.Mtime.After(b.Mtime)
			}
		case SortByCount:
			if a.ItemCount != b.ItemCount {
				return a.ItemCount > b.ItemCount
			}
		default:
			if va, vb := m.sizeValue(a), m.sizeValue(b); va != vb {
				return va > vb
			}
		}
		// Name is the deterministic tiebreak so equal keys keep a stable,
		// predictable order across sorts.
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	m.entries = out
	if m.cursor >= len(out) {
		m.cursor = len(out) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureVisible()
}

func (m *AnalyzeModel) ensureVisible() {
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
	if maxOffset := len(m.entries) - rows; m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *AnalyzeModel) listHeight() int {
	// path, this-dir totals, stats, blank before list, blank before footer,
	// footer.
	overhead := 6
	if m.unknown > 0 {
		overhead++
	}
	if m.root != nil && len(m.root.SkippedMounts) > 0 {
		overhead++
	}
	if len(m.footerLines()) > 1 {
		overhead++
	}
	h := m.height - overhead
	if h < 1 {
		h = 1
	}
	return h
}

// View implements tea.Model.
func (m *AnalyzeModel) View() string {
	if m.loading || m.loadErr != nil {
		return m.loadingView()
	}
	if m.help.ShowAll {
		return m.helpView()
	}
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n")
	b.WriteString(m.list())
	b.WriteString("\n")
	if hint := m.reclaimHint(); hint != "" {
		b.WriteString(m.styles.warn.Render(hint) + "\n")
	}
	b.WriteString(m.footer())
	return b.String()
}

func (m *AnalyzeModel) loadingView() string {
	var b strings.Builder
	b.WriteString(m.styles.header.Render("Analyzing") + "\n\n")
	if m.loadErr != nil {
		b.WriteString(m.styles.warn.Render("  scan failed: "+m.loadErr.Error()) + "\n")
		b.WriteString("\n" + m.styles.subtle.Render("  q quit") + "\n")
		return b.String()
	}
	b.WriteString("  " + m.spin.View() + " scanning")
	if m.hasProgress && (m.progress.ItemCount > 0 || m.progress.TotalUsage > 0) {
		fmt.Fprintf(&b, " \u00b7 %d items \u00b7 %s", m.progress.ItemCount, config.FormatBytes(m.progress.TotalUsage))
	}
	b.WriteString("\n")
	if m.hasProgress && m.progress.CurrentDir != "" {
		b.WriteString(m.styles.subtle.Render("  "+truncatePath(m.progress.CurrentDir, m.width-4)) + "\n")
	}
	b.WriteString("\n" + m.styles.subtle.Render("  q cancel") + "\n")
	return b.String()
}

// truncatePath shortens p from the left so the deepest directory stays visible.
func truncatePath(p string, width int) string {
	r := []rune(p)
	if width < 8 || len(r) <= width {
		return p
	}
	return "\u2026" + string(r[len(r)-(width-1):])
}

func (m *AnalyzeModel) header() string {
	free := "unknown"
	if m.dir != nil {
		if total, avail, err := statfs(m.dir.Path); err == nil {
			free = fmt.Sprintf("%s free of %s", config.FormatBytes(int64(avail)), config.FormatBytes(int64(total)))
		}
	}
	line1 := ""
	if m.dir != nil {
		line1 = m.styles.header.Render(m.dir.Path)
	}
	line2 := m.styles.subtle.Render(m.dirTotalsLine())
	line3 := m.styles.subtle.Render(fmt.Sprintf("%s · %d item(s) · sort %s",
		free, len(m.entries), m.sort))
	var warn string
	if m.unknown > 0 {
		warn += m.styles.warn.Render(fmt.Sprintf(
			"partial scan: %d item(s) could not be read (size shown as unknown)", m.unknown)) + "\n"
	}
	if m.root != nil && len(m.root.SkippedMounts) > 0 {
		warn += m.styles.warn.Render(fmt.Sprintf(
			"skipped %d mount(s) on other filesystems: %s", len(m.root.SkippedMounts), skippedMountPaths(m.root.SkippedMounts))) + "\n"
	}
	return line1 + "\n" + warn + line2 + "\n" + line3
}

// dirTotalsLine states the size of the directory being explored and, when it
// has a parent in the scanned tree, how much of that parent it accounts for.
func (m *AnalyzeModel) dirTotalsLine() string {
	if m.dir == nil {
		return ""
	}
	line := "this dir: " + m.sizeLabel(m.dir)
	if parent := m.parents[m.dir]; parent != nil {
		if ps := m.sizeValue(parent); ps > 0 {
			line += fmt.Sprintf(" (%.1f%% of parent)", float64(m.sizeValue(m.dir))*100/float64(ps))
		}
	}
	return line
}

// skippedMountPaths renders the skipped mount points, bounded so a host with
// many mounts does not overflow the header.
func skippedMountPaths(skips []scan.SkippedMount) string {
	const max = 3
	paths := make([]string, 0, max)
	for i, s := range skips {
		if i == max {
			paths = append(paths, "…")
			break
		}
		paths = append(paths, s.Path)
	}
	return strings.Join(paths, ", ")
}

// rowLayout carries the columns that fit at the current terminal width. The
// name column absorbs whatever is left; optional columns are dropped from the
// right as the terminal narrows so the row never overflows.
type rowLayout struct {
	bar      bool
	pct      bool
	mtime    bool
	count    bool
	nameCols int
}

func (m *AnalyzeModel) rowLayout() rowLayout {
	l := rowLayout{bar: true, pct: true, mtime: true, count: m.sort == SortByCount}
	width := m.width
	if width <= 0 {
		width = 80
	}
	const (
		// marker, space, [R], space, size, space.
		baseCols = 1 + 1 + 3 + 1 + 10 + 1
		pctCols  = 6 + 1
		// "Jan 02 15:04" plus its separator.
		mtimeCols = 12 + 1
		countCols = 7 + 1
		// "[##########]" plus its separator.
		barCols = 10 + 2 + 1
	)
	fixed := baseCols + pctCols
	if l.mtime {
		fixed += mtimeCols
	}
	if l.count {
		fixed += countCols
	}
	if l.bar {
		fixed += barCols
	}
	name := width - fixed
	const minName = 10
	if name < minName && l.bar {
		l.bar = false
		name += barCols
	}
	if name < minName && l.mtime {
		l.mtime = false
		name += mtimeCols
	}
	if name < minName && l.count {
		l.count = false
		name += countCols
	}
	if name < minName && l.pct {
		l.pct = false
		name += pctCols
	}
	if name < 4 {
		name = 4
	}
	l.nameCols = name
	return l
}

func (m *AnalyzeModel) list() string {
	if len(m.entries) == 0 {
		return m.styles.subtle.Render("  (empty)")
	}
	maxSize := int64(1)
	for _, n := range m.entries {
		if s := m.sizeValue(n); s > maxSize {
			maxSize = s
		}
	}
	rows := m.listHeight()
	end := m.offset + rows
	if end > len(m.entries) {
		end = len(m.entries)
	}
	layout := m.rowLayout()
	var b strings.Builder
	for i := m.offset; i < end; i++ {
		n := m.entries[i]
		b.WriteString(m.row(n, i == m.cursor, maxSize, layout))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *AnalyzeModel) row(n *scan.Node, cursor bool, maxSize int64, layout rowLayout) string {
	marker := " "
	if cursor {
		marker = ">"
	}
	reclaim := "   "
	if m.isCategoryRoot(n.Path) {
		reclaim = "[R]"
	}
	var b strings.Builder
	b.WriteString(marker + " " + reclaim + " ")
	if layout.bar {
		b.WriteString("[" + m.bar(n, maxSize) + "] ")
	}
	fmt.Fprintf(&b, "%10s ", m.sizeLabel(n))
	if layout.pct {
		fmt.Fprintf(&b, "%6s ", m.pctLabel(n))
	}
	if layout.mtime {
		b.WriteString(padRight(mtimeLabel(n), 12) + " ")
	}
	if layout.count {
		fmt.Fprintf(&b, "%7d ", n.ItemCount)
	}
	name := n.Name
	if n.IsDir {
		name += "/"
	}
	b.WriteString(clip(name, layout.nameCols))
	line := b.String()
	if cursor {
		return m.styles.cursor.Render(line)
	}
	if n.IsDir {
		return m.styles.dir.Render(line)
	}
	return line
}

// pctLabel is this entry's share of the directory being displayed.
func (m *AnalyzeModel) pctLabel(n *scan.Node) string {
	if !n.SizeKnown {
		return "-"
	}
	parent := m.sizeValue(m.dir)
	if parent <= 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f%%", float64(m.sizeValue(n))*100/float64(parent))
}

// mtimeLabel renders the modification time that sort-by-mtime orders on.
func mtimeLabel(n *scan.Node) string {
	if n.Mtime.IsZero() {
		return "\u2014"
	}
	return n.Mtime.Format("Jan 02 15:04")
}

// clip shortens s to width cells, marking the cut with an ellipsis.
func clip(s string, width int) string {
	if width <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= width {
		return s
	}
	if width == 1 {
		return "\u2026"
	}
	return string(r[:width-1]) + "\u2026"
}

func padRight(s string, width int) string {
	if w := lipgloss.Width(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}

func (m *AnalyzeModel) bar(n *scan.Node, maxSize int64) string {
	const width = 10
	v := m.sizeValue(n)
	filled := 0
	if maxSize > 0 {
		filled = int(v * width / maxSize)
	}
	if filled > width {
		filled = width
	}
	return strings.Repeat("#", filled) + strings.Repeat(".", width-filled)
}

func (m *AnalyzeModel) sizeValue(n *scan.Node) int64 {
	if m.cfg.Apparent {
		return n.Apparent
	}
	return n.Size
}

func (m *AnalyzeModel) sizeLabel(n *scan.Node) string {
	if !n.SizeKnown {
		return "unknown"
	}
	return config.FormatBytes(m.sizeValue(n))
}

func (m *AnalyzeModel) isCategoryRoot(path string) bool {
	for _, root := range m.cfg.CategoryRoots {
		if safe.WithinRoot(path, root) {
			return true
		}
	}
	return false
}

// reclaimHint names the clean invocation for the cursor's location when it
// sits inside a directory the clean flow can reclaim. Read-only: analyze
// never offers a delete key, the hint only points at the clean flow.
func (m *AnalyzeModel) reclaimHint() string {
	if len(m.cfg.CategoryRoots) == 0 {
		return ""
	}
	if m.cursor < 0 || m.cursor >= len(m.entries) {
		return ""
	}
	path := m.entries[m.cursor].Path
	best := ""
	for _, root := range m.cfg.CategoryRoots {
		if safe.WithinRoot(path, root) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return ""
	}
	if sel := m.cfg.RootSelectors[best]; sel != "" {
		return fmt.Sprintf("reclaimable \u2014 run burrow clean --only %s", sel)
	}
	return "reclaimable \u2014 run burrow clean"
}

func (m *AnalyzeModel) footer() string {
	return strings.Join(m.footerLines(), "\n")
}

// footerLines renders the short help as at most two lines so the list keeps a
// predictable height.
func (m *AnalyzeModel) footerLines() []string {
	if m.filtering {
		return []string{m.filter.View()}
	}
	lines := renderKeyHelp(m.keys.shortHelp(), m.width)
	for i := range lines {
		lines[i] = m.styles.subtle.Render(lines[i])
	}
	return lines
}

func (m *AnalyzeModel) helpView() string {
	return m.help.FullHelpView(m.keys.fullHelp())
}

// helpKey handles a key while the full help overlay is open. It reports whether
// the overlay consumed the key. q and esc close the overlay rather than quit, so
// the next q still reaches the quit binding.
func helpKey(h *help.Model, msg tea.KeyMsg) bool {
	if !h.ShowAll {
		return false
	}
	switch msg.String() {
	case "?", "esc", "q":
		h.ShowAll = false
	}
	return true
}

// renderKeyHelp packs bindings into at most two lines of width columns. The last
// two bindings (help and quit by convention) are always shown: they are how a
// user reaches the bindings the footer could not fit, so the footer is never a
// lie about what exists.
func renderKeyHelp(bindings []key.Binding, width int) []string {
	if width <= 0 {
		width = 80
	}
	const sep = " \u00b7 "
	var items []string
	for _, kb := range bindings {
		if !kb.Enabled() {
			continue
		}
		items = append(items, kb.Help().Key+" "+kb.Help().Desc)
	}
	if len(items) == 0 {
		return nil
	}

	head := items
	tail := items[0]
	if len(items) >= 2 {
		head = items[:len(items)-2]
		tail = items[len(items)-2] + sep + items[len(items)-1]
	}
	if len(head) == 0 {
		return []string{clip(tail, width)}
	}
	tailReserve := lipgloss.Width(sep) + lipgloss.Width(tail)
	if tailReserve+8 > width {
		return []string{clip(strings.Join(items, sep), width)}
	}

	var lines []string
	cur := ""
	for _, it := range head {
		limit := width
		if len(lines) == 1 {
			limit = width - tailReserve
		}
		cand := it
		if cur != "" {
			cand = cur + sep + it
		}
		if lipgloss.Width(cand) <= limit {
			cur = cand
			continue
		}
		if cur != "" {
			lines = append(lines, cur)
			cur = clip(it, limit)
			if len(lines) == 2 {
				cur = ""
				break
			}
			continue
		}
		cur = clip(it, limit)
	}
	if cur != "" {
		lines = append(lines, cur)
	}

	switch len(lines) {
	case 0:
		return []string{clip(tail, width)}
	case 1:
		if joined := lines[0] + sep + tail; lipgloss.Width(joined) <= width {
			return []string{joined}
		}
		return []string{lines[0], tail}
	default:
		return []string{lines[0], lines[1] + sep + tail}
	}
}

// statfs returns total and available bytes for the filesystem holding path.
func statfs(path string) (total, avail uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.Bsize)
	return st.Blocks * bsize, st.Bavail * bsize, nil
}

type keyMap struct {
	Up        key.Binding
	Down      key.Binding
	PageUp    key.Binding
	PageDown  key.Binding
	Top       key.Binding
	Bottom    key.Binding
	Enter     key.Binding
	Parent    key.Binding
	SortSize  key.Binding
	SortName  key.Binding
	SortMtime key.Binding
	SortCount key.Binding
	Filter    key.Binding
	Help      key.Binding
	Quit      key.Binding
}

// Rationale for the shared keymap: enter means "act on the selected row" and
// →/← mean navigation in both the analyze and clean models, so muscle memory
// carries between them.
func defaultKeys() keyMap {
	return keyMap{
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("\u2191/k", "up")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("\u2193/j", "down")),
		PageUp:    key.NewBinding(key.WithKeys("pgup", "ctrl+b"), key.WithHelp("pgup/^b", "page up")),
		PageDown:  key.NewBinding(key.WithKeys("pgdown", "ctrl+f"), key.WithHelp("pgdn/^f", "page down")),
		Top:       key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g/home", "top")),
		Bottom:    key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G/end", "bottom")),
		Enter:     key.NewBinding(key.WithKeys("enter", "right", "l"), key.WithHelp("enter/\u2192/l", "open dir")),
		Parent:    key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("\u2190/h", "back")),
		SortSize:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort size")),
		SortName:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort name")),
		SortMtime: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "sort mtime")),
		SortCount: key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "sort count")),
		Filter:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "all keys")),
		Quit:      key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// shortHelp lists every binding in a compact order: navigation, sorting, then
// the paging keys. renderKeyHelp wraps it to two lines and always keeps the
// help and quit bindings.
func (k keyMap) shortHelp() []key.Binding {
	return []key.Binding{
		k.Up, k.Down, k.Enter, k.Parent,
		k.SortSize, k.SortName, k.SortMtime, k.SortCount,
		k.Filter, k.PageUp, k.PageDown, k.Top, k.Bottom,
		k.Help, k.Quit,
	}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom},
		{k.Enter, k.Parent},
		{k.SortSize, k.SortName, k.SortMtime, k.SortCount},
		{k.Filter, k.Help, k.Quit},
	}
}

type styles struct {
	header lipgloss.Style
	subtle lipgloss.Style
	cursor lipgloss.Style
	dir    lipgloss.Style
	warn   lipgloss.Style
}

func newStyles(color bool) styles {
	if !color {
		return styles{}
	}
	return styles{
		header: lipgloss.NewStyle().Bold(true),
		subtle: lipgloss.NewStyle().Faint(true),
		cursor: lipgloss.NewStyle().Reverse(true),
		dir:    lipgloss.NewStyle().Foreground(lipgloss.Color("12")),
		warn:   lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
	}
}
