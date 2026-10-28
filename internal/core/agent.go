package core

import (
	"strings"
	"sync"
	"time"
	"unicode"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/dmr"
	"dmr-hotspot/internal/protocol"
)

const emptyASRFallback = "Не расслышал. Повторите."

// Agent is a local DMR voice assistant: RX call -> STT -> LLM -> TTS -> TX.
// Heavy work runs in a worker goroutine so the 30 ms loop never blocks.
type SpeechToText interface {
	Transcribe([]byte) (string, error)
	Close()
}

type ChatModel interface {
	Chat(string) string
	TestConnection(time.Duration) (bool, string)
}

type TextToSpeech interface {
	Speak(string) ([]byte, error)
}

type Agent struct {
	cfg config.AgentConfig
	cc  int

	stt SpeechToText
	llm ChatModel
	tts TextToSpeech

	mu           sync.Mutex
	rxFrames     [][]byte
	txQueue      [][]byte
	txGainDB     int
	lastActivity time.Time
	replaying    bool
	lastEmit     time.Time
	callSrc      int
	callDst      int
	callFLCO     string
	state        string
	framesRX     int
	framesTX     int
	calls        int
	replies      int
	errors       int
	onLastHeard  func(src, dst int, flco string)

	work   chan [][]byte
	stop   chan struct{}
	done   chan struct{}
	closed bool
}

func NewAgent(cfg config.AgentConfig, colorCode int) *Agent {
	a := &Agent{
		cfg: cfg, cc: colorCode, state: "IDLE", txGainDB: 0,
		work: make(chan [][]byte, 8), stop: make(chan struct{}), done: make(chan struct{}),
		callFLCO: "TG",
	}
	if cfg.STTEngine != "none" && cfg.STTEngine != "off" {
		a.stt = NewWhisperSTT(cfg.WhisperModel, cfg.WhisperLanguage, cfg.WhisperDevice)
	}
	timeout := time.Duration(cfg.LLMTimeout * float64(time.Second))
	baseURL, model := cfg.ResolveLLM()
	if baseURL != "" {
		lm := NewLMStudio(baseURL, model, cfg.SystemPrompt,
			cfg.LLMAPIKey, cfg.LLMTemperature, cfg.LLMMaxTokens, timeout)
		if cfg.WebSearch {
			language := cfg.WebSearchLanguage
			if language == "" {
				language = cfg.WhisperLanguage
			}
			lm.Search = NewWebSearch(WebSearchOptions{
				Provider:   cfg.WebSearchProvider,
				BaseURL:    cfg.WebSearchBaseURL,
				Language:   language,
				MaxResults: cfg.WebSearchMaxResults,
				FetchPages: cfg.WebSearchFetchPages,
				Timeout:    time.Duration(cfg.WebSearchTimeout * float64(time.Second)),
			})
		}
		a.llm = lm
	}
	if cfg.TTSEngine != "none" && cfg.TTSEngine != "off" {
		tts := NewPiperTTS(cfg.PiperVoice, "", cfg.PiperVoiceDir, cfg.TTSLengthScale, cfg.TTSRate)
		if cfg.PiperModelPath != "" {
			tts.Voice = cfg.PiperModelPath
		}
		a.tts = tts
	}
	go a.worker()
	return a
}

// SetBackends replaces the STT/LLM/TTS backends (used by tests).
func (a *Agent) SetBackends(stt SpeechToText, llm ChatModel, tts TextToSpeech) {
	a.stt, a.llm, a.tts = stt, llm, tts
}

func (a *Agent) SetTxGain(gainDB int) {
	a.mu.Lock()
	a.txGainDB = gainDB
	a.mu.Unlock()
}

func (a *Agent) SetLastHeardCallback(cb func(src, dst int, flco string)) {
	a.mu.Lock()
	a.onLastHeard = cb
	a.mu.Unlock()
}

func (a *Agent) holdTimeout() time.Duration {
	if a.cfg.HoldTimeout <= 0 {
		return 1500 * time.Millisecond
	}
	return time.Duration(a.cfg.HoldTimeout * float64(time.Second))
}

