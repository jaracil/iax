package iax

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

type FrameType uint8
type IEType uint8
type Subclass uint8

const (
	FrameMaxSize = 1024
)

// Frame types
const (
	FrmDTMF         FrameType = 0x01
	FrmVoice        FrameType = 0x02
	FrmVideo        FrameType = 0x03
	FrmControl      FrameType = 0x04
	FrmNull         FrameType = 0x05
	FrmIAXCtl       FrameType = 0x06
	FrmText         FrameType = 0x07
	FrmImage        FrameType = 0x08
	FrmHTML         FrameType = 0x09
	FrmConfortNoise FrameType = 0x0a
)

// FrameTypeToString returns the string representation of the FrameType
func (ft FrameType) String() string {
	switch ft {
	case FrmDTMF:
		return "DTMF"
	case FrmVoice:
		return "Voice"
	case FrmVideo:
		return "Video"
	case FrmControl:
		return "Control"
	case FrmNull:
		return "Null"
	case FrmIAXCtl:
		return "IAXCtl"
	case FrmText:
		return "Text"
	case FrmImage:
		return "Image"
	case FrmHTML:
		return "HTML"
	case FrmConfortNoise:
		return "ConfortNoise"
	default:
		return fmt.Sprintf("Unknown(%d)", ft)
	}
}

type Codec uint8
type CodecMask uint64

// Codecs
const (
	CODEC_G723      Codec = 0
	CODEC_GSM       Codec = 1
	CODEC_ULAW      Codec = 2
	CODEC_ALAW      Codec = 3
	CODEC_G726      Codec = 4
	CODEC_ADPCM     Codec = 5
	CODEC_SLIN      Codec = 6
	CODEC_LPC10     Codec = 7
	CODEC_G729      Codec = 8
	CODEC_SPEEX     Codec = 9
	CODEC_ILBC      Codec = 10
	CODEC_G726_AAL2 Codec = 11
	CODEC_G722      Codec = 12
	CODEC_SIREN7    Codec = 13
	CODEC_SIREN14   Codec = 14
	CODEC_SLIN16    Codec = 15
	CODEC_JPEG      Codec = 16
	CODEC_PNG       Codec = 17
	CODEC_H261      Codec = 18
	CODEC_H263      Codec = 19
	CODEC_H263P     Codec = 20
	CODEC_H264      Codec = 21
	CODEC_MP4       Codec = 22
	CODEC_VP8       Codec = 23
	CODEC_T140_RED  Codec = 26
	CODEC_T140      Codec = 27
	CODEC_G719      Codec = 32
	CODEC_SPEEX16   Codec = 33
	CODEC_OPUS      Codec = 34
	CODEC_UNKNOWN   Codec = 255
)

var pereferredCodecs = []Codec{
	CODEC_G723,
	CODEC_GSM,
	CODEC_ULAW,
	CODEC_ALAW,
	CODEC_G726,
	CODEC_ADPCM,
	CODEC_SLIN,
	CODEC_LPC10,
	CODEC_G729,
	CODEC_SPEEX,
	CODEC_SPEEX16,
	CODEC_ILBC,
	CODEC_G726_AAL2,
	CODEC_G722,
	CODEC_SLIN16,
	CODEC_JPEG,
	CODEC_PNG,
	CODEC_H261,
	CODEC_H263,
	CODEC_H263P,
	CODEC_H264,
	CODEC_MP4,
	CODEC_T140_RED,
	CODEC_T140,
	CODEC_SIREN7,
	CODEC_SIREN14,
	0, /* reserved; was AST_FORMAT_TESTLAW */
	CODEC_G719,
	0, /* Place holder */
	0, /* Place holder */
	0, /* Place holder */
	0, /* Place holder */
	0, /* Place holder */
	0, /* Place holder */
	0, /* Place holder */
	0, /* Place holder */
	CODEC_OPUS,
	CODEC_VP8,
}

func encodePrefCodec(c Codec) string {
	for i, codec := range pereferredCodecs {
		if codec == c {
			// Return char 'B' + index
			return string(rune('B' + i))
		}
	}
	return ""
}

