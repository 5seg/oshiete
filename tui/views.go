package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func section(title string, lines []string) string {
	var b strings.Builder
	b.WriteString(styleBold.Render(title))
	b.WriteString("\n")
	if len(lines) == 0 {
		b.WriteString(styleDim.Render("  —"))
	} else {
		b.WriteString(strings.Join(lines, "\n"))
	}
	return b.String()
}

func twoCol(left, right string, w int) string {
	lw := w/2 - 2
	if lw < 12 {
		lw = 12
	}
	rw := w - lw - 4
	if rw < 12 {
		rw = 12
	}
	return lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(lw).Render(left),
		"   ",
		lipgloss.NewStyle().Width(rw).Render(right),
	)
}

func stat(label, value string) string {
	return styleDim.Render(label) + " " + styleTitle.Render(value)
}

func delta(now, before int) string {
	if before == 0 {
		if now == 0 {
			return styleDim.Render("—")
		}
		return styleGood.Render("new")
	}
	d := 100 * float64(now-before) / float64(before)
	s := fmt.Sprintf("%+.0f%%", d)
	if now >= before {
		return styleGood.Render(s)
	}
	return styleBad.Render(s)
}

// seriesRows renders a labelled distribution with proportional bars.
func seriesRows(rows []NameCount, width int) []string {
	if len(rows) == 0 {
		return nil
	}
	maxN := rows[0].N
	labelW := 0
	for _, r := range rows {
		if n := len([]rune(r.Name)); n > labelW {
			labelW = n
		}
	}
	if labelW > width/2 {
		labelW = width / 2
	}

	numW, barW := 5, width-labelW-10
	if barW < 4 {
		barW = 4
	}

	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, "  "+
			pad(truncate(r.Name, labelW), labelW)+" "+
			styleDim.Render(padLeft(humanN(r.N), numW))+" "+
			styleAccent.Render(bar(r.N, maxN, barW)))
	}
	return out
}

func (m model) liveRows(width int) []string {
	if len(m.live) == 0 {
		return nil
	}
	pathW := width - 12
	if pathW < 8 {
		pathW = 8
	}
	out := make([]string, 0, len(m.live))
	for _, r := range m.live {
		out = append(out, "  "+styleGood.Render("●")+" "+
			pad(truncate(r.Path, pathW), pathW)+" "+
			styleDim.Render(padLeft(ago(r.Ts), 4))+" "+
			styleDim.Render(r.Country))
	}
	return out
}

// ---- views -------------------------------------------------------------------

func (m model) viewOverview() []string {
	out := []string{
		"  " + stat("閲覧", humanN(m.ov.Views)) +
			"     " + stat("訪問者", humanN(m.ov.Visitors)) +
			"     " + stat("新規", pct(m.ov.New, m.ov.Visitors)) +
			"     " + stat("中央値", humanDur(m.ov.MedianSess)) +
			"     " + stat("再訪", humanN(m.ov.Visitors-m.ov.New)),
		"",
		"  " + styleAccent.Render(sparkline(m.spark)),
		styleDim.Render("  " + periods[m.pIdx].Name + " の推移"),
		"",
	}

	half := m.w/2 - 3
	left := section("ライブ（5分）", m.liveRows(half))
	right := section("上位ページ", seriesRows(pageCounts(m.pages), half))
	out = append(out, twoCol(left, right, m.w), "")

	left = section("流入元", seriesRows(m.refs, half))
	right = section("国", seriesRows(m.countries, half))
	out = append(out, twoCol(left, right, m.w), "")

	out = append(out, twoCol(
		section("端末", seriesRows(m.devices, half)),
		section("キー", []string{
			styleDim.Render("  s  サイト切替"),
			styleDim.Render("  t  期間切替"),
			styleDim.Render("  3  ページ別"),
			styleDim.Render("  4  訪問者別と軌跡"),
		}), m.w))

	return out
}

