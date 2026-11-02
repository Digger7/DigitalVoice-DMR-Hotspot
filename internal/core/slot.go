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

// slotIdleReset drops the slot call state after this much RF silence.
const slotIdleReset = 1000 * time.Millisecond

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
	now := time.Now()
	if !s.LastRFTS.IsZero() && now.Sub(s.LastRFTS) > slotIdleReset {
		// Previous transmission ended without a terminator; drop the
		// stale LC/stream state so a fresh call starts cleanly.
		s.InVoice = false
		s.LC = nil
		s.RXEmbedded.Reset()
	}
	s.LastRFTS = now
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

	// Feed embedded signalling carried by voice bursts; when a full
	// sequence decodes, its LC becomes the call context.
	if d.DataType == protocol.DTVoiceSync || d.DataType == protocol.DTVoice {
		if lcss := dmr.LCSSFromEMB(slot); lcss != 0 {
			if s.RXEmbedded.Add(slot, lcss) && s.RXEmbedded.LC != nil {
				s.LC = s.RXEmbedded.LC
			}
		}
	}

	switch d.DataType {
	case protocol.DTVoiceLCHeader:
		hlc, err := dmr.FullLCDecode(slot, protocol.DTVoiceLCHeader)
		if err != nil || hlc == nil {
			return nil
		}
		s.LC = hlc
		s.RXEmbedded.Reset()
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

	if !s.InVoice && (d.DataType == protocol.DTVoiceLCHeader || d.DataType == protocol.DTVoiceSync) {
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
