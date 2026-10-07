package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/purge"
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

// PurgeConfig carries the dependencies the purge model needs. Artifacts are the
// already-discovered candidates; discovery and measurement happen before the
// model is created (invariant I1).
type PurgeConfig struct {
	Artifacts []purge.Artifact
	Color     bool
	Now       time.Time
	Executor  *purge.Executor
}

// purgeRow is one visible line: either a project group header (art == nil) or an
// artifact.
type purgeRow struct {
	group string
	art   *purge.Artifact
}

// PurgeModel is the Bubble Tea model for `burrow purge`. It groups candidates
// by project and deletes the selected artifacts through purge.Executor.
type PurgeModel struct {
	cfg  PurgeConfig
	exec *purge.Executor

	artifacts []purge.Artifact
	rows      []purgeRow
	cursor    int
	offset    int
	selected  map[string]bool

	filter    textinput.Model
	filtering bool

	confirm bool

	status string
	freed  int64

	width  int
	height int
	help   help.Model
	keys   purgeKeyMap
	styles styles
}

// NewPurgeModel builds the purge model from discovered artifacts.
func NewPurgeModel(cfg PurgeConfig) *PurgeModel {
	if cfg.Now.IsZero() {
		cfg.Now = time.Now()
	}
	filter := textinput.New()
	filter.Placeholder = "search"
	filter.Prompt = "/"
	m := &PurgeModel{
		cfg:       cfg,
		exec:      cfg.Executor,
		artifacts: append([]purge.Artifact(nil), cfg.Artifacts...),
		selected:  map[string]bool{},
		filter:    filter,
		help:      help.New(),
		keys:      defaultPurgeKeys(),
		styles:    newStyles(cfg.Color),
		height:    24,
		width:     80,
	}
	m.rebuildRows()
	return m
}

// Run starts the Bubble Tea program and closes the journal run frame on exit.
func (m *PurgeModel) Run() error {
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return err
	}
	if m.exec != nil {
		return m.exec.EndRun(m.freed)
	}
	return nil
}

