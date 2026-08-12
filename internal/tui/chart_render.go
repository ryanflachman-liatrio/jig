package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"jig/internal/workflow"
)

// chart_render.go draws the layered layout (chart_layout.go) as a mermaid-style
// top-down flowchart. It follows the help.go compositing recipe: connectors are
// drawn into a hand-built rune grid (the base layer) and the node boxes are
// composited on top via a lipgloss v2 Compositor/Canvas. No SetCell, no new
// imports — the grid is plain []rune rows turned into a styled string.
//
// The grid stores connectors as per-cell direction bitmasks (up/down/left/right)
// so junctions resolve to the correct box-drawing rune (│ ─ ╭ ╮ ╰ ╯ ├ ┤ ┬ ┴ ┼)
// regardless of the order edges are drawn — which keeps the output deterministic
// and golden-testable. A parallel class grid colors each connector cell by edge
// class (normal / conditional / back-edge). Node boxes are drawn on top, so a
// long edge that must cross an intermediate rank passes behind the boxes (the
// documented MVP tradeoff: no crossing-minimization).

// Connector geometry. Boxes are a uniform width (content + padding + border);
// ranks stack with chartVGap blank rows between them, leaving room for a
// horizontal routing "bus" row and an arrowhead row above each child.
const (
	chartHGap        = 4  // horizontal gap between boxes in a rank
	chartHGapMin     = 2  // compressed gap when the natural layout overflows width
	chartVGap        = 2  // blank rows between ranks: [bus row, arrow row]
	chartBoxMaxInner = 18 // cap on box content width so wide graphs still fit
	chartBoxPadBrd   = 4  // padding (1+1) + border (1+1) added around inner width
	chartBoxHeight   = 4  // top border + 2 content lines + bottom border
)

// connector direction bits.
const (
	dirUp = 1 << iota
	dirDown
	dirLeft
	dirRight
)

// connector cell classes (higher wins when strokes overlap).
const (
	clsNormal = iota
	clsCond
	clsBack
)

// boxRunes maps a direction bitmask to its box-drawing rune.
var boxRunes = map[int]rune{
	dirUp | dirDown:                      '│',
	dirLeft | dirRight:                   '─',
	dirDown | dirRight:                   '╭',
	dirDown | dirLeft:                    '╮',
	dirUp | dirRight:                     '╰',
	dirUp | dirLeft:                      '╯',
	dirUp | dirDown | dirRight:           '├',
	dirUp | dirDown | dirLeft:            '┤',
	dirDown | dirLeft | dirRight:         '┬',
	dirUp | dirLeft | dirRight:           '┴',
	dirUp | dirDown | dirLeft | dirRight: '┼',
	dirUp:                                '│',
	dirDown:                              '│',
	dirLeft:                              '─',
	dirRight:                             '─',
}

// chartGrid is the mutable connector canvas: per-cell direction bits, edge
// class, and an override rune (arrowheads, loop glyph) that wins over bits.
type chartGrid struct {
	w, h  int
	bits  [][]int
	class [][]int
	over  [][]rune
}

func newChartGrid(w, h int) *chartGrid {
	if w < 1 {
		w = 1
	}
	if h < 1 {
		h = 1
	}
	g := &chartGrid{w: w, h: h}
	g.bits = make([][]int, h)
	g.class = make([][]int, h)
	g.over = make([][]rune, h)
	for y := 0; y < h; y++ {
		g.bits[y] = make([]int, w)
		g.class[y] = make([]int, w)
		g.over[y] = make([]rune, w)
	}
	return g
}

func (g *chartGrid) inBounds(x, y int) bool {
	return x >= 0 && x < g.w && y >= 0 && y < g.h
}

// addBit ORs a direction bit into a cell and raises its class.
func (g *chartGrid) addBit(x, y, bit, cls int) {
	if !g.inBounds(x, y) {
		return
	}
	g.bits[y][x] |= bit
	if cls > g.class[y][x] {
		g.class[y][x] = cls
	}
}

// setRune stamps an override rune (e.g. an arrowhead) that renders instead of
// the cell's bit-derived rune.
func (g *chartGrid) setRune(x, y int, r rune, cls int) {
	if !g.inBounds(x, y) {
		return
	}
	g.over[y][x] = r
	if cls > g.class[y][x] {
		g.class[y][x] = cls
	}
}

// drawV connects a vertical run down column x from y1 to y2 (order-agnostic).
func (g *chartGrid) drawV(x, y1, y2, cls int) {
	if y1 > y2 {
		y1, y2 = y2, y1
	}
	for y := y1; y <= y2; y++ {
		b := 0
		if y > y1 {
			b |= dirUp
		}
		if y < y2 {
			b |= dirDown
		}
		g.addBit(x, y, b, cls)
	}
}

