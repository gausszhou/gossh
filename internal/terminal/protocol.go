package terminal

import (
	"encoding/json"
	"fmt"
)

// Protocols defines the name of this protocol,
// which is supposed to be used as the subprotocol of WebSocket streams.
var Protocols = []string{"webtty"}

// Message types sent from the client to the server.
const (
	// UnknownInput message type, maybe sent by a bug.
	UnknownInput = '0'
	// Input is user input, typically from a keyboard.
	Input = '1'
	// Ping is a keep-alive message from the client.
	Ping = '2'
	// ResizeTerminal notifies the server that the terminal size has changed.
	ResizeTerminal = '3'
)

// Message types sent from the server to the client.
const (
	// UnknownOutput message type, maybe set by a bug.
	UnknownOutput = '0'
	// Output is normal terminal output (raw bytes, no base64).
	Output = '1'
	// Pong is the response to a client Ping.
	Pong = '2'
	// SetWindowTitle sets the window title of the terminal.
	SetWindowTitle = '3'
	// SetPreferences sets terminal preferences.
	SetPreferences = '4'
	// SetReconnect tells the client to reconnect after disconnection.
	SetReconnect = '5'
	// SetReplayDone is sent right after the attach-time init frames. It is
	// the handshake marker after which the client may forward input: xterm
	// auto-generates answers for terminal queries (DSR/DECRQM/OSC) it sees
	// in the output stream, and those answers must NOT be written back into
	// the PTY — the program that issued the queries is not waiting for them.
	// (Named for the historical attach-time output replay; the marker itself
	// is still what gates input forwarding in the browser.)
	SetReplayDone = '6'
)

// EncodeFrame wraps payload with a message type byte:
// [type byte] [payload bytes...]
func EncodeFrame(msgType byte, payload []byte) []byte {
	frame := make([]byte, 1+len(payload))
	frame[0] = msgType
	copy(frame[1:], payload)
	return frame
}

// EncodeOutput builds an Output frame carrying raw terminal output.
func EncodeOutput(payload []byte) []byte {
	return EncodeFrame(Output, payload)
}

// EncodePong builds a Pong frame.
func EncodePong() []byte {
	return []byte{Pong}
}

// EncodeWindowTitle builds a SetWindowTitle frame.
func EncodeWindowTitle(title []byte) []byte {
	return EncodeFrame(SetWindowTitle, title)
}

// EncodePreferences builds a SetPreferences frame.
func EncodePreferences(prefs []byte) []byte {
	return EncodeFrame(SetPreferences, prefs)
}

// EncodeReconnect builds a SetReconnect frame whose payload is a JSON number.
func EncodeReconnect(seconds int) []byte {
	payload, _ := json.Marshal(seconds)
	return EncodeFrame(SetReconnect, payload)
}

// EncodeReplayDone builds an empty-byte SetReplayDone frame.
func EncodeReplayDone() []byte {
	return []byte{SetReplayDone}
}

// ClientMessage is a decoded frame received from the client.
type ClientMessage struct {
	Type    byte
	Payload []byte
}

// DecodeClientFrame parses a frame received from the client.
// An empty frame is invalid: both Ping and ResizeTerminal are
// distinguished by their type byte, and Input requires a payload.
func DecodeClientFrame(frame []byte) (ClientMessage, error) {
	if len(frame) == 0 {
		return ClientMessage{}, fmt.Errorf("%w: empty frame", ErrInvalidMessage)
	}

	switch frame[0] {
	case Input, Ping, ResizeTerminal:
		return ClientMessage{Type: frame[0], Payload: frame[1:]}, nil
	default:
		return ClientMessage{}, fmt.Errorf("%w: unknown message type `%c`", ErrInvalidMessage, frame[0])
	}
}

// ResizeArgs is the JSON payload of a ResizeTerminal message.
// encoding/json matches keys case-insensitively, so both
// `{"columns":80,"rows":24}` and `{"Columns":80,"Rows":24}` are accepted.
type ResizeArgs struct {
	Columns int `json:"columns"`
	Rows    int `json:"rows"`
}

// ParseResizeArgs decodes the JSON payload of a ResizeTerminal message.
func ParseResizeArgs(payload []byte) (ResizeArgs, error) {
	var args ResizeArgs
	if err := json.Unmarshal(payload, &args); err != nil {
		return ResizeArgs{}, fmt.Errorf("%w: invalid resize payload: %v", ErrInvalidMessage, err)
	}
	return args, nil
}

// ---------------------------------------------------------------------------
// multiplexed framing
//
// The single-session protocol identified the session by the URL query
// (?session_id=), which forced one WebSocket per session. The multiplexed
// protocol instead carries the session id in a routing header, so one
// connection can serve any number of sessions:
//
//	[session_id (16B)] [type (1B)] [len (2B BE)] [payload (len B)]
//
// The session-level bytes handed to Session.Attach are still the plain
// [type][payload] frames of EncodeFrame/DecodeClientFrame — the router strips
// the header and the length, so nothing below it changes.
//
// See docs/design/ws-multiplex.md.
// ---------------------------------------------------------------------------

