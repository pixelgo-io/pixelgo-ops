package proxy

// Reading the proxy's log.
//
// The README argues hardest for this of all the commands: "the refusals are the
// interesting part... they show you where the agent's reasoning went". A
// refused request is the agent telling you what it thought it needed, which is
// not visible anywhere else - not in its output, where it reports what it did,
// and not in its journal, where it reports what it meant to do.
//
// The formats below were taken from a tinyproxy 1.11.1 log rather than from
// documentation. Two lines matter out of five kinds:
//
//	CONNECT   Nov 18 11:12:52.185 [283]: Request (file descriptor 5): GET http://github.com/ HTTP/1.1
//	CONNECT   Nov 18 11:13:26.019 [314]: Request (file descriptor 5): CONNECT github.com:443 HTTP/1.1
//	NOTICE    Nov 18 11:13:12.230 [314]: Proxying refused on filtered domain "evil-github.community"
//	NOTICE    Nov 18 11:12:52.213 [283]: Proxying refused on filtered url "http://example.net/"
//
// The last two are the same event worded differently: tinyproxy says "domain"
// when it matched on the host and "url" when it matched on the whole URL. The
// generated configuration no longer asks for URL matching, but the log is
// append-only and outlives a configuration change, so both are read.

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Entry is one request the proxy saw.
type Entry struct {
	// Time as logged. Kept as a string on purpose: tinyproxy writes no year,
	// so parsing it into a timestamp would mean supplying one - correct for
	// most of the year and wrong for the entries either side of New Year, in a
	// way nobody would catch. The log is read to be read.
	Time string

	Host   string // the host asked for, without a port
	Method string // GET, CONNECT, ...; empty on a refusal, which logs no method

	// Refused is the filter's decision, not the outcome. A request the filter
	// permitted may still have failed to connect - that is a different line in
	// the log and a different problem.
	Refused bool

	Pid int    // the tinyproxy child that handled it; used to join lines
	Raw string // the line, for anything this does not model
}

// ReadTraffic returns the entries in the proxy's log, oldest first.
//
// A missing log is an empty list and not an error: before the proxy has run
// there is nothing to read, and that is a legitimate answer rather than a
// fault.
func ReadTraffic(path string, deniedOnly bool) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var out []Entry
	s := bufio.NewScanner(f)

	// A logged URL can be long - a query string with a payload in it - and the
	// default scanner buffer is 64KB. Raised so one long line does not end the
	// read with an error that looks like a corrupt file.
	s.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for s.Scan() {
		e, ok := parseLine(s.Text())
		if !ok {
			continue
		}

		/*
		 * One request, one entry.
		 *
		 * tinyproxy writes a refused request as TWO lines: the Request first,
		 * then the refusal, from the same child and at the same moment:
		 *
		 *	CONNECT  ... [1236]: Request (file descriptor 5): GET http://example.test/ HTTP/1.1
		 *	NOTICE   ... [1236]: Proxying refused on filtered domain "example.test"
		 *
		 * Reported separately, a refused host appears twice - once as allowed
		 * and once as refused - and someone scanning the output concludes it
		 * got through. So a refusal folds into the request it refers to, which
		 * also keeps the method: a refused CONNECT says something a refused GET
		 * does not.
		 *
		 * Matched on the pid AND the host rather than adjacency alone, because
		 * tinyproxy reuses child pids across requests.
		 */
		if e.Refused && len(out) > 0 {
			prev := &out[len(out)-1]
			if !prev.Refused && prev.Pid == e.Pid && prev.Host == e.Host {
				prev.Refused = true
				prev.Raw = e.Raw
				continue
			}
		}

		out = append(out, e)
	}
	if s.Err() != nil {
		return nil, s.Err()
	}

	// Filtered after the join, not during: a refusal needs the request before
	// it to fold into, and dropping the requests first would leave nothing to
	// fold.
	if deniedOnly {
		var denied []Entry
		for _, e := range out {
			if e.Refused {
				denied = append(denied, e)
			}
		}
		return denied, nil
	}
	return out, nil
}

// parseLine recognises the two kinds of line worth reporting.
func parseLine(line string) (Entry, bool) {
	// The timestamp sits between the level and the pid: "LEVEL  <time> [pid]:".
	// Taken by position between those two markers rather than by a fixed width,
	// because the level names differ in length.
	ts := ""
	pid := 0
	if br := strings.Index(line, " ["); br > 0 {
		fields := strings.Fields(line[:br])
		if len(fields) > 1 {
			ts = strings.Join(fields[1:], " ")
		}
		if end := strings.Index(line[br:], "]"); end > 0 {
			pid, _ = strconv.Atoi(line[br+2 : br+end])
		}
	}

	// A refusal. The host or URL is the quoted value at the end.
	if i := strings.Index(line, "Proxying refused on filtered "); i >= 0 {
		q := line[i:]
		start := strings.Index(q, `"`)
		end := strings.LastIndex(q, `"`)
		if start < 0 || end <= start {
			return Entry{}, false
		}
		target := q[start+1 : end]

		return Entry{
			Time:    ts,
			Host:    hostOf(target),
			Refused: true,
			Pid:     pid,
			Raw:     line,
		}, true
	}

	// A request that got past the filter.
	const marker = "Request (file descriptor "
	if i := strings.Index(line, marker); i >= 0 {
		rest := line[i+len(marker):]
		// Skip "N): "
		if j := strings.Index(rest, "): "); j >= 0 {
			rest = rest[j+3:]
		}
		fields := strings.Fields(rest)
		if len(fields) < 2 {
			return Entry{}, false
		}
		return Entry{
			Time:   ts,
			Host:   hostOf(fields[1]),
			Method: fields[0],
			Pid:    pid,
			Raw:    line,
		}, true
	}

	return Entry{}, false
}

// hostOf reduces a request target to a bare host.
//
// Three shapes reach this: a full URL from a plain HTTP request, a host:port
// from a CONNECT, and a bare host from a refusal that matched on the domain.
func hostOf(target string) string {
	t := strings.TrimSpace(target)

	if strings.Contains(t, "://") {
		if u, err := url.Parse(t); err == nil && u.Host != "" {
			return stripPort(u.Host)
		}
	}
	return stripPort(t)
}

func stripPort(h string) string {
	// Rightmost colon, and only if what follows is a port rather than part of
	// an IPv6 address.
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i+1:], ":") {
		if _, err := fmt.Sscanf(h[i+1:], "%d", new(int)); err == nil {
			return h[:i]
		}
	}
	return h
}
