package ftpfs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/textproto"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"noitsdav/internal/config"
)

var (
	ErrNotFound           = errors.New("not found")
	ErrUnavailable        = errors.New("backend unavailable")
	ErrReadOnly           = errors.New("read-only")
	ErrInvalidRange       = errors.New("invalid range")
	ErrUnsatisfiableRange = errors.New("unsatisfiable range")
	ErrNotAFile           = errors.New("not a file")
	ErrNotADirectory      = errors.New("not a directory")
)

type Client struct {
	mount  config.MountConfig
	logger *slog.Logger
	pool   *sessionPool
}

func NewClient(mount config.MountConfig, logger *slog.Logger) *Client {
	client := &Client{mount: mount, logger: logger}
	if mount.ConnectionPool > 0 {
		client.pool = newSessionPool(mount.ConnectionPool)
	}
	return client
}

func (c *Client) Probe(ctx context.Context) error {
	s, err := c.newSession(ctx)
	if err != nil {
		return err
	}
	defer c.releaseSession(s)
	_, err = s.MLST(c.mount.RootPath)
	return err
}

type session struct {
	conn   net.Conn
	tp     *textproto.Conn
	cfg    config.MountConfig
	closed bool
}

type sessionPool struct {
	mu       sync.Mutex
	sessions []*session
	size     int
}

func newSessionPool(size int) *sessionPool {
	return &sessionPool{
		sessions: make([]*session, 0, size),
		size:     size,
	}
}

func (c *Client) newSession(ctx context.Context) (*session, error) {
	if s, ok := c.tryAcquire(ctx); ok {
		return s, nil
	}

	timeout := 5 * time.Second
	if c.mount.ConnectTimeout > 0 {
		timeout = time.Duration(c.mount.ConnectTimeout) * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", c.mount.Host, c.mount.Port))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if c.logger != nil {
		c.logger.Info("opened ftp connection", "mount", c.mount.Name, "host", c.mount.Host, "port", c.mount.Port)
	}
	tp := textproto.NewConn(conn)
	if _, _, err := tp.ReadResponse(220); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s := &session{conn: conn, tp: tp, cfg: c.mount}
	if err := s.login(); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

func (c *Client) tryAcquire(ctx context.Context) (*session, bool) {
	if c.pool == nil {
		return nil, false
	}
	for {
		s := c.pool.pop()
		if s == nil {
			return nil, false
		}
		if err := s.ping(ctx); err == nil {
			return s, true
		}
		_ = s.Close()
	}
}

func (c *Client) releaseSession(s *session) error {
	if s == nil {
		return nil
	}
	if c.pool == nil {
		return s.Close()
	}
	if s.closed {
		return nil
	}
	if c.pool.push(s) {
		return nil
	}
	return s.Close()
}

func (s *session) login() error {
	if err := s.cmdExpect(331, 230, "USER %s", s.cfg.Username); err != nil {
		return err
	}
	if err := s.cmdExpect(230, 230, "PASS %s", s.cfg.Password); err != nil {
		return err
	}
	if err := s.cmdExpect(200, 200, "TYPE I"); err != nil {
		return err
	}
	return nil
}

func (s *session) cmd(format string, args ...any) (int, string, error) {
	id, err := s.tp.Cmd(format, args...)
	if err != nil {
		return 0, "", fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	s.tp.StartResponse(id)
	defer s.tp.EndResponse(id)
	code, msg, err := s.tp.ReadResponse(-1)
	if err != nil {
		return 0, "", mapResponseErr(err)
	}
	return code, msg, nil
}

func (s *session) cmdExpect(expected int, alt int, format string, args ...any) error {
	code, _, err := s.cmd(format, args...)
	if err != nil {
		return err
	}
	if code != expected && code != alt {
		return fmt.Errorf("%w: unexpected FTP code %d", ErrUnavailable, code)
	}
	return nil
}

func (s *session) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.tp != nil {
		_, _, _ = s.cmd("QUIT")
		return s.tp.Close()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
}

func (s *sessionPool) pop() *session {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.sessions)
	if n == 0 {
		return nil
	}
	session := s.sessions[n-1]
	s.sessions[n-1] = nil
	s.sessions = s.sessions[:n-1]
	return session
}

func (s *sessionPool) push(session *session) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sessions) >= s.size {
		return false
	}
	s.sessions = append(s.sessions, session)
	return true
}

func (s *session) ping(ctx context.Context) error {
	if deadline, ok := ctx.Deadline(); ok {
		_ = s.conn.SetDeadline(deadline)
		defer s.conn.SetDeadline(time.Time{})
	}
	return s.cmdExpect(200, 200, "NOOP")
}

func (s *session) resolve(rel string) string {
	if rel == "" || rel == "/" {
		return s.cfg.RootPath
	}
	return path.Clean(path.Join(s.cfg.RootPath, rel))
}

func mapResponseErr(err error) error {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "550"):
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	case strings.HasPrefix(msg, "450"), strings.HasPrefix(msg, "421"), strings.HasPrefix(msg, "425"), strings.HasPrefix(msg, "426"):
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	default:
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
}

