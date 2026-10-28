package modem

import (
	"testing"

	"dmr-hotspot/internal/protocol"
)

func TestParseStatusLayout(t *testing.T) {
	m := NewModem(nil, false, false, false, false, 0, 0, 0, 0, false)
	if m.HasStatus() {
		t.Fatal("HasStatus() = true before any status response")
	}
	// OpenGD77: [0]=caps [1]=mode [2]=flags [4]=space1 [5]=space2
	payload := []byte{0x22, protocol.ModeDMR, 0x41, 0x00, 10, 7}
	m.parseStatus(payload)

	if !m.HasStatus() {
		t.Fatal("HasStatus() = false after status response")
	}
	if got := m.Mode(); got != protocol.ModeDMR {
		t.Fatalf("mode = %d, want %d", got, protocol.ModeDMR)
	}
	if !m.TX() || !m.CD() {
		t.Fatalf("flags not parsed: tx=%v cd=%v", m.TX(), m.CD())
	}
	if got := m.DMRSpace2(); got != 7 {
		t.Fatalf("space2 = %d, want 7", got)
	}
}

func TestNoteDMR2Sent(t *testing.T) {
	m := NewModem(nil, false, false, false, false, 0, 0, 0, 0, false)
	m.parseStatus([]byte{0x22, protocol.ModeDMR, 0x01, 0x00, 10, 3})

	m.NoteDMR2Sent(2)
	if got := m.DMRSpace2(); got != 1 {
		t.Fatalf("space2 after 2 sends = %d, want 1", got)
	}
	m.NoteDMR2Sent(5)
	if got := m.DMRSpace2(); got != 0 {
		t.Fatalf("space2 must not go negative, got %d", got)
	}
}
