package dmr

// BPTC(196,96) code for the full LC (voice header / terminator).
// Ported from MMDVMHost BPTC19696.cpp (GPLv2).

func extractBinary(in33 []byte) []byte {
	raw := make([]byte, 196)
	for i := 0; i < 12; i++ {
		copy(raw[i*8:], ByteToBitsBE(in33[i]))
	}
	bits := ByteToBitsBE(in33[20])
	raw[98] = bits[6]
	raw[99] = bits[7]
	for i := 0; i < 12; i++ {
		copy(raw[100+i*8:], ByteToBitsBE(in33[21+i]))
	}
	return raw
}

func deinterleave(raw []byte) []byte {
	out := make([]byte, 196)
	for a := 0; a < 196; a++ {
		out[a] = raw[(a*181)%196]
	}
	return out
}

func errorCheck(data []byte) {
	count := 0
	fixing := true
	for fixing && count < 5 {
		fixing = false
		for c := 0; c < 15; c++ {
			col := make([]byte, 13)
			for a := 0; a < 13; a++ {
				col[a] = data[c+1+a*15]
			}
			if Decode1393(col, 0) {
				pos := c + 1
				for a := 0; a < 13; a++ {
					data[pos] = col[a]
					pos += 15
				}
				fixing = true
			}
		}
		for r := 0; r < 9; r++ {
			if Decode15113_2(data, r*15+1) {
				fixing = true
			}
		}
		count++
	}
}

var lcDataPositions = []int{
	4, 5, 6, 7, 8, 9, 10, 11,
	16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26,
	31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41,
	46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56,
	61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71,
	76, 77, 78, 79, 80, 81, 82, 83, 84, 85, 86,
	91, 92, 93, 94, 95, 96, 97, 98, 99, 100, 101,
	106, 107, 108, 109, 110, 111, 112, 113, 114, 115, 116,
	121, 122, 123, 124, 125, 126, 127, 128, 129, 130, 131,
}

func DecodeFullLC(in33 []byte) []byte {
	raw := extractBinary(in33)
	deinter := deinterleave(raw)
	errorCheck(deinter)
	data := make([]byte, 96)
	for i, p := range lcDataPositions {
		data[i] = deinter[p]
	}
	out := make([]byte, 12)
	for i := 0; i < 12; i++ {
		out[i] = BitsToByteBE(data, i*8)
	}
	return out
}

func EncodeFullLC(lc12 []byte) []byte {
	data := make([]byte, 196)
	bdata := BytesToBits(lc12)
	for i, p := range lcDataPositions {
		data[p] = bdata[i]
	}
	for r := 0; r < 9; r++ {
		Encode15113_2(data, r*15+1)
	}
	for c := 0; c < 15; c++ {
		col := make([]byte, 13)
		for a := 0; a < 9; a++ {
			col[a] = data[c+1+a*15]
		}
		Encode1393(col, 0)
		cpar := 136 + c
		for pbit := 0; pbit < 4; pbit++ {
			data[cpar] = col[9+pbit]
			cpar += 15
		}
	}
	raw := make([]byte, 196)
	for a := 0; a < 196; a++ {
		raw[(a*181)%196] = data[a]
	}
	return Pack196To33(raw)
}

func Pack196To33(raw []byte) []byte {
	out := make([]byte, 33)
	for i := 0; i < 12; i++ {
		out[i] = BitsToByteBE(raw, i*8)
	}
	b := BitsToByteBE(raw, 96)
	out[12] = (out[12] & 0x3F) | (b & 0xC0)
	out[20] = (out[20] & 0xFC) | ((b >> 4) & 0x03)
	for i := 0; i < 12; i++ {
		out[21+i] = BitsToByteBE(raw, 100+i*8)
	}
	return out
}