// Ingest buffers one received DMR_DATA2 slot.
func (a *Agent) Ingest(meta byte, burst []byte, rssi int) {
	if len(burst) != protocol.DMRFrameLengthBytes {
		return
	}
	a.mu.Lock()
	a.framesRX++
	a.lastActivity = time.Now()
	a.mu.Unlock()

	if meta&metaDataSync != 0 {
		switch meta & 0x0F {
		case protocol.DTVoiceLCHeader:
			a.decodeHeader(burst)
		case protocol.DTTerminatorWithLC:
			a.mu.Lock()
			n := len(a.rxFrames)
			a.mu.Unlock()
			log.Debugf("Agent: terminator received, keeping %d buffered bursts", n)
		}
		return
	}

	a.mu.Lock()
	if len(a.txQueue) > 0 {
		a.txQueue = nil
		a.replaying = false
		log.Infof("Agent: new call interrupts previous reply")
	}
	a.rxFrames = append(a.rxFrames, append([]byte(nil), burst...))
	maxq := a.cfg.MaxQueue
	if maxq <= 0 {
		maxq = 300
	}
	if len(a.rxFrames) > maxq {
		a.rxFrames = a.rxFrames[1:]
	}
	a.mu.Unlock()
}

func (a *Agent) HandleLost() {}

func (a *Agent) decodeHeader(burst []byte) {
	lc, err := dmr.FullLCDecode(burst, protocol.DTVoiceLCHeader)
	if err != nil || lc == nil {
		return
	}
	flco := "TG"
	if !lc.IsGroup() {
		flco = "PC"
	}
	a.mu.Lock()
	a.callSrc, a.callDst, a.callFLCO = lc.SrcID, lc.DstID, flco
	cb := a.onLastHeard
	a.mu.Unlock()
	log.Infof("Agent: heard %d -> %s%d", lc.SrcID, flco, lc.DstID)
	if cb != nil {
		func() {
			defer func() { _ = recover() }()
			cb(lc.SrcID, lc.DstID, flco)
		}()
	}
}

// Tick starts the pipeline once the incoming call has ended.
func (a *Agent) Tick(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	a.mu.Lock()
	if len(a.rxFrames) == 0 || now.Sub(a.lastActivity) <= a.holdTimeout() {
		a.mu.Unlock()
		return
	}
	bursts := a.rxFrames
	a.rxFrames = nil
	a.calls++
	a.state = "PROCESSING"
	a.mu.Unlock()
	log.Infof("Agent: call ended, %d voice bursts buffered", len(bursts))
	select {
	case a.work <- bursts:
	case <-a.stop:
	}
}