func decodePrefCodec(s string) Codec {
	if len(s) != 1 {
		return CODEC_UNKNOWN
	}
	i := int(s[0] - 'B')
	if i < 0 || i >= len(pereferredCodecs) {
		return CODEC_UNKNOWN
	}
	return pereferredCodecs[i]
}

// EncodePreferredCodecs returns the string representation of the preferred codecs
func EncodePreferredCodecs(codecs []Codec) string {
	res := ""
	for _, c := range codecs {
		res += encodePrefCodec(c)
	}
	return res
}

// DecodePreferredCodecs returns the preferred codecs from the string representation
func DecodePreferredCodecs(s string) []Codec {
	res := make([]Codec, 0, len(s))
	for _, c := range s {
		res = append(res, decodePrefCodec(string(c)))
	}
	return res
}

// BitMask returns the bitmask for the codec
func (c Codec) BitMask() CodecMask {
	return 1 << c
}

// Subclass returns the subclass audio frame for the codec
func (c Codec) Subclass() Subclass {
	return Subclass(0x80 + c)
}

// CodecFromSubclass returns the codec from the audio frame subclass
func CodecFromSubclass(sc Subclass) Codec {
	return Codec(sc - 0x80)
}

// String returns the string representation of the codec
func (c Codec) String() string {
	switch c {
	case CODEC_G723:
		return "G723"
	case CODEC_GSM:
		return "GSM"
	case CODEC_ULAW:
		return "ULAW"
	case CODEC_ALAW:
		return "ALAW"
	case CODEC_G726:
		return "G726"
	case CODEC_ADPCM:
		return "ADPCM"
	case CODEC_SLIN:
		return "SLIN"
	case CODEC_LPC10:
		return "LPC10"
	case CODEC_G729:
		return "G729"
	case CODEC_SPEEX:
		return "SPEEX"
	case CODEC_ILBC:
		return "ILBC"
	case CODEC_G726_AAL2:
		return "G726_AAL2"
	case CODEC_G722:
		return "G722"
	case CODEC_SIREN7:
		return "SIREN7"
	case CODEC_SIREN14:
		return "SIREN14"
	case CODEC_SLIN16:
		return "SLIN16"
	case CODEC_JPEG:
		return "JPEG"
	case CODEC_PNG:
		return "PNG"
	case CODEC_H261:
		return "H261"
	case CODEC_H263:
		return "H263"
	case CODEC_H263P:
		return "H263P"
	case CODEC_H264:
		return "H264"
	case CODEC_MP4:
		return "MP4"
	case CODEC_VP8:
		return "VP8"
	case CODEC_T140_RED:
		return "T140_RED"
	case CODEC_T140:
		return "T140"
	case CODEC_G719:
		return "G719"
	case CODEC_SPEEX16:
		return "SPEEX16"
	case CODEC_OPUS:
		return "OPUS"
	default:
		return fmt.Sprintf("Unknown(%d)", c)
	}
}

// HasCodec checks if the CodecMask has the given codec
func (cm CodecMask) HasCodec(c Codec) bool {
	return cm&(1<<c) != 0
}

// AddCodec adds a codec to the CodecMask
func (cm CodecMask) AddCodec(c Codec) CodecMask {
	return cm | (1 << c)
}

// RemoveCodec removes a codec from the CodecMask
func (cm CodecMask) RemoveCodec(c Codec) CodecMask {
	return cm &^ (1 << c)
}

// String returns the string representation of the CodecMask
func (cm CodecMask) String() string {
	res := ""
	for c := 0; c < 64; c++ {
		codec := Codec(c)
		if cm.HasCodec(codec) {
			res += codec.String() + " "
		}
	}
	return res
}

// String returns the string representation of the CodecMask
func (cm CodecMask) FirstCodec() Codec {
	for c := 0; c < 64; c++ {
		codec := Codec(c)
		if cm.HasCodec(codec) {
			return codec
		}
	}
	return CODEC_UNKNOWN
}

