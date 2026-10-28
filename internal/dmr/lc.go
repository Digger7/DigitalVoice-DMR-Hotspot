package dmr

import "fmt"

const (
	FLCOGroup    = 0x00
	FLCOUserUser = 0x03

	DTVoiceLCHeader    = 0x01
	DTTerminatorWithLC = 0x02

	EMBSignalSync              = 0x00
	EMBSignalLC1st             = 0x01
	EMBSignalLCMid             = 0x03
	EMBSignalLCLast            = 0x02
	EMBSignalTalkerAliasHeader = 0x07
	EMBSignalTalkerAliasBlk1   = 0x05
	EMBSignalTalkerAliasBlk2   = 0x04
)

var (
	voiceLCHeaderCRCMask    = [3]byte{0x96, 0x96, 0x96}
	terminatorWithLCCRCMask = [3]byte{0x99, 0x99, 0x99}
)

type DMRLC struct {
	FLCO     byte
	SrcID    int
	DstID    int
	Service  byte
	Priority byte
}

func NewDMRLCFromBytes(data []byte) DMRLC {
	return DMRLC{
		FLCO:    data[0] & 0x3F,
		SrcID:   int(data[6])<<16 | int(data[7])<<8 | int(data[8]),
		DstID:   int(data[3])<<16 | int(data[4])<<8 | int(data[5]),
		Service: data[2],
	}
}

func NewDMRLC(flco byte, srcID, dstID int, service byte) DMRLC {
	return DMRLC{FLCO: flco, SrcID: srcID, DstID: dstID, Service: service}
}

func (lc DMRLC) GetData() []byte {
	b := make([]byte, 9)
	b[0] = lc.FLCO & 0x3F
	b[1] = 0x00
	b[2] = lc.Service
	b[3] = byte((lc.DstID >> 16) & 0xFF)
	b[4] = byte((lc.DstID >> 8) & 0xFF)
	b[5] = byte(lc.DstID & 0xFF)
	b[6] = byte((lc.SrcID >> 16) & 0xFF)
	b[7] = byte((lc.SrcID >> 8) & 0xFF)
	b[8] = byte(lc.SrcID & 0xFF)
	return b
}

func (lc DMRLC) IsGroup() bool { return lc.FLCO == FLCOGroup }

func FullLCEncode(lc DMRLC, dataType byte) ([]byte, error) {
	lcData := lc.GetData()
	parity := RSEncode(lcData[:9], 9, 4)
	switch dataType {
	case DTVoiceLCHeader:
		lcData = append(lcData,
			parity[2]^voiceLCHeaderCRCMask[0],
			parity[1]^voiceLCHeaderCRCMask[1],
			parity[0]^voiceLCHeaderCRCMask[2])
	case DTTerminatorWithLC:
		lcData = append(lcData,
			parity[2]^terminatorWithLCCRCMask[0],
			parity[1]^terminatorWithLCCRCMask[1],
			parity[0]^terminatorWithLCCRCMask[2])
	default:
		return nil, fmt.Errorf("unsupported data type 0x%02X", dataType)
	}
	return EncodeFullLC(lcData), nil
}

func FullLCDecode(burst33 []byte, dataType byte) (*DMRLC, error) {
	if len(burst33) != 33 {
		return nil, fmt.Errorf("burst must be 33 bytes")
	}
	lcData := DecodeFullLC(burst33)
	switch dataType {
	case DTVoiceLCHeader:
		lcData[9] ^= voiceLCHeaderCRCMask[0]
		lcData[10] ^= voiceLCHeaderCRCMask[1]
		lcData[11] ^= voiceLCHeaderCRCMask[2]
	case DTTerminatorWithLC:
		lcData[9] ^= terminatorWithLCCRCMask[0]
		lcData[10] ^= terminatorWithLCCRCMask[1]
		lcData[11] ^= terminatorWithLCCRCMask[2]
	default:
		return nil, fmt.Errorf("unsupported data type 0x%02X", dataType)
	}
	if !RSCheck(lcData) {
		return nil, fmt.Errorf("RS check failed")
	}
	lc := NewDMRLCFromBytes(lcData)
	return &lc, nil
}

// DemodulatedEMB is the decoded embedded signalling block.
type DemodulatedEMB struct {
	ColorCode byte
	PI        bool
	LCSS      byte
	Valid     bool
}

func QREMBExtract(burst33 []byte) [2]byte {
	return [2]byte{
		(burst33[13] << 4 & 0xF0) | (burst33[14] >> 4 & 0x0F),
		(burst33[18] << 4 & 0xF0) | (burst33[19] >> 4 & 0x0F),
	}
}

func QREMBInsert(burst33 []byte, emb [2]byte) {
	burst33[13] = (burst33[13] & 0xF0) | (emb[0] >> 4 & 0x0F)
	burst33[14] = (burst33[14] & 0x0F) | (emb[0] << 4 & 0xF0)
	burst33[18] = (burst33[18] & 0xF0) | (emb[1] >> 4 & 0x0F)
	burst33[19] = (burst33[19] & 0x0F) | (emb[1] << 4 & 0xF0)
}

func EMBDecode(burst33 []byte) DemodulatedEMB {
	emb2 := QREMBExtract(burst33)
	code := QR1676Decode(emb2[:])
	return DemodulatedEMB{
		ColorCode: (code >> 4) & 0x0F,
		PI:        code&0x08 == 0x08,
		LCSS:      (code >> 1) & 0x03,
		Valid:     true,
	}
}

func EMBEncode(colorCode int, pi bool, lcss byte) [2]byte {
	var b byte = byte((colorCode & 0x0F) << 4)
	if pi {
		b |= 0x08
	}
	b |= (lcss & 0x03) << 1
	return QR1676Encode(b)
}