// pageCounts adapts the page table to the series renderer.
func pageCounts(pages []PageRow) []NameCount {
	out := make([]NameCount, 0, len(pages))
	for _, p := range pages {
		out = append(out, NameCount{p.Path, p.Views})
	}
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

func (m model) viewLive() []string {
	out := []string{
		"  " + stat("今", humanN(len(m.live))) + styleDim.Render("  直近5分のページビュー"),
		"",
		section("いま起きていること", m.liveRows(m.w-6)),
	}
	if len(m.live) == 0 {
		out = append(out, "", styleDim.Render("  だれも来ていない。t で期間を広げると過去の動きが見える。"))
	}
	return out
}

func (m model) viewPages() []string {
	pathW := m.w - 30
	if pathW < 16 {
		pathW = 16
	}
	head := "  " + styleDim.Render(pad("path", pathW)) + " " +
		styleDim.Render(padLeft("views", 7)) + " " +
		styleDim.Render(padLeft("visitors", 7)) + " " +
		styleDim.Render(padLeft("bounce", 7))

	out := []string{head, styleDim.Render("  " + strings.Repeat("─", min(m.w-4, pathW+24)))}

	if len(m.pages) == 0 {
		out = append(out, styleDim.Render("  この期間にページビューが無い。"))
		return out
	}

	for i, p := range m.pages {
		line := "  " +
			pad(truncate(p.Path, pathW), pathW) + " " +
			padLeft(humanN(p.Views), 7) + " " +
			padLeft(humanN(p.Visitors), 7) + " " +
			padLeft(fmt.Sprintf("%.0f%%", p.Bounce*100), 7)
		if i == m.cursor {
			out = append(out, styleSel.Reverse(true).Render(line))
		} else {
			out = append(out, line)
		}
	}

	if m.cursor < len(m.pages) {
		p := m.pages[m.cursor]
		out = append(out, "",
			styleBold.Render("  "+p.Path),
			styleDim.Render(fmt.Sprintf("  %s 閲覧 · %s 訪問者 · %.0f%% がここで離脱",
				humanN(p.Views), humanN(p.Visitors), p.Bounce*100)))
	}
	return out
}

func (m model) viewVisitors() []string {
	if m.trailFor != "" {
		return m.viewTrail()
	}

	out := []string{
		"  " + stat("訪問者", humanN(len(m.visitors))) +
			styleDim.Render("   "+periods[m.pIdx].Name+"    enter で軌跡"),
		"",
	}

	if len(m.visitors) == 0 {
		return append(out, styleDim.Render("  この期間に訪問者がいない。"))
	}

	head := "  " + styleDim.Render(pad("visitor", 10)) + " " +
		styleDim.Render(padLeft("last", 6)) + " " +
		styleDim.Render(padLeft("sess", 5)) + " " +
		styleDim.Render(padLeft("views", 6)) + "   " +
		styleDim.Render(pad("entry", max(10, m.w-52))) + "  " +
		styleDim.Render("country")
	out = append(out, head, styleDim.Render("  "+strings.Repeat("─", min(m.w-4, 88))))

	for i, v := range m.visitors {
		entryW := max(10, m.w-52)
		line := "  " +
			pad(shortVisitor(v.Visitor), 10) + " " +
			padLeft(ago(v.LastSeen), 6) + " " +
			padLeft(fmt.Sprintf("%d", v.Sessions), 5) + " " +
			padLeft(humanN(v.Views), 6) + "   " +
			pad(truncate(v.Entry, entryW), entryW) + "  " +
			v.Country

		switch {
		case i == m.cursor:
			out = append(out, styleSel.Reverse(true).Render(line))
		case v.Sessions > 1:
			// Returning visitors carry the strongest signal, so they are marked.
			out = append(out, line+" "+styleGood.Render("↺"))
		default:
			out = append(out, line)
		}
	}
	return out
}

func (m model) viewTrail() []string {
	if len(m.trail) == 0 {
		return []string{styleDim.Render("  軌跡がまだ読み込まれていない。")}
	}

	first, last := m.trail[0], m.trail[len(m.trail)-1]
	sessions := map[string]bool{}
	for _, r := range m.trail {
		sessions[r.Session] = true
	}

	out := []string{
		"  " + styleBold.Render(shortVisitor(m.trailFor)) +
			styleDim.Render("   初回 "+clock(first.Ts)+
				"   最終 "+clock(last.Ts)+
				fmt.Sprintf("   通算 %d 閲覧 / %d セッション", len(m.trail), len(sessions))),
		"",
	}

	lastSession := ""
	for _, r := range m.trail {
		if lastSession != "" && r.Session != lastSession {
			out = append(out, styleDim.Render("  "+strings.Repeat("·", min(m.w-4, 60))))
		}
		lastSession = r.Session

		ref := r.Ref
		if ref == "" {
			ref = "—"
		}
		mark := " "
		if r.IsBot {
			mark = styleBad.Render("b")
		}

		out = append(out, "  "+
			styleDim.Render(clock(r.Ts))+"  "+
			pad(truncate(r.Path, 32), 32)+" "+
			styleDim.Render(pad(truncate(ref, 22), 22))+" "+
			styleDim.Render(r.Country)+" "+
			styleDim.Render(r.Device)+" "+
			styleDim.Render(fmt.Sprintf("%d", r.Width))+" "+
			mark)
	}

	out = append(out, "", styleDim.Render("  esc で一覧に戻る"))
	return out
}

func (m model) viewCompare() []string {
	name := periods[m.pIdx].Name
	out := []string{
		"  " + styleDim.Render("直前の"+name+"と比較"),
		"",
	}

	row := func(label string, now, before int) string {
		return "  " + pad(label, 14) +
			padLeft(humanN(now), 9) + "  " +
			padLeft(humanN(before), 9) + "  " +
			padLeft(delta(now, before), 10)
	}

	out = append(out,
		"  "+styleDim.Render(pad("", 14))+styleDim.Render(padLeft("now", 9))+"  "+
			styleDim.Render(padLeft("before", 9))+"  "+styleDim.Render(padLeft("change", 10)),
		styleDim.Render("  "+strings.Repeat("─", min(m.w-4, 48))),
		row("閲覧", m.ov.Views, m.prev.Views),
		row("訪問者", m.ov.Visitors, m.prev.Visitors),
		row("新規", m.ov.New, m.prev.New),
		row("再訪", m.ov.Visitors-m.ov.New, m.prev.Visitors-m.prev.New),
		row("bot", m.ov.Bots, m.prev.Bots),
		"",
		"  "+pad("中央値", 14)+
			padLeft(humanDur(m.ov.MedianSess), 9)+"  "+
			padLeft(humanDur(m.prev.MedianSess), 9),
	)

	return out
}
