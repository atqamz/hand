package board

import (
	"errors"
	"fmt"
	"html/template"
	"strings"
)

var (
	qrECC    = [...]int{0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26}
	qrBlocks = [...]int{0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5}
	qrAlign  = [...][]int{nil, nil, {6, 18}, {6, 22}, {6, 26}, {6, 30}, {6, 34}, {6, 22, 38}, {6, 24, 42}, {6, 26, 46}, {6, 28, 50}}
)

var errQRTooLong = errors.New("qr: text longer than 213 bytes")

type qrCode struct {
	size    int
	dark    [][]bool
	reserve [][]bool
}

func qrRaw(v int) int {
	n := (16*v+128)*v + 64
	if v >= 2 {
		a := v/7 + 2
		n -= (25*a-10)*a - 55
		if v >= 7 {
			n -= 36
		}
	}
	return n / 8
}

func qrData(v int) int { return qrRaw(v) - qrECC[v]*qrBlocks[v] }

func qrEncode(text string) (*qrCode, error) {
	q, err := qrLayout(text)
	if err != nil {
		return nil, err
	}
	best, score := 0, -1
	for m := range 8 {
		q.mask(m)
		q.format(m)
		if s := q.penalty(); score < 0 || s < score {
			best, score = m, s
		}
		q.mask(m)
	}
	q.mask(best)
	q.format(best)
	return q, nil
}

func qrLayout(text string) (*qrCode, error) {
	v := 1
	for ; v <= 10; v++ {
		count := 8
		if v >= 10 {
			count = 16
		}
		if 4+count+8*len(text) <= 8*qrData(v) {
			break
		}
	}
	if v > 10 {
		return nil, errQRTooLong
	}
	var bits []bool
	put := func(val, n int) {
		for i := n - 1; i >= 0; i-- {
			bits = append(bits, val>>i&1 == 1)
		}
	}
	put(4, 4)
	if v >= 10 {
		put(len(text), 16)
	} else {
		put(len(text), 8)
	}
	for i := 0; i < len(text); i++ {
		put(int(text[i]), 8)
	}
	capacity := 8 * qrData(v)
	put(0, min(4, capacity-len(bits)))
	put(0, (8-len(bits)%8)%8)
	data := make([]byte, 0, qrData(v))
	for i := 0; i < len(bits); i += 8 {
		var b byte
		for j := range 8 {
			if bits[i+j] {
				b |= 1 << (7 - j)
			}
		}
		data = append(data, b)
	}
	for pad := byte(0xEC); len(data) < qrData(v); pad ^= 0xEC ^ 0x11 {
		data = append(data, pad)
	}
	q := &qrCode{size: 17 + 4*v}
	q.dark, q.reserve = make([][]bool, q.size), make([][]bool, q.size)
	for i := range q.size {
		q.dark[i], q.reserve[i] = make([]bool, q.size), make([]bool, q.size)
	}
	q.functions(v)
	q.place(qrInterleave(v, data))
	return q, nil
}

func (q *qrCode) set(x, y int, dark bool) {
	q.dark[y][x], q.reserve[y][x] = dark, true
}

func (q *qrCode) functions(v int) {
	for i := range q.size {
		q.set(6, i, i%2 == 0)
		q.set(i, 6, i%2 == 0)
	}
	for _, c := range [][2]int{{3, 3}, {q.size - 4, 3}, {3, q.size - 4}} {
		for dy := -4; dy <= 4; dy++ {
			for dx := -4; dx <= 4; dx++ {
				x, y := c[0]+dx, c[1]+dy
				if x >= 0 && x < q.size && y >= 0 && y < q.size {
					d := max(qrAbs(dx), qrAbs(dy))
					q.set(x, y, d != 2 && d != 4)
				}
			}
		}
	}
	pos := qrAlign[v]
	for i, ay := range pos {
		for j, ax := range pos {
			last := len(pos) - 1
			if i == 0 && j == 0 || i == 0 && j == last || i == last && j == 0 {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					q.set(ax+dx, ay+dy, max(qrAbs(dx), qrAbs(dy)) != 1)
				}
			}
		}
	}
	q.format(0)
	if v >= 7 {
		rem := v
		for range 12 {
			rem = rem<<1 ^ (rem>>11)*0x1F25
		}
		bits := v<<12 | rem
		for i := range 18 {
			a, b := q.size-11+i%3, i/3
			q.set(a, b, bits>>i&1 == 1)
			q.set(b, a, bits>>i&1 == 1)
		}
	}
}

func (q *qrCode) format(mask int) {
	data := mask
	rem := data
	for range 10 {
		rem = rem<<1 ^ (rem>>9)*0x537
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return bits>>i&1 == 1 }
	for i := range 6 {
		q.set(8, i, bit(i))
	}
	q.set(8, 7, bit(6))
	q.set(8, 8, bit(7))
	q.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		q.set(14-i, 8, bit(i))
	}
	for i := range 8 {
		q.set(q.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		q.set(8, q.size-15+i, bit(i))
	}
	q.set(8, q.size-8, true)
}

