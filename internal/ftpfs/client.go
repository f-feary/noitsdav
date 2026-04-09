package ftpfs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"path"
	"strconv"
	"strings"
	"time"

	"noitsdav/internal/config"
)

var (
	ErrNotFound            = errors.New("not found")
	ErrUnavailable         = errors.New("backend unavailable")
	ErrReadOnly            = errors.New("read-only")
	ErrInvalidRange        = errors.New("invalid range")
	ErrUnsatisfiableRange  = errors.New("unsatisfiable range")
	ErrNotAFile            = errors.New("not a file")
	ErrNotADirectory       = errors.New("not a directory")
)

type Client struct {
	mount config.MountConfig
}

func NewClient(mount config.MountConfig) *Client {
	return &Client{mount: mount}
}

func (c *Client) Probe(ctx context.Context) error {
	s, err := c.newSession(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	_, err = s.MLST(c.mount.RootPath)
	return err
}

type session struct {
	conn net.Conn
	tp   *textproto.Conn
	cfg  config.MountConfig
}

func (c *Client) newSession(ctx context.Context) (*session, error) {
	timeout := 5 * time.Second
	if c.mount.ConnectTimeout > 0 {
		timeout = time.Duration(c.mount.ConnectTimeout) * time.Second
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", fmt.Sprintf("%s:%d", c.mount.Host, c.mount.Port))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
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
	if s.tp != nil {
		_, _, _ = s.cmd("QUIT")
		return s.tp.Close()
	}
	if s.conn != nil {
		return s.conn.Close()
	}
	return nil
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
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if _, _, err := s.tp.ReadResponse(226); err != nil {
		return nil, mapResponseErr(err)
	}
	return entries, nil
}

func (c *Client) ListDir(ctx context.Context, rel string) ([]Entry, error) {
	s, err := c.newSession(ctx)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	entry, err := s.MLST(s.resolve(rel))
	if err != nil {
		return nil, err
	}
	if !entry.IsDir {
		return nil, ErrNotADirectory
	}
	return s.MLSD(ctx, s.resolve(rel))
}

func (c *Client) Stat(ctx context.Context, rel string) (Entry, error) {
	s, err := c.newSession(ctx)
	if err != nil {
		return Entry{}, err
	}
	defer s.Close()
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
	return &remoteFile{data: dataConn, session: s}, nil
}

type remoteFile struct {
	data    net.Conn
	session *session
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
	_, _, _ = r.session.tp.ReadResponse(226)
	return r.session.Close()
}
