package core

import (
	"testing"
	"time"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/network"
	"dmr-hotspot/internal/protocol"
)

func callCfg() config.AppConfig {
	var cfg config.AppConfig
	cfg.Mode = "network"
	return cfg
}

func finishedEntries(c *Core) []CallLogEntry {
	var out []CallLogEntry
	for _, e := range c.GatewayActivity() {
		if !e.Active {
			out = append(out, e)
		}
	}
	return out
}

func TestCallLogAggregatesNetCall(t *testing.T) {
	c := NewCore(callCfg())
	mk := func(seq, ber int, dt int) network.DMRData {
		return network.DMRData{SlotNo: 2, SrcID: 2500123, DstID: 2501,
			FLCO: 0, DataType: dt, SeqNo: seq, StreamID: 42, BER: ber}
	}
	// Full call: sync burst first, then voice frames, one frame lost
	// mid-stream (seq 4), then the terminator.
	c.noteCallFrame(true, mk(1, 2, protocol.DTVoiceSync))
	for seq := 2; seq <= 3; seq++ {
		c.noteCallFrame(true, mk(seq, 2, protocol.DTVoice))
	}
	c.noteCallFrame(true, mk(5, 2, protocol.DTVoice))
	c.noteCallFrame(true, mk(6, 2, protocol.DTVoice))
	c.noteCallFrame(true, mk(7, 2, protocol.DTVoice))
	c.noteCallFrame(true, mk(0, 2, protocol.DTTerminatorWithLC))

	log := c.GatewayActivity()
	if len(log) != 1 {
		t.Fatalf("want 1 entry, got %d: %+v", len(log), log)
	}
	e := log[0]
	if e.Active || e.Src != 2500123 || e.Dst != 2501 || e.FLCO != "TG" ||
		e.Source != "Net" || e.Slot != 2 {
		t.Fatalf("bad entry: %+v", e)
	}
	if e.Loss < 12 || e.Loss > 16 { // 1 lost of 7 (frames+loss)
		t.Fatalf("loss %.1f%%, want ~14.3%%", e.Loss)
	}
	if e.BER != 2 {
		t.Fatalf("ber %.1f, want 2", e.BER)
	}
}

func TestCallLogNetMidCallSyncDoesNotSplit(t *testing.T) {
	c := NewCore(callCfg())
	base := network.DMRData{SlotNo: 1, SrcID: 111, DstID: 91, StreamID: 7}
	seq := 0
	// 3 superframes: sync-flagged burst at the start of a superframe
	// plus regular voice frames.
	for sf := 0; sf < 3; sf++ {
		s := base
		s.DataType = protocol.DTVoiceSync
		s.SeqNo = seq
		c.noteCallFrame(true, s)
		seq++
		for n := 1; n <= 5; n++ {
			v := base
			v.DataType = protocol.DTVoice
			v.SeqNo = seq
			c.noteCallFrame(true, v)
			seq++
		}
	}
	c.noteCallFrame(true, func() network.DMRData {
		d := base
		d.DataType = protocol.DTTerminatorWithLC
		return d
	}())
	if got := len(c.GatewayActivity()); got != 1 {
		t.Fatalf("want 1 entry across 3 superframes, got %d", got)
	}
}

func TestCallLogRFMidCallSyncDoesNotSplit(t *testing.T) {
	c := NewCore(callCfg())
	mk := func(n int, dt int, stream int) network.DMRData {
		return network.DMRData{SlotNo: 2, SrcID: 2500123, DstID: 2501,
			FLCO: 0, DataType: dt, StreamID: stream, N: n, RSSI: 90}
	}
	c.noteCallFrame(false, mk(0, protocol.DTVoiceLCHeader, 9))
	for sf := 0; sf < 3; sf++ {
		c.noteCallFrame(false, mk(0, protocol.DTVoiceSync, 9)) // burst A
		for n := 1; n <= 5; n++ {
			c.noteCallFrame(false, mk(n, protocol.DTVoice, 9))
		}
	}
	c.noteCallFrame(false, mk(0, protocol.DTTerminatorWithLC, 9))
	log := c.GatewayActivity()
	if len(log) != 1 {
		t.Fatalf("want 1 entry across 3 superframes, got %d: %+v", len(log), log)
	}
	if e := log[0]; e.Active || e.Source != "RF" || e.Src != 2500123 || e.Loss != 0 {
		t.Fatalf("bad RF entry: %+v", e)
	}
	if log[0].Duration == 0 {
		t.Fatalf("duration must not be zero for a multi-frame call")
	}
}

