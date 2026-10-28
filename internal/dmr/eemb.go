package dmr

// Embedded LC accumulation from EMB blocks in voice bursts. Ported from
// MMDVMHost DMREmbeddedData.cpp (GPLv2).

func extractEMBBlock(burst33 []byte) []byte {
	bits := make([]byte, 0, 40)
	for _, b := range burst33[14:19] {
		bits = append(bits, ByteToBitsBE(b)...)
	}
	return bits[:40]
}

type EmbeddedLC struct {
	state int
	raw   [128]byte
	data  [72]byte
	Valid bool
	LC    *DMRLC
}

func NewEmbeddedLC() *EmbeddedLC { return &EmbeddedLC{} }

func (e *EmbeddedLC) Reset() {
	e.state = 0
	e.raw = [128]byte{}
	e.data = [72]byte{}
	e.Valid = false
	e.LC = nil
}

func (e *EmbeddedLC) Add(burst33 []byte, lcss byte) bool {
	if len(burst33) < 33 {
		return false
	}
	block := extractEMBBlock(burst33)
	switch {
	case lcss == EMBSignalLC1st:
		copy(e.raw[0:32], block[4:36])
		e.state = 1
		e.Valid = false
		return false
	case lcss == EMBSignalLCMid && e.state == 1:
		copy(e.raw[32:64], block[4:36])
		e.state = 2
		return false
	case lcss == EMBSignalLCMid && e.state == 2:
		copy(e.raw[64:96], block[4:36])
		e.state = 3
		return false
	case lcss == EMBSignalLCLast && e.state == 3:
		copy(e.raw[96:128], block[4:36])
		e.state = 0
		return e.decode()
	}
	return false
}

func (e *EmbeddedLC) decode() bool {
	data := make([]byte, 128)
	b := 0
	for a := 0; a < 128; a++ {
		data[b] = e.raw[a]
		b += 16
		if b > 127 {
			b -= 127
		}
	}
	for a := 0; a < 112; a += 16 {
		if !Decode16114(data, a) {
			return false
		}
	}
	for a := 0; a < 16; a++ {
		if data[a]^data[a+16]^data[a+32]^data[a+48]^data[a+64]^data[a+80]^data[a+96]^data[a+112] != 0 {
			return false
		}
	}
	b = 0
	for _, a := range embDataPositions {
		e.data[b] = data[a]
		b++
	}
	var crc byte
	if data[42] != 0 {
		crc += 16
	}
	if data[58] != 0 {
		crc += 8
	}
	if data[74] != 0 {
		crc += 4
	}
	if data[90] != 0 {
		crc += 2
	}
	if data[106] != 0 {
		crc += 1
	}
	if crc != e.csum5() {
		return false
	}
	lcBytes := make([]byte, 9)
	for i := 0; i < 9; i++ {
		lcBytes[i] = BitsToByteBE(e.data[:], i*8)
	}
	lc := NewDMRLCFromBytes(lcBytes)
	e.LC = &lc
	e.Valid = true
	return true
}

func (e *EmbeddedLC) csum5() byte {
	var total int
	for i := 0; i < 72; i += 8 {
		total += int(BitsToByteBE(e.data[:], i))
	}
	return byte(total % 31)
}

var embDataPositions = []int{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10,
	16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26,
	32, 33, 34, 35, 36, 37, 38, 39, 40, 41,
	48, 49, 50, 51, 52, 53, 54, 55, 56, 57,
	64, 65, 66, 67, 68, 69, 70, 71, 72, 73,
	80, 81, 82, 83, 84, 85, 86, 87, 88, 89,
	96, 97, 98, 99, 100, 101, 102, 103, 104, 105,
}

func EncodeLCEMBMatrix(lc DMRLC) []byte {
	data9 := lc.GetData()
	var sum int
	for _, b := range data9 {
		sum += int(b)
	}
	crc := byte(sum % 31)
	bits := make([]byte, 128)
	bits[106] = crc & 1
	bits[90] = (crc >> 1) & 1
	bits[74] = (crc >> 2) & 1
	bits[58] = (crc >> 3) & 1
	bits[42] = (crc >> 4) & 1
	dbits := BytesToBits(data9)
	for i, a := range embDataPositions {
		bits[a] = dbits[i]
	}
	for a := 0; a < 112; a += 16 {
		Encode16114(bits, a)
	}
	for a := 0; a < 16; a++ {
		bits[a+112] = bits[a] ^ bits[a+16] ^ bits[a+32] ^ bits[a+48] ^
			bits[a+64] ^ bits[a+80] ^ bits[a+96]
	}
	raw := make([]byte, 128)
	b := 0
	for a := 0; a < 128; a++ {
		raw[a] = bits[b]
		b += 16
		if b > 127 {
			b -= 127
		}
	}
	return raw
}

// EmbedLCBlocks writes embedded signalling into four 33-byte voice bursts
// and returns the LCSS values [1,3,3,2].
func EmbedLCBlocks(lc DMRLC, bursts [][]byte) []byte {
	raw := EncodeLCEMBMatrix(lc)
	lcss := []byte{EMBSignalLC1st, EMBSignalLCMid, EMBSignalLCMid, EMBSignalLCLast}
	for n, burst := range bursts {
		if n >= 4 {
			break
		}
		blk := make([]byte, 40)
		copy(blk[4:36], raw[n*32:(n+1)*32])
		for i := 0; i < 5; i++ {
			var v byte
			for k := 0; k < 8; k++ {
				v |= blk[i*8+k] << (7 - k)
			}
			burst[14+i] = v
		}
	}
	return lcss
}
