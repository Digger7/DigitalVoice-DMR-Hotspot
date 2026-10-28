package core

import (
	"bytes"
	"testing"
	"time"
)

func burstWith(n byte) []byte {
	b := bytes.Repeat([]byte{n}, 33)
	return b
}

func TestEchoHoldsUntilIdleThenReplaysInOrder(t *testing.T) {
	e := NewEchoRepeater(1, 100, 100*time.Millisecond)
	for i := 0; i < 10; i++ {
		e.Ingest(0, burstWith(byte(i)), 0)
	}
	now := time.Now()
	if got := e.Flush(10, now); len(got) != 0 {
		t.Fatalf("must hold during activity, got %d bursts", len(got))
	}
	now = now.Add(101 * time.Millisecond)
	got := e.Flush(10, now)
	if len(got) != 2 {
		t.Fatalf("first replay chunk = %d, want 2", len(got))
	}
	if got[0][0] != 0 || got[1][0] != 1 {
		t.Fatalf("wrong order: %d %d", got[0][0], got[1][0])
	}
	now = now.Add(2 * DMRSlotInterval)
	got = e.Flush(10, now)
	if len(got) != 2 || got[0][0] != 2 || got[1][0] != 3 {
		t.Fatalf("second chunk wrong: %v", firstBytes(got))
	}
}

func TestEchoLostKeepsBuffer(t *testing.T) {
	e := NewEchoRepeater(1, 100, time.Second)
	e.Ingest(0, burstWith(1), 0)
	e.HandleLost()
	if e.Stats().Queued != 1 {
		t.Fatalf("lost cleared the buffer: %+v", e.Stats())
	}
}

func TestEchoNewTransmissionAbortsReplay(t *testing.T) {
	e := NewEchoRepeater(1, 100, 10*time.Millisecond)
	for i := 0; i < 6; i++ {
		e.Ingest(0, burstWith(byte(i)), 0)
	}
	now := time.Now().Add(20 * time.Millisecond)
	if got := e.Flush(10, now); len(got) == 0 {
		t.Fatal("expected replay to start")
	}
	e.Ingest(0, burstWith(99), 0)
	stats := e.Stats()
	if stats.Replaying || stats.Queued != 1 {
		t.Fatalf("new transmission must abort replay: %+v", stats)
	}
	if got := e.Flush(10, time.Now()); len(got) != 0 {
		t.Fatalf("must hold the new call, got %d", len(got))
	}
}

func TestEchoQueueLimit(t *testing.T) {
	e := NewEchoRepeater(1, 3, time.Second)
	for i := 0; i < 5; i++ {
		e.Ingest(0, burstWith(byte(i)), 0)
	}
	st := e.Stats()
	if st.Queued != 3 || st.FramesLost != 2 {
		t.Fatalf("queue stats %+v", st)
	}
}

func TestEchoZeroConfigUsesDefaults(t *testing.T) {
	e := NewEchoRepeater(1, 0, 0)
	for i := 0; i < 5; i++ {
		e.Ingest(0, burstWith(byte(i)), 0)
	}
	if st := e.Stats(); st.Queued != 5 || st.FramesLost != 0 {
		t.Fatalf("zero config must not drop frames: %+v", st)
	}
	if got := e.Flush(10, time.Now()); len(got) != 0 {
		t.Fatalf("must hold while receiving, got %d bursts", len(got))
	}
	got := e.Flush(10, time.Now().Add(defaultEchoHoldTimeout+time.Millisecond))
	if len(got) != 2 || got[0][0] != 0 || got[1][0] != 1 {
		t.Fatalf("replay did not start after hold: %v", firstBytes(got))
	}
}

func firstBytes(in [][]byte) []byte {
	out := make([]byte, len(in))
	for i, b := range in {
		out[i] = b[0]
	}
	return out
}