// Init implements tea.Model.
func (m *PurgeModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m *PurgeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *PurgeModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.confirm {
		switch msg.String() {
		case "y", "Y":
			m.execute()
		default:
			m.confirm = false
			m.status = "purge cancelled"
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
	case key.Matches(msg, m.keys.Select):
		m.toggle()
	case key.Matches(msg, m.keys.SelectAll):
		m.selectAll()
	case key.Matches(msg, m.keys.NextProject):
		m.jumpGroup(1)
	case key.Matches(msg, m.keys.PrevProject):
		m.jumpGroup(-1)
	case key.Matches(msg, m.keys.Filter):
		m.filtering = true
		m.filter.Focus()
	case key.Matches(msg, m.keys.Confirm):
		m.beginConfirm()
	}
	return m, nil
}

// SelectedCount returns the number of selected purgeable artifacts.
func (m *PurgeModel) SelectedCount() int { return len(m.selected) }

// SelectedSize returns the total on-disk size of the selected artifacts.
func (m *PurgeModel) SelectedSize() int64 {
	var total int64
	for _, a := range m.artifacts {
		if m.selected[a.Path] {
			total += a.Size
		}
	}
	return total
}

// purgeable returns the artifacts that are not protected.
func (m *PurgeModel) purgeable() []purge.Artifact {
	var out []purge.Artifact
	for _, a := range m.artifacts {
		if !a.Kept() {
			out = append(out, a)
		}
	}
	return out
}

func (m *PurgeModel) move(delta int) {
	if len(m.rows) == 0 {
		return
	}
	m.cursor += delta
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	m.ensureVisible()
}

func (m *PurgeModel) toggle() {
	if m.cursor >= len(m.rows) {
		return
	}
	row := m.rows[m.cursor]
	if row.art == nil {
		m.toggleGroup(row.group)
		return
	}
	if row.art.Kept() {
		m.status = "kept: " + row.art.KeepReason
		return
	}
	m.togglePath(row.art.Path)
}

func (m *PurgeModel) toggleGroup(group string) {
	targets := m.groupPaths(group)
	if len(targets) == 0 {
		return
	}
	allSelected := true
	for _, p := range targets {
		if !m.selected[p] {
			allSelected = false
			break
		}
	}
	for _, p := range targets {
		if allSelected {
			delete(m.selected, p)
		} else {
			m.selected[p] = true
		}
	}
}

func (m *PurgeModel) togglePath(path string) {
	if m.selected[path] {
		delete(m.selected, path)
		return
	}
	m.selected[path] = true
}

func (m *PurgeModel) groupPaths(group string) []string {
	var out []string
	for _, a := range m.artifacts {
		if a.Project == group && !a.Kept() {
			out = append(out, a.Path)
		}
	}
	return out
}

func (m *PurgeModel) selectAll() {
	for _, a := range m.purgeable() {
		m.selected[a.Path] = true
	}
}

func (m *PurgeModel) jumpGroup(dir int) {
	if len(m.rows) == 0 {
		return
	}
	for i := m.cursor + dir; i >= 0 && i < len(m.rows); i += dir {
		if m.rows[i].art == nil {
			m.cursor = i
			m.ensureVisible()
			return
		}
	}
}

func (m *PurgeModel) beginConfirm() {
	if m.SelectedCount() == 0 {
		m.status = "nothing selected"
		return
	}
	m.confirm = true
	m.status = fmt.Sprintf("permanently delete %d artifact(s), %s? [y/n]",
		m.SelectedCount(), config.FormatBytes(m.SelectedSize()))
}

func (m *PurgeModel) execute() {
	m.confirm = false
	if m.exec == nil {
		m.status = "purge executor unavailable"
		return
	}
	deleted := 0
	failed := 0
	remaining := m.artifacts[:0:0]
	for _, a := range m.artifacts {
		if !m.selected[a.Path] || a.Kept() {
			remaining = append(remaining, a)
			continue
		}
		n, err := m.exec.Delete(&a, false)
		if err != nil {
			failed++
			m.status = fmt.Sprintf("skip %s: %v", a.Path, err)
			remaining = append(remaining, a)
			continue
		}
		m.freed += n
		deleted++
	}
	m.artifacts = remaining
	m.selected = map[string]bool{}
	m.rebuildRows()
	if deleted > 0 {
		m.status = fmt.Sprintf("purged %d item(s), %s freed", deleted, config.FormatBytes(m.freed))
	}
	if failed > 0 {
		m.status += fmt.Sprintf(" (%d failed)", failed)
	}
}

func (m *PurgeModel) rebuildRows() {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	byGroup := map[string][]purge.Artifact{}
	var groups []string
	for _, a := range m.artifacts {
		if q != "" && !matchesQuery(a, q) {
			continue
		}
		if _, ok := byGroup[a.Project]; !ok {
			groups = append(groups, a.Project)
		}
		byGroup[a.Project] = append(byGroup[a.Project], a)
	}
	sort.Strings(groups)
	var rows []purgeRow
	for _, g := range groups {
		items := byGroup[g]
		sort.SliceStable(items, func(i, j int) bool { return items[i].Path < items[j].Path })
		rows = append(rows, purgeRow{group: g})
		for i := range items {
			art := items[i]
			rows = append(rows, purgeRow{group: g, art: &art})
		}
	}
	m.rows = rows
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.ensureVisible()
}

func matchesQuery(a purge.Artifact, q string) bool {
	return strings.Contains(strings.ToLower(a.Path), q) ||
		strings.Contains(strings.ToLower(a.Type), q) ||
		strings.Contains(strings.ToLower(a.Project), q)
}

func (m *PurgeModel) ensureVisible() {
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

func (m *PurgeModel) listHeight() int {
	h := m.height - 6
	if h < 1 {
		h = 1
	}
	return h
}

// View implements tea.Model.
func (m *PurgeModel) View() string {
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

func (m *PurgeModel) header() string {
	purgeable := len(m.purgeable())
	kept := len(m.artifacts) - purgeable
	line1 := m.styles.header.Render("purge — rebuildable project artifacts")
	line2 := m.styles.subtle.Render(fmt.Sprintf(
		"%d candidate(s) · %d kept · %d selected (%s) · permanent delete",
		len(m.artifacts), kept, m.SelectedCount(), config.FormatBytes(m.SelectedSize())))
	return line1 + "\n" + line2
}

func (m *PurgeModel) list() string {
	if len(m.rows) == 0 {
		return m.styles.subtle.Render("  (no artifacts found)")
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

func (m *PurgeModel) row(r purgeRow, cursor bool) string {
	if r.art == nil {
		return m.groupHeader(r.group, cursor)
	}
	a := r.art
	marker := " "
	if cursor {
		marker = ">"
	}
	box := " "
	note := ""
	if a.Kept() {
		box = "-"
		note = "  keep: " + a.KeepReason
	} else if m.selected[a.Path] {
		box = "x"
	}
	size := fmt.Sprintf("%10s", m.sizeLabel(*a))
	age := m.ageLabel(*a)
	line := fmt.Sprintf("%s [%s] %s %-14s %-8s %s%s", marker, box, size, a.Type, age, a.Path, note)
	if cursor {
		return m.styles.cursor.Render(line)
	}
	if a.Kept() {
		return m.styles.subtle.Render(line)
	}
	return line
}

func (m *PurgeModel) groupHeader(group string, cursor bool) string {
	var count int
	var total int64
	var selected int
	for _, a := range m.artifacts {
		if a.Project != group {
			continue
		}
		count++
		total += a.Size
		if m.selected[a.Path] {
			selected++
		}
	}
	line := fmt.Sprintf("▸ %s  %d item(s), %s, %d selected", group, count, config.FormatBytes(total), selected)
	if cursor {
		return m.styles.header.Render("> " + line)
	}
	return m.styles.header.Render(line)
}

func (m *PurgeModel) sizeLabel(a purge.Artifact) string {
	if !a.SizeKnown {
		return "unknown"
	}
	return config.FormatBytes(a.Size)
}

func (m *PurgeModel) ageLabel(a purge.Artifact) string {
	if !a.AgeKnown {
		return "unknown"
	}
	age := m.cfg.Now.Sub(a.Mtime)
	if age < 0 {
		age = 0
	}
	days := int(age.Hours() / 24)
	if days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(age.Hours()))
}

func (m *PurgeModel) footer() string {
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

func (m *PurgeModel) helpView() string {
	return m.help.FullHelpView(m.keys.fullHelp())
}

type purgeKeyMap struct {
	Up          key.Binding
	Down        key.Binding
	Select      key.Binding
	SelectAll   key.Binding
	NextProject key.Binding
	PrevProject key.Binding
	Filter      key.Binding
	Confirm     key.Binding
	Help        key.Binding
	Quit        key.Binding
}

func defaultPurgeKeys() purgeKeyMap {
	return purgeKeyMap{
		Up:          key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "up")),
		Down:        key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "down")),
		Select:      key.NewBinding(key.WithKeys(" "), key.WithHelp("space", "select")),
		SelectAll:   key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "select all")),
		NextProject: key.NewBinding(key.WithKeys("]"), key.WithHelp("]", "next project")),
		PrevProject: key.NewBinding(key.WithKeys("["), key.WithHelp("[", "prev project")),
		Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
		Confirm:     key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "purge selected")),
		Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:        key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

func (k purgeKeyMap) shortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Select, k.SelectAll, k.NextProject, k.Filter, k.Confirm, k.Help, k.Quit}
}

func (k purgeKeyMap) fullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.NextProject, k.PrevProject},
		{k.Select, k.SelectAll, k.Filter},
		{k.Confirm, k.Help, k.Quit},
	}
}
