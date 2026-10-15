package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type period struct {
	Name string
	Dur  time.Duration
}

var periods = []period{
	{"1h", time.Hour},
	{"24h", 24 * time.Hour},
	{"7d", 7 * 24 * time.Hour},
	{"30d", 30 * 24 * time.Hour},
}

const (
	viewOverview = iota
	viewLive
	viewPages
	viewVisitors
	viewCompare
	viewCount
)

var viewNames = []string{"概要", "ライブ", "ページ", "訪問者", "比較"}

// refreshEvery is how often the TUI asks the collector for new events. The
// request returns nothing most of the time, so this is cheap.
const refreshEvery = 3 * time.Second

type model struct {
	cfg   Config
	store *Store
	sy    *Syncer

	sites []string
	site  string
	pIdx  int

	view    int
	w, h    int
	cursor  int
	filter  string
	editing bool

	ov        Overview
	prev      Overview
	spark     []int64
	live      []LiveRow
	pages     []PageRow
	visitors  []VisitorRow
	trail     []TrailRow
	trailFor  string
	refs      []NameCount
	countries []NameCount
	devices   []NameCount

	syncing bool
	added   int
	lastErr error
	ready   bool
}

func newModel(cfg Config, store *Store, sy *Syncer) (model, error) {
	m := model{cfg: cfg, store: store, sy: sy, w: 100, h: 32, pIdx: 1}
	m.reload()
	return m, nil
}

func (m model) Init() tea.Cmd {
	return tea.Batch(syncCmd(m.sy, m.store), tick())
}

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// syncCmd pulls new events. It runs off the UI goroutine, so the dashboard keeps
// repainting while a large first sync is in flight.
func syncCmd(sy *Syncer, store *Store) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		n, err := sy.Run(ctx, store)
		return syncedMsg{added: n, err: err}
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.reload()

	case tickMsg:
		if m.syncing {
			return m, tick()
		}
		m.syncing = true
		return m, tea.Batch(syncCmd(m.sy, m.store), tick())

	case syncedMsg:
		m.syncing = false
		m.added = msg.added
		m.lastErr = msg.err
		m.reload()

	case tea.KeyMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m model) key(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.editing {
		switch k.Type {
		case tea.KeyEsc:
			m.editing, m.filter = false, ""
		case tea.KeyEnter:
			m.editing = false
		case tea.KeyBackspace:
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			if len(k.Runes) > 0 {
				m.filter += string(k.Runes)
			} else {
				m.filter += " "
			}
		}
		m.reload()
		return m, nil
	}

	switch k.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "1", "2", "3", "4", "5":
		m.view = int(k.String()[0] - '1')
		m.cursor = 0
	case "tab":
		m.view = (m.view + 1) % viewCount
		m.cursor = 0

	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < m.rowCount()-1 {
			m.cursor++
		}

	case "t":
		m.pIdx = (m.pIdx + 1) % len(periods)
		m.cursor = 0
	case "s":
		if len(m.sites) > 0 {
			next := 0
			for i, s := range m.sites {
				if s == m.site {
					next = (i + 1) % len(m.sites)
					break
				}
			}
			m.site = m.sites[next]
			m.cursor, m.trailFor, m.trail, m.filter = 0, "", nil, ""
		}

	case "/":
		m.editing = true
	case "esc":
		m.filter, m.editing, m.trailFor, m.trail, m.cursor = "", false, "", nil, 0

	case "enter":
		if m.view == viewVisitors && m.cursor < len(m.visitors) {
			m.trailFor = m.visitors[m.cursor].Visitor
			m.trail, _ = m.store.Trail(m.trailFor)
		}

	case "r":
		if !m.syncing {
			m.syncing = true
			return m, syncCmd(m.sy, m.store)
		}
	}

	return m, nil
}

func (m *model) rowCount() int {
	switch m.view {
	case viewLive:
		return len(m.live)
	case viewPages:
		return len(m.pages)
	case viewVisitors:
		if m.trailFor != "" {
			return len(m.trail)
		}
		return len(m.visitors)
	}
	return 0
}

// window is the reporting period currently selected, as a half-open range.
func (m *model) window() (from, to int64) {
	to = time.Now().Unix()
	return to - int64(periods[m.pIdx].Dur.Seconds()), to
}