// drawH connects a horizontal run along row y from x1 to x2 (order-agnostic).
func (g *chartGrid) drawH(y, x1, x2, cls int) {
	if x1 > x2 {
		x1, x2 = x2, x1
	}
	for x := x1; x <= x2; x++ {
		b := 0
		if x > x1 {
			b |= dirLeft
		}
		if x < x2 {
			b |= dirRight
		}
		g.addBit(x, y, b, cls)
	}
}

// renderChart lays out and draws the whole chart to fit within width columns,
// returning a styled multi-line string. When the graph is wider than width the
// chart renders at its natural width (the detail view offers horizontal scroll
// as the escape hatch).
func renderChart(wf *workflow.Workflow, width int) string {
	lay := layoutChart(wf)
	if len(lay.nodes) == 0 {
		return ""
	}
	if width < 1 {
		width = 1
	}

	innerW := chartInnerWidth(lay.nodes)
	bw := innerW + chartBoxPadBrd // full rendered box width
	bh := chartBoxHeight

	// Reserve a right-side margin: one dedicated vertical channel per back-edge.
	// Compute this before the layout so the node positions use the remaining
	// width — otherwise chanX = totalW+1 falls just outside the viewport and
	// the channels are invisible at zero scroll offset.
	loopMargin := 0
	if len(lay.backEdges) > 0 {
		loopMargin = 2 + 2*len(lay.backEdges)
	}
	// layoutW is the columns available for node boxes and horizontal connectors.
	layoutW := width - loopMargin
	if layoutW < 1 {
		layoutW = 1
	}

	// Choose the horizontal gap: prefer chartHGap, compress to the minimum when
	// the widest rank would overflow the available layout width.
	maxCount := 0
	for _, r := range lay.ranks {
		if len(r) > maxCount {
			maxCount = len(r)
		}
	}
	hgap := chartHGap
	if maxCount > 1 && maxCount*bw+(maxCount-1)*hgap > layoutW {
		hgap = chartHGapMin
	}
	natural := maxCount*bw + max(0, maxCount-1)*hgap
	totalW := max(layoutW, natural)

	// Node top-left coordinates: ranks stack vertically; each rank's row is
	// centered within totalW so near-linear graphs render as a centered spine.
	xs := make([]int, len(lay.nodes))
	ys := make([]int, len(lay.nodes))
	for r, row := range lay.ranks {
		count := len(row)
		rowW := count*bw + max(0, count-1)*hgap
		startX := (totalW - rowW) / 2
		if startX < 0 {
			startX = 0
		}
		y := r * (bh + chartVGap)
		for j, idx := range row {
			xs[idx] = startX + j*(bw+hgap)
			ys[idx] = y
		}
	}
	numRanks := len(lay.ranks)
	totalH := numRanks*bh + max(0, numRanks-1)*chartVGap

	// Anchor back-edge channels just past the actual rightmost node, not at
	// totalW. Nodes are centered so totalW is often much wider than the content,
	// making the horizontal connector span most of the screen unnecessarily.
	rightEdge := 0
	for i := range lay.nodes {
		if r := xs[i] + bw; r > rightEdge {
			rightEdge = r
		}
	}
	canvasW := max(totalW, rightEdge+loopMargin)

	cx := func(idx int) int { return xs[idx] + bw/2 }

	g := newChartGrid(canvasW, totalH)

	// Downward depends_on edges. Each drops from the parent's bottom-center, runs
	// horizontally along the bus row just above the child, then down into the
	// child's top-center with an arrowhead. Fan-in falls out naturally: every
	// parent's vertical converges on the same bus row at the child's column.
	for _, e := range lay.edges {
		cls := clsNormal
		if e.conditional {
			cls = clsCond
		}
		pcx, ccx := cx(e.from), cx(e.to)
		pBottom := ys[e.from] + bh // first blank row under the parent box
		cTop := ys[e.to]
		busRow := cTop - chartVGap // horizontal routing row
		arrowRow := cTop - 1       // arrowhead sits directly above the child

		g.drawV(pcx, pBottom, busRow, cls) // down the parent column to the bus
		g.addBit(pcx, pBottom, dirUp, cls) // stub connecting up into the parent box
		g.drawH(busRow, pcx, ccx, cls)     // across to the child column
		g.drawV(ccx, busRow, arrowRow, cls)
		arrow := ArrowDownGlyph
		if e.conditional {
			arrow = CondArrowGlyph
		}
		g.setRune(ccx, arrowRow, []rune(arrow)[0], cls)
	}

	// Loop back-edges, routed up a dedicated right-side channel: out of the loop
	// step's right edge, up the channel, back into the goto target's right edge
	// with a left-pointing arrowhead, marked with the loop glyph.
	for k, be := range lay.backEdges {
		chanX := rightEdge + 1 + k*2
		if chanX >= canvasW {
			chanX = canvasW - 1
		}
		sRow := ys[be.from] + bh/2
		gRow := ys[be.to] + bh/2
		sRight := xs[be.from] + bw // cell just right of the loop step's box
		gRight := xs[be.to] + bw   // cell just right of the goto target's box

		g.drawH(sRow, sRight, chanX, clsBack)
		g.drawV(chanX, gRow, sRow, clsBack)
		g.drawH(gRow, gRight, chanX, clsBack)
		g.setRune(gRight, gRow, []rune(ArrowLeftGlyph)[0], clsBack)
		g.setRune(chanX, (sRow+gRow)/2, []rune(LoopGlyph)[0], clsBack)
	}

	base := g.render()

	// Composite the node boxes on top of the connector base at their positions.
	layers := make([]*lipgloss.Layer, 0, len(lay.nodes)+1)
	layers = append(layers, lipgloss.NewLayer(base))
	for i := range lay.nodes {
		box := renderNodeBox(lay.nodes[i], innerW)
		layers = append(layers, lipgloss.NewLayer(box).X(xs[i]).Y(ys[i]).Z(1))
	}
	comp := lipgloss.NewCompositor(layers...)
	return lipgloss.NewCanvas(canvasW, totalH).Compose(comp).Render()
}

