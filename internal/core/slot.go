package core

import (
	"math/rand"
	"time"

	"dmr-hotspot/internal/dmr"
	"dmr-hotspot/internal/logx"
	"dmr-hotspot/internal/network"
	"dmr-hotspot/internal/protocol"
)

var log = logx.GetLogger("core")

// Slot is the per-timeslot DMR state machine.
type Slot struct {
	SlotNo     int
	ColorCode  int
	HangFrames int

	StreamID   int
	Seq        int
	InVoice    bool
	LC         *dmr.DMRLC
	RXEmbedded *dmr.EmbeddedLC
	LastRFTS   time.Time
	LastNetTS  time.Time
}

func NewSlot(slotNo, colorCode, hang int) *Slot {
	return &Slot{
		SlotNo: slotNo, ColorCode: colorCode, HangFrames: hang,
		RXEmbedded: dmr.NewEmbeddedLC(),
	}
}

// ProcessRFFrame converts a modem RX frame into a network DMRData.
func (s *Slot) ProcessRFFrame(meta byte, slot []byte, rssi int) *network.DMRData {
	s.LastRFTS = time.Now()
	d := &network.DMRData{SlotNo: s.SlotNo, Data: append([]byte(nil), slot...), RSSI: rssi}

	switch {
	case meta&0x40 != 0:
		d.DataType = int(meta & 0x0F)
		d.N = 0
	case meta&0x20 != 0:
		d.DataType = protocol.DTVoiceSync
		d.N = 0
	default:
		d.DataType = protocol.DTVoice
		d.N = int(meta & 0x0F)
	}

	switch d.DataType {
	case protocol.DTVoiceLCHeader:
		hlc, err := dmr.FullLCDecode(slot, protocol.DTVoiceLCHeader)
		if err != nil || hlc == nil {
			return nil
		}
		s.LC = hlc
		s.RXEmbedded = dmr.NewEmbeddedLC()
		d.SrcID, d.DstID = hlc.SrcID, hlc.DstID
		if hlc.FLCO == dmr.FLCOGroup {
			d.FLCO = 0
		} else {
			d.FLCO = 1
		}
	case protocol.DTTerminatorWithLC:
		tlc, err := dmr.FullLCDecode(slot, protocol.DTTerminatorWithLC)
		if err != nil || tlc == nil {
			return nil
		}
		d.SrcID, d.DstID = tlc.SrcID, tlc.DstID
		if tlc.FLCO == dmr.FLCOGroup {
			d.FLCO = 0
		} else {
			d.FLCO = 1
		}
	default:
		if s.LC != nil {
			d.SrcID, d.DstID = s.LC.SrcID, s.LC.DstID
			if s.LC.FLCO == dmr.FLCOGroup {
				d.FLCO = 0
			} else {
				d.FLCO = 1
			}
		}
	}

	if d.DataType == protocol.DTVoiceLCHeader && !s.InVoice {
		s.StreamID = rand.Intn(0xFFFFFFFE-1) + 1
		s.InVoice = true
		s.Seq = 0
	}
	if s.InVoice {
		d.StreamID = s.StreamID
		d.SeqNo = s.Seq
		s.Seq = (s.Seq + 1) & 0xFF
	}
	if d.DataType == protocol.DTTerminatorWithLC {
		s.InVoice = false
		s.LC = nil
	}
	return d
}
