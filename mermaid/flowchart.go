package mermaid

import (
	"errors"
	"regexp"
	"sort"
	"strings"
)

var errUnsupported = errors.New("mermaid: unsupported syntax")

type shape int

const (
	shapeRect shape = iota
	shapeRound
	shapeDiamond
)

type fnode struct {
	id, label string
	shape     shape
	labelled  bool
}

type fedge struct {
	from, to int
	label    string
	arrow    bool
	style    int
}

type flowchart struct {
	horizontal bool // LR
	nodes      []fnode
	edges      []fedge
	index      map[string]int
}

// shapes: opener → (closer, shape), longest openers first.
var shapes = []struct {
	open, close string
	shape       shape
}{
	{"(((", ")))", shapeRound},
	{"((", "))", shapeRound},
	{"([", "])", shapeRound},
	{"[[", "]]", shapeRect},
	{"[(", ")]", shapeRect},
	{"{{", "}}", shapeDiamond},
	{"[/", "/]", shapeRect},
	{"[/", `\]`, shapeRect},
	{`[\`, `\]`, shapeRect},
	{`[\`, "/]", shapeRect},
	{"[", "]", shapeRect},
	{"(", ")", shapeRound},
	{"{", "}", shapeDiamond},
	{">", "]", shapeRect},
}

var (
	reNodeID = regexp.MustCompile(`^[\p{L}\p{N}_]+`)
	reClass  = regexp.MustCompile(`^:::[\w-]+`)
	// A -- text --> B, A -. text .-> B, A == text ==> B
	reEdgeText = regexp.MustCompile(`^(--|==|-\.)\s+([^|]+?)\s+(-{2,}>|-{3,}|\.-+>|\.-+|={2,}>|={3,})`)
	// -->, --->, ---, -.->, -.-, ==>, ===, optionally |text|
	reEdge     = regexp.MustCompile(`^(-{2,}>|-{3,}|-\.+->|-\.+-|={2,}>|={3,})`)
	reEdgePipe = regexp.MustCompile(`^\|([^|]*)\|`)
	reHeader   = regexp.MustCompile(`(?i)^(graph|flowchart|flowchart-elk)(?:\s+(TB|TD|BT|RL|LR))?\s*;?\s*`)
	// statements lumi ignores: they only style the drawing
	reStyling = regexp.MustCompile(`^(classDef|class|style|linkStyle|click|accTitle|accDescr|direction)\b`)
)

func parseFlowchart(st []string) (*flowchart, error) {
	f := &flowchart{index: map[string]int{}}
	h := reHeader.FindStringSubmatch(st[0])
	if h == nil {
		return nil, errUnsupported
	}
	switch strings.ToUpper(h[2]) {
	case "", "TB", "TD":
	case "LR":
		f.horizontal = true
	default: // BT, RL: drawn upside down / mirrored — not worth faking
		return nil, errUnsupported
	}
	rest := []string{strings.TrimSpace(st[0][len(h[0]):])}
	rest = append(rest, st[1:]...)
	for _, line := range rest {
		for _, s := range splitStatements(line) {
			if s == "" || reStyling.MatchString(s) {
				continue
			}
			if err := f.statement(s); err != nil {
				return nil, err
			}
			if len(f.nodes) > maxNodes || len(f.edges) > maxEdges {
				return nil, errUnsupported
			}
		}
	}
	if len(f.nodes) == 0 {
		return nil, errUnsupported
	}
	return f, nil
}

// splitStatements cuts a line at ';' outside brackets and quotes.
func splitStatements(line string) []string {
	var out []string
	depth, quote, start := 0, false, 0
	for i, r := range line {
		switch {
		case r == '"':
			quote = !quote
		case quote:
		case strings.ContainsRune("([{", r):
			depth++
		case strings.ContainsRune(")]}", r):
			depth--
		case r == ';' && depth <= 0:
			out = append(out, strings.TrimSpace(line[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(line[start:]))
}

// statement parses `node (edge node)*`.
func (f *flowchart) statement(s string) error {
	if strings.HasPrefix(s, "subgraph") || s == "end" {
		return errUnsupported
	}
	prev, s, err := f.node(s)
	if err != nil {
		return err
	}
	for {
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		e, rest, err := parseEdge(s)
		if err != nil {
			return err
		}
		var next int
		next, s, err = f.node(strings.TrimSpace(rest))
		if err != nil {
			return err
		}
		e.from, e.to = prev, next
		f.edges = append(f.edges, e)
		prev = next
	}
}

func parseEdge(s string) (fedge, string, error) {
	e := fedge{}
	var op string
	if m := reEdgeText.FindStringSubmatch(s); m != nil {
		op = m[1] + m[3]
		e.label = cleanLabel(m[2], maxMsgLabel)
		s = s[len(m[0]):]
	} else if m := reEdge.FindString(s); m != "" {
		op = m
		s = s[len(m):]
		if p := reEdgePipe.FindStringSubmatch(strings.TrimLeft(s, " ")); p != nil {
			e.label = cleanLabel(p[1], maxMsgLabel)
			s = strings.TrimLeft(s, " ")[len(p[0]):]
		}
	} else {
		return e, "", errUnsupported
	}
	e.arrow = strings.HasSuffix(op, ">")
	switch {
	case strings.Contains(op, "."):
		e.style = dotted
	case strings.Contains(op, "="):
		e.style = thick
	}
	return e, s, nil
}

// node parses `id`, `id[label]`, `id(label)`, `id{label}` … and
// returns its index and the rest of s.
func (f *flowchart) node(s string) (int, string, error) {
	id := reNodeID.FindString(s)
	if id == "" {
		return 0, "", errUnsupported
	}
	s = s[len(id):]
	label, sh, has := "", shapeRect, false
	for _, sp := range shapes {
		if !strings.HasPrefix(s, sp.open) {
			continue
		}
		body := s[len(sp.open):]
		var end int
		if strings.HasPrefix(body, `"`) {
			q := strings.Index(body[1:], `"`)
			if q < 0 {
				return 0, "", errUnsupported
			}
			end = q + 2
			if !strings.HasPrefix(body[end:], sp.close) {
				continue
			}
		} else {
			end = strings.Index(body, sp.close)
			if end < 0 {
				continue
			}
		}
		label, sh, has = body[:end], sp.shape, true
		s = body[end+len(sp.close):]
		break
	}
	if !has && s != "" && strings.ContainsRune("([{>", rune(s[0])) {
		return 0, "", errUnsupported // an opener without its closer
	}
	if c := reClass.FindString(s); c != "" {
		s = s[len(c):]
	}
	i, ok := f.index[id]
	if !ok {
		i = len(f.nodes)
		f.index[id] = i
		f.nodes = append(f.nodes, fnode{id: id, label: clip(id, maxLabel)})
	}
	if has && !f.nodes[i].labelled {
		f.nodes[i].label = cleanLabel(label, maxLabel)
		if f.nodes[i].label == "" {
			f.nodes[i].label = clip(id, maxLabel)
		}
		f.nodes[i].shape = sh
		f.nodes[i].labelled = true
	}
	return i, s, nil
}

