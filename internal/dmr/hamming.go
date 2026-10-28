package dmr

// Hamming codes used by DMR, ported from MMDVMHost Hamming.cpp.
// Functions operate on bit slices (values 0/1), in place.

func flip(d []byte, i int) { d[i] ^= 1 }

func Decode15113_2(d []byte, start int) bool {
	a := start
	c0 := d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+5] ^ d[a+7] ^ d[a+8]
	c1 := d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+4] ^ d[a+6] ^ d[a+8] ^ d[a+9]
	c2 := d[a+2] ^ d[a+3] ^ d[a+4] ^ d[a+5] ^ d[a+7] ^ d[a+9] ^ d[a+10]
	c3 := d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+4] ^ d[a+6] ^ d[a+7] ^ d[a+10]
	var n int
	if c0 != d[a+11] {
		n |= 0x01
	}
	if c1 != d[a+12] {
		n |= 0x02
	}
	if c2 != d[a+13] {
		n |= 0x04
	}
	if c3 != d[a+14] {
		n |= 0x08
	}
	parity := map[int]int{0x01: 11, 0x02: 12, 0x04: 13, 0x08: 14}
	data := map[int]int{0x09: 0, 0x0B: 1, 0x0F: 2, 0x07: 3, 0x0E: 4, 0x05: 5,
		0x0A: 6, 0x0D: 7, 0x03: 8, 0x06: 9, 0x0C: 10}
	if p, ok := parity[n]; ok {
		flip(d, a+p)
		return true
	}
	if p, ok := data[n]; ok {
		flip(d, a+p)
		return true
	}
	return false
}

func Encode15113_2(d []byte, start int) {
	a := start
	d[a+11] = d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+5] ^ d[a+7] ^ d[a+8]
	d[a+12] = d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+4] ^ d[a+6] ^ d[a+8] ^ d[a+9]
	d[a+13] = d[a+2] ^ d[a+3] ^ d[a+4] ^ d[a+5] ^ d[a+7] ^ d[a+9] ^ d[a+10]
	d[a+14] = d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+4] ^ d[a+6] ^ d[a+7] ^ d[a+10]
}

func Decode1393(d []byte, start int) bool {
	a := start
	c0 := d[a+0] ^ d[a+1] ^ d[a+3] ^ d[a+5] ^ d[a+6]
	c1 := d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+4] ^ d[a+6] ^ d[a+7]
	c2 := d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+5] ^ d[a+7] ^ d[a+8]
	c3 := d[a+0] ^ d[a+2] ^ d[a+4] ^ d[a+5] ^ d[a+8]
	var n int
	if c0 != d[a+9] {
		n |= 0x01
	}
	if c1 != d[a+10] {
		n |= 0x02
	}
	if c2 != d[a+11] {
		n |= 0x04
	}
	if c3 != d[a+12] {
		n |= 0x08
	}
	parity := map[int]int{0x01: 9, 0x02: 10, 0x04: 11, 0x08: 12}
	data := map[int]int{0x0F: 0, 0x07: 1, 0x0E: 2, 0x05: 3, 0x0A: 4, 0x0D: 5,
		0x03: 6, 0x06: 7, 0x0C: 8}
	if p, ok := parity[n]; ok {
		flip(d, a+p)
		return true
	}
	if p, ok := data[n]; ok {
		flip(d, a+p)
		return true
	}
	return false
}

func Encode1393(d []byte, start int) {
	a := start
	d[a+9] = d[a+0] ^ d[a+1] ^ d[a+3] ^ d[a+5] ^ d[a+6]
	d[a+10] = d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+4] ^ d[a+6] ^ d[a+7]
	d[a+11] = d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+5] ^ d[a+7] ^ d[a+8]
	d[a+12] = d[a+0] ^ d[a+2] ^ d[a+4] ^ d[a+5] ^ d[a+8]
}

func Decode16114(d []byte, start int) bool {
	a := start
	c0 := d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+5] ^ d[a+7] ^ d[a+8]
	c1 := d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+4] ^ d[a+6] ^ d[a+8] ^ d[a+9]
	c2 := d[a+2] ^ d[a+3] ^ d[a+4] ^ d[a+5] ^ d[a+7] ^ d[a+9] ^ d[a+10]
	c3 := d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+4] ^ d[a+6] ^ d[a+7] ^ d[a+10]
	c4 := d[a+0] ^ d[a+2] ^ d[a+5] ^ d[a+6] ^ d[a+8] ^ d[a+9] ^ d[a+10]
	var n int
	if c0 != d[a+11] {
		n |= 0x01
	}
	if c1 != d[a+12] {
		n |= 0x02
	}
	if c2 != d[a+13] {
		n |= 0x04
	}
	if c3 != d[a+14] {
		n |= 0x08
	}
	if c4 != d[a+15] {
		n |= 0x10
	}
	parity := map[int]int{0x01: 11, 0x02: 12, 0x04: 13, 0x08: 14, 0x10: 15}
	data := map[int]int{0x19: 0, 0x0B: 1, 0x1F: 2, 0x07: 3, 0x0E: 4, 0x15: 5,
		0x1A: 6, 0x0D: 7, 0x13: 8, 0x16: 9, 0x1C: 10}
	if p, ok := parity[n]; ok {
		flip(d, a+p)
		return true
	}
	if p, ok := data[n]; ok {
		flip(d, a+p)
		return true
	}
	return n == 0
}

func Encode16114(d []byte, start int) {
	a := start
	d[a+11] = d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+5] ^ d[a+7] ^ d[a+8]
	d[a+12] = d[a+1] ^ d[a+2] ^ d[a+3] ^ d[a+4] ^ d[a+6] ^ d[a+8] ^ d[a+9]
	d[a+13] = d[a+2] ^ d[a+3] ^ d[a+4] ^ d[a+5] ^ d[a+7] ^ d[a+9] ^ d[a+10]
	d[a+14] = d[a+0] ^ d[a+1] ^ d[a+2] ^ d[a+4] ^ d[a+6] ^ d[a+7] ^ d[a+10]
	d[a+15] = d[a+0] ^ d[a+2] ^ d[a+5] ^ d[a+6] ^ d[a+8] ^ d[a+9] ^ d[a+10]
}
