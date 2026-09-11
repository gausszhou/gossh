package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// listSessions fetches GET /api/sessions with the test token.
func listSessions(t *testing.T, ts *httptest.Server) (int, sessionListResponse) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list sessions: %s", err)
	}
	defer resp.Body.Close()

	var out sessionListResponse
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestListSessionsReturnsEveryAliveSession(t *testing.T) {
	ts, _, sink := newAgentTestServer(t, true, true)

	first := createAgentSession(t, ts, sink, "one")
	second := createAgentSession(t, ts, sink, "two")

	status, payload := listSessions(t, ts)
	if status != http.StatusOK {
		t.Fatalf("status %d, want 200", status)
	}
	if len(payload.Sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: %v", len(payload.Sessions), payload.Sessions)
	}

	seen := map[string]bool{}
	for _, s := range payload.Sessions {
		seen[s.ID] = true
	}
	if !seen[first] || !seen[second] {
		t.Errorf("listed %v, want both %s and %s", payload.Sessions, first, second)
	}

	// The list is sorted by (created_at, id) so scripts get stable output.
	for i := 1; i < len(payload.Sessions); i++ {
		prev, cur := payload.Sessions[i-1], payload.Sessions[i]
		if prev.CreatedAt > cur.CreatedAt {
			t.Errorf("sessions are not ordered by created_at: %s before %s", prev.CreatedAt, cur.CreatedAt)
		}
		if prev.CreatedAt == cur.CreatedAt && prev.ID > cur.ID {
			t.Errorf("ties are not broken by id: %s before %s", prev.ID, cur.ID)
		}
	}
}

func TestListSessionsEmptyIsAnEmptyArray(t *testing.T) {
	ts, _, _ := newAgentTestServer(t, true, true)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("list sessions: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}
	// Decoding the field raw proves the wire form is [] and not null.
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %s", err)
	}
	if got := string(raw["sessions"]); got != "[]" {
		t.Errorf(`sessions = %s, want "[]" (an empty listing must not be null)`, got)
	}
}

func TestListSessionsRequiresToken(t *testing.T) {
	ts, _, _ := newAgentTestServer(t, true, true)

	resp, err := http.Get(ts.URL + "/api/sessions")
	if err != nil {
		t.Fatalf("list sessions: %s", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status %d, want 401 without a token", resp.StatusCode)
	}
}
