package core

import (
	"bytes"
	"math"
	"testing"
	"time"

	"dmr-hotspot/internal/config"
	"dmr-hotspot/internal/dmr"
	"dmr-hotspot/internal/network"
	"dmr-hotspot/internal/protocol"
)

func TestPiperTTSRateScalesLengthScale(t *testing.T) {
	p := NewPiperTTS("", "", "", 1, 1)
	if got := p.effectiveLengthScale(); math.Abs(got-1) > 1e-9 {
		t.Fatalf("1x rate: got %v, want 1", got)
	}
	p.Rate = 2
	if got := p.effectiveLengthScale(); math.Abs(got-0.5) > 1e-9 {
		t.Fatalf("2x rate: got %v, want 0.5", got)
	}
	p.Rate = 0
	if got := p.effectiveLengthScale(); math.Abs(got-1) > 1e-9 {
		t.Fatalf("unset rate: got %v, want 1", got)
	}
}

type fakeSTT struct{ text string }

func (f *fakeSTT) Transcribe([]byte) (string, error) { return f.text, nil }
func (f *fakeSTT) Close()                            {}

type fakeLLM struct{ text string }

func (f *fakeLLM) Chat(string) string                          { return f.text }
func (f *fakeLLM) TestConnection(time.Duration) (bool, string) { return true, "ok" }

type fakeTTS struct{ pcm []byte }

func (f *fakeTTS) Speak(string) ([]byte, error) { return f.pcm, nil }

func silenceFrames(t *testing.T, n int) [][]byte {
	t.Helper()
	enc, err := dmr.NewEncoder()
	if err != nil {
		t.Skipf("vocoder unavailable: %v", err)
	}
	defer enc.Close()
	pcm := make([]byte, 320)
	frames := make([][]byte, n)
	for i := range frames {
		f, err := enc.Encode(pcm)
		if err != nil {
			t.Fatal(err)
		}
		frames[i] = f
	}
	return frames
}

func buildTestCall(t *testing.T, frames [][]byte) (header []byte, voice [][]byte, term []byte) {
	t.Helper()
	lc := dmr.NewDMRLC(dmr.FLCOGroup, 2500001, 9, 0)
	header, err := dmr.FullLCEncode(lc, dmr.DTVoiceLCHeader)
	if err != nil {
		t.Fatal(err)
	}
	dmr.NewSlotType(1, dmr.DTVoiceLCHeader).Encode(header)
	dmr.AddDataSync(header, false)
	for i := 0; i+2 < len(frames); i += 3 {
		b, err := dmr.BuildVoiceBurst(frames[i], frames[i+1], frames[i+2])
		if err != nil {
			t.Fatal(err)
		}
		voice = append(voice, b)
	}
	term, err = dmr.FullLCEncode(lc, dmr.DTTerminatorWithLC)
	if err != nil {
		t.Fatal(err)
	}
	dmr.NewSlotType(1, dmr.DTTerminatorWithLC).Encode(term)
	dmr.AddDataSync(term, false)
	return
}

func newTestAgent(stt SpeechToText, llm ChatModel, tts TextToSpeech) *Agent {
	a := NewAgent(config.AgentConfig{
		STTEngine: "none", TTSEngine: "none", HoldTimeout: 0.05,
		MaxQueue: 300, ReplyFLCO: 0,
	}, 1)
	a.SetBackends(stt, llm, tts)
	return a
}