func (q *qrCode) place(data []byte) {
	i := 0
	for right := q.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := range q.size {
			for j := range 2 {
				x := right - j
				y := vert
				if (right+1)&2 == 0 {
					y = q.size - 1 - vert
				}
				if !q.reserve[y][x] && i < len(data)*8 {
					q.dark[y][x] = data[i>>3]>>(7-i&7)&1 == 1
					i++
				}
			}
		}
	}
}

func (q *qrCode) mask(m int) {
	for y := range q.size {
		for x := range q.size {
			var flip bool
			switch m {
			case 0:
				flip = (x+y)%2 == 0
			case 1:
				flip = y%2 == 0
			case 2:
				flip = x%3 == 0
			case 3:
				flip = (x+y)%3 == 0
			case 4:
				flip = (x/3+y/2)%2 == 0
			case 5:
				flip = x*y%2+x*y%3 == 0
			case 6:
				flip = (x*y%2+x*y%3)%2 == 0
			default:
				flip = ((x+y)%2+x*y%3)%2 == 0
			}
			if flip && !q.reserve[y][x] {
				q.dark[y][x] = !q.dark[y][x]
			}
		}
	}
}

func (q *qrCode) penalty() int {
	n := q.size
	at := func(x, y int, col bool) bool {
		if col {
			return q.dark[x][y]
		}
		return q.dark[y][x]
	}
	score := 0
	finder := []bool{true, false, true, true, true, false, true}
	for _, col := range []bool{false, true} {
		for y := range n {
			run := 1
			for x := 1; x <= n; x++ {
				if x < n && at(x, y, col) == at(x-1, y, col) {
					run++
					continue
				}
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
			for x := 0; x+7 <= n; x++ {
				match := true
				for k, d := range finder {
					if at(x+k, y, col) != d {
						match = false
						break
					}
				}
				if !match {
					continue
				}
				light := func(from, to int) bool {
					for k := from; k < to; k++ {
						if k >= 0 && k < n && at(k, y, col) {
							return false
						}
					}
					return true
				}
				if light(x-4, x) {
					score += 40
				}
				if light(x+7, x+11) {
					score += 40
				}
			}
		}
	}
	dark := 0
	for y := range n {
		for x := range n {
			if q.dark[y][x] {
				dark++
			}
			if x+1 < n && y+1 < n && q.dark[y][x] == q.dark[y][x+1] && q.dark[y][x] == q.dark[y+1][x] && q.dark[y][x] == q.dark[y+1][x+1] {
				score += 3
			}
		}
	}
	total := n * n
	k := (qrAbs(dark*20-total*10)+total-1)/total - 1
	return score + 10*max(k, 0)
}

func qrInterleave(v int, data []byte) []byte {
	blocks, ecc, raw := qrBlocks[v], qrECC[v], qrRaw(v)
	short := blocks - raw%blocks
	shortLen := raw / blocks
	div := qrDivisor(ecc)
	var all [][]byte
	k := 0
	for i := range blocks {
		n := shortLen - ecc
		if i >= short {
			n++
		}
		dat := append([]byte(nil), data[k:k+n]...)
		k += n
		rem := qrRemainder(dat, div)
		if i < short {
			dat = append(dat, 0)
		}
		all = append(all, append(dat, rem...))
	}
	var out []byte
	for i := range all[0] {
		for j, b := range all {
			if i != shortLen-ecc || j >= short {
				out = append(out, b[i])
			}
		}
	}
	return out
}

func qrMul(x, y byte) byte {
	var z int
	for i := 7; i >= 0; i-- {
		z = z<<1 ^ (z>>7)*0x11D
		z ^= int(y>>i&1) * int(x)
	}
	return byte(z)
}

func qrDivisor(degree int) []byte {
	out := make([]byte, degree)
	out[degree-1] = 1
	root := byte(1)
	for range degree {
		for j := range degree {
			out[j] = qrMul(out[j], root)
			if j+1 < degree {
				out[j] ^= out[j+1]
			}
		}
		root = qrMul(root, 2)
	}
	return out
}

func qrRemainder(data, div []byte) []byte {
	out := make([]byte, len(div))
	for _, b := range data {
		factor := b ^ out[0]
		copy(out, out[1:])
		out[len(out)-1] = 0
		for i, d := range div {
			out[i] ^= qrMul(d, factor)
		}
	}
	return out
}

func qrAbs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func qrSVG(text, label string) (template.HTML, error) {
	q, err := qrEncode(text)
	if err != nil {
		return "", err
	}
	side := q.size + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg class="qr" viewBox="0 0 %d %d" role="img" aria-label="%s" shape-rendering="crispEdges"><rect width="%d" height="%d" fill="#fff"/><path fill="#000" d="`, side, side, template.HTMLEscapeString(label), side, side)
	for y := range q.size {
		for x := 0; x < q.size; {
			if !q.dark[y][x] {
				x++
				continue
			}
			w := 1
			for x+w < q.size && q.dark[y][x+w] {
				w++
			}
			fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", x+4, y+4, w, w)
			x += w
		}
	}
	b.WriteString(`"/></svg>`)
	return template.HTML(b.String()), nil
}
