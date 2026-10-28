package dmr

import "fmt"

// DMR voice burst <-> 72-bit AMBE+2 air frames, following the ETSI TS 102
// 361-1 interleave schedule used by DSD / mbelib and OpenDMR.

var rnW = [36]int{
	0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1,
	0, 1, 0, 1, 0, 2, 0, 2, 0, 2, 0, 2, 0, 2, 0, 2, 0, 2,
}

var rnX = [36]int{
	23, 10, 22, 9, 21, 8, 20, 7, 19, 6, 18, 5, 17, 4, 16, 3, 15, 2,
	14, 1, 13, 0, 12, 10, 11, 9, 10, 8, 9, 7, 8, 6, 7, 5, 6, 4,
}

var rnY = [36]int{
	0, 2, 0, 2, 0, 2, 0, 2, 0, 3, 0, 3, 1, 3, 1, 3, 1, 3,
	1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3, 1, 3,
}

var rnZ = [36]int{
	5, 3, 4, 2, 3, 1, 2, 0, 1, 13, 0, 12, 22, 11, 21, 10, 20, 9,
	19, 8, 18, 7, 17, 6, 16, 5, 15, 4, 14, 3, 13, 2, 12, 1, 11, 0,
}

var voicePos = func() []int {
	out := make([]int, 0, 216)
	for i := 0; i < 108; i++ {
		out = append(out, i)
	}
	for i := 156; i < 192; i++ {
		out = append(out, i)
	}
	for i := 192; i < 264; i++ {
		out = append(out, i)
	}
	return out
}()

func burstBits(burst33 []byte) []byte { return BytesToBits(burst33) }

func bitsToBurst33(bits []byte) []byte {
	if len(bits) != 264 {
		panic("bitsToBurst33: need 264 bits")
	}
	out := make([]byte, 33)
	for i, v := range bits {
		if v != 0 {
			out[i/8] |= 1 << (7 - (i % 8))
		}
	}
	return out
}

func cellsToFrame(cells [4][24]byte) []byte {
	out := make([]byte, 9)
	for i := 0; i < 24; i++ {
		if cells[0][23-i] != 0 {
			out[i/8] |= 1 << (7 - (i % 8))
		}
	}
	for i := 0; i < 23; i++ {
		if cells[1][22-i] != 0 {
			pos := 24 + i
			out[pos/8] |= 1 << (7 - (pos % 8))
		}
	}
	for i := 0; i < 11; i++ {
		if cells[2][10-i] != 0 {
			pos := 47 + i
			out[pos/8] |= 1 << (7 - (pos % 8))
		}
	}
	for i := 0; i < 14; i++ {
		if cells[3][13-i] != 0 {
			pos := 58 + i
			out[pos/8] |= 1 << (7 - (pos % 8))
		}
	}
	return out
}

func frameToCells(frame72 []byte) [4][24]byte {
	var cells [4][24]byte
	bits := burstBits(frame72)
	for i := 0; i < 24; i++ {
		cells[0][23-i] = bits[i]
	}
	for i := 0; i < 23; i++ {
		cells[1][22-i] = bits[24+i]
	}
	for i := 0; i < 11; i++ {
		cells[2][10-i] = bits[47+i]
	}
	for i := 0; i < 14; i++ {
		cells[3][13-i] = bits[58+i]
	}
	return cells
}

func voiceDeinterleave(cells *[4][24]byte, voice72 []byte) {
	for k := 0; k < 36; k++ {
		cells[rnW[k]][rnX[k]] = voice72[2*k]
		cells[rnY[k]][rnZ[k]] = voice72[2*k+1]
	}
}

func voiceInterleave(cells [4][24]byte, voice72 []byte) {
	for k := 0; k < 36; k++ {
		voice72[2*k] = cells[rnW[k]][rnX[k]]
		voice72[2*k+1] = cells[rnY[k]][rnZ[k]]
	}
}

// ExtractVoiceFrames returns the three 9-byte AMBE frames from a 33-byte burst.
func ExtractVoiceFrames(burst33 []byte) ([3][9]byte, error) {
	var frames [3][9]byte
	if len(burst33) != 33 {
		return frames, fmt.Errorf("burst must be 33 bytes, got %d", len(burst33))
	}
	bits := burstBits(burst33)
	voice := make([]byte, 216)
	for i, p := range voicePos {
		voice[i] = bits[p]
	}
	for f := 0; f < 3; f++ {
		chunk := voice[f*72 : (f+1)*72]
		cells := [4][24]byte{}
		voiceDeinterleave(&cells, chunk)
		copy(frames[f][:], cellsToFrame(cells))
	}
	return frames, nil
}

// BuildVoiceBurst encodes three 9-byte AMBE frames into one 33-byte burst.
// Sync/EMB and slot-type fields are left zeroed for the caller.
func BuildVoiceBurst(frame1, frame2, frame3 []byte) ([]byte, error) {
	frames := [3][]byte{frame1, frame2, frame3}
	for _, f := range frames {
		if len(f) != 9 {
			return nil, fmt.Errorf("each AMBE frame must be 9 bytes")
		}
	}
	voice := make([]byte, 0, 216)
	for _, f := range frames {
		cells := frameToCells(f)
		chunk := make([]byte, 72)
		voiceInterleave(cells, chunk)
		voice = append(voice, chunk...)
	}
	bits := make([]byte, 264)
	for j, p := range voicePos {
		bits[p] = voice[j]
	}
	return bitsToBurst33(bits), nil
}

// VoiceRoundtrip extracts and rebuilds a burst, reinserting the sync/EMB field.
func VoiceRoundtrip(burst33 []byte) []byte {
	frames, err := ExtractVoiceFrames(burst33)
	if err != nil {
		return nil
	}
	b, err := BuildVoiceBurst(frames[0][:], frames[1][:], frames[2][:])
	if err != nil {
		return nil
	}
	b[13] = (b[13] & 0xF0) | (burst33[13] & 0x0F)
	b[19] = (b[19] & 0x0F) | (burst33[19] & 0xF0)
	for i := 14; i < 19; i++ {
		b[i] = burst33[i]
	}
	return b
}
