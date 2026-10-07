// Package tui holds burrow's Bubble Tea models. This file is the read-only
// analyze explorer: a navigable tree with trash keys routed through the Trash
// module.
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
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/scan"
	"github.com/brandtg/burrow/internal/trash"
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
)

func (s SortMode) String() string {
	switch s {
	case SortByName:
		return "name"
	case SortByMtime:
		return "mtime"
	default:
		return "size"
	}
}

// LoadFunc scans the analyze roots, reporting progress until it finishes. It
// runs on a background goroutine owned by the model.
type LoadFunc func(ctx context.Context, progress func(scan.Progress)) (*scan.Node, error)

// AnalyzeConfig carries the dependencies the analyze model needs. Root is the
// already-scanned tree; the model never mutates it except by removing nodes it
// has successfully trashed.
//
// Root and Load are mutually exclusive: when Root is nil the model starts in a
// loading state and calls Load, rendering progress until the tree is ready.
type AnalyzeConfig struct {
	Root *scan.Node
	Load LoadFunc

	Apparent bool
	NoDelete bool
	Color    bool

	TrashDir string
	UID      int

	Validator     *safe.Validator
	Journal       *journal.Writer
	CategoryRoots []string
	Command       string
}

type progressMsg scan.Progress

type loadDoneMsg struct {
	root *scan.Node
	err  error
}