// IE types
const (
	IECalledNumber    IEType = 1
	IECallingNumber   IEType = 2
	IECallingAni      IEType = 3
	IECallingName     IEType = 4
	IECalledContext   IEType = 5
	IEUsername        IEType = 6
	IEPassword        IEType = 7
	IECapability      IEType = 8
	IEFormat          IEType = 9
	IELanguage        IEType = 10
	IEVersion         IEType = 11
	IEADSICPE         IEType = 12
	IEDNID            IEType = 13
	IEAuthMethods     IEType = 14
	IEChallenge       IEType = 15
	IEMD5Result       IEType = 16
	IERSAResult       IEType = 17
	IEApparentAddr    IEType = 18
	IERefresh         IEType = 19
	IEDPStatus        IEType = 20
	IECallNumber      IEType = 21
	IECause           IEType = 22
	IEIAXUnknown      IEType = 23
	IEMsgCount        IEType = 24
	IEAutoAnswer      IEType = 25
	IEMusiconHold     IEType = 26
	IETransferID      IEType = 27
	IERDNIS           IEType = 28
	IEProvisioning    IEType = 29
	IEAesProvisioning IEType = 30
	IEDateTime        IEType = 31
	IEDeviceType      IEType = 32
	IEServiceIdent    IEType = 33
	IEFirmwareVer     IEType = 34
	IEFwBlockDesc     IEType = 35
	IEFwBlockData     IEType = 36
	IEProvVer         IEType = 37
	IECallingPres     IEType = 38
	IECallingTON      IEType = 39
	IECallingTNS      IEType = 40
	IESamplingRate    IEType = 41
	IECauseCode       IEType = 42
	IEEncryption      IEType = 43
	IEEncKey          IEType = 44
	IECodecPrefs      IEType = 45
	IERRJitter        IEType = 46
	IERRLoss          IEType = 47
	IERRPackets       IEType = 48
	IERRDelay         IEType = 49
	IERRDropped       IEType = 50
	IERROOO           IEType = 51
	IEVariable        IEType = 52
	IEOSPToken        IEType = 53
	IECallToken       IEType = 54
	IECapability2     IEType = 55
	IEFormat2         IEType = 56
	IECallingANI2     IEType = 57
)

func (ie IEType) String() string {
	switch ie {
	case IECalledNumber:
		return "CalledNumber"
	case IECallingNumber:
		return "CallingNumber"
	case IECallingAni:
		return "CallingAni"
	case IECallingName:
		return "CallingName"
	case IECalledContext:
		return "CalledContext"
	case IEUsername:
		return "Username"
	case IEPassword:
		return "Password"
	case IECapability:
		return "Capability"
	case IEFormat:
		return "Format"
	case IELanguage:
		return "Language"
	case IEVersion:
		return "Version"
	case IEADSICPE:
		return "ADSICPE"
	case IEDNID:
		return "DNID"
	case IEAuthMethods:
		return "AuthMethods"
	case IEChallenge:
		return "Challenge"
	case IEMD5Result:
		return "MD5Result"
	case IERSAResult:
		return "RSAResult"
	case IEApparentAddr:
		return "ApparentAddr"
	case IERefresh:
		return "Refresh"
	case IEDPStatus:
		return "DPStatus"
	case IECallNumber:
		return "CallNumber"
	case IECause:
		return "Cause"
	case IEIAXUnknown:
		return "IAXUnknown"
	case IEMsgCount:
		return "MsgCount"
	case IEAutoAnswer:
		return "AutoAnswer"
	case IEMusiconHold:
		return "MusiconHold"
	case IETransferID:
		return "TransferID"
	case IERDNIS:
		return "RDNIS"
	case IEProvisioning:
		return "Provisioning"
	case IEAesProvisioning:
		return "AesProvisioning"
	case IEDateTime:
		return "DateTime"
	case IEDeviceType:
		return "DeviceType"
	case IEServiceIdent:
		return "ServiceIdent"
	case IEFirmwareVer:
		return "FirmwareVer"
	case IEFwBlockDesc:
		return "FwBlockDesc"
	case IEFwBlockData:
		return "FwBlockData"
	case IEProvVer:
		return "ProvVer"
	case IECallingPres:
		return "CallingPres"
	case IECallingTON:
		return "CallingTON"
	case IECallingTNS:
		return "CallingTNS"
	case IESamplingRate:
		return "SamplingRate"
	case IECauseCode:
		return "CauseCode"
	case IEEncryption:
		return "Encryption"
	case IEEncKey:
		return "EncKey"
	case IECodecPrefs:
		return "CodecPrefs"
	case IERRJitter:
		return "RRJitter"
	case IERRLoss:
		return "RRLoss"
	case IERRPackets:
		return "RRPackets"
	case IERRDelay:
		return "RRDelay"
	case IERRDropped:
		return "RRDropped"
	case IERROOO:
		return "RROOO"
	case IEVariable:
		return "Variable"
	case IEOSPToken:
		return "OSPToken"
	case IECallToken:
		return "CallToken"
	case IECapability2:
		return "Capability2"
	case IEFormat2:
		return "Format2"
	case IECallingANI2:
		return "CallingANI2"
	default:
		return fmt.Sprintf("Unknown(%d)", ie)
	}
}

