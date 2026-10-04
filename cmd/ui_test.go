package cmd

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseServeFlagsDefaults(t *testing.T) {
	o, err := parseServeFlags(nil)
	if err != nil {
		t.Fatalf("parseServeFlags: %v", err)
	}
	if o.addr != "127.0.0.1:8080" {
		t.Errorf("addr = %q", o.addr)
	}
	if len(o.dirs) != 1 || o.dirs[0] != "." {
		t.Errorf("dirs = %v, want [.]", o.dirs)
	}
}

func TestParseServeFlagsRepeatableDirAndAddr(t *testing.T) {
	o, err := parseServeFlags([]string{"--addr", "0.0.0.0:9000", "--dir", "/a", "--dir", "/b"})
	if err != nil {
		t.Fatalf("parseServeFlags: %v", err)
	}
	if o.addr != "0.0.0.0:9000" {
		t.Errorf("addr = %q", o.addr)
	}
	if len(o.dirs) != 2 || o.dirs[0] != "/a" || o.dirs[1] != "/b" {
		t.Errorf("dirs = %v", o.dirs)
	}
}

func TestParseServeFlagsErrors(t *testing.T) {
	if _, err := parseServeFlags([]string{"--addr"}); err == nil {
		t.Error("expected missing-value error")
	}
	if _, err := parseServeFlags([]string{"--dir"}); err == nil {
		t.Error("expected missing-value error")
	}
	if _, err := parseServeFlags([]string{"--bogus"}); err == nil {
		t.Error("expected unknown-flag error")
	}
}

func TestServeHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute([]string{"serve", "--help"}, strings.NewReader(""), &out, &errOut)
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	if !strings.Contains(out.String(), "setu serve") {
		t.Errorf("help missing usage: %q", out.String())
	}
}

func TestServeListenFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	// 256.256.256.256 is not a valid address: listen must fail with exit 1.
	code := Execute([]string{"serve", "--addr", "256.256.256.256:80"}, strings.NewReader(""), &out, &errOut)
	if code != 1 {
		t.Errorf("exit code = %d, want 1 (output: %s)", code, errOut.String())
	}
	if !strings.Contains(errOut.String(), "setu serve:") {
		t.Errorf("stderr = %q", errOut.String())
	}
}

func TestServeBadFlag(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Execute([]string{"serve", "--frobnicate"}, strings.NewReader(""), &out, &errOut)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
}

// syncWriter lets the test read runServe's startup output while the server
// goroutine keeps writing.
type syncWriter struct {
	mu sync.Mutex
	sb strings.Builder
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sb.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sb.String()
}

// TestRunServeServesKnowledgeBase starts the real server on an ephemeral
// port and fetches the API end-to-end.
func TestRunServeServesKnowledgeBase(t *testing.T) {
	root := t.TempDir()
	ctx := filepath.Join(root, ".ai-context", "sessions")
	if err := os.MkdirAll(ctx, 0o755); err != nil {
		t.Fatal(err)
	}
	log := "[2026-10-04T10:00:00Z] session start: PROJ-7 (Serve me)\n" +
		"[2026-10-04T10:05:00Z] session end: harness=claude-code duration=1s status=ok\n"
	if err := os.WriteFile(filepath.Join(ctx, "2026-10-04_PROJ-7.log"), []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}

	out := &syncWriter{}
	go func() {
		_ = runServe([]string{"--addr", "127.0.0.1:0", "--dir", root}, out, io.Discard)
	}()

	addrRe := regexp.MustCompile(`http://([^\s]+)`)
	var url string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := addrRe.FindStringSubmatch(out.String()); m != nil {
			url = "http://" + m[1]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if url == "" {
		t.Fatalf("server never announced its address; output: %s", out.String())
	}

	resp, err := http.Get(url + "/api/kb")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "PROJ-7") {
		t.Errorf("api payload missing session: %s", body)
	}
	if !strings.Contains(out.String(), "1 run(s)") {
		t.Errorf("startup summary missing: %s", out.String())
	}
}