// reload re-runs every query the current view needs. All of this is local
// SQLite, so it is cheap enough to do on each keypress.
func (m *model) reload() {
	sites, err := m.store.Sites()
	if err != nil {
		m.lastErr = err
		return
	}
	m.sites = sites
	if len(sites) == 0 {
		m.ready = false
		return
	}

	known := false
	for _, s := range sites {
		if s == m.site {
			known = true
			break
		}
	}
	if !known {
		m.site, m.cursor, m.trailFor, m.trail = sites[0], 0, "", nil
	}
	m.ready = true

	from, to := m.window()
	span := int64(periods[m.pIdx].Dur.Seconds())

	m.ov, _ = m.store.Overview(m.site, from, to)
	m.prev, _ = m.store.Overview(m.site, from-span, from)
	m.spark, _ = m.store.Buckets(m.site, from, to, m.sparkWidth())
	m.live, _ = m.store.Live(m.site, to-300, 14)
	m.refs, _ = m.store.Top(m.site, "ref", from, to, 8)
	m.countries, _ = m.store.Top(m.site, "country", from, to, 8)
	m.devices, _ = m.store.Top(m.site, "device", from, to, 8)
	m.pages, _ = m.store.Pages(m.site, from, to, 200, m.filter)
	m.visitors, _ = m.store.Visitors(m.site, from, to, 200, m.filter)

	if m.trailFor != "" {
		m.trail, _ = m.store.Trail(m.trailFor)
	}

	if n := m.rowCount(); m.cursor >= n && n > 0 {
		m.cursor = n - 1
	}
}

func (m *model) sparkWidth() int {
	w := m.w - 8
	if w < 16 {
		w = 16
	}
	if w > 120 {
		w = 120
	}
	return w
}

// ---------------------------------------------------------------------------
// chrome
// ---------------------------------------------------------------------------

var (
	styleTitle  = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Faint(true)
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))
	styleOn     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
	styleGood   = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	styleBad    = lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	styleBold   = lipgloss.NewStyle().Bold(true)
	styleSel    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("212"))
)

func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m model) View() string {
	if !m.ready {
		return m.emptyView()
	}
	var b []string
	b = append(b, m.header(), m.tabs(), "")

	switch m.view {
	case viewOverview:
		b = append(b, m.viewOverview()...)
	case viewLive:
		b = append(b, m.viewLive()...)
	case viewPages:
		b = append(b, m.viewPages()...)
	case viewVisitors:
		b = append(b, m.viewVisitors()...)
	case viewCompare:
		b = append(b, m.viewCompare()...)
	}

	body := strings.Join(b, "\n")
	return body + "\n" + m.footer()
}

func (m model) header() string {
	left := styleTitle.Render("oshiete") + "  " + styleAccent.Render(m.site) +
		"  " + periods[m.pIdx].Name
	if len(m.sites) > 1 {
		left += styleDim.Render("  (" + strconv.Itoa(len(m.sites)) + " sites)")
	}
	if m.filter != "" {
		left += styleDim.Render("  /") + m.filter
	}

	right := ""
	switch {
	case m.syncing:
		right = styleDim.Render("syncing…")
	case m.lastErr != nil:
		right = styleBad.Render("sync failed")
	default:
		right = styleDim.Render("synced " + ago(m.store.SyncedAt(m.site).Unix()))
	}
	return spread(left, right, m.w)
}

func (m model) tabs() string {
	var parts []string
	for i, name := range viewNames {
		if i == m.view {
			parts = append(parts, styleOn.Render("["+name+"]"))
		} else {
			parts = append(parts, styleDim.Render(" "+name+" "))
		}
	}
	return strings.Join(parts, " ")
}

func (m model) footer() string {
	left := styleDim.Render("1-5 views  t period  s site  / filter  enter drill  r resync  q quit")
	if m.filter != "" {
		left = styleDim.Render("filter: ") + m.filter + styleDim.Render("   esc clears")
	}
	if m.editing {
		left = styleDim.Render("filter: ") + m.filter + styleBold.Render("▌")
	}

	right := ""
	if m.added > 0 {
		right = styleGood.Render("+" + strconv.Itoa(m.added))
	}
	if m.lastErr != nil {
		avail := m.w - lipgloss.Width(left) - 8
		if avail < 12 {
			avail = 12
		}
		right = styleBad.Render(truncate(m.lastErr.Error(), avail))
	}
	return spread(left, right, m.w)
}

func (m model) emptyView() string {
	return styleTitle.Render("oshiete") + "\n\n" +
		"  mirror is empty.\n\n" +
		styleDim.Render("  the first sync is running; if this stays here, check:\n"+
			"    · OSHIETE_ENDPOINT = "+m.cfg.Endpoint+"\n"+
			"    · token file readable\n"+
			"    · the beacon is installed on a site and has fired") + "\n"
}
