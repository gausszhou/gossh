package api

import (
	"context"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/gausszhou/gossh/internal/session"
	"github.com/gausszhou/gossh/internal/terminal"
	"github.com/gausszhou/gossh/internal/utils"
)

// Tests for the multiplexed WebSocket protocol: one socket, N sessions, each
// frame carrying a 16-byte session id in a routing header. The legacy
// ?session_id= protocol keeps working alongside it — see
// TestLegacyAndMultiplexedCoexist.

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// writtenText is the input a stub terminal received so far.
func (s *stubTerminal) writtenText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.written)
}

func dialMultiplexed(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?token=" + testToken
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial multiplexed ws: %s", err)
	}
	conn.SetReadLimit(4 << 20)
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func sendRouted(t *testing.T, conn *websocket.Conn, sid string, msgType byte, payload []byte) {
	t.Helper()
	if err := conn.Write(context.Background(), websocket.MessageBinary,
		mustRouted(t, sid, msgType, payload)); err != nil {
		t.Fatalf("write routed frame: %s", err)
	}
}

func sendConnLevel(t *testing.T, conn *websocket.Conn, msgType byte, payload []byte) {
	t.Helper()
	if err := conn.Write(context.Background(), websocket.MessageBinary,
		terminal.EncodeRoutedConn(msgType, payload)); err != nil {
		t.Fatalf("write connection-level frame: %s", err)
	}
}

func mustRouted(t *testing.T, sid string, msgType byte, payload []byte) []byte {
	t.Helper()
	frame, err := terminal.EncodeRouted(sid, msgType, payload)
	if err != nil {
		t.Fatalf("encode routed frame: %s", err)
	}
	return frame
}

// readRoutedUntil reads frames until one matches, skipping the rest — on a
// shared connection frames for other sessions legitimately interleave.
func readRoutedUntil(t *testing.T, conn *websocket.Conn, match func(terminal.RoutedFrame) bool, what string) terminal.RoutedFrame {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			continue
		}
		f, err := terminal.DecodeRouted(data)
		if err != nil {
			t.Fatalf("%s: malformed routed frame (%d bytes): %s", what, len(data), err)
		}
		if match(f) {
			return f
		}
	}
	t.Fatalf("timed out waiting for %s", what)
	return terminal.RoutedFrame{}
}

// attachAndWait attaches a session and waits for the handshake the browser
// waits for too: AttachOK, then SetReplayDone (after which input may flow).
func attachAndWait(t *testing.T, conn *websocket.Conn, sid string) {
	t.Helper()
	sendRouted(t, conn, sid, terminal.Attach, nil)
	readRoutedUntil(t, conn, func(f terminal.RoutedFrame) bool {
		return f.SessionID == sid && f.Type == terminal.AttachOK
	}, "AttachOK for "+sid)
	readRoutedUntil(t, conn, func(f terminal.RoutedFrame) bool {
		return f.SessionID == sid && f.Type == terminal.SetReplayDone
	}, "SetReplayDone for "+sid)
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func stateOf(t *testing.T, mgr *session.Manager, id string) session.State {
	t.Helper()
	sess, err := mgr.Get(id)
	if err != nil {
		t.Fatalf("get session %s: %s", id, err)
	}
	return sess.State()
}

// ---------------------------------------------------------------------------
// frame codec
// ---------------------------------------------------------------------------

func TestRoutedFrameRoundTrip(t *testing.T) {
	sid := utils.RandomString(terminal.SessionIDLen)
	for _, tc := range []struct {
		name    string
		msgType byte
		payload []byte
	}{
		{"empty control frame", terminal.AttachOK, nil},
		{"input", terminal.Input, []byte("ls -la\r")},
		{"event", terminal.SessionEvent, []byte{terminal.EventPreempted}},
		{"binary output", terminal.Output, []byte{0x1b, 0x5b, 0x33, 0x31, 0x6d}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := terminal.EncodeRouted(sid, tc.msgType, tc.payload)
			if err != nil {
				t.Fatalf("encode: %s", err)
			}
			if want := terminal.SessionIDLen + 3 + len(tc.payload); len(frame) != want {
				t.Fatalf("frame is %d bytes, want %d", len(frame), want)
			}
			got, err := terminal.DecodeRouted(frame)
			if err != nil {
				t.Fatalf("decode: %s", err)
			}
			if got.SessionID != sid {
				t.Errorf("session id = %q, want %q", got.SessionID, sid)
			}
			if got.Type != tc.msgType {
				t.Errorf("type = %q, want %q", got.Type, tc.msgType)
			}
			if string(got.Payload) != string(tc.payload) {
				t.Errorf("payload = %q, want %q", got.Payload, tc.payload)
			}
			if got.IsConnLevel() {
				t.Error("a session frame must not report as connection-level")
			}
		})
	}
}