// Subclasses for IAXCtl frame
const (
	IAXCtlNew       Subclass = 0x01
	IAXCtlPing      Subclass = 0x02
	IAXCtlPong      Subclass = 0x03
	IAXCtlAck       Subclass = 0x04
	IAXCtlHangup    Subclass = 0x05
	IAXCtlReject    Subclass = 0x06
	IAXCtlAccept    Subclass = 0x07
	IAXCtlAuthReq   Subclass = 0x08
	IAXCtlAuthRep   Subclass = 0x09
	IAXCtlInval     Subclass = 0x0a
	IAXCtlLagRqst   Subclass = 0x0b
	IAXCtlLagRply   Subclass = 0x0c
	IAXCtlRegReq    Subclass = 0x0d
	IAXCtlRegAuth   Subclass = 0x0e
	IAXCtlRegAck    Subclass = 0x0f
	IAXCtlRegRej    Subclass = 0x10
	IAXCtlRegRel    Subclass = 0x11
	IAXCtlVnak      Subclass = 0x12
	IAXCtlDpReq     Subclass = 0x13
	IAXCtlDpRep     Subclass = 0x14
	IAXCtlDial      Subclass = 0x15
	IAXCtlTxReq     Subclass = 0x16
	IAXCtlTxCnt     Subclass = 0x17
	IAXCtlTxAcc     Subclass = 0x18
	IAXCtlTxReady   Subclass = 0x19
	IAXCtlTxRel     Subclass = 0x1a
	IAXCtlTxRej     Subclass = 0x1b
	IAXCtlQuelch    Subclass = 0x1c
	IAXCtlUnquelch  Subclass = 0x1d
	IAXCtlPoke      Subclass = 0x1e
	IAXCtlMWI       Subclass = 0x20
	IAXCtlUnsupport Subclass = 0x21
	IAXCtlTransfer  Subclass = 0x22
)

// Subclasses for Control frames
const (
	CtlHangup     Subclass = 0x01
	CtlRinging    Subclass = 0x03
	CtlAnswer     Subclass = 0x04
	CtlBusy       Subclass = 0x05
	CtlCongest    Subclass = 0x08
	CtlFlash      Subclass = 0x09
	CtlOption     Subclass = 0x0b
	CtlKey        Subclass = 0x0c
	CtlUnkey      Subclass = 0x0d
	CtlProgress   Subclass = 0x0e
	CtlProceeding Subclass = 0x0f
	CtlHold       Subclass = 0x10
	CtlUnhold     Subclass = 0x11
)

