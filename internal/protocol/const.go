// Package protocol holds the shared MMDVM / DMR / Homebrew constants.
package protocol

const (
	MMDVMFrameStart = 0xE0

	// Host -> modem commands.
	MMDVMGetVersion = 0x00
	MMDVMGetStatus  = 0x01
	MMDVMSetConfig  = 0x02
	MMDVMSetMode    = 0x03
	MMDVMSetFreq    = 0x04
	MMDVMSendCWID   = 0x0A

	// DMR traffic (both directions).
	MMDVMDMRAloha   = 0x17
	MMDVMDMRData1   = 0x18
	MMDVMDMRLost1   = 0x19
	MMDVMDMRData2   = 0x1A
	MMDVMDMRLost2   = 0x1B
	MMDVMDMRShortLC = 0x1C
	MMDVMDMRStart   = 0x1D
	MMDVMDMRAbort   = 0x1E

	// Responses / misc.
	MMDVMAck         = 0x70
	MMDVMNak         = 0x7F
	MMDVMSerialData  = 0x80
	MMDVMTransparent = 0x90
	MMDVMQSOInfo     = 0x91
	MMDVMDebug1      = 0xF1
	MMDVMDebug2      = 0xF2
	MMDVMDebug3      = 0xF3
	MMDVMDebug4      = 0xF4
	MMDVMDebug5      = 0xF5
	MMDVMDebugDump   = 0xFA
)

// Capability flags (protocol version 2).
const (
	Cap1DStar  = 0x01
	Cap1DMR    = 0x02
	Cap1YSF    = 0x04
	Cap1P25    = 0x08
	Cap1NXDN   = 0x10
	Cap1FM     = 0x40
	Cap2POCSAG = 0x01
)

// Modem modes.
const (
	ModeIdle    = 0x00
	ModeDStar   = 0x01
	ModeDMR     = 0x02
	ModeYSF     = 0x03
	ModeP25     = 0x04
	ModeNXDN    = 0x05
	ModePOCSAG  = 0x08
	ModeFM      = 0x09
	ModeLockout = 0x0A
)

// DMR.
const (
	DMRFrameLengthBytes = 33

	DTVoiceLCHeader    = 0x01
	DTTerminatorWithLC = 0x02
	DTCSBK             = 0x03
	DTDataHeader       = 0x04
	DTRate12Data       = 0x05
	DTRate34Data       = 0x06
	DTIdle             = 0x07
	DTRate1Data        = 0x08
	DTVoiceSync        = 0xF0
	DTVoice            = 0xF1

	DataSyncMask  = 0x20
	VoiceSyncMask = 0x10

	LCSSFirst = 0x01
	LCSSLast  = 0x03
)

// BrandMeister / Homebrew network protocol.
const (
	HomebrewDataPacketLength = 55
	BMDefaultPort            = 62031
	MaxStaticTGPerSlot       = 4
	DefaultPassword          = "passw0rd"
)
