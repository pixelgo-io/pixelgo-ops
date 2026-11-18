package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A real HTTP server on a real unix socket, so these tests exercise the
// transport rather than a stand-in for it. A fake that answered without a
// socket would pass while the dialer was wrong, which is the one thing in this
// package that has nowhere else to be caught.
func serve(t *testing.T, h http.Handler) *Client {
	t.Helper()

	// Short directory: a unix socket path has to fit in sun_path, around 108
	// bytes, and a temp dir named after a long test function does not always.
	dir, err := os.MkdirTemp("", "eng")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sock := filepath.Join(dir, "e.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := &httptest.Server{Listener: ln, Config: &http.Server{Handler: h}}
	srv.Start()
	t.Cleanup(srv.Close)

	return NewUnix(sock)
}

func routes(m map[string]string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := m[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"error":"not found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		fmt.Fprint(w, body)
	})
}

func healthBody(contract int) string {
	return fmt.Sprintf(`{"status":"ok","version":"20.2","contract":%d}`, contract)
}

// --- the ordinary path ------------------------------------------------

func TestHealthReadyAndLogs(t *testing.T) {
	c := serve(t, routes(map[string]string{
		"/healthz":  healthBody(ContractVersion),
		"/readyz":   `{"ready":true,"reason":""}`,
		"/api/logs": `{"lines":["one","two"],"truncated":true}`,
	}))
	ctx := context.Background()

	h, err := c.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if h.Status != "ok" || h.Version != "20.2" || h.Contract != ContractVersion {
		t.Errorf("health decoded wrong: %+v", h)
	}

	r, err := c.Ready(ctx)
	if err != nil {
		t.Fatalf("ready: %v", err)
	}
	if !r.Ready || r.Reason != "" {
		t.Errorf("ready decoded wrong: %+v", r)
	}

	l, err := c.Logs(ctx, 2)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if len(l.Lines) != 2 || l.Lines[0] != "one" || !l.Truncated {
		t.Errorf("logs decoded wrong: %+v", l)
	}
}

// Not ready is not an error. An engine that is draining, or that cannot write
// where it records work, answers correctly and says no - and a client that
// treated that as a failure would report the wrong thing about a working engine.
func TestNotReadyIsNotAnError(t *testing.T) {
	c := serve(t, routes(map[string]string{
		"/readyz": `{"ready":false,"reason":"shutting down: draining the running jobs"}`,
	}))

	r, err := c.Ready(context.Background())
	if err != nil {
		t.Fatalf("a not-ready answer must not be an error: %v", err)
	}
	if r.Ready {
		t.Error("ready should be false")
	}
	if !strings.Contains(r.Reason, "draining") {
		t.Errorf("the reason was lost: %q", r.Reason)
	}
}

// --- the contract check -----------------------------------------------

// The reason this package exists rather than three calls to net/http. ops
// serves its own copy of pixelgo's UI, so an engine that disagrees about the
// shape of the API has to be refused before anything is served, not discovered
// later on a field that moved.
func TestConnectRefusesAMismatchedContract(t *testing.T) {
	c := serve(t, routes(map[string]string{
		"/healthz": healthBody(ContractVersion + 7),
	}))

	h, err := c.Connect(context.Background())
	if !errors.Is(err, ErrContract) {
		t.Fatalf("expected ErrContract, got %v", err)
	}

	// Both numbers in the message: knowing only that they differ leaves the
	// person with nothing to do about it.
	msg := err.Error()
	for _, want := range []string{
		fmt.Sprint(ContractVersion + 7),
		fmt.Sprint(ContractVersion),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message does not name contract %s: %q", want, msg)
		}
	}
	if h.Contract != ContractVersion+7 {
		t.Errorf("the health reply should still be returned: %+v", h)
	}
}

func TestConnectAcceptsTheMatchingContract(t *testing.T) {
	c := serve(t, routes(map[string]string{
		"/healthz": healthBody(ContractVersion),
	}))

	if _, err := c.Connect(context.Background()); err != nil {
		t.Fatalf("a matching contract must connect: %v", err)
	}
}

