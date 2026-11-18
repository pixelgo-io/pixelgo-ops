// Package engine talks to pixelgo's HTTP API.
//
// pixelgo runs inside the VM and ops runs on the host, so everything ops knows
// about the agent it learns through this package. The decided channel is vsock;
// a unix socket is the same listener with a different address family, and it
// comes first because it is a file - which means ops can be developed and
// tested against a real pixelgo with no VM, no hypervisor and no network.
//
// The transport is behind one function, Client.dial. Nothing above it knows
// whether it is speaking over a socket file or a vsock port, which is the whole
// point: when vsock lands, this is the only place that changes.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/pixelgo-io/pixelgo-ops/internal/config"
)

// ContractVersion is the version of pixelgo's API contract this package is
// written against - WEB_API_CONTRACT_VERSION in pixelgo's include/web_api.h,
// documented in its docs/API.md.
//
// It is checked rather than assumed. ops serves its own copy of pixelgo's web
// UI, so a mismatch is not a detail that shows up later on a field that moved:
// it is a pair of programs that disagree about the shape of everything between
// them, and the useful moment to say so is before anything is served.
const ContractVersion = 1

// DefaultTimeout bounds a request. Short, because every route this package
// calls answers from memory or from the end of a file; a slow answer means the
// engine is in trouble, and waiting longer does not produce a better one.
const DefaultTimeout = 10 * time.Second

// MaxLogLines is pixelgo's documented maximum for GET /api/logs. Asking for
// more is clamped there; clamping here too means the client does not rely on
// the server to be forgiving.
const MaxLogLines = 1000

// Errors callers are expected to distinguish. "Unreachable" and "mismatched"
// lead to different things being printed and different advice.
var (
	// ErrUnreachable means nothing answered. Usually pixelgo is not running,
	// or is running without PIXELGO_UNIX_SOCKET, or points at another path.
	ErrUnreachable = errors.New("engine is unreachable")

	// ErrContract means the engine speaks a different contract version.
	ErrContract = errors.New("engine speaks a different API contract")
)

// Client is a connection to one pixelgo instance.
type Client struct {
	addr string // for messages: the socket path, later the vsock address
	http *http.Client
}

// Health is GET /healthz.
type Health struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Contract int    `json:"contract"`
}

// Ready is GET /readyz.
//
// Ready is false while the engine is draining, and when it cannot record work -
// a jobs directory it cannot write to is an engine that is healthy, reachable
// and unable to do the only thing it is for. Reason says which.
type Ready struct {
	Ready  bool   `json:"ready"`
	Reason string `json:"reason"`
}

// Logs is GET /api/logs.
//
// Truncated is true when what came back is not the whole tail that was asked
// for. It is not an error and it is not rare: the engine bounds the response in
// bytes as well as in lines, because one logged tool result can be larger than
// a thousand ordinary lines.
type Logs struct {
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
}

// NewUnix returns a client speaking over a unix socket.
func NewUnix(path string) *Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", path)
	}
	return &Client{
		addr: path,
		http: &http.Client{
			Timeout:   DefaultTimeout,
			Transport: &http.Transport{DialContext: dial},
		},
	}
}

// New returns a client for the engine the configuration points at.
func New(cfg *config.Config) *Client {
	return NewUnix(cfg.EngineSocketPath())
}

// Addr is where this client is pointed, for messages and for "status".
func (c *Client) Addr() string { return c.addr }

// get fetches a route and decodes it into out.
//
// The URL's host is a placeholder: over a unix socket there is no authority to
// name, the dialer ignores it, and pixelgo does not apply its Host and Origin
// check on that channel - that check defends browsers, and no browser can open
// a unix socket. Something has to go there for the URL to be well formed.
func (c *Client) get(ctx context.Context, path string, query url.Values, out any) error {
	u := "http://pixelgo" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		// Every reason this fails - no socket file, nothing listening, a
		// timeout - is the same thing to the person asking: the engine did not
		// answer. The path is in the message because a wrong path is the most
		// common cause and the least obvious one.
		//
		// The cause is unwrapped rather than printed as it arrives, because
		// net/http wraps it in the request URL - and that URL contains the
		// placeholder authority this package invents for a channel that has
		// none. Printed raw, it reads as a hostname that failed to resolve,
		// which sends the reader looking at DNS for a missing file.
		return fmt.Errorf("%w at %s: %v", ErrUnreachable, c.addr, rootCause(err))
	}
	defer resp.Body.Close()

	// Bounded: pixelgo caps its own responses, but a client that trusts a
	// server's restraint for its memory safety is one bug away from not being
	// able to. The limit is above pixelgo's largest declared cap.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("reading the response from %s: %w", c.addr, err)
	}

	if resp.StatusCode != http.StatusOK {
		// pixelgo's errors are {"error": "..."}; fall back to the status line
		// when the body is not that shape, so a proxy or a wrong path cannot
		// produce an empty complaint.
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &e) == nil && e.Error != "" {
			return fmt.Errorf("%s: %s (HTTP %d)", path, e.Error, resp.StatusCode)
		}
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}

	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%s returned something that is not the expected JSON: %w", path, err)
	}
	return nil
}

// rootCause unwraps to the innermost error, which for a failed dial is the
// syscall reason: "no such file or directory", "connection refused", and so on.
func rootCause(err error) error {
	for {
		inner := errors.Unwrap(err)
		if inner == nil {
			return err
		}
		err = inner
	}
}

// Health asks whether the engine is alive and what it is.
func (c *Client) Health(ctx context.Context) (Health, error) {
	var h Health
	err := c.get(ctx, "/healthz", nil, &h)
	return h, err
}

// Ready asks whether the engine takes new work.
func (c *Client) Ready(ctx context.Context) (Ready, error) {
	var r Ready
	err := c.get(ctx, "/readyz", nil, &r)
	return r, err
}

// Logs fetches the end of the engine's log, oldest line first.
//
// lines of zero or less means the engine's default.
func (c *Client) Logs(ctx context.Context, lines int) (Logs, error) {
	q := url.Values{}
	if lines > 0 {
		if lines > MaxLogLines {
			lines = MaxLogLines
		}
		q.Set("lines", strconv.Itoa(lines))
	}
	var l Logs
	err := c.get(ctx, "/api/logs", q, &l)
	return l, err
}

// Connect reaches the engine and refuses a contract it does not speak.
//
// Every command that reads from the engine goes through this rather than
// calling Health itself, so that the version check cannot be forgotten in one
// place and present in the others.
func (c *Client) Connect(ctx context.Context) (Health, error) {
	h, err := c.Health(ctx)
	if err != nil {
		return h, err
	}
	if h.Contract != ContractVersion {
		return h, fmt.Errorf(
			"%w: pixelgo %s speaks contract %d, this pixelgo-ops was built for %d",
			ErrContract, h.Version, h.Contract, ContractVersion)
	}
	return h, nil
}