// Flush returns TX bursts ready to transmit.
func (a *Agent) Flush(space int, now time.Time) [][]byte {
	if now.IsZero() {
		now = time.Now()
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	if len(a.txQueue) > 0 && !a.replaying {
		if now.Sub(a.lastActivity) <= a.holdTimeout() {
			a.state = "HOLDING"
			return nil
		}
		a.replaying = true
		a.lastEmit = time.Time{}
		a.state = "TX"
		log.Infof("Agent: transmitting reply (%d bursts)", len(a.txQueue))
	}
	if !a.replaying || len(a.txQueue) == 0 {
		if len(a.rxFrames) > 0 {
			a.state = "RX"
		} else {
			a.state = "IDLE"
		}
		return nil
	}
	if space <= 1 || (!a.lastEmit.IsZero() && now.Sub(a.lastEmit) < DMRSlotInterval) {
		return nil
	}
	gap := now.Sub(a.lastEmit)
	if a.lastEmit.IsZero() {
		gap = DMRSlotInterval * 2
	}
	n := int(gap / DMRSlotInterval)
	if n > 2 {
		n = 2
	}
	if n > len(a.txQueue) {
		n = len(a.txQueue)
	}
	if n > space-1 {
		n = space - 1
	}
	if n <= 0 {
		return nil
	}
	a.lastEmit = now
	out := make([][]byte, n)
	copy(out, a.txQueue[:n])
	a.txQueue = a.txQueue[n:]
	a.framesTX += n
	if len(a.txQueue) == 0 {
		a.replaying = false
		a.state = "IDLE"
		log.Infof("Agent: reply finished")
	}
	return out
}

func (a *Agent) worker() {
	defer close(a.done)
	for {
		select {
		case <-a.stop:
			return
		case bursts := <-a.work:
			if len(bursts) == 0 {
				continue
			}
			a.processCall(bursts)
		}
	}
}

func (a *Agent) processCall(bursts [][]byte) {
	a.mu.Lock()
	callSrc, callDst := a.callSrc, a.callDst
	a.mu.Unlock()

	pcm := a.decodePCM(bursts)
	if len(pcm) == 0 {
		log.Infof("Agent: no audio decoded, skipping answer")
		a.mu.Lock()
		a.state = "IDLE"
		a.mu.Unlock()
		return
	}

	textIn := ""
	if a.stt != nil {
		var err error
		textIn, err = a.stt.Transcribe(ResampleLinear(pcm, 8000, 16000))
		if err != nil {
			log.Warningf("Agent STT failed: %s", err)
		}
		log.Infof("Agent STT: %s", orEmpty(textIn))
	}

	textOut := textIn
	if a.llm != nil && textIn != "" {
		textOut = a.llm.Chat(textIn)
		if textOut == "" {
			log.Warningf("Agent LLM returned nothing, answering with input text")
			textOut = textIn
		}
	}
	textOut = sanitizeSpeech(textOut)
	log.Infof("Agent TTS text: %s", orEmpty(textOut))
	if textOut == "" {
		log.Infof("Agent: no answer text, falling back to %q", emptyASRFallback)
		textOut = emptyASRFallback
	}

	if a.tts == nil {
		log.Warningf("Agent TTS unavailable; no answer transmitted")
		a.mu.Lock()
		a.state = "IDLE"
		a.mu.Unlock()
		return
	}
	replyPCM, err := a.tts.Speak(textOut)
	if err != nil || len(replyPCM) == 0 {
		log.Warningf("Agent TTS failed: %v", err)
		a.mu.Lock()
		a.state = "IDLE"
		a.mu.Unlock()
		return
	}
	replyPCM = PolishSpeech(replyPCM, a.cfg.TTSVolume)
	srcID := a.cfg.ReplySrcID
	if srcID == 0 {
		srcID = callSrc
	}
	dstID := a.cfg.ReplyDstID
	if dstID == 0 {
		dstID = callDst
	}
	reply, err := BuildReplyBursts(replyPCM, a.cc, srcID, dstID, a.cfg.ReplyFLCO, a.txGainDB)
	if err != nil {
		log.Errorf("Agent: cannot build reply: %s", err)
		a.mu.Lock()
		a.errors++
		a.state = "ERROR"
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	a.txQueue = append(a.txQueue, reply...)
	a.replies++
	a.state = "READY"
	a.mu.Unlock()
	log.Infof("Agent: reply ready (%d bursts)", len(reply))
}

func sanitizeSpeech(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch r {
		case '*', '_', '`', '#', '>', '<', '|', '~', '[', ']', '{', '}', '(', ')', '"', '\'':
			r = ' '
		case '\n', '\r', '\t':
			r = ' '
		}
		if unicode.IsSymbol(r) {
			r = ' '
		}
		if unicode.IsSpace(r) {
			if !space && b.Len() > 0 {
				b.WriteRune(' ')
				space = true
			}
			continue
		}
		b.WriteRune(r)
		space = false
	}
	out := strings.TrimSpace(b.String())
	for strings.Contains(out, "..") {
		out = strings.ReplaceAll(out, "..", ".")
	}
	out = strings.ReplaceAll(out, " .", ".")
	out = strings.ReplaceAll(out, " ,", ",")
	return out
}

func orEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return s
}

func (a *Agent) decodePCM(bursts [][]byte) []byte {
	dec, err := dmr.NewDecoder()
	if err != nil {
		log.Warningf("Agent: vocoder decoder unavailable: %s", err)
		return nil
	}
	defer dec.Close()
	var pcm []byte
	for _, burst := range bursts {
		frames, err := dmr.ExtractVoiceFrames(burst)
		if err != nil {
			continue
		}
		for _, f := range frames {
			out, _, err := dec.Decode(f[:])
			if err != nil {
				continue
			}
			pcm = append(pcm, out...)
		}
	}
	return pcm
}