// SubclassToString returns the string representation of the Subclass
func SubclassToString(ft FrameType, sc Subclass) string {
	switch ft {
	case FrmIAXCtl:
		switch sc {
		case IAXCtlNew:
			return "New"
		case IAXCtlPing:
			return "Ping"
		case IAXCtlPong:
			return "Pong"
		case IAXCtlAck:
			return "Ack"
		case IAXCtlHangup:
			return "Hangup"
		case IAXCtlReject:
			return "Reject"
		case IAXCtlAccept:
			return "Accept"
		case IAXCtlAuthReq:
			return "AuthReq"
		case IAXCtlAuthRep:
			return "AuthRep"
		case IAXCtlInval:
			return "Inval"
		case IAXCtlLagRqst:
			return "LagRqst"
		case IAXCtlLagRply:
			return "LagRply"
		case IAXCtlRegReq:
			return "RegReq"
		case IAXCtlRegAuth:
			return "RegAuth"
		case IAXCtlRegAck:
			return "RegAck"
		case IAXCtlRegRej:
			return "RegRej"
		case IAXCtlRegRel:
			return "RegRel"
		case IAXCtlVnak:
			return "Vnak"
		case IAXCtlDpReq:
			return "DpReq"
		case IAXCtlDpRep:
			return "DpRep"
		case IAXCtlDial:
			return "Dial"
		case IAXCtlTxReq:
			return "TxReq"
		case IAXCtlTxCnt:
			return "TxCnt"
		case IAXCtlTxAcc:
			return "TxAcc"
		case IAXCtlTxReady:
			return "TxReady"
		case IAXCtlTxRel:
			return "TxRel"
		case IAXCtlTxRej:
			return "TxRej"
		case IAXCtlQuelch:
			return "Quelch"
		case IAXCtlUnquelch:
			return "Unquelch"
		case IAXCtlPoke:
			return "Poke"
		case IAXCtlMWI:
			return "MWI"
		case IAXCtlUnsupport:
			return "Unsupport"
		case IAXCtlTransfer:
			return "Transfer"
		default:
			return fmt.Sprintf("Unknown(%v - %v)", ft, sc)
		}
	case FrmControl:
		switch sc {
		case CtlHangup:
			return "Hangup"
		case CtlRinging:
			return "Ringing"
		case CtlAnswer:
			return "Answer"
		case CtlBusy:
			return "Busy"
		case CtlCongest:
			return "Congest"
		case CtlFlash:
			return "Flash"
		case CtlOption:
			return "Option"
		case CtlKey:
			return "Key"
		case CtlUnkey:
			return "Unkey"
		case CtlProgress:
			return "Progress"
		case CtlProceeding:
			return "Proceeding"
		case CtlHold:
			return "Hold"
		case CtlUnhold:
			return "Unhold"
		default:
			return fmt.Sprintf("Unknown(%v - %v)", ft, sc)
		}
	case FrmVoice:
		return Codec(sc).String()
	default:
		return fmt.Sprintf("Unknown(%v - %v)", ft, sc)
	}
}

// Information Element Frame
type IEFrame struct {
	ie   IEType
	data []byte
}

// Uint32IE returns an IEFrame with the given IE and uint32 data
func Uint32IE(ie IEType, data uint32) *IEFrame {
	return &IEFrame{ie, []byte{byte(data >> 24), byte(data >> 16), byte(data >> 8), byte(data)}}
}

// Uint16IE returns an IEFrame with the given IE and uint16 data
func Uint16IE(ie IEType, data uint16) *IEFrame {
	return &IEFrame{ie, []byte{byte(data >> 8), byte(data)}}
}

// Uint8IE returns an IEFrame with the given IE and uint8 data
func Uint8IE(ie IEType, data uint8) *IEFrame {
	return &IEFrame{ie, []byte{byte(data)}}
}

// StringIE returns an IEFrame with the given IE and string data
func StringIE(ie IEType, data string) *IEFrame {
	return &IEFrame{ie, []byte(data)}
}

// BytesIE returns an IEFrame with the given IE and data
func BytesIE(ie IEType, data []byte) *IEFrame {
	return &IEFrame{ie, data}
}

// IE returns the IE of the IEFrame
func (ief *IEFrame) IE() IEType {
	return ief.ie
}

// AsUint64 returns the IE data as uint64
func (ief *IEFrame) AsUint64() uint64 {
	return binary.BigEndian.Uint64(ief.data)
}

// AsUint32 returns the IE data as uint32
func (ief *IEFrame) AsUint32() uint32 {
	return binary.BigEndian.Uint32(ief.data)
}

// AsUint16 returns the IE data as uint16
func (ief *IEFrame) AsUint16() uint16 {
	return binary.BigEndian.Uint16(ief.data)
}

// AsUint8 returns the IE data as uint8
func (ief *IEFrame) AsUint8() uint8 {
	return uint8(ief.data[0])
}

// AsString returns the IE data as string
func (ief *IEFrame) AsString() string {
	return string(ief.data)
}

// AsBytes returns the IE data as []byte
func (ief *IEFrame) AsBytes() []byte {
	return ief.data
}

