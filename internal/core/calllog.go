package core

import (
	"strconv"
	"time"

	"dmr-hotspot/internal/network"
)

const (
	callLogMax      = 50
	callIdleTimeout = 1500 * time.Millisecond
)

// CallLogEntry is one aggregated call, similar to a pi-star
// "Gateway Activity" row.
type CallLogEntry struct {
	ID       uint64    `json:"id"`
	Start    time.Time `json:"start"`
	Slot     int       `json:"slot"`
	Src      int       `json:"src"`
	Dst      int       `json:"dst"`
	FLCO     string    `json:"flco"`
	Source   string    `json:"source"` // "RF" or "Net"
	Duration float64   `json:"duration"`
	Loss     float64   `json:"loss"` // percent
	BER      float64   `json:"ber"`  // percent
	RSSI     int       `json:"rssi"`
	Active   bool      `json:"active"`
}

// activeCall aggregates the bursts of one ongoing call per source/slot.
type activeCall struct {
	id       uint64
	start    time.Time
	slot     int
	streamID int
	prevSeq  int
	seqMod   int
	src      int
	dst      int
	flco     string
	source   string
	lastTS   time.Time
	frames   int
	loss     int
	berSum   int
	berCount int
	rssi     int
}

func callKey(source string, slot int) string { return source + "/" + strconv.Itoa(slot) }

// noteCallFrame feeds one DMR burst into the per-call aggregator.
// net indicates the frame origin: false = RF, true = network.
//
// Burst A of every DMR superframe carries the VOICE sync pattern and is
// NOT a call boundary; a call only ends on a terminator, a stream or
// talker change, or silence.
func (c *Core) noteCallFrame(net bool, d network.DMRData) {
	source := "RF"
	if net {
		source = "Net"
	}
	key := callKey(source, d.SlotNo)
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	if d.IsTerminator() && c.active[key] != nil {
		c.active[key].lastTS = now
		c.finishCallLocked(key)
		return
	}

	a := c.active[key]
	if a == nil {
		a = c.startCallLocked(key, d, source, now)
	} else {
		startNew := now.Sub(a.lastTS) > callIdleTimeout
		if !startNew && !net && d.SrcID != 0 && a.src != 0 && d.SrcID != a.src {
			startNew = true // different talker started on the air
		}
		if !startNew && net && a.streamID != 0 && d.StreamID != 0 &&
			d.StreamID != a.streamID {
			startNew = true // different network stream
		}
		if startNew {
			c.finishCallLocked(key)
			a = c.startCallLocked(key, d, source, now)
		}
	}

	a.lastTS = now
	if d.SrcID != 0 {
		a.src = d.SrcID
	}
	if d.DstID != 0 {
		a.dst = d.DstID
	}
	if d.FLCO == 1 {
		a.flco = "PC"
	} else if d.FLCO == 0 {
		a.flco = "TG"
	}
	a.frames++
	if d.RSSI != 0 {
		a.rssi = d.RSSI
	}
	seq := d.SeqNo
	if !net {
		seq = d.N
	}
	if a.frames > 1 && a.seqMod > 0 {
		gap := (seq - a.prevSeq + a.seqMod) % a.seqMod
		if gap > 1 && gap < a.seqMod/2 {
			a.loss += gap - 1
		}
	}
	a.prevSeq = seq

	if d.IsVoice() || net {
		a.berSum += d.BER
		a.berCount++
	}
}

// startCallLocked opens a new tracker (call log lock must be held).
func (c *Core) startCallLocked(key string, d network.DMRData, source string, now time.Time) *activeCall {
	a := &activeCall{
		id:       c.nextCallID,
		start:    now,
		slot:     d.SlotNo,
		flco:     "TG",
		source:   source,
		seqMod:   256,
		prevSeq:  d.SeqNo,
		streamID: d.StreamID,
	}
	c.nextCallID++
	if source != "Net" {
		a.seqMod = 6
		a.prevSeq = d.N
	}
	c.active[key] = a
	return a
}

// noteCallEnd finalizes the ongoing call for a source (only used by
// tests / diagnostics).
func (c *Core) noteCallEnd(source string, slot int) {
	c.mu.Lock()
	c.finishCallLocked(callKey(source, slot))
	c.mu.Unlock()
}

// cleanupCalls finalizes calls whose bursts went silent (lost terminator).
func (c *Core) cleanupCalls(now time.Time) {
	c.mu.Lock()
	for key, a := range c.active {
		if now.Sub(a.lastTS) > callIdleTimeout {
			c.finishCallLocked(key)
		}
	}
	c.mu.Unlock()
}

func (c *Core) finishCallLocked(key string) {
	a, ok := c.active[key]
	if !ok {
		return
	}
	delete(c.active, key)
	ent := CallLogEntry{
		ID: a.id, Start: a.start, Slot: a.slot,
		Src: a.src, Dst: a.dst, FLCO: a.flco, Source: a.source, RSSI: a.rssi,
	}
	if dur := a.lastTS.Sub(a.start); dur > 0 {
		ent.Duration = dur.Seconds()
	}
	if total := a.frames + a.loss; total > 0 {
		ent.Loss = float64(a.loss) / float64(total) * 100
	}
	if a.berCount > 0 {
		ent.BER = float64(a.berSum) / float64(a.berCount)
	}
	c.callLog = append([]CallLogEntry{ent}, c.callLog...)
	if len(c.callLog) > callLogMax {
		c.callLog = c.callLog[:callLogMax]
	}
}

func (c *Core) clearCalls() {
	c.mu.Lock()
	c.callLog = nil
	c.active = map[string]*activeCall{}
	c.mu.Unlock()
}

// finishAllCalls freezes every ongoing call into the journal.
func (c *Core) finishAllCalls() {
	c.mu.Lock()
	for key := range c.active {
		c.finishCallLocked(key)
	}
	c.mu.Unlock()
}

// GatewayActivity returns the aggregated call journal, newest first; the
// ongoing calls carry a live duration.
func (c *Core) GatewayActivity() []CallLogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	out := make([]CallLogEntry, 0, len(c.callLog)+len(c.active))
	for _, a := range c.active {
		out = append(out, CallLogEntry{
			ID: a.id, Start: a.start, Slot: a.slot,
			Src: a.src, Dst: a.dst, FLCO: a.flco, Source: a.source, RSSI: a.rssi,
			Duration: now.Sub(a.start).Seconds(),
			Loss:     pct(a.loss, a.frames+a.loss),
			BER:      pct(a.berSum, a.berCount),
			Active:   true,
		})
	}
	out = append(out, c.callLog...)
	return out
}

func pct(part, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total) * 100
}