// AnalyzeModel is the Bubble Tea model for `burrow analyze`.
type AnalyzeModel struct {
	cfg  AnalyzeConfig
	exec *TrashExec

	root    *scan.Node
	dir     *scan.Node
	parents map[*scan.Node]*scan.Node
	entries []*scan.Node
	unknown int

	cursor int
	offset int
	sort   SortMode
	showCt bool

	selected      map[string]bool
	selectedNodes map[string]*scan.Node

	filter    textinput.Model
	filtering bool

	confirm bool
	pending []deleteTarget

	status string
	freed  int64

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

type deleteTarget struct {
	node *scan.Node
	snap *safe.Snapshot
	size int64
}

// NewAnalyzeModel builds the analyze model from a scanned tree.
func NewAnalyzeModel(cfg AnalyzeConfig) *AnalyzeModel {
	filter := textinput.New()
	filter.Placeholder = "filter"
	filter.Prompt = "/"

	m := &AnalyzeModel{
		cfg:           cfg,
		root:          cfg.Root,
		dir:           cfg.Root,
		parents:       map[*scan.Node]*scan.Node{},
		sort:          SortBySize,
		selected:      map[string]bool{},
		selectedNodes: map[string]*scan.Node{},
		filter:        filter,
		help:          help.New(),
		keys:          defaultKeys(),
		styles:        newStyles(cfg.Color),
		spin:          spinner.New(spinner.WithSpinner(spinner.Dot)),
		height:        24,
		width:         80,
	}
	m.exec = &TrashExec{
		Trash:     trash.New(cfg.TrashDir, cfg.UID),
		Validator: cfg.Validator,
		Journal:   cfg.Journal,
		Command:   cfg.Command,
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

// Run starts the Bubble Tea program and closes the journal run frame on exit.
func (m *AnalyzeModel) Run() error {
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	if m.cancel != nil {
		m.cancel()
	}
	if err != nil {
		return err
	}
	if m.loadErr != nil {
		return m.loadErr
	}
	return m.exec.EndRun(m.freed)
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
	if m.confirm {
		switch msg.String() {
		case "y", "Y":
			m.executeDelete()
		default:
			m.confirm = false
			m.pending = nil
			m.status = "delete cancelled"
		}
		return m, nil
	}
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
	if m.help.ShowAll {
		if msg.String() == "?" || msg.String() == "esc" || msg.String() == "q" {
			m.help.ShowAll = false
		}
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
	case key.Matches(msg, m.keys.Count):
		m.showCt = !m.showCt
	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		m.filter.Focus()
	case key.Matches(msg, m.keys.Delete):
		m.beginDelete(false)
	case key.Matches(msg, m.keys.DeleteOne):
		m.beginDelete(true)
	case key.Matches(msg, m.keys.Select):
		m.toggleSelect()
	case key.Matches(msg, m.keys.SelectAll):
		m.selectAll()
	}
	return m, nil
}

func (m *AnalyzeModel) move(delta int) {
	if len(m.entries) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.entries) {
		m.cursor = len(m.entries) - 1
	}
	m.ensureVisible()
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

func (m *AnalyzeModel) setSort(s SortMode) {
	m.sort = s
	m.rebuildEntries()
}

func (m *AnalyzeModel) toggleSelect() {
	if m.cursor >= len(m.entries) {
		return
	}
	n := m.entries[m.cursor]
	if m.selected[n.Path] {
		m.deselect(n.Path)
	} else {
		m.selected[n.Path] = true
		m.selectedNodes[n.Path] = n
	}
}

// deselect drops path from both selection maps. It is called when a selected
// node is skipped or fails, so a stale selection cannot shadow the cursor on a
// later delete.
func (m *AnalyzeModel) deselect(path string) {
	delete(m.selected, path)
	delete(m.selectedNodes, path)
}

func (m *AnalyzeModel) selectAll() {
	for _, n := range m.entries {
		m.selected[n.Path] = true
		m.selectedNodes[n.Path] = n
	}
}

func (m *AnalyzeModel) beginDelete(cursorOnly bool) {
	if m.cfg.NoDelete {
		m.status = "deletion disabled (--no-delete)"
		return
	}
	var nodes []*scan.Node
	if !cursorOnly && len(m.selectedNodes) > 0 {
		for _, n := range m.selectedNodes {
			nodes = append(nodes, n)
		}
	} else if m.cursor < len(m.entries) {
		nodes = append(nodes, m.entries[m.cursor])
	}
	if len(nodes) == 0 {
		return
	}
	var targets []deleteTarget
	skipped := 0
	for _, n := range nodes {
		if m.cfg.Validator != nil {
			if err := m.cfg.Validator.Validate(n.Path); err != nil {
				skipped++
				m.status = fmt.Sprintf("skip %s: %v", n.Name, err)
				m.deselect(n.Path)
				continue
			}
		}
		snap, err := safe.TakeSnapshot(n.Path)
		if err != nil {
			skipped++
			m.status = fmt.Sprintf("skip %s: %v", n.Name, err)
			m.deselect(n.Path)
			continue
		}
		targets = append(targets, deleteTarget{node: n, snap: snap, size: n.Size})
	}
	if len(targets) == 0 {
		if skipped > 0 {
			m.status = fmt.Sprintf("nothing to trash (%d skipped)", skipped)
		}
		return
	}
	m.pending = targets
	m.confirm = true
	m.status = fmt.Sprintf("trash %d item(s)? [y/n]", len(targets))
}

func (m *AnalyzeModel) executeDelete() {
	m.confirm = false
	trashed := 0
	for _, t := range m.pending {
		if err := m.exec.MoveToTrash(t.snap, t.size, t.node.Path); err != nil {
			m.status = fmt.Sprintf("skip %s: %v", t.node.Name, err)
			m.deselect(t.node.Path)
			continue
		}
		m.freed += t.size
		trashed++
		m.removeNode(t.node)
		m.deselect(t.node.Path)
	}
	m.pending = nil
	if trashed > 0 {
		m.status = fmt.Sprintf("trashed %d item(s), %s freed", trashed, config.FormatBytes(m.freed))
	}
	m.rebuildEntries()
}

// removeNode detaches a trashed node from the tree and subtracts its size from
// every ancestor so the displayed totals stay honest.
func (m *AnalyzeModel) removeNode(n *scan.Node) {
	parent, ok := m.parents[n]
	if !ok {
		return
	}
	m.unknown -= scan.UnknownCount(n)
	for i, c := range parent.Children {
		if c == n {
			parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
			break
		}
	}
	delete(m.parents, n)
	// gdu's ItemCount includes the node itself plus its whole subtree, so
	// subtract the removed subtree's count (not 1) from each ancestor.
	for p := parent; p != nil; p = m.parents[p] {
		p.Size -= n.Size
		p.Apparent -= n.Apparent
		p.ItemCount -= n.ItemCount
		if p.ItemCount < 0 {
			p.ItemCount = 0
		}
	}
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
		switch m.sort {
		case SortByName:
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		case SortByMtime:
			return out[i].Mtime.After(out[j].Mtime)
		default:
			if m.cfg.Apparent {
				return out[i].Apparent > out[j].Apparent
			}
			return out[i].Size > out[j].Size
		}
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
	overhead := 5
	if m.unknown > 0 {
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
	if total, avail, err := statfs(m.dir.Path); err == nil {
		free = fmt.Sprintf("%s free of %s", config.FormatBytes(int64(avail)), config.FormatBytes(int64(total)))
	}
	cursorInfo := ""
	if m.cursor < len(m.entries) {
		n := m.entries[m.cursor]
		cursorInfo = " · cursor " + m.sizeLabel(n)
	}
	line1 := m.styles.header.Render(m.dir.Path)
	line2 := m.styles.subtle.Render(fmt.Sprintf("%s · %d item(s) · sort %s%s",
		free, len(m.entries), m.sort, cursorInfo))
	if m.unknown > 0 {
		warn := m.styles.warn.Render(fmt.Sprintf(
			"partial scan: %d item(s) could not be read (size shown as unknown)", m.unknown))
		return line1 + "\n" + warn + "\n" + line2
	}
	return line1 + "\n" + line2
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
	var b strings.Builder
	for i := m.offset; i < end; i++ {
		n := m.entries[i]
		b.WriteString(m.row(n, i == m.cursor, maxSize))
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *AnalyzeModel) row(n *scan.Node, cursor bool, maxSize int64) string {
	marker := " "
	if cursor {
		marker = ">"
	}
	sel := " "
	if m.selected[n.Path] {
		sel = "x"
	}
	reclaim := "   "
	if m.isCategoryRoot(n.Path) {
		reclaim = "[R]"
	}
	bar := m.bar(n, maxSize)
	size := fmt.Sprintf("%10s", m.sizeLabel(n))
	name := n.Name
	if n.IsDir {
		name += "/"
	}
	count := ""
	if m.showCt {
		count = fmt.Sprintf(" %d", n.ItemCount)
	}
	line := fmt.Sprintf("%s%s %s [%s] %s %s%s", marker, sel, reclaim, bar, size, name, count)
	if cursor {
		return m.styles.cursor.Render(line)
	}
	if n.IsDir {
		return m.styles.dir.Render(line)
	}
	return line
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

func (m *AnalyzeModel) footer() string {
	if m.confirm {
		return m.styles.warn.Render(m.status + "  [y] confirm  [n] cancel")
	}
	if m.filtering {
		return m.filter.View()
	}
	line := m.help.ShortHelpView(m.keys.shortHelp())
	if m.status != "" {
		return line + "\n" + m.styles.subtle.Render(m.status)
	}
	return line
}

func (m *AnalyzeModel) helpView() string {
	return m.help.FullHelpView(m.keys.fullHelp())
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
	Enter     key.Binding
	Parent    key.Binding
	SortSize  key.Binding
	SortName  key.Binding
	SortMtime key.Binding
	Count     key.Binding
	Filter    key.Binding
	Delete    key.Binding
	DeleteOne key.Binding
	Select    key.Binding
	SelectAll key.Binding
	Help      key.Binding
	Quit      key.Binding
}

func defaultKeys() keyMap {
	return keyMap{
		Up:        key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:      key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Enter:     key.NewBinding(key.WithKeys("right", "l", "enter"), key.WithHelp("→/l", "enter")),
		Parent:    key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "parent")),
		SortSize:  key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sort size")),
		SortName:  key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "sort name")),
		SortMtime: key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "sort mtime")),
		Count:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "item count")),
		Filter:    key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		Delete:    key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "trash selected")),
		DeleteOne: key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "trash highlighted")),
		Select:    key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
		SelectAll: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "select all")),
		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:      key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k keyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Parent, k.SortSize, k.Filter, k.Delete, k.Help, k.Quit}
}

func (k keyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Enter, k.Parent},
		{k.SortSize, k.SortName, k.SortMtime, k.Count, k.Filter},
		{k.Delete, k.DeleteOne, k.Select, k.SelectAll},
		{k.Help, k.Quit},
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
