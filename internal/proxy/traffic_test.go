package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

// tinyproxy writes a refused request as two lines - the Request, then the
// refusal - from the same child at the same moment. Reported separately, a
// refused host appears twice, once as allowed and once as refused, and someone
// scanning the output concludes it got through. This is the regression test for
// that: the lines below are copied verbatim from a log the real binary wrote.
func TestARefusedRequestIsOneEntryNotTwo(t *testing.T) {
	log := `CONNECT   Nov 18 11:24:19.507 [1236]: Connect (file descriptor 5): 127.0.0.1
CONNECT   Nov 18 11:24:19.507 [1236]: Request (file descriptor 5): GET http://example.test/ HTTP/1.1
NOTICE    Nov 18 11:24:19.507 [1236]: Proxying refused on filtered domain "example.test"
CONNECT   Nov 18 11:24:19.600 [1237]: Request (file descriptor 5): CONNECT github.invalid:443 HTTP/1.1
`
	path := filepath.Join(t.TempDir(), "traffic")
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	all, err := ReadTraffic(path, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("expected two requests, got %d: %+v", len(all), all)
	}

	if all[0].Host != "example.test" || !all[0].Refused {
		t.Errorf("the refused request is wrong: %+v", all[0])
	}
	// The method survives the fold: a refused CONNECT says something a refused
	// GET does not.
	if all[0].Method != "GET" {
		t.Errorf("the method was lost in the fold: %+v", all[0])
	}
	if all[1].Host != "github.invalid" || all[1].Refused {
		t.Errorf("the permitted request is wrong: %+v", all[1])
	}
	if all[1].Method != "CONNECT" {
		t.Errorf("CONNECT was not read: %+v", all[1])
	}

	denied, err := ReadTraffic(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(denied) != 1 || denied[0].Host != "example.test" {
		t.Errorf("--denied is wrong: %+v", denied)
	}
}

// tinyproxy words the same event differently depending on what it matched
// against: "domain" for the host, "url" for the whole URL. The generated
// configuration no longer asks for URL matching, but the log is append-only and
// outlives a configuration change, so both have to be read.
func TestBothRefusalWordingsAreRead(t *testing.T) {
	log := `NOTICE    Nov 18 11:12:52.213 [283]: Proxying refused on filtered url "http://example.net/"
NOTICE    Nov 18 11:13:12.230 [314]: Proxying refused on filtered domain "evil-github.community"
`
	path := filepath.Join(t.TempDir(), "traffic")
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ReadTraffic(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two refusals, got %d: %+v", len(got), got)
	}
	// The URL form has to come out as a bare host, like the domain form, or the
	// output cannot be grouped.
	if got[0].Host != "example.net" {
		t.Errorf("the url form was not reduced to a host: %q", got[0].Host)
	}
	if got[1].Host != "evil-github.community" {
		t.Errorf("the domain form is wrong: %q", got[1].Host)
	}
}

// A missing log is the state before the proxy has ever run, which is an answer
// rather than a fault.
func TestReadTrafficMissingLogIsEmpty(t *testing.T) {
	got, err := ReadTraffic(filepath.Join(t.TempDir(), "nope"), false)
	if err != nil {
		t.Fatalf("a missing log should not be an error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}