// --- the failures that get their own type -----------------------------

func TestUnreachableNamesThePath(t *testing.T) {
	c := NewUnix("/tmp/pixelgo-ops-no-such-socket-12345.sock")

	_, err := c.Connect(context.Background())
	if !errors.Is(err, ErrUnreachable) {
		t.Fatalf("expected ErrUnreachable, got %v", err)
	}
	// A wrong path is the most common cause and the least visible one, so it
	// has to be in the message rather than left for the person to guess.
	if !strings.Contains(err.Error(), "no-such-socket-12345") {
		t.Errorf("the message does not name the socket: %q", err.Error())
	}
}

// A socket file that exists but has nothing behind it is the state a crashed
// engine leaves, and it must read as unreachable rather than as something odd.
func TestStaleSocketIsUnreachable(t *testing.T) {
	dir, err := os.MkdirTemp("", "eng")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	defer os.RemoveAll(dir)

	sock := filepath.Join(dir, "e.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln.Close() // the file stays, nothing is listening

	c := NewUnix(sock)
	if _, err := c.Connect(context.Background()); !errors.Is(err, ErrUnreachable) {
		t.Fatalf("expected ErrUnreachable, got %v", err)
	}
}

func TestHTTPErrorCarriesTheEngineMessage(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"lines must be a positive integer"}`)
	}))

	_, err := c.Logs(context.Background(), 5)
	if err == nil {
		t.Fatal("a 400 must be an error")
	}
	if !strings.Contains(err.Error(), "lines must be a positive integer") {
		t.Errorf("the engine's own message was dropped: %q", err.Error())
	}
	// And it must not be mistaken for a connection problem: the engine answered.
	if errors.Is(err, ErrUnreachable) {
		t.Error("a 400 is not unreachable")
	}
}

func TestNonJSONIsReported(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>a proxy sat in the way</html>")
	}))

	_, err := c.Health(context.Background())
	if err == nil {
		t.Fatal("a non-JSON 200 must be an error")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("the message should say what was wrong: %q", err.Error())
	}
}

// --- the lines parameter ----------------------------------------------

func TestLogLinesIsPassedAndClamped(t *testing.T) {
	var seen string
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Query().Get("lines")
		fmt.Fprint(w, `{"lines":[],"truncated":false}`)
	}))
	ctx := context.Background()

	if _, err := c.Logs(ctx, 50); err != nil {
		t.Fatalf("logs: %v", err)
	}
	if seen != "50" {
		t.Errorf("lines was not passed through: %q", seen)
	}

	// Zero means the engine's default, so the parameter is left off entirely
	// rather than sent as 0 - which the engine refuses as a 400.
	if _, err := c.Logs(ctx, 0); err != nil {
		t.Fatalf("logs: %v", err)
	}
	if seen != "" {
		t.Errorf("zero should send no parameter, sent %q", seen)
	}

	// Clamped here as well as there: a client that relies on the server being
	// forgiving breaks when it stops being.
	if _, err := c.Logs(ctx, MaxLogLines*10); err != nil {
		t.Fatalf("logs: %v", err)
	}
	if seen != fmt.Sprint(MaxLogLines) {
		t.Errorf("expected clamping to %d, sent %q", MaxLogLines, seen)
	}
}

// An enormous body must not be read without bound, whatever the engine does.
// pixelgo caps its own responses, but a client whose memory safety depends on
// the server's restraint has none of its own.
func TestResponseIsBounded(t *testing.T) {
	c := serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"lines":["`))
		chunk := strings.Repeat("A", 64*1024)
		for i := 0; i < 64; i++ { // 4MB, past the 1MB limit
			w.Write([]byte(chunk))
		}
		w.Write([]byte(`"],"truncated":false}`))
	}))

	// The read stops at the limit, so the JSON is cut and decoding fails. That
	// is the correct outcome: an error, not four megabytes held in memory.
	if _, err := c.Logs(context.Background(), 1); err == nil {
		t.Error("an over-size body should not decode successfully")
	}
}