func waitFor(t *testing.T, timeout time.Duration, fn func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

func TestAgentPipelineProducesReply(t *testing.T) {
	frames := silenceFrames(t, 6)
	header, voice, term := buildTestCall(t, frames)
	a := newTestAgent(&fakeSTT{"привет"}, &fakeLLM{"ответ"}, &fakeTTS{make([]byte, 320*3)})
	defer a.Close()
	a.Ingest(metaDataSync|protocol.DTVoiceLCHeader, header, 0)
	for _, b := range voice {
		a.Ingest(0, b, 0)
	}
	a.Ingest(metaDataSync|protocol.DTTerminatorWithLC, term, 0)
	if !waitFor(t, 2*time.Second, func() bool {
		a.Tick(time.Now())
		return a.Stats().Calls > 0
	}) {
		t.Fatal("call was not queued")
	}
	if !waitFor(t, 5*time.Second, func() bool { return a.Stats().Replies > 0 }) {
		t.Fatal("agent produced no reply")
	}
	now := time.Now().Add(100 * time.Millisecond)
	var bursts [][]byte
	if !waitFor(t, time.Second, func() bool {
		got := a.Flush(10, now)
		if len(got) > 0 {
			bursts = append(bursts, got...)
		}
		now = now.Add(DMRSlotInterval)
		return len(bursts) >= 3
	}) {
		t.Fatal("reply was not flushed")
	}
	lc, err := dmr.FullLCDecode(bursts[0], protocol.DTVoiceLCHeader)
	if err != nil {
		t.Fatal(err)
	}
	if lc.SrcID != 2500001 || lc.DstID != 9 {
		t.Fatalf("wrong reply LC: %+v", lc)
	}
}

func TestAgentFlushHonoursModemSpace(t *testing.T) {
	a := newTestAgent(&fakeSTT{"a"}, &fakeLLM{"b"}, &fakeTTS{make([]byte, 320)})
	defer a.Close()
	a.mu.Lock()
	a.txQueue = [][]byte{make([]byte, 33), make([]byte, 33)}
	a.replaying = true
	a.lastEmit = time.Time{}
	a.mu.Unlock()

	now := time.Now()
	if got := a.Flush(0, now); len(got) != 0 {
		t.Fatalf("Flush(0) sent %d bursts with a full modem buffer", len(got))
	}
	if got := a.Flush(1, now); len(got) != 0 {
		t.Fatalf("Flush(1) sent %d bursts, leaving no headroom", len(got))
	}
	if got := a.Flush(2, now); len(got) != 1 {
		t.Fatalf("Flush(2) sent %d bursts, want 1", len(got))
	}
}

func TestAgentNewCallInterruptsReply(t *testing.T) {
	a := newTestAgent(&fakeSTT{"a"}, &fakeLLM{"b"}, &fakeTTS{make([]byte, 320*3)})
	defer a.Close()
	a.mu.Lock()
	a.txQueue = [][]byte{make([]byte, 33), make([]byte, 33)}
	a.mu.Unlock()
	a.Ingest(0, make([]byte, 33), 0)
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.txQueue) != 0 {
		t.Fatal("new call did not clear the reply queue")
	}
}

func TestResampleLengths(t *testing.T) {
	data := make([]byte, 800*2)
	if got := ResampleLinear(data, 8000, 8000); len(got) != len(data) {
		t.Fatal("identity resample changed length")
	}
	if got := ResampleLinear(data, 8000, 16000); len(got) != 1600*2 {
		t.Fatalf("8k->16k length %d", len(got))
	}
	if got := ResampleAntiAliased(data, 22050, 8000); len(got) == 0 {
		t.Fatal("anti-aliased resample produced nothing")
	}
}

func TestBuildReplyBursts(t *testing.T) {
	pcm := make([]byte, 320*9+100)
	bursts, err := BuildReplyBursts(pcm, 2, 771, 9000, 3, 13)
	if err != nil {
		t.Skipf("vocoder unavailable: %v", err)
	}
	if len(bursts) < 3 {
		t.Fatalf("too few bursts: %d", len(bursts))
	}
	lc, err := dmr.FullLCDecode(bursts[0], dmr.DTVoiceLCHeader)
	if err != nil {
		t.Fatal(err)
	}
	if lc.SrcID != 771 || lc.DstID != 9000 || lc.FLCO != dmr.FLCOUserUser {
		t.Fatalf("header LC %+v", lc)
	}
	termLC, err := dmr.FullLCDecode(bursts[len(bursts)-1], dmr.DTTerminatorWithLC)
	if err != nil {
		t.Fatal(err)
	}
	if termLC.SrcID != 771 {
		t.Fatalf("terminator LC %+v", termLC)
	}
}

func TestPackDMRDRoundtrip(t *testing.T) {
	d := network.DMRData{SeqNo: 7, SlotNo: 2, SrcID: 2500001, DstID: 9,
		FLCO: 0, DataType: protocol.DTVoice, StreamID: 12345, N: 3,
		Data: bytes.Repeat([]byte{0xAB}, 33), BER: 2, RSSI: -70}
	raw := network.PackDMRD(d, 2500001)
	got := network.UnpackDMRD(raw)
	if got == nil {
		t.Fatal("unpack returned nil")
	}
	if got.SrcID != d.SrcID || got.DstID != d.DstID || got.SlotNo != d.SlotNo || got.FLCO != d.FLCO || got.StreamID != d.StreamID {
		t.Fatalf("got %+v want %+v", got, d)
	}
	if !bytes.Equal(got.Data, d.Data) {
		t.Fatal("payload mismatch")
	}
}
