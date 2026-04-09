package contract

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"noitsdav/internal/config"
	"noitsdav/internal/observability"
	"noitsdav/internal/server"
	"noitsdav/tests/testutil"
)

func newTestServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	ftpA := testutil.NewFakeFTPServer("ftpuser", "ftppass", map[string]testutil.Node{
		"/":              {IsDir: true},
		"/media":         {IsDir: true},
		"/media/one.bin": {Data: []byte("0123456789"), ModTime: time.Unix(200, 0).UTC()},
	})
	cfg := &config.Config{
		ListenAddress: ":0",
		Auth:          config.AuthConfig{Username: "davuser", Password: "davpass", Realm: "noitsdav"},
		Mounts: []config.MountConfig{
			{Name: "media", Host: "127.0.0.1", Port: testutil.FTPPort(ftpA.Addr()), Username: "ftpuser", Password: "ftppass", RootPath: "/media"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	app, err := server.NewApp(context.Background(), cfg, observability.New("error"))
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(app.Handler)
	return ts, func() {
		ts.Close()
		_ = ftpA.Close()
	}
}

func TestWebDAVContract(t *testing.T) {
	t.Parallel()
	ts, cleanup := newTestServer(t)
	defer cleanup()

	t.Run("rejects unauthenticated requests", func(t *testing.T) {
		req, _ := http.NewRequest("PROPFIND", ts.URL+"/", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("expected 401 got %d", resp.StatusCode)
		}
	})

	t.Run("options advertises read-only methods", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodOptions, ts.URL+"/", nil)
		req.SetBasicAuth("davuser", "davpass")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if allow := resp.Header.Get("Allow"); !strings.Contains(allow, "PROPFIND") || strings.Contains(allow, "PUT") {
			t.Fatalf("unexpected allow header %q", allow)
		}
	})

	t.Run("propfind root and mount", func(t *testing.T) {
		req, _ := http.NewRequest("PROPFIND", ts.URL+"/", nil)
		req.SetBasicAuth("davuser", "davpass")
		req.Header.Set("Depth", "1")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusMultiStatus || !strings.Contains(string(body), "/media/") {
			t.Fatalf("unexpected response: %d %s", resp.StatusCode, body)
		}

		req2, _ := http.NewRequest("PROPFIND", ts.URL+"/media/", nil)
		req2.SetBasicAuth("davuser", "davpass")
		req2.Header.Set("Depth", "1")
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatal(err)
		}
		defer resp2.Body.Close()
		body2, _ := io.ReadAll(resp2.Body)
		if resp2.StatusCode != http.StatusMultiStatus {
			t.Fatalf("unexpected mount status: %d %s", resp2.StatusCode, body2)
		}
		if !strings.Contains(string(body2), "/media/one.bin") {
			t.Fatalf("expected mounted file in response, got %s", body2)
		}
		if strings.Contains(string(body2), "/offline/") {
			t.Fatalf("mount listing should not contain unrelated virtual mounts: %s", body2)
		}
	})

	t.Run("head and full get", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodHead, ts.URL+"/media/one.bin", nil)
		req.SetBasicAuth("davuser", "davpass")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if got := resp.Header.Get("Content-Length"); got != "10" {
			t.Fatalf("expected content-length 10 got %q", got)
		}

		req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/media/one.bin", nil)
		req2.SetBasicAuth("davuser", "davpass")
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatal(err)
		}
		defer resp2.Body.Close()
		body, _ := io.ReadAll(resp2.Body)
		if string(body) != "0123456789" {
			t.Fatalf("unexpected body %q", body)
		}
	})

	t.Run("write rejected", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPut, ts.URL+"/media/one.bin", strings.NewReader("x"))
		req.SetBasicAuth("davuser", "davpass")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("expected 405 got %d", resp.StatusCode)
		}
	})

	t.Run("range and unsatisfiable range", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/media/one.bin", nil)
		req.SetBasicAuth("davuser", "davpass")
		req.Header.Set("Range", "bytes=2-5")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusPartialContent || string(body) != "2345" {
			t.Fatalf("unexpected partial response: %d %q", resp.StatusCode, body)
		}

		req2, _ := http.NewRequest(http.MethodGet, ts.URL+"/media/one.bin", nil)
		req2.SetBasicAuth("davuser", "davpass")
		req2.Header.Set("Range", "bytes=20-30")
		resp2, err := http.DefaultClient.Do(req2)
		if err != nil {
			t.Fatal(err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("expected 416 got %d", resp2.StatusCode)
		}
	})
}