// SessionIDLen is the fixed width of the session id in a routed frame.
const SessionIDLen = 16

// routedHeaderLen is session id + type + 2-byte length.
const routedHeaderLen = SessionIDLen + 3

// maxRoutedPayload is the largest payload the 2-byte length can express.
const maxRoutedPayload = 0xFFFF

// Multiplexed control messages. The data messages reuse the type bytes above
// ('1' input/output, '3' resize/title, '4' preferences, '5' reconnect,
// '6' replay-done), so only the control plane needs new bytes.
const (
	// Attach asks the server to attach to the routed session. Client to
	// server; the optional payload is a JSON {columns,rows} hint.
	Attach = 'A'
	// Detach releases the routed session's channel. Client to server.
	Detach = 'D'
	// AttachOK reports a successful attach, after which output flows.
	// Server to client.
	AttachOK = 'a'
	// AttachFail rejects an attach; the payload is a human-readable
	// reason. Server to client.
	AttachFail = 'b'
	// SessionEvent carries a one-byte status about the routed session.
	// Server to client.
	SessionEvent = 'E'
)

// SessionEvent statuses, the payload of a SessionEvent frame.
const (
	// EventPreempted means another client took over the session.
	EventPreempted = 0x01
	// EventDestroyed means the session was destroyed on the server.
	EventDestroyed = 0x02
)

// connLevelID is the all-zero session id: it marks a frame as belonging to
// the connection itself (Ping/Pong) rather than to a session.
const connLevelID = "\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"

// RoutedFrame is a decoded multiplexed frame. SessionID is "" for
// connection-level frames.
type RoutedFrame struct {
	SessionID string
	Type      byte
	Payload   []byte
}

// IsConnLevel reports whether the frame addresses the connection itself
// rather than a session.
func (f RoutedFrame) IsConnLevel() bool { return f.SessionID == "" }

// EncodeRouted wraps a session-level frame in the routing header.
// sid must be exactly SessionIDLen bytes (a 16-character base36 id as
// generated by the client, see ADR-0001).
func EncodeRouted(sid string, msgType byte, payload []byte) ([]byte, error) {
	if len(sid) != SessionIDLen {
		return nil, fmt.Errorf("%w: session id must be %d bytes, got %d", ErrInvalidMessage, SessionIDLen, len(sid))
	}
	if len(payload) > maxRoutedPayload {
		return nil, fmt.Errorf("%w: payload of %d bytes exceeds the %d-byte limit",
			ErrInvalidMessage, len(payload), maxRoutedPayload)
	}

	frame := make([]byte, routedHeaderLen+len(payload))
	copy(frame[:SessionIDLen], sid)
	frame[SessionIDLen] = msgType
	frame[SessionIDLen+1] = byte(len(payload) >> 8)
	frame[SessionIDLen+2] = byte(len(payload))
	copy(frame[routedHeaderLen:], payload)
	return frame, nil
}

// EncodeRoutedConn builds a connection-level frame (Ping/Pong). It cannot
// fail: there is no session id to validate and control payloads are tiny.
func EncodeRoutedConn(msgType byte, payload []byte) []byte {
	frame := make([]byte, routedHeaderLen+len(payload))
	copy(frame[:SessionIDLen], connLevelID)
	frame[SessionIDLen] = msgType
	frame[SessionIDLen+1] = byte(len(payload) >> 8)
	frame[SessionIDLen+2] = byte(len(payload))
	copy(frame[routedHeaderLen:], payload)
	return frame
}

// EncodeRoutedSession is EncodeRouted for the frames a session bridge
// produces: b[0] is the message type and b[1:] the payload.
func EncodeRoutedSession(sid string, b []byte) ([]byte, error) {
	if len(b) == 0 {
		return nil, fmt.Errorf("%w: empty session frame", ErrInvalidMessage)
	}
	return EncodeRouted(sid, b[0], b[1:])
}

// DecodeRouted parses a multiplexed frame received from the client.
func DecodeRouted(frame []byte) (RoutedFrame, error) {
	if len(frame) < routedHeaderLen {
		return RoutedFrame{}, fmt.Errorf("%w: routed frame is %d bytes, want at least %d",
			ErrInvalidMessage, len(frame), routedHeaderLen)
	}

	sid := string(frame[:SessionIDLen])
	if sid == connLevelID {
		sid = ""
	}

	length := int(frame[SessionIDLen+1])<<8 | int(frame[SessionIDLen+2])
	payload := frame[routedHeaderLen:]
	if length != len(payload) {
		return RoutedFrame{}, fmt.Errorf("%w: declared length %d but %d bytes follow",
			ErrInvalidMessage, length, len(payload))
	}
	if len(payload) > maxRoutedPayload {
		return RoutedFrame{}, fmt.Errorf("%w: payload of %d bytes exceeds the %d-byte limit",
			ErrInvalidMessage, len(payload), maxRoutedPayload)
	}

	return RoutedFrame{SessionID: sid, Type: frame[SessionIDLen], Payload: payload}, nil
}