// chartInnerWidth is the uniform box content width: the widest node's id or
// type line, capped so a wide graph still fits (longer text is truncated).
func chartInnerWidth(nodes []chartNode) int {
	w := len("id")
	for _, n := range nodes {
		if v := lipgloss.Width(n.id); v > w {
			w = v
		}
		if v := lipgloss.Width(nodeTypeLine(n)); v > w {
			w = v
		}
	}
	if w > chartBoxMaxInner {
		w = chartBoxMaxInner
	}
	return w
}

// nodeTypeLine is the box's second line: the step type plus gate/loop markers.
func nodeTypeLine(n chartNode) string {
	line := n.typ
	if n.gate {
		line += " " + GateGlyph
	}
	if n.loop != nil {
		line += " " + LoopGlyph
	}
	return line
}

// renderNodeBox renders one node into a fixed-width rounded box, colored by step
// type (agent/command/review) from the shared theme.Step.Types map.
func renderNodeBox(n chartNode, innerW int) string {
	box := theme.Chart.Box
	label := theme.Chart.Label
	if ts, ok := theme.Step.Types[n.typ]; ok {
		box = box.BorderForeground(ts.GetForeground())
		label = ts
	}

	id := truncateTitle(n.id, innerW)
	typ := truncateTitle(nodeTypeLine(n), innerW)
	line1 := padRight(theme.Step.ID.Render(id), lipgloss.Width(id), innerW)
	line2 := padRight(label.Render(typ), lipgloss.Width(typ), innerW)
	return box.Render(line1 + "\n" + line2)
}

// render turns the connector grid into a styled string: an override rune wins,
// otherwise the cell's direction bits pick a box-drawing rune; empty cells are
// spaces. Consecutive cells of the same class are styled as one run to keep the
// escape-code volume down.
func (g *chartGrid) render() string {
	edgeStyle := func(cls int) lipgloss.Style {
		switch cls {
		case clsCond:
			return theme.Chart.Conditional
		case clsBack:
			return theme.Chart.BackEdge
		default:
			return theme.Chart.Edge
		}
	}

	var b strings.Builder
	for y := 0; y < g.h; y++ {
		var run strings.Builder
		runCls := -1
		flush := func() {
			if run.Len() == 0 {
				return
			}
			b.WriteString(edgeStyle(runCls).Render(run.String()))
			run.Reset()
		}
		for x := 0; x < g.w; x++ {
			r := g.cellRune(x, y)
			if r == ' ' {
				flush()
				b.WriteByte(' ')
				runCls = -1
				continue
			}
			cls := g.class[y][x]
			if cls != runCls {
				flush()
				runCls = cls
			}
			run.WriteRune(r)
		}
		flush()
		if y < g.h-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// cellRune resolves one grid cell to its display rune.
func (g *chartGrid) cellRune(x, y int) rune {
	if r := g.over[y][x]; r != 0 {
		return r
	}
	if bits := g.bits[y][x]; bits != 0 {
		if r, ok := boxRunes[bits]; ok {
			return r
		}
	}
	return ' '
}