// BuildReplyBursts encodes 8 kHz s16le PCM into a complete DMR voice call.
func BuildReplyBursts(pcm []byte, colorCode, srcID, dstID, flco, gainDB int) ([][]byte, error) {
	if len(pcm)%320 != 0 {
		pad := 320 - len(pcm)%320
		pcm = append(pcm, make([]byte, pad)...)
	}
	pcm = ScalePCM16(pcm, EncoderPreGain(gainDB))

	enc, err := dmr.NewEncoder()
	if err != nil {
		return nil, err
	}
	defer enc.Close()
	enc.SetGain(0)

	frames := make([][]byte, 0, len(pcm)/320)
	for i := 0; i < len(pcm); i += 320 {
		frame, err := enc.Encode(pcm[i : i+320])
		if err != nil {
			log.Warningf("encode failed for a frame; reusing previous")
			if len(frames) > 0 {
				frame = frames[len(frames)-1]
			} else {
				frame = make([]byte, 9)
			}
		}
		frames = append(frames, frame)
	}
	for len(frames)%3 != 0 {
		if len(frames) > 0 {
			frames = append(frames, frames[len(frames)-1])
		} else {
			frames = append(frames, make([]byte, 9))
		}
	}
	frames = frames[:3*(len(frames)/3)]

	bursts := make([][]byte, 0, 2+len(frames)/3)
	lc := dmr.NewDMRLC(byte(flco), srcID, dstID, 0)
	header, err := dmr.FullLCEncode(lc, dmr.DTVoiceLCHeader)
	if err != nil {
		return nil, err
	}
	dmr.NewSlotType(byte(colorCode), dmr.DTVoiceLCHeader).Encode(header)
	dmr.AddDataSync(header, false)
	bursts = append(bursts, header)

	for bi := 0; bi < len(frames); bi += 3 {
		b, err := dmr.BuildVoiceBurst(frames[bi], frames[bi+1], frames[bi+2])
		if err != nil {
			return nil, err
		}
		if bi == 0 {
			dmr.AddVoiceSync(b, false)
		} else {
			dmr.SetEMB(b, dmr.EMBEncode(colorCode, false, 0))
		}
		bursts = append(bursts, b)
	}

	term, err := dmr.FullLCEncode(lc, dmr.DTTerminatorWithLC)
	if err != nil {
		return nil, err
	}
	dmr.NewSlotType(byte(colorCode), dmr.DTTerminatorWithLC).Encode(term)
	dmr.AddDataSync(term, false)
	bursts = append(bursts, term)
	return bursts, nil
}

func (a *Agent) Idle(timeout time.Duration) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Since(a.lastActivity) > timeout
}

type AgentStats struct {
	State    string `json:"state"`
	FramesRX int    `json:"frames_rx"`
	FramesTX int    `json:"frames_tx"`
	Calls    int    `json:"calls"`
	Replies  int    `json:"replies"`
	Errors   int    `json:"errors"`
	Queued   int    `json:"queued"`
	LastSrc  int    `json:"last_src"`
	LastDst  int    `json:"last_dst"`
	LastFLCO string `json:"last_flco"`
}

func (a *Agent) Stats() AgentStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return AgentStats{
		State: a.state, FramesRX: a.framesRX, FramesTX: a.framesTX,
		Calls: a.calls, Replies: a.replies, Errors: a.errors,
		Queued: len(a.txQueue), LastSrc: a.callSrc, LastDst: a.callDst,
		LastFLCO: a.callFLCO,
	}
}

// TestLLM probes the configured OpenAI-compatible endpoint.
func (a *Agent) TestLLM() (bool, string) {
	if a.llm == nil {
		return false, "LLM is not configured"
	}
	return a.llm.TestConnection(5 * time.Second)
}

func (a *Agent) Close() {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return
	}
	a.closed = true
	a.mu.Unlock()
	close(a.stop)
	if a.stt != nil {
		a.stt.Close()
	}
	select {
	case <-a.done:
	case <-time.After(2 * time.Second):
	}
}