func TestRoutedConnLevelFrame(t *testing.T) {
	frame := terminal.EncodeRoutedConn(terminal.Ping, nil)
	got, err := terminal.DecodeRouted(frame)
	if err != nil {
		t.Fatalf("decode: %s", err)
	}
	if !got.IsConnLevel() {
		t.Error("an all-zero session id must decode as connection-level")
	}
	if got.SessionID != "" {
		t.Errorf("session id = %q, want empty", got.SessionID)
	}
	if got.Type != terminal.Ping {
		t.Errorf("type = %q, want Ping", got.Type)
	}
}

func TestRoutedFrameRejectsMalformedInput(t *testing.T) {
	sid := utils.RandomString(terminal.SessionIDLen)

	if _, err := terminal.EncodeRouted("too-short", terminal.Input, nil); err == nil {
		t.Error("encoding a session id shorter than 16 bytes must fail")
	}
	if _, err := terminal.EncodeRouted(sid, terminal.Input, make([]byte, 0x10000)); err == nil {
		t.Error("encoding a payload over the 2-byte length limit must fail")
	}
	if _, err := terminal.EncodeRoutedSession(sid, nil); err == nil {
		t.Error("encoding an empty session frame must fail")
	}

	// A frame whose declared length disagrees with the bytes that follow.
	frame := mustRouted(t, sid, terminal.Input, []byte("abc"))
	frame[terminal.SessionIDLen+2] = 0x09 // claim 9 bytes, only 3 follow
	if _, err := terminal.DecodeRouted(frame); err == nil {
		t.Error("a lying length must be rejected")
	}

	for _, short := range [][]byte{nil, {0x01}, make([]byte, terminal.SessionIDLen+2)} {
		if _, err := terminal.DecodeRouted(short); err == nil {
			t.Errorf("a %d-byte frame must be rejected as too short", len(short))
		}
	}
}