func (s *session) openDataConn(ctx context.Context) (net.Conn, error) {
	code, msg, err := s.cmd("EPSV")
	if err != nil {
		return nil, err
	}
	if code != 229 {
		return nil, fmt.Errorf("%w: EPSV unsupported", ErrUnavailable)
	}
	start := strings.LastIndex(msg, "|||")
	end := strings.LastIndex(msg, "|")
	if start == -1 || end == -1 || end <= start+3 {
		return nil, fmt.Errorf("%w: malformed EPSV response %q", ErrUnavailable, msg)
	}
	port, err := strconv.Atoi(msg[start+3 : end])
	if err != nil {
		return nil, fmt.Errorf("%w: invalid EPSV port", ErrUnavailable)
	}
	host := s.cfg.Host
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", host, port))
}

func (s *session) MLST(fullPath string) (Entry, error) {
	code, msg, err := s.cmd("MLST %s", fullPath)
	if err != nil {
		return Entry{}, err
	}
	if code != 250 {
		return Entry{}, fmt.Errorf("%w: unexpected MLST code %d", ErrUnavailable, code)
	}
	lines := strings.Split(msg, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, ";") && strings.Contains(line, " ") {
			return parseFactsLine(line)
		}
	}
	return Entry{}, fmt.Errorf("%w: MLST facts missing", ErrUnavailable)
}

func (s *session) MLSD(ctx context.Context, fullPath string) ([]Entry, error) {
	dataConn, err := s.openDataConn(ctx)
	if err != nil {
		return nil, err
	}
	code, _, err := s.cmd("MLSD %s", fullPath)
	if err != nil {
		dataConn.Close()
		return nil, err
	}
	if code != 150 {
		dataConn.Close()
		return nil, fmt.Errorf("%w: unexpected MLSD code %d", ErrUnavailable, code)
	}
	defer dataConn.Close()

	scanner := bufio.NewScanner(dataConn)
	var entries []Entry
	for scanner.Scan() {
		entry, err := parseFactsLine(scanner.Text())
		if err == nil {
			if entry.Name == "." || entry.Name == ".." {
				continue
			}
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if _, _, err := s.tp.ReadResponse(226); err != nil {
		return nil, mapResponseErr(err)
	}
	sortEntries(entries)
	return entries, nil
}

func (c *Client) ListDir(ctx context.Context, rel string) ([]Entry, error) {
	return c.listDir(ctx, rel, true)
}

func (c *Client) ListKnownDir(ctx context.Context, rel string) ([]Entry, error) {
	return c.listDir(ctx, rel, false)
}

func (c *Client) listDir(ctx context.Context, rel string, verifyDir bool) ([]Entry, error) {
	s, err := c.newSession(ctx)
	if err != nil {
		return nil, err
	}
	defer c.releaseSession(s)
	if verifyDir {
		entry, err := s.MLST(s.resolve(rel))
		if err != nil {
			return nil, err
		}
		if !entry.IsDir {
			return nil, ErrNotADirectory
		}
	}
	return s.MLSD(ctx, s.resolve(rel))
}

func (c *Client) Stat(ctx context.Context, rel string) (Entry, error) {
	s, err := c.newSession(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer c.releaseSession(s)
	return s.MLST(s.resolve(rel))
}

func (c *Client) OpenFile(ctx context.Context, rel string, offset int64) (io.ReadCloser, error) {
	s, err := c.newSession(ctx)
	if err != nil {
		return nil, err
	}
	entry, err := s.MLST(s.resolve(rel))
	if err != nil {
		s.Close()
		return nil, err
	}
	if entry.IsDir {
		s.Close()
		return nil, ErrNotAFile
	}
	dataConn, err := s.openDataConn(ctx)
	if err != nil {
		s.Close()
		return nil, err
	}
	if offset > 0 {
		if err := s.cmdExpect(350, 350, "REST %d", offset); err != nil {
			dataConn.Close()
			s.Close()
			return nil, err
		}
	}
	code, _, err := s.cmd("RETR %s", s.resolve(rel))
	if err != nil {
		dataConn.Close()
		s.Close()
		return nil, err
	}
	if code != 150 {
		dataConn.Close()
		s.Close()
		return nil, fmt.Errorf("%w: unexpected RETR code %d", ErrUnavailable, code)
	}
	return &remoteFile{data: dataConn, session: s, client: c}, nil
}

type remoteFile struct {
	data    net.Conn
	session *session
	client  *Client
	closed  bool
}

func (r *remoteFile) Read(p []byte) (int, error) {
	return r.data.Read(p)
}

func (r *remoteFile) Close() error {
	if r.closed {
		return nil
	}
	r.closed = true
	_ = r.data.Close()
	if _, _, err := r.session.tp.ReadResponse(226); err != nil {
		return r.session.Close()
	}
	return r.client.releaseSession(r.session)
}
