package integration

import (
	"context"
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

func TestDegradedStartupAndMountBrowsing(t *testing.T) {
	t.Parallel()

	ftpA := testutil.NewFakeFTPServer("ftpuser1", "ftppass1", map[string]testutil.Node{
		"/":                   {IsDir: true},
		"/folder":             {IsDir: true},
		"/folder/example.bin": {Data: []byte("hello world"), ModTime: time.Unix(100, 0).UTC()},
	})
	defer ftpA.Close()

	cfg := &config.Config{
		ListenAddress: ":0",
		Auth:          config.AuthConfig{Username: "davuser", Password: "davpass", Realm: "noitsdav"},
		Mounts: []config.MountConfig{
			{Name: "media", Host: "127.0.0.1", Port: testutil.FTPPort(ftpA.Addr()), Username: "ftpuser1", Password: "ftppass1", RootPath: "/"},
			{Name: "offline", Host: "127.0.0.1", Port: 65099, Username: "x", Password: "x", RootPath: "/"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	app, err := server.NewApp(context.Background(), cfg, observability.New("error"))
	if err != nil {
		t.Fatalf("startup failed: %v", err)
	}

	ts := httptest.NewServer(app.Handler)
	defer ts.Close()

	req, _ := http.NewRequest("PROPFIND", ts.URL+"/", nil)
	req.SetBasicAuth("davuser", "davpass")
	req.Header.Set("Depth", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("expected 207 got %d", resp.StatusCode)
	}
	body := string(testutil.ReadAll(resp.Body))
	if !strings.Contains(body, "/media/") || !strings.Contains(body, "/offline/") {
		t.Fatalf("root listing missing mounts: %s", body)
	}

	req2, _ := http.NewRequest("PROPFIND", ts.URL+"/offline/", nil)
	req2.SetBasicAuth("davuser", "davpass")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 for offline mount, got %d", resp2.StatusCode)
	}
}