// tsEncoderFixtures are frames produced by the TypeScript encoder
// (apps/web/src/utils/session-channel.ts) and checked in as hex, so the two
// implementations cannot silently drift apart — this is the actual wire
// contract between browser and server.
func TestGoDecodesWhatTheBrowserEncodes(t *testing.T) {
	const sid = "abc0123456789def" // must be 16 chars, like a real session id

	for _, tc := range []struct {
		name      string
		hex       string
		wantSID   string
		wantType  byte
		wantPing  bool
		wantLoad  string
		wantEvent byte
	}{
		{
			name: "attach", hex: "61626330313233343536373839646566410000",
			wantSID: sid, wantType: terminal.Attach,
		},
		{
			name: "attach with a size hint", hex: "616263303132333435363738396465664100187b22636f6c756d6e73223a38302c22726f7773223a32347d",
			wantSID: sid, wantType: terminal.Attach, wantLoad: `{"columns":80,"rows":24}`,
		},
		{
			name: "detach", hex: "61626330313233343536373839646566440000",
			wantSID: sid, wantType: terminal.Detach,
		},
		{
			name: "input", hex: "616263303132333435363738396465663100036c730d",
			wantSID: sid, wantType: terminal.Input, wantLoad: "ls\r",
		},
		{
			name: "resize", hex: "616263303132333435363738396465663300197b22636f6c756d6e73223a3132302c22726f7773223a34307d",
			wantSID: sid, wantType: terminal.ResizeTerminal, wantLoad: `{"columns":120,"rows":40}`,
		},
		{
			name: "session event", hex: "6162633031323334353637383964656645000101",
			wantSID: sid, wantType: terminal.SessionEvent, wantEvent: terminal.EventPreempted,
		},
		{
			name: "connection-level ping", hex: "00000000000000000000000000000000320000",
			wantType: terminal.Ping, wantPing: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := hex.DecodeString(tc.hex)
			if err != nil {
				t.Fatalf("bad fixture: %s", err)
			}
			f, err := terminal.DecodeRouted(raw)
			if err != nil {
				t.Fatalf("decode: %s", err)
			}
			if tc.wantPing {
				if !f.IsConnLevel() {
					t.Error("want a connection-level frame")
				}
			} else {
				if f.SessionID != tc.wantSID {
					t.Errorf("session id = %q, want %q", f.SessionID, tc.wantSID)
				}
			}
			if f.Type != tc.wantType {
				t.Errorf("type = %q (0x%02x), want %q (0x%02x)", f.Type, f.Type, tc.wantType, tc.wantType)
			}
			if tc.wantEvent != 0 && (len(f.Payload) != 1 || f.Payload[0] != tc.wantEvent) {
				t.Errorf("event = %v, want [%d]", f.Payload, tc.wantEvent)
			}
			if tc.wantEvent == 0 && string(f.Payload) != tc.wantLoad {
				t.Errorf("payload = %q, want %q", f.Payload, tc.wantLoad)
			}
		})
	}
}

// EncodeRoutedSession must wrap the plain [type][payload] frames the session
// layer produces, so nothing below the router has to change.
func TestEncodeRoutedSessionWrapsPlainFrame(t *testing.T) {
	sid := utils.RandomString(terminal.SessionIDLen)
	frame, err := terminal.EncodeRoutedSession(sid, terminal.EncodeOutput([]byte("hi")))
	if err != nil {
		t.Fatalf("encode: %s", err)
	}
	got, err := terminal.DecodeRouted(frame)
	if err != nil {
		t.Fatalf("decode: %s", err)
	}
	if got.Type != terminal.Output || string(got.Payload) != "hi" {
		t.Errorf("got type %q payload %q, want Output/hi", got.Type, got.Payload)
	}
}

// ---------------------------------------------------------------------------
// end-to-end over one connection
// ---------------------------------------------------------------------------

func TestMultiplexedTwoSessionsShareOneConnection(t *testing.T) {
	ts, _, sink := newTestServer(t, nil)
	idA := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)
	idB := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)

	conn := dialMultiplexed(t, ts)
	attachAndWait(t, conn, idA)
	attachAndWait(t, conn, idB)

	termA, termB := sink.byIndex(0), sink.byIndex(1)
	if termA == nil || termB == nil {
		t.Fatalf("expected two stub terminals, sink has %d", sink.count())
	}

	// Input must land on the session it names.
	sendRouted(t, conn, idA, terminal.Input, []byte("AAA"))
	sendRouted(t, conn, idB, terminal.Input, []byte("BBB"))

	waitFor(t, func() bool { return strings.Contains(termA.writtenText(), "AAA") }, "session A to receive its input")
	waitFor(t, func() bool { return strings.Contains(termB.writtenText(), "BBB") }, "session B to receive its input")
	if strings.Contains(termA.writtenText(), "BBB") {
		t.Error("session A received input addressed to B")
	}
	if strings.Contains(termB.writtenText(), "AAA") {
		t.Error("session B received input addressed to A")
	}

	// Output must come back tagged with its own session id.
	if err := termA.PipeWrite([]byte("from-A")); err != nil {
		t.Fatalf("feed A: %s", err)
	}
	if err := termB.PipeWrite([]byte("from-B")); err != nil {
		t.Fatalf("feed B: %s", err)
	}

	seen := map[string]string{}
	for len(seen) < 2 {
		f := readRoutedUntil(t, conn, func(f terminal.RoutedFrame) bool {
			return f.Type == terminal.Output &&
				(strings.Contains(string(f.Payload), "from-A") || strings.Contains(string(f.Payload), "from-B"))
		}, "output from both sessions")
		if strings.Contains(string(f.Payload), "from-A") {
			seen["A"] = f.SessionID
		} else {
			seen["B"] = f.SessionID
		}
	}
	if seen["A"] != idA {
		t.Errorf("A's output arrived tagged %q, want %q", seen["A"], idA)
	}
	if seen["B"] != idB {
		t.Errorf("B's output arrived tagged %q, want %q", seen["B"], idB)
	}
}

