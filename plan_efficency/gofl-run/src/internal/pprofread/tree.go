package pprofread

import "sort"

// Cây ngữ cảnh gọi (phần "chuẩn bị chung" ở mục 4.1, Minh Long và Đức Anh
// dùng chung). Mỗi tiền tố của một stack là một nút; nút được định danh
// bằng (hàm, tệp) của khung cuối tiền tố, nên các call site khác dòng của
// cùng một hàm cha gộp vào một nút (giống nút VizTracer của paper).

// Node là một nút của cây.
type Node struct {
	Name     string
	File     string
	Parent   *Node
	Children []*Node // theo thứ tự xuất hiện đầu tiên
	childIdx map[Key]*Node

	SelfSamples  int64   // mẫu của các stack kết thúc đúng tại nút này
	TotalSamples int64   // mẫu của mọi stack đi qua nút này
	Self         float64 // giây
	Total        float64 // giây
	Depth        int     // gốc ảo = 0
	// lines: số mẫu theo dòng của khung này (dùng để chọn dòng đại diện).
	// Slice nhỏ thay cho map: thường chỉ 1–3 dòng, đỡ cấp phát.
	lines []lineCount
}

type lineCount struct{ line, n int64 }

func (n *Node) addLine(l, c int64) {
	for i := range n.lines {
		if n.lines[i].line == l {
			n.lines[i].n += c
			return
		}
	}
	n.lines = append(n.lines, lineCount{l, c})
}

func (n *Node) Key() Key { return Key{n.Name, n.File} }

// IsRoot: nút gốc ảo (không ứng với hàm nào).
func (n *Node) IsRoot() bool { return n.Parent == nil }

// Line trả dòng có nhiều mẫu nhất của khung này (hoà thì dòng nhỏ hơn).
func (n *Node) Line() int64 {
	var best, bestN int64
	for _, lc := range n.lines {
		if lc.n > bestN || (lc.n == bestN && lc.line < best) {
			best, bestN = lc.line, lc.n
		}
	}
	return best
}

// IsAncestorOf: n là tổ tiên (hoặc chính) m.
func (n *Node) IsAncestorOf(m *Node) bool {
	for x := m; x != nil; x = x.Parent {
		if x == n {
			return true
		}
	}
	return false
}

// Tree là cây ngữ cảnh gọi của một profile.
type Tree struct {
	Root   *Node
	ByFunc map[Key][]*Node // mọi nút của một hàm, theo thứ tự duyệt trước
}

// Tree dựng cây ngữ cảnh gọi từ các stack (dựng một lần, gọi lại dùng chung).
func (p *Profile) Tree() *Tree {
	if p.tree != nil {
		return p.tree
	}
	root := &Node{Name: "<root>"}
	for _, s := range p.Stacks {
		cur := root
		cur.TotalSamples += s.Samples
		for _, f := range s.Frames {
			k := f.Key()
			ch := cur.child(k)
			if ch == nil {
				ch = &Node{Name: f.Name, File: f.File, Parent: cur, Depth: cur.Depth + 1}
				cur.addChild(k, ch)
			}
			ch.TotalSamples += s.Samples
			ch.addLine(f.Line, s.Samples)
			cur = ch
		}
		cur.SelfSamples += s.Samples
	}
	t := &Tree{Root: root, ByFunc: map[Key][]*Node{}}
	var walk func(n *Node)
	walk = func(n *Node) {
		n.Self = float64(n.SelfSamples) * p.SecondsPerSample
		n.Total = float64(n.TotalSamples) * p.SecondsPerSample
		if !n.IsRoot() {
			t.ByFunc[n.Key()] = append(t.ByFunc[n.Key()], n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	p.tree = t
	return t
}

// Con được tìm tuyến tính khi ít, chuyển sang map khi nhiều hơn 8.
func (n *Node) child(k Key) *Node {
	if n.childIdx != nil {
		return n.childIdx[k]
	}
	for _, c := range n.Children {
		if c.Name == k.Name && c.File == k.File {
			return c
		}
	}
	return nil
}

func (n *Node) addChild(k Key, c *Node) {
	n.Children = append(n.Children, c)
	if n.childIdx == nil && len(n.Children) > 8 {
		n.childIdx = make(map[Key]*Node, 16)
		for _, x := range n.Children {
			n.childIdx[x.Key()] = x
		}
		return
	}
	if n.childIdx != nil {
		n.childIdx[k] = c
	}
}

// Occurrences: số nút của hàm trong cây (dùng cho *_occurrences ở
// hotspots.json, cách 1).
func (t *Tree) Occurrences(k Key) int { return len(t.ByFunc[k]) }

// MeanSelf: tổng self của hàm chia max(1, số nút) — "trung bình baseline"
// ở bước 4 cách 1.
func (t *Tree) MeanSelf(k Key) float64 {
	nodes := t.ByFunc[k]
	var sum float64
	for _, n := range nodes {
		sum += n.Self
	}
	d := len(nodes)
	if d < 1 {
		d = 1
	}
	return sum / float64(d)
}

// FindEntries trả mọi nút khớp chuỗi điểm vào, sắp theo Total giảm dần.
func (t *Tree) FindEntries(entry string) []*Node {
	var out []*Node
	for k, nodes := range t.ByFunc {
		if MatchEntry(k.Name, entry) {
			out = append(out, nodes...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].TotalSamples != out[j].TotalSamples {
			return out[i].TotalSamples > out[j].TotalSamples
		}
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// PathFrom: đường từ entry xuống target (gồm cả hai đầu), đi ngược theo
// nút cha như _path_from của paper. Trả nil nếu entry không là tổ tiên.
// Nếu entry là gốc ảo thì gốc không nằm trong kết quả.
func PathFrom(entry, target *Node) []*Node {
	var rev []*Node
	for x := target; x != nil; x = x.Parent {
		if x == entry {
			if !x.IsRoot() {
				rev = append(rev, x)
			}
			out := make([]*Node, len(rev))
			for i, n := range rev {
				out[len(rev)-1-i] = n
			}
			return out
		}
		rev = append(rev, x)
	}
	return nil
}