// Frame represents an IAX frame
type Frame interface {
	Encode() []byte
	SetSrcCallNumber(uint16)
	SrcCallNumber() uint16
	SetDstCallNumber(uint16)
	DstCallNumber() uint16
	SetOSeqNo(uint8)
	OSeqNo() uint8
	SetISeqNo(uint8)
	ISeqNo() uint8
	SetTimestamp(uint32)
	Timestamp() uint32
	Payload() []byte
	IsFullFrame() bool
	String() string
}

// MiniFrame represents a mini IAX frame
type MiniFrame struct {
	sourceCallNumber uint16
	timestamp        uint16
	payload          []byte
}

// NewMiniFrame returns a new MiniFrame
func NewMiniFrame(sourceCallNumber uint16, timestamp uint32, payload []byte) *MiniFrame {
	return &MiniFrame{
		sourceCallNumber: sourceCallNumber,
		timestamp:        uint16(timestamp),
		payload:          payload,
	}
}

func (f *MiniFrame) String() string {
	res := fmt.Sprintf("MiniFrame: SrcCallNumber=%d, Timestamp=%d\n", f.SrcCallNumber(), f.Timestamp())
	if len(f.Payload()) > 0 {
		res += fmt.Sprintf("Payload:\n%s", hex.Dump(f.Payload()))
	}
	return res
}

// SetSrcCallNumber sets the source call number of the MiniFrame
func (f *MiniFrame) SetSrcCallNumber(sourceCallNumber uint16) {
	f.sourceCallNumber = sourceCallNumber
}

// srcCallNumber returns the source call number of the MiniFrame
func (f *MiniFrame) SrcCallNumber() uint16 {
	return f.sourceCallNumber
}

// SetDstCallNumber sets the destination call number of the MiniFrame
func (f *MiniFrame) SetDstCallNumber(destCallNumber uint16) {
	// MiniFrame has no destination call number
}

func (f *MiniFrame) DstCallNumber() uint16 {
	return 0
}

// SetOSeqNo sets the OSeqno of the MiniFrame
func (f *MiniFrame) SetOSeqNo(oSeqno uint8) {
	// MiniFrame has no OSeqno
}

// OSeqNo returns the OSeqNo of the MiniFrame
func (f *MiniFrame) OSeqNo() uint8 {
	return 0
}

// SetISeqNo sets the ISeqno of the MiniFrame
func (f *MiniFrame) SetISeqNo(iSeqno uint8) {
	// MiniFrame has no ISeqno
}

// ISeqNo returns the ISeqNo of the MiniFrame
func (f *MiniFrame) ISeqNo() uint8 {
	return 0
}

// IsFullFrame returns false for MiniFrame
func (f *MiniFrame) IsFullFrame() bool {
	return false
}

// Payload returns the payload of the MiniFrame
func (f *MiniFrame) Payload() []byte {
	return f.payload
}

// SetTimestamp sets the timestamp of the MiniFrame
func (f *MiniFrame) SetTimestamp(timestamp uint32) {
	f.timestamp = uint16(timestamp)
}

// Timestamp returns the timestamp of the MiniFrame
func (f *MiniFrame) Timestamp() uint32 {
	return uint32(f.timestamp)
}

// Encode returns the MiniFrame as byte slice
func (f *MiniFrame) Encode() []byte {
	frame := make([]byte, 4+len(f.payload))

	binary.BigEndian.PutUint16(frame[0:], f.sourceCallNumber)
	binary.BigEndian.PutUint16(frame[2:], f.timestamp)

	if f.payload != nil {
		copy(frame[4:], f.payload)
	}

	return frame
}

// FullFrame represents a full IAX frame
type FullFrame struct {
	retransmit       bool
	sourceCallNumber uint16
	destCallNumber   uint16
	timestamp        uint32
	oSeqNo           uint8
	iSeqNo           uint8
	frameType        FrameType
	subclass         Subclass
	ies              []*IEFrame
	payload          []byte
}

// NewFullFrame returns a new FullFrame
func NewFullFrame(frameType FrameType, subclass Subclass) *FullFrame {
	return &FullFrame{
		frameType: frameType,
		subclass:  subclass,
	}
}