func TestMultiplexedPreemptionNotifiesTheLoser(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)
	id := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)

	loser := dialMultiplexed(t, ts)
	attachAndWait(t, loser, id)

	winner := dialMultiplexed(t, ts)
	attachAndWait(t, winner, id)

	f := readRoutedUntil(t, loser, func(f terminal.RoutedFrame) bool {
		return f.SessionID == id && f.Type == terminal.SessionEvent
	}, "preemption event on the losing connection")
	if len(f.Payload) != 1 || f.Payload[0] != terminal.EventPreempted {
		t.Errorf("event status = %v, want [%d] (EventPreempted)", f.Payload, terminal.EventPreempted)
	}
}

func TestMultiplexedCloseReturnsEverySessionToIdle(t *testing.T) {
	ts, mgr, _ := newTestServer(t, nil)
	idA := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)
	idB := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)

	conn := dialMultiplexed(t, ts)
	attachAndWait(t, conn, idA)
	attachAndWait(t, conn, idB)
	waitFor(t, func() bool {
		return stateOf(t, mgr, idA) == session.StateRunning && stateOf(t, mgr, idB) == session.StateRunning
	}, "both sessions to be running")

	if err := conn.Close(websocket.StatusNormalClosure, "done"); err != nil {
		t.Fatalf("close: %s", err)
	}
	waitFor(t, func() bool {
		return stateOf(t, mgr, idA) == session.StateIdle && stateOf(t, mgr, idB) == session.StateIdle
	}, "both sessions to return to idle")
}

func TestMultiplexedAttachRejectsMissingAndDestroyedSessions(t *testing.T) {
	ts, mgr, _ := newTestServer(t, nil)
	gone := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)
	if err := mgr.Destroy(gone); err != nil {
		t.Fatalf("destroy: %s", err)
	}

	conn := dialMultiplexed(t, ts)

	// Never existed.
	sendRouted(t, conn, utils.RandomString(terminal.SessionIDLen), terminal.Attach, nil)
	// Existed, now destroyed.
	sendRouted(t, conn, gone, terminal.Attach, nil)

	for i := 0; i < 2; i++ {
		f := readRoutedUntil(t, conn, func(f terminal.RoutedFrame) bool {
			return f.Type == terminal.AttachFail
		}, "AttachFail")
		if len(f.Payload) == 0 {
			t.Error("AttachFail should carry a human-readable reason")
		}
	}
}

// A second attach for the same session on the same connection must not spin
// up a second bridge: it answers AttachOK and leaves the live one alone.
func TestMultiplexedAttachIsIdempotent(t *testing.T) {
	ts, mgr, sink := newTestServer(t, nil)
	id := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)

	conn := dialMultiplexed(t, ts)
	attachAndWait(t, conn, id)

	sendRouted(t, conn, id, terminal.Attach, nil)
	readRoutedUntil(t, conn, func(f terminal.RoutedFrame) bool {
		return f.SessionID == id && f.Type == terminal.AttachOK
	}, "a second AttachOK")

	// The original bridge must still be the one serving input.
	sendRouted(t, conn, id, terminal.Input, []byte("still-here"))
	term := sink.byIndex(0)
	waitFor(t, func() bool { return strings.Contains(term.writtenText(), "still-here") }, "input after a repeated attach")
	if stateOf(t, mgr, id) != session.StateRunning {
		t.Errorf("state = %v, want running", stateOf(t, mgr, id))
	}
	if sink.count() != 1 {
		t.Errorf("%d terminals created, want 1", sink.count())
	}
}