func TestCallLogStreamChangeStartsNewEntry(t *testing.T) {
	c := NewCore(callCfg())
	c.noteCallFrame(true, network.DMRData{SlotNo: 1, SrcID: 111, DstID: 91,
		DataType: protocol.DTVoiceSync, StreamID: 7})
	c.noteCallFrame(true, network.DMRData{SlotNo: 1, SrcID: 111, DstID: 91,
		DataType: protocol.DTVoice, StreamID: 7})
	c.noteCallFrame(true, network.DMRData{SlotNo: 1, SrcID: 222, DstID: 91,
		DataType: protocol.DTVoiceSync, StreamID: 8})
	if fin := finishedEntries(c); len(fin) != 1 || fin[0].Src != 111 {
		t.Fatalf("want finished entry src 111: %+v", fin)
	}
	if live := c.GatewayActivity(); len(live) != 2 || !live[0].Active || live[0].Src != 222 {
		t.Fatalf("want live entry src 222: %+v", live)
	}
	c.noteCallFrame(true, network.DMRData{SlotNo: 1, SrcID: 222, DstID: 91,
		DataType: protocol.DTTerminatorWithLC, StreamID: 8})
	log := c.GatewayActivity()
	if len(log) != 2 || log[0].Src != 222 || log[1].Src != 111 {
		t.Fatalf("order mismatch: %+v", log)
	}
}

func TestCallLogRFNewTalkerStartsNewEntry(t *testing.T) {
	c := NewCore(callCfg())
	mk := func(src int, dt int) network.DMRData {
		return network.DMRData{SlotNo: 2, SrcID: src, DstID: 2501,
			FLCO: 0, DataType: dt, StreamID: 5, N: 0}
	}
	c.noteCallFrame(false, mk(111, protocol.DTVoiceLCHeader))
	c.noteCallFrame(false, mk(111, protocol.DTVoice))
	// Someone else keys up before the idle timeout: talker change splits.
	c.noteCallFrame(false, mk(222, protocol.DTVoiceLCHeader))
	if fin := finishedEntries(c); len(fin) != 1 || fin[0].Src != 111 {
		t.Fatalf("want finished entry src 111: %+v", fin)
	}
	c.cleanupCalls(time.Now().Add(2 * callIdleTimeout))
	live := c.GatewayActivity()
	if len(live) != 2 || live[0].Src != 222 || live[1].Src != 111 {
		t.Fatalf("want entries 222 / 111: %+v", live)
	}
}

func TestCallLogRFSilenceFinalizes(t *testing.T) {
	c := NewCore(callCfg())
	mk := func(n int) network.DMRData {
		return network.DMRData{SlotNo: 2, SrcID: 5, DstID: 91, StreamID: 9,
			DataType: protocol.DTVoice, N: n, RSSI: 90}
	}
	c.noteCallFrame(false, mk(0))
	c.noteCallFrame(false, mk(2)) // frame N=1 lost on the air
	c.cleanupCalls(time.Now().Add(2 * callIdleTimeout))
	log := c.GatewayActivity()
	if len(log) != 1 {
		t.Fatalf("want 1 entry, got %d", len(log))
	}
	if e := log[0]; e.Source != "RF" || e.Loss < 25 || e.Loss > 35 || e.RSSI == 0 {
		t.Fatalf("bad RF entry: %+v", e)
	}

	// Live call shows up as active before the terminator.
	c.noteCallFrame(true, network.DMRData{SlotNo: 2, SrcID: 77, DstID: 98,
		DataType: protocol.DTVoiceSync, StreamID: 12})
	live := c.GatewayActivity()
	if !live[0].Active || live[0].Src != 77 {
		t.Fatalf("want live entry first: %+v", live)
	}
}
