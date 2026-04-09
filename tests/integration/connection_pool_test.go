package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"noitsdav/internal/config"
	"noitsdav/internal/observability"
	"noitsdav/internal/server"
	"noitsdav/tests/testutil"
)

func TestPerMountConnectionPooling(t *testing.T) {
	t.Parallel()

	pooled := testutil.NewFakeFTPServer("ftpuser1", "ftppass1", map[string]testutil.Node{
		"/":        {IsDir: true},
		"/media":   {IsDir: true},
		"/media/a": {Data: []byte("a"), ModTime: time.Unix(100, 0).UTC()},
	})
	defer pooled.Close()

	unpooled := testutil.NewFakeFTPServer("ftpuser2", "ftppass2", map[string]testutil.Node{
		"/":          {IsDir: true},
		"/library":   {IsDir: true},
		"/library/b": {Data: []byte("b"), ModTime: time.Unix(100, 0).UTC()},
	})
	defer unpooled.Close()

	cfg := &config.Config{
		ListenAddress: ":0",
		Auth:          config.AuthConfig{Username: "davuser", Password: "davpass", Realm: "noitsdav"},
		Mounts: []config.MountConfig{
			{Name: "media", Host: "127.0.0.1", Port: testutil.FTPPort(pooled.Addr()), Username: "ftpuser1", Password: "ftppass1", RootPath: "/media", ConnectionPool: 1},
			{Name: "library", Host: "127.0.0.1", Port: testutil.FTPPort(unpooled.Addr()), Username: "ftpuser2", Password: "ftppass2", RootPath: "/library", ConnectionPool: 0},
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
	defer ts.Close()

	for _, target := range []string{"/media/a", "/library/b", "/media/a", "/library/b"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+target, nil)
		req.SetBasicAuth("davuser", "davpass")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("unexpected status for %s: %d", target, resp.StatusCode)
		}
	}

	if got := pooled.TotalConnections(); got != 1 {
		t.Fatalf("expected pooled mount to reuse startup connection, got %d connections", got)
	}
	if got := unpooled.TotalConnections(); got != 5 {
		t.Fatalf("expected unpooled mount to reconnect on each access, got %d connections", got)
	}
}