// Detach releases the channel the client asked to drop; the session itself
// keeps running on the server.
func TestMultiplexedDetachReleasesTheSession(t *testing.T) {
	ts, mgr, _ := newTestServer(t, nil)
	idA := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)
	idB := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)

	conn := dialMultiplexed(t, ts)
	attachAndWait(t, conn, idA)
	attachAndWait(t, conn, idB)

	sendRouted(t, conn, idA, terminal.Detach, nil)

	waitFor(t, func() bool { return stateOf(t, mgr, idA) == session.StateIdle }, "detached session to go idle")
	if stateOf(t, mgr, idB) != session.StateRunning {
		t.Error("detaching A must not disturb B on the same connection")
	}
	if stateOf(t, mgr, idA) == session.StateDestroyed {
		t.Error("detach must not destroy the session")
	}
}

func TestMultiplexedConnectionLevelPingPong(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)
	conn := dialMultiplexed(t, ts)

	sendConnLevel(t, conn, terminal.Ping, nil)

	f := readRoutedUntil(t, conn, func(f terminal.RoutedFrame) bool {
		return f.IsConnLevel() && f.Type == terminal.Pong
	}, "connection-level pong")
	if len(f.Payload) != 0 {
		t.Errorf("pong should be empty, got %v", f.Payload)
	}
}

// A session-level Ping must still be answered by the session bridge (the
// legacy behaviour), so both ping paths work during the transition.
func TestMultiplexedSessionLevelPingStillWorks(t *testing.T) {
	ts, _, _ := newTestServer(t, nil)
	id := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)

	conn := dialMultiplexed(t, ts)
	attachAndWait(t, conn, id)

	sendRouted(t, conn, id, terminal.Ping, nil)

	f := readRoutedUntil(t, conn, func(f terminal.RoutedFrame) bool {
		return f.SessionID == id && f.Type == terminal.Pong
	}, "session-level pong")
	if len(f.Payload) != 0 {
		t.Errorf("pong should be empty, got %v", f.Payload)
	}
}

// The whole point of the transition: both protocols answer on the same
// server, so an old bundle keeps working until it is rebuilt.
func TestLegacyAndMultiplexedCoexist(t *testing.T) {
	ts, _, sink := newTestServer(t, nil)
	legacyID := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)
	muxID := createSession(t, ts, `{"host_id":"h1"}`)["id"].(string)

	// Legacy: one socket bound to one session.
	legacyURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?session_id=" + legacyID + "&token=" + testToken
	legacy, _, err := websocket.Dial(context.Background(), legacyURL, nil)
	if err != nil {
		t.Fatalf("dial legacy ws: %s", err)
	}
	defer legacy.CloseNow()
	legacy.SetReadLimit(1 << 20)
	if err := legacy.Write(context.Background(), websocket.MessageBinary, []byte{terminal.Input, 'L'}); err != nil {
		t.Fatalf("legacy write: %s", err)
	}

	// Multiplexed: another socket carrying a different session.
	mux := dialMultiplexed(t, ts)
	attachAndWait(t, mux, muxID)
	sendRouted(t, mux, muxID, terminal.Input, []byte("M"))

	legacyTerm, muxTerm := sink.byIndex(0), sink.byIndex(1)
	waitFor(t, func() bool { return strings.Contains(legacyTerm.writtenText(), "L") }, "legacy input to arrive")
	waitFor(t, func() bool { return strings.Contains(muxTerm.writtenText(), "M") }, "multiplexed input to arrive")
}
