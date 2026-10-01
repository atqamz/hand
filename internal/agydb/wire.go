package agydb

import "encoding/binary"

type field struct {
	num    int
	wire   int
	varint uint64
	bytes  []byte
}

func fields(b []byte) ([]field, bool) {
	var out []field
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 || key>>3 == 0 {
			return nil, false
		}
		b = b[n:]
		f := field{num: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case 0:
			v, n := binary.Uvarint(b)
			if n <= 0 {
				return nil, false
			}
			f.varint, b = v, b[n:]
		case 1:
			if len(b) < 8 {
				return nil, false
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return nil, false
			}
			b = b[4:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return nil, false
			}
			f.bytes, b = b[n:n+int(l)], b[n+int(l):]
		default:
			return nil, false
		}
		out = append(out, f)
	}
	return out, true
}

func path(b []byte, nums ...int) (field, bool) {
	var found field
	for i, num := range nums {
		fs, ok := fields(b)
		if !ok {
			return field{}, false
		}
		hit := false
		for _, f := range fs {
			if f.num == num {
				found, hit = f, true
				break
			}
		}
		if !hit || (i < len(nums)-1 && found.wire != 2) {
			return field{}, false
		}
		b = found.bytes
	}
	return found, true
}
