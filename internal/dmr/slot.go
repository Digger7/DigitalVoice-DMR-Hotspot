package dmr

// DMR slot: 33-byte burst structure, slot type (Golay) and sync handling.
// Ported from MMDVMHost DMRSlotType.cpp / DMRDefines / CSync (GPLv2).

var (
	dmrDataSyncBytes  = [6]byte{0xDF, 0xF5, 0x7D, 0x75, 0xDF, 0x5D}
	dmrVoiceSyncBytes = [6]byte{0x75, 0x5F, 0xD7, 0xDF, 0x75, 0xF7}
)

var slotTypeNames = map[byte]string{
	0x01: "LC_HEADER", 0x02: "TERM_LC", 0x03: "CSBK", 0x04: "DATA_HEADER",
	0x05: "RATE_12_DATA", 0x06: "RATE_34_DATA", 0x07: "IDLE", 0x08: "RATE_1_DATA",
	0xF0: "VOICE_SYNC", 0xF1: "VOICE",
}

func SlotTypeName(dt byte) string { return slotTypeNames[dt] }

type SlotType struct {
	ColorCode byte
	DataType  byte
}

func NewSlotType(colorCode, dataType byte) SlotType {
	return SlotType{ColorCode: colorCode, DataType: dataType}
}

func DecodeSlotType(burst33 []byte) SlotType {
	st := make([]byte, 3)
	st[0] = (burst33[12] << 2 & 0xFC) | (burst33[13] >> 6 & 0x03)
	st[1] = (burst33[13] << 2 & 0xC0) | (burst33[19] << 2 & 0x3C) | (burst33[20] >> 6 & 0x03)
	st[2] = burst33[20] << 2 & 0xF0
	code := Golay2087Decode(st)
	return SlotType{ColorCode: (code >> 4) & 0x0F, DataType: code & 0x0F}
}

func (st SlotType) Encode(burst33 []byte) {
	enc := Golay2087Encode((st.ColorCode&0x0F)<<4 | (st.DataType & 0x0F))
	burst33[12] = (burst33[12] & 0xC0) | (enc[0] >> 2 & 0x3F)
	burst33[13] = (burst33[13] & 0x0F) | (enc[0] << 6 & 0xC0) | (enc[1] >> 2 & 0x30)
	burst33[19] = (burst33[19] & 0xF0) | (enc[1] >> 2 & 0x0F)
	burst33[20] = (burst33[20] & 0x03) | (enc[1] << 6 & 0xC0) | (enc[2] >> 2 & 0x3C)
}

var syncMask = [7]byte{0x0F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xF0}

var (
	bsSourcedDataSync  = [7]byte{0x0D, 0xFF, 0x57, 0xD7, 0x5D, 0xF5, 0xD0}
	bsSourcedAudioSync = [7]byte{0x07, 0x55, 0xFD, 0x7D, 0xF7, 0x5F, 0x70}
	msSourcedDataSync  = [7]byte{0x0D, 0x5D, 0x7F, 0x77, 0xFD, 0x75, 0x70}
	msSourcedAudioSync = [7]byte{0x07, 0xF7, 0xD5, 0xDD, 0x57, 0xDF, 0xD0}
)

func addSync(burst33 []byte, sync [7]byte) {
	for i := 0; i < 7; i++ {
		burst33[13+i] = (burst33[13+i] &^ syncMask[i]) | (sync[i] & syncMask[i])
	}
}

func AddDataSync(burst33 []byte, duplex bool) {
	if duplex {
		addSync(burst33, bsSourcedDataSync)
	} else {
		addSync(burst33, msSourcedDataSync)
	}
}

func AddVoiceSync(burst33 []byte, duplex bool) {
	if duplex {
		addSync(burst33, bsSourcedAudioSync)
	} else {
		addSync(burst33, msSourcedAudioSync)
	}
}

func GetSyncType(burst33 []byte) string {
	data := [7]byte{}
	for i := 0; i < 7; i++ {
		data[i] = burst33[13+i] & syncMask[i]
	}
	patterns := []struct {
		name string
		pat  [7]byte
	}{
		{"BS_VOICE", bsSourcedAudioSync},
		{"MS_VOICE", msSourcedAudioSync},
		{"BS_DATA", bsSourcedDataSync},
		{"MS_DATA", msSourcedDataSync},
	}
	for _, p := range patterns {
		if data == p.pat {
			return p.name
		}
	}
	return ""
}

func EMBBytes(burst33 []byte) [2]byte {
	return [2]byte{
		(burst33[13] << 4 & 0xF0) | (burst33[14] >> 4 & 0x0F),
		(burst33[18] << 4 & 0xF0) | (burst33[19] >> 4 & 0x0F),
	}
}

func SetEMB(burst33 []byte, emb [2]byte) {
	burst33[13] = (burst33[13] & 0xF0) | (emb[0] >> 4 & 0x0F)
	burst33[14] = (burst33[14] & 0x0F) | (emb[0] << 4 & 0xF0)
	burst33[18] = (burst33[18] & 0xF0) | (emb[1] >> 4 & 0x0F)
	burst33[19] = (burst33[19] & 0x0F) | (emb[1] << 4 & 0xF0)
}
