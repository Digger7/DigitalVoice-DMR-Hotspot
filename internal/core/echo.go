package core

import (
	"sync"
	"time"

	"dmr-hotspot/internal/dmr"
	"dmr-hotspot/internal/protocol"
)

const (
	metaDataSync  = 0x40
	metaAudioSync = 0x20

	// DMRSlotInterval is one 33-byte DMR burst of air time (~27.5 ms).
	DMRSlotInterval = 27 * time.Millisecond

	defaultEchoMaxQueue    = 300
	defaultEchoHoldTimeout = 1500 * time.Millisecond
)

// EchoRepeater buffers a received transmission and replays it after the call
// ends (offline parrot repeater).
type EchoRepeater struct {
	ColorCode   int
	MaxQueue    int
	HoldTimeout time.Duration

	mu            sync.Mutex
	queue         [][]byte
	framesRX      int
	framesTX      int
	framesLost    int
	lastActivity  time.Time
	lastHeardSrc  int
	lastHeardDst  int
	lastHeardFLCO string
	onLastHeard   func(src, dst int, flco string)
	replaying     bool
	lastEmit      time.Time
}

func NewEchoRepeater(colorCode, maxQueue int, holdTimeout time.Duration) *EchoRepeater {
	if maxQueue <= 0 {
		maxQueue = defaultEchoMaxQueue
	}
	if holdTimeout <= 0 {
		holdTimeout = defaultEchoHoldTimeout
	}
	return &EchoRepeater{
		ColorCode: colorCode, MaxQueue: maxQueue, HoldTimeout: holdTimeout,
		lastHeardFLCO: "TG",
	}
}

func (e *EchoRepeater) SetLastHeardCallback(cb func(src, dst int, flco string)) {
	e.mu.Lock()
	e.onLastHeard = cb
	e.mu.Unlock()
}

// Ingest buffers one received DMR_DATA2 slot.
func (e *EchoRepeater) Ingest(meta byte, slot []byte, rssi int) {
	if len(slot) != protocol.DMRFrameLengthBytes {
		return
	}
	e.mu.Lock()
	e.framesRX++
	e.lastActivity = time.Now()
	if e.replaying {
		e.replaying = false
		e.queue = nil
	}
	e.queue = append(e.queue, append([]byte(nil), slot...))
	if len(e.queue) > e.MaxQueue {
		e.queue = e.queue[1:]
		e.framesLost++
	}
	e.mu.Unlock()

	if meta == metaDataSync|protocol.DTVoiceLCHeader {
		e.decodeHeader(slot)
	}
}

func (e *EchoRepeater) HandleLost() {
	e.mu.Lock()
	n, replay := len(e.queue), e.replaying
	e.mu.Unlock()
	log.Debugf("Echo: RF loss; %d frames buffered, replay=%v", n, replay)
}

func (e *EchoRepeater) decodeHeader(slot []byte) {
	lc, err := dmr.FullLCDecode(slot, protocol.DTVoiceLCHeader)
	if err != nil || lc == nil {
		return
	}
	flco := "TG"
	if lc.FLCO != dmr.FLCOGroup {
		flco = "PC"
	}
	e.mu.Lock()
	e.lastHeardSrc = lc.SrcID
	e.lastHeardDst = lc.DstID
	e.lastHeardFLCO = flco
	cb := e.onLastHeard
	e.mu.Unlock()
	log.Infof("Echo: heard %d -> %s%d", lc.SrcID, flco, lc.DstID)
	if cb != nil {
		func() {
			defer func() { _ = recover() }()
			cb(lc.SrcID, lc.DstID, flco)
		}()
	}
}

// Flush returns up to `space` buffered slots ready to transmit.
func (e *EchoRepeater) Flush(space int, now time.Time) [][]byte {
	if now.IsZero() {
		now = time.Now()
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(e.queue) == 0 {
		e.replaying = false
		return nil
	}
	if !e.replaying {
		if now.Sub(e.lastActivity) <= e.HoldTimeout {
			return nil
		}
		e.replaying = true
		e.lastEmit = time.Time{}
		log.Infof("Echo: RX ended, replaying %d frames", len(e.queue))
	}
	if space <= 1 {
		return nil
	}
	if now.Sub(e.lastEmit) < DMRSlotInterval {
		return nil
	}
	gap := now.Sub(e.lastEmit)
	if e.lastEmit.IsZero() {
		gap = DMRSlotInterval * 2
	}
	n := int(gap / DMRSlotInterval)
	if n > 2 {
		n = 2
	}
	if n > len(e.queue) {
		n = len(e.queue)
	}
	if n > space-1 {
		n = space - 1
	}
	if n <= 0 {
		return nil
	}
	e.lastEmit = now
	out := make([][]byte, n)
	copy(out, e.queue[:n])
	e.queue = e.queue[n:]
	e.framesTX += n
	if len(e.queue) == 0 {
		e.replaying = false
		log.Infof("Echo: replay finished")
	}
	return out
}

func (e *EchoRepeater) Idle(timeout time.Duration) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return time.Since(e.lastActivity) > timeout
}

type EchoStats struct {
	FramesRX   int    `json:"frames_rx"`
	FramesTX   int    `json:"frames_tx"`
	FramesLost int    `json:"frames_lost"`
	Queued     int    `json:"queued"`
	Idle       bool   `json:"idle"`
	Replaying  bool   `json:"replaying"`
	LastSrc    int    `json:"last_src"`
	LastDst    int    `json:"last_dst"`
	LastFLCO   string `json:"last_flco"`
}

func (e *EchoRepeater) Stats() EchoStats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return EchoStats{
		FramesRX: e.framesRX, FramesTX: e.framesTX, FramesLost: e.framesLost,
		Queued: len(e.queue), Idle: time.Since(e.lastActivity) > 3*time.Second,
		Replaying: e.replaying, LastSrc: e.lastHeardSrc, LastDst: e.lastHeardDst,
		LastFLCO: e.lastHeardFLCO,
	}
}