func (f *FullFrame) String() string {
	res := fmt.Sprintf("FullFrame: Retry=%t, SrcCallNumber=%d, DstCallNumber=%d, Timestamp=%d, OSeqNo=%d, ISeqNo=%d, FrameType=%s, Subclass=%s\n", f.retransmit, f.sourceCallNumber, f.destCallNumber, f.timestamp, f.oSeqNo, f.iSeqNo, f.frameType.String(), SubclassToString(f.FrameType(), f.Subclass()))

	for _, ie := range f.IEs() {
		res += fmt.Sprintf("IE(%s):\n%s", ie.IE().String(), hex.Dump(ie.AsBytes()))
	}
	if len(f.Payload()) > 0 {
		res += fmt.Sprintf("Payload:\n%s", hex.Dump(f.Payload()))
	}
	return res
}

// IsFullFrame returns true for FullFrame
func (f *FullFrame) IsFullFrame() bool {
	return true
}

// AddIE adds an IE to the FullFrame
func (f *FullFrame) AddIE(ie *IEFrame) {
	f.ies = append(f.ies, ie)
}

// SetPayload sets the payload of the FullFrame
func (f *FullFrame) SetPayload(payload []byte) {
	f.payload = payload
}

// Payload returns the payload of the FullFrame
func (f *FullFrame) Payload() []byte {
	return f.payload
}

// IEs returns the IEs of the FullFrame
func (f *FullFrame) IEs() []*IEFrame {
	return f.ies
}

// FindIE returns the IEFrame with the given IEType
func (f *FullFrame) FindIE(ie IEType) *IEFrame {
	for _, ief := range f.ies {
		if ief.ie == ie {
			return ief
		}
	}
	return nil
}

// SetRetransmit sets the retransmit flag of the FullFrame
func (f *FullFrame) SetRetransmit(retransmit bool) {
	f.retransmit = retransmit
}

// Retransmit returns the retransmit flag of the FullFrame
func (f *FullFrame) Retransmit() bool {
	return f.retransmit
}

// SetSrcCallNumber sets the source call number of the FullFrame
func (f *FullFrame) SetSrcCallNumber(sourceCallNumber uint16) {
	f.sourceCallNumber = sourceCallNumber
}

// SourceCallNumber returns the source call number of the FullFrame
func (f *FullFrame) SrcCallNumber() uint16 {
	return f.sourceCallNumber
}

// SetDstCallNumber sets the destination call number of the FullFrame
func (f *FullFrame) SetDstCallNumber(destCallNumber uint16) {
	f.destCallNumber = destCallNumber
}

// DestCallNumber returns the destination call number of the FullFrame
func (f *FullFrame) DstCallNumber() uint16 {
	return f.destCallNumber
}

// SetTimestamp sets the timestamp of the FullFrame
func (f *FullFrame) SetTimestamp(timestamp uint32) {
	f.timestamp = timestamp
}

// Timestamp returns the timestamp of the FullFrame
func (f *FullFrame) Timestamp() uint32 {
	return f.timestamp
}

// SetOSeqNo sets the OSeqno of the FullFrame
func (f *FullFrame) SetOSeqNo(oSeqno uint8) {
	f.oSeqNo = oSeqno
}

// OSeqNo returns the OSeqNo of the FullFrame
func (f *FullFrame) OSeqNo() uint8 {
	return f.oSeqNo
}

// SetISeqNo sets the ISeqno of the FullFrame
func (f *FullFrame) SetISeqNo(iSeqno uint8) {
	f.iSeqNo = iSeqno
}

// ISeqNo returns the ISeqNo of the FullFrame
func (f *FullFrame) ISeqNo() uint8 {
	return f.iSeqNo
}

// FrameType returns the FrameType of the FullFrame
func (f *FullFrame) FrameType() FrameType {
	return f.frameType
}

// Subclass returns the Subclass of the FullFrame
func (f *FullFrame) Subclass() Subclass {
	return f.subclass
}