// --- layout ------------------------------------------------------------

// item is a node or a dummy (a long edge passing through a layer).
type item struct {
	node   int // -1: dummy
	layer  int
	pos    int // order inside the layer
	x, y   int // top-left
	w, h   int
	center int // anchor along the cross axis
}

// seg is one layer-to-layer piece of an edge.
type seg struct {
	from, to int // item indexes
	edge     int
	last     bool // ends at the real target
}

type layout struct {
	f      *flowchart
	items  []item
	layers [][]int
	segs   []seg
	back   []int // edges drawn as footnotes (cycles, self-loops)
}

func (f *flowchart) layout() *layout {
	n := len(f.nodes)
	out := make([][]int, n)
	for i, e := range f.edges {
		out[e.from] = append(out[e.from], i)
	}
	// back edges: DFS in declaration order
	state := make([]int, n) // 0 new, 1 on stack, 2 done
	isBack := make([]bool, len(f.edges))
	var dfs func(v int)
	dfs = func(v int) {
		state[v] = 1
		for _, ei := range out[v] {
			w := f.edges[ei].to
			switch state[w] {
			case 0:
				dfs(w)
			case 1:
				isBack[ei] = true
			}
		}
		state[v] = 2
	}
	for v := 0; v < n; v++ {
		if state[v] == 0 {
			dfs(v)
		}
	}
	// longest-path layering over the forward edges (Kahn)
	indeg := make([]int, n)
	for i, e := range f.edges {
		if !isBack[i] {
			indeg[e.to]++
		}
	}
	layer := make([]int, n)
	queue := []int{}
	for v := 0; v < n; v++ {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, ei := range out[v] {
			if isBack[ei] {
				continue
			}
			w := f.edges[ei].to
			layer[w] = max(layer[w], layer[v]+1)
			if indeg[w]--; indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	L := &layout{f: f}
	nl := 0
	for v := 0; v < n; v++ {
		nl = max(nl, layer[v]+1)
	}
	L.layers = make([][]int, nl)
	for v := 0; v < n; v++ {
		L.items = append(L.items, item{node: v, layer: layer[v]})
		L.layers[layer[v]] = append(L.layers[layer[v]], v)
	}
	for i, e := range f.edges {
		if isBack[i] {
			L.back = append(L.back, i)
			continue
		}
		prev := e.from
		for l := layer[e.from] + 1; l < layer[e.to]; l++ {
			d := len(L.items)
			L.items = append(L.items, item{node: -1, layer: l})
			L.layers[l] = append(L.layers[l], d)
			L.segs = append(L.segs, seg{from: prev, to: d, edge: i})
			prev = d
		}
		L.segs = append(L.segs, seg{from: prev, to: e.to, edge: i, last: true})
	}
	L.order()
	return L
}

// order reduces crossings with a few barycentre sweeps.
func (L *layout) order() {
	up := make([][]int, len(L.items))
	down := make([][]int, len(L.items))
	for _, s := range L.segs {
		down[s.from] = append(down[s.from], s.to)
		up[s.to] = append(up[s.to], s.from)
	}
	setPos := func() {
		for _, ly := range L.layers {
			for p, it := range ly {
				L.items[it].pos = p
			}
		}
	}
	setPos()
	for pass := 0; pass < 4; pass++ {
		fwd := pass%2 == 0
		for k := range L.layers {
			l := k
			nb := up
			if !fwd {
				l = len(L.layers) - 1 - k
				nb = down
			}
			ly := L.layers[l]
			bary := make(map[int]float64, len(ly))
			for _, it := range ly {
				if len(nb[it]) == 0 {
					bary[it] = float64(L.items[it].pos)
					continue
				}
				sum := 0
				for _, o := range nb[it] {
					sum += L.items[o].pos
				}
				bary[it] = float64(sum) / float64(len(nb[it]))
			}
			sort.SliceStable(ly, func(a, b int) bool { return bary[ly[a]] < bary[ly[b]] })
			for p, it := range ly {
				L.items[it].pos = p
			}
		}
	}
	setPos()
}

// place assigns cross-axis coordinates: each item is pulled towards the
// mean anchor of its neighbours (down, up, down), never overlapping.
// size is an item's extent on the cross axis, anchor its anchor offset.
func (L *layout) place(size func(it int) int, anchor func(it int) int, gap int) []int {
	pos := make([]int, len(L.items))
	up := make([][]int, len(L.items))
	down := make([][]int, len(L.items))
	for _, s := range L.segs {
		down[s.from] = append(down[s.from], s.to)
		up[s.to] = append(up[s.to], s.from)
	}
	for _, ly := range L.layers {
		x := 0
		for _, it := range ly {
			pos[it] = x
			x += size(it) + gap
		}
	}
	// sweep places layer l: every item wants the mean anchor of its
	// neighbours; items that would overlap merge into a block placed at
	// the mean of its members' wishes (so a fan-out centres under its
	// parent instead of drifting right).
	sweep := func(l int, nb [][]int) {
		type block struct {
			items            []int
			sum, n, w, start int
		}
		var blocks []*block
		for _, it := range L.layers[l] {
			want := pos[it]
			if len(nb[it]) > 0 {
				sum := 0
				for _, o := range nb[it] {
					sum += pos[o] + anchor(o)
				}
				want = sum/len(nb[it]) - anchor(it)
			}
			blocks = append(blocks, &block{items: []int{it}, sum: want, n: 1, w: size(it), start: want})
			for len(blocks) > 1 {
				p, c := blocks[len(blocks)-2], blocks[len(blocks)-1]
				if p.start+p.w+gap <= c.start {
					break
				}
				p.sum += c.sum - c.n*(p.w+gap)
				p.n += c.n
				p.w += gap + c.w
				p.items = append(p.items, c.items...)
				p.start = p.sum / p.n
				blocks = blocks[:len(blocks)-1]
			}
		}
		for _, b := range blocks {
			x := b.start
			for _, it := range b.items {
				pos[it] = x
				x += size(it) + gap
			}
		}
	}
	for pass := 0; pass < 4; pass++ {
		if pass%2 == 0 {
			for l := 1; l < len(L.layers); l++ {
				sweep(l, up)
			}
		} else {
			for l := len(L.layers) - 2; l >= 0; l-- {
				sweep(l, down)
			}
		}
	}
	lo := 1 << 30
	for i := range pos {
		lo = min(lo, pos[i])
	}
	for i := range pos {
		pos[i] -= lo
	}
	return pos
}

// --- drawing -------------------------------------------------------------

func (f *flowchart) render() []Line {
	L := f.layout()
	c := &canvas{}
	var notes []string
	if f.horizontal {
		notes = L.drawLR(c)
	} else {
		notes = L.drawTD(c)
	}
	lines := c.lines()
	for _, ei := range L.back {
		e := f.edges[ei]
		notes = append(notes, footnote(f, e))
	}
	if len(notes) > 0 {
		lines = append(lines, Line{})
		for _, n := range notes {
			lines = append(lines, Line{{Text: n, Class: Note}})
		}
	}
	return lines
}

func footnote(f *flowchart, e fedge) string {
	s := "↺ " + f.nodes[e.from].label
	if e.from != e.to {
		s = "↩ " + f.nodes[e.from].label + " → " + f.nodes[e.to].label
	}
	if e.label != "" {
		s += " (" + e.label + ")"
	}
	return s
}

func boxWidth(label string) int { return textWidth(label) + 4 }

// drawBox draws a node box with its top-left at (x,y), 3 rows high.
func drawBox(c *canvas, x, y, w int, n fnode) {
	c.hline(x, x+w-1, y, solid, Box)
	c.hline(x, x+w-1, y+2, solid, Box)
	c.vline(x, y, y+2, solid, Box)
	c.vline(x+w-1, y, y+2, solid, Box)
	tl, tr, bl, br := "┌", "┐", "└", "┘"
	switch n.shape {
	case shapeRound:
		tl, tr, bl, br = "╭", "╮", "╰", "╯"
	case shapeDiamond:
		tl, tr, bl, br = "╱", "╲", "╲", "╱"
	}
	c.put(x, y, tl, Box)
	c.put(x+w-1, y, tr, Box)
	c.put(x, y+2, bl, Box)
	c.put(x+w-1, y+2, br, Box)
	c.put(x+2, y+1, n.label, Text)
}

func (L *layout) drawTD(c *canvas) (notes []string) {
	f := L.f
	size := func(it int) int {
		if L.items[it].node < 0 {
			return 1
		}
		return boxWidth(f.nodes[L.items[it].node].label)
	}
	anchor := func(it int) int { return size(it) / 2 }
	xs := L.place(size, anchor, 3)
	for it := range L.items {
		L.items[it].center = xs[it] + anchor(it)
	}
	y := 0
	for l, ly := range L.layers {
		for _, it := range ly {
			L.items[it].x, L.items[it].y = xs[it], y
			L.items[it].w, L.items[it].h = size(it), 3
			L.items[it].center = xs[it] + anchor(it)
			if n := L.items[it].node; n >= 0 {
				drawBox(c, xs[it], y, size(it), f.nodes[n])
			} else {
				c.vline(L.items[it].center, y, y+2, f.edges[L.segAt(it).edge].style, Edge)
			}
		}
		if l == len(L.layers)-1 {
			break
		}
		// the band to the next layer
		var band []seg
		for _, s := range L.segs {
			if L.items[s.from].layer == l {
				band = append(band, s)
			}
		}
		tracks, ntracks := L.assignTracks(band)
		hasLabel := false
		for _, s := range band {
			if s.last && f.edges[s.edge].label != "" {
				hasLabel = true
			}
		}
		top := y + 3 // first band row
		labelRow := top + 1 + ntracks
		arrowRow := labelRow
		if hasLabel {
			arrowRow++
		}
		for _, s := range band {
			e := f.edges[s.edge]
			x1, x2 := L.items[s.from].center, L.items[s.to].center
			end := arrowRow
			if !s.last || !e.arrow {
				end = arrowRow + 1 // into the dummy / the target's border
			}
			if x1 == x2 {
				c.vline(x1, y+2, end, e.style, Edge)
			} else {
				ty := top + 1 + tracks[s.from]
				c.vline(x1, y+2, ty, e.style, Edge)
				c.hline(x1, x2, ty, e.style, Edge)
				c.vline(x2, ty, end, e.style, Edge)
			}
			if s.last && e.arrow {
				c.put(x2, arrowRow, "▼", Arrow)
			}
		}
		for _, s := range band {
			e := f.edges[s.edge]
			if !s.last || e.label == "" {
				continue
			}
			x2, w := L.items[s.to].center, textWidth(e.label)
			switch {
			case c.free(x2+1, x2+w+2, labelRow):
				c.put(x2+2, labelRow, e.label, Label)
			case c.free(x2-w-2, x2-1, labelRow):
				c.put(x2-w-1, labelRow, e.label, Label)
			default:
				notes = append(notes, "· "+f.nodes[e.from].label+" → "+f.nodes[e.to].label+": "+e.label)
			}
		}
		y = arrowRow + 1
	}
	return notes
}

func (L *layout) drawLR(c *canvas) (notes []string) {
	f := L.f
	size := func(it int) int {
		if L.items[it].node < 0 {
			return 1
		}
		return 3
	}
	anchor := func(it int) int { return size(it) / 2 }
	ys := L.place(size, anchor, 1)
	for it := range L.items {
		L.items[it].center = ys[it] + anchor(it)
	}
	x := 0
	for l, ly := range L.layers {
		colW := 1
		for _, it := range ly {
			if n := L.items[it].node; n >= 0 {
				colW = max(colW, boxWidth(f.nodes[n].label))
			}
		}
		for _, it := range ly {
			L.items[it].x, L.items[it].y = x, ys[it]
			L.items[it].center = ys[it] + anchor(it)
			if n := L.items[it].node; n >= 0 {
				L.items[it].w, L.items[it].h = boxWidth(f.nodes[n].label), 3
				drawBox(c, x, ys[it], L.items[it].w, f.nodes[n])
			} else {
				L.items[it].w, L.items[it].h = colW, 1
				c.hline(x, x+colW-1, ys[it], f.edges[L.segAt(it).edge].style, Edge)
			}
		}
		if l == len(L.layers)-1 {
			break
		}
		var band []seg
		labelW := 0
		for _, s := range L.segs {
			if L.items[s.from].layer == l {
				band = append(band, s)
				if e := f.edges[s.edge]; s.last && e.label != "" {
					labelW = max(labelW, textWidth(e.label)+2)
				}
			}
		}
		tracks, ntracks := L.assignTracks(band)
		start := x + colW // first band column
		labelX := start + 1 + ntracks
		arrowX := labelX + labelW + 1
		next := arrowX + 1
		for _, s := range band {
			e := f.edges[s.edge]
			from := L.items[s.from]
			y1, y2 := from.center, L.items[s.to].center
			x0 := from.x + from.w - 1 // the box's right border / dummy end
			end := arrowX
			if !s.last || !e.arrow {
				end = next // into the dummy / the target's border
			}
			if y1 == y2 {
				c.hline(x0, end, y1, e.style, Edge)
			} else {
				tx := start + 1 + tracks[s.from]
				c.hline(x0, tx, y1, e.style, Edge)
				c.vline(tx, y1, y2, e.style, Edge)
				c.hline(tx, end, y2, e.style, Edge)
			}
			if s.last && e.arrow {
				c.put(arrowX, y2, "▶", Arrow)
			}
		}
		used := map[int]bool{}
		for _, s := range band {
			e := f.edges[s.edge]
			if !s.last || e.label == "" {
				continue
			}
			y2 := L.items[s.to].center
			if used[y2] {
				notes = append(notes, "· "+f.nodes[e.from].label+" → "+f.nodes[e.to].label+": "+e.label)
				continue
			}
			used[y2] = true
			c.put(labelX+1, y2, e.label, Label)
		}
		x = next
	}
	return notes
}

// segAt is the segment that enters dummy it.
func (L *layout) segAt(it int) seg {
	for _, s := range L.segs {
		if s.to == it {
			return s
		}
	}
	return seg{}
}

// assignTracks gives every source with a bent segment in band a track
// (a row in TD, a column in LR). Sources whose spans do not overlap
// share one; spans may touch only where both end on the same target.
func (L *layout) assignTracks(band []seg) (map[int]int, int) {
	type span struct {
		src, lo, hi int
		ends        map[int]bool
	}
	by := map[int]*span{}
	var spans []*span
	for _, s := range band {
		a, b := L.items[s.from].center, L.items[s.to].center
		if a == b {
			continue
		}
		sp := by[s.from]
		if sp == nil {
			sp = &span{src: s.from, lo: a, hi: a, ends: map[int]bool{}}
			by[s.from] = sp
			spans = append(spans, sp)
		}
		sp.lo, sp.hi = min(sp.lo, b), max(sp.hi, b)
		sp.ends[b] = true
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })
	tracks := map[int]int{}
	var last []*span
	for _, sp := range spans {
		k := -1
		for t, l := range last {
			if l.hi+3 <= sp.lo || (l.hi == sp.lo && l.ends[l.hi] && sp.ends[sp.lo]) {
				k = t
				break
			}
		}
		if k < 0 {
			k = len(last)
			last = append(last, sp)
		}
		last[k] = sp
		tracks[sp.src] = k
	}
	return tracks, len(last)
}