// Encode returns the FullFrame as byte slice
func (f *FullFrame) Encode() []byte {

	iesSize := 0
	for _, ie := range f.ies {
		iesSize += 2 + len(ie.data) // IE + len + data
	}
	frame := make([]byte, 12+iesSize+len(f.payload))

	binary.BigEndian.PutUint16(frame[0:], f.sourceCallNumber|0x8000)
	if f.retransmit {
		binary.BigEndian.PutUint16(frame[2:], f.destCallNumber|0x8000)
	} else {
		binary.BigEndian.PutUint16(frame[2:], f.destCallNumber&0x7fff)
	}
	binary.BigEndian.PutUint32(frame[4:], f.timestamp)
	frame[8] = f.oSeqNo
	frame[9] = f.iSeqNo
	frame[10] = byte(f.frameType)
	frame[11] = byte(f.subclass)

	frmIdx := 12

	for _, ie := range f.ies {
		frame[frmIdx] = byte(ie.ie)
		frame[frmIdx+1] = byte(len(ie.data))
		copy(frame[frmIdx+2:], ie.data)
		frmIdx += 2 + len(ie.data)
	}

	if f.payload != nil {
		copy(frame[frmIdx:], f.payload)
	}

	return frame
}

// Check if this is a full frame
func IsFullFrame(frame []byte) bool {
	return frame[0]&0x80 == 0x80
}

// Check if frame is a response to a previous frame
func (f *FullFrame) IsResponse() bool {
	switch f.frameType {
	case FrmIAXCtl:
		switch f.subclass {
		case IAXCtlRegAck, IAXCtlRegRej, IAXCtlRegAuth, IAXCtlPong, IAXCtlAck:
			return true
		}
	}
	return false
}

// Check if frame needs an ACK
func (f *FullFrame) NeedACK() bool {
	switch f.frameType {
	case FrmIAXCtl:
		switch f.subclass {
		case IAXCtlNew, IAXCtlRegAck, IAXCtlRegRej, IAXCtlRegRel, IAXCtlPong, IAXCtlAccept, IAXCtlReject, IAXCtlHangup, IAXCtlAuthRep, IAXCtlTxRel:
			return true
		}
	case FrmControl:
		switch f.subclass {
		case CtlHangup, CtlRinging, CtlAnswer, CtlBusy, CtlCongest, CtlFlash, CtlOption, CtlKey, CtlUnkey, CtlProgress, CtlProceeding, CtlHold, CtlUnhold:
			return true
		}
	}
	return false
}

func (f *FullFrame) NeedResponse() bool {
	switch f.frameType {
	case FrmIAXCtl:
		switch f.subclass {
		case IAXCtlRegReq, IAXCtlAuthReq, IAXCtlPing, IAXCtlPoke:
			return true
		}
	}
	return false
}

// DecodeFrame decodes a byte slice to a FullFrame
func DecodeFrame(frame []byte) (Frame, error) {

	if IsFullFrame(frame) {
		frm := &FullFrame{}

		if len(frame) < 12 {
			return nil, ErrInvalidFrame
		}

		// Check if this is a retransmited frame
		if frame[2]&0x80 == 0x80 {
			frm.retransmit = true
		}

		frm.sourceCallNumber = binary.BigEndian.Uint16(frame[0:]) & 0x7fff
		frm.destCallNumber = binary.BigEndian.Uint16(frame[2:]) & 0x7fff
		frm.timestamp = binary.BigEndian.Uint32(frame[4:])
		frm.oSeqNo = frame[8]
		frm.iSeqNo = frame[9]
		frm.frameType = FrameType(frame[10])
		frm.subclass = Subclass(frame[11])

		frmIdx := 12

		if frm.frameType == FrmIAXCtl || frm.frameType == FrmControl {
			for frmIdx+1 < len(frame) {
				ieDataLen := int(frame[frmIdx+1])
				if frmIdx+ieDataLen+2 > len(frame) {
					break
				}
				ie := &IEFrame{IEType(frame[frmIdx]), frame[frmIdx+2 : frmIdx+2+ieDataLen]}
				frm.ies = append(frm.ies, ie)
				frmIdx += ieDataLen + 2
			}
		} else {
			if frmIdx < len(frame) {
				frm.payload = frame[frmIdx:]
			}
		}

		return frm, nil
	} else {
		frm := &MiniFrame{}

		if len(frame) < 4 {
			return nil, ErrInvalidFrame
		}

		frm.sourceCallNumber = binary.BigEndian.Uint16(frame[0:])
		frm.timestamp = binary.BigEndian.Uint16(frame[2:])

		if len(frame) > 4 {
			frm.payload = frame[4:]
		}

		return frm, nil
	}
}
