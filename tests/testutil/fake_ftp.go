package testutil

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Node struct {
	IsDir   bool
	Data    []byte
	ModTime time.Time
}

type FakeFTPServer struct {
	listener net.Listener
	addr     string
	user     string
	pass     string
	root     map[string]Node
	mu       sync.RWMutex
}

func NewFakeFTPServer(user, pass string, root map[string]Node) *FakeFTPServer {
	s := &FakeFTPServer{user: user, pass: pass, root: normalizeTree(root)}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	s.listener = ln
	s.addr = ln.Addr().String()
	go s.serve()
	return s
}

func (s *FakeFTPServer) Addr() string { return s.addr }

func (s *FakeFTPServer) Close() error { return s.listener.Close() }

func (s *FakeFTPServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(conn)
	}
}

func (s *FakeFTPServer) handleConn(conn net.Conn) {
	defer conn.Close()
	tp := textproto.NewConn(conn)
	defer tp.Close()
	_ = tp.PrintfLine("220 fake ftp ready")

	var rest int64
	var passive net.Listener
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return
		}
		fields := strings.SplitN(line, " ", 2)
		cmd := strings.ToUpper(fields[0])
		arg := ""
		if len(fields) == 2 {
			arg = strings.TrimSpace(fields[1])
		}

		switch cmd {
		case "USER":
			_ = tp.PrintfLine("331 password required")
		case "PASS":
			_ = tp.PrintfLine("230 logged in")
		case "TYPE":
			_ = tp.PrintfLine("200 type set")
		case "QUIT":
			_ = tp.PrintfLine("221 bye")
			return
		case "EPSV":
			if passive != nil {
				_ = passive.Close()
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				_ = tp.PrintfLine("425 can't open passive connection")
				continue
			}
			passive = ln
			port := ln.Addr().(*net.TCPAddr).Port
			_ = tp.PrintfLine("229 Entering Extended Passive Mode (|||%d|)", port)
		case "MLST":
			node, p, ok := s.lookup(arg)
			if !ok {
				_ = tp.PrintfLine("550 not found")
				continue
			}
			_ = tp.PrintfLine("250-Listing")
			_ = tp.PrintfLine(" type=%s;size=%d;modify=%s; %s", typeName(node.IsDir), len(node.Data), node.ModTime.UTC().Format("20060102150405"), path.Base(p))
			_ = tp.PrintfLine("250 End")
		case "MLSD":
			node, p, ok := s.lookup(arg)
			if !ok || !node.IsDir {
				_ = tp.PrintfLine("550 not found")
				continue
			}
			if passive == nil {
				_ = tp.PrintfLine("425 use EPSV first")
				continue
			}
			_ = tp.PrintfLine("150 opening data connection")
			dataConn, err := passive.Accept()
			if err != nil {
				_ = tp.PrintfLine("425 data connection failed")
				continue
			}
			children := s.children(p)
			w := bufio.NewWriter(dataConn)
			for _, child := range children {
				_, _ = fmt.Fprintf(w, "type=%s;size=%d;modify=%s; %s\r\n", typeName(child.node.IsDir), len(child.node.Data), child.node.ModTime.UTC().Format("20060102150405"), child.name)
			}
			_ = w.Flush()
			_ = dataConn.Close()
			_ = passive.Close()
			passive = nil
			_ = tp.PrintfLine("226 transfer complete")
		case "REST":
			value, err := strconv.ParseInt(arg, 10, 64)
			if err != nil {
				_ = tp.PrintfLine("501 bad offset")
				continue
			}
			rest = value
			_ = tp.PrintfLine("350 restart accepted")
		case "RETR":
			node, _, ok := s.lookup(arg)
			if !ok || node.IsDir {
				_ = tp.PrintfLine("550 not found")
				continue
			}
			if passive == nil {
				_ = tp.PrintfLine("425 use EPSV first")
				continue
			}
			_ = tp.PrintfLine("150 opening data connection")
			dataConn, err := passive.Accept()
			if err != nil {
				_ = tp.PrintfLine("425 data connection failed")
				continue
			}
			if rest > int64(len(node.Data)) {
				rest = int64(len(node.Data))
			}
			_, _ = dataConn.Write(node.Data[rest:])
			rest = 0
			_ = dataConn.Close()
			_ = passive.Close()
			passive = nil
			_ = tp.PrintfLine("226 transfer complete")
		case "NOOP":
			_ = tp.PrintfLine("200 ok")
		default:
			_ = tp.PrintfLine("502 not implemented")
		}
	}
}

type childNode struct {
	name string
	node Node
}

func (s *FakeFTPServer) children(dir string) []childNode {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := dir
	if prefix != "/" {
		prefix += "/"
	}
	seen := map[string]childNode{}
	for p, n := range s.root {
		if !strings.HasPrefix(p, prefix) || p == dir {
			continue
		}
		rest := strings.TrimPrefix(p, prefix)
		part := strings.SplitN(rest, "/", 2)[0]
		if _, ok := seen[part]; ok {
			continue
		}
		childPath := path.Join(dir, part)
		child, ok := s.root[childPath]
		if !ok {
			child = Node{IsDir: true, ModTime: n.ModTime}
		}
		seen[part] = childNode{name: part, node: child}
	}
	out := make([]childNode, 0, len(seen))
	for _, child := range seen {
		out = append(out, child)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func (s *FakeFTPServer) lookup(raw string) (Node, string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p := path.Clean("/" + raw)
	node, ok := s.root[p]
	return node, p, ok
}

func normalizeTree(tree map[string]Node) map[string]Node {
	out := map[string]Node{
		"/": {IsDir: true, ModTime: time.Unix(0, 0).UTC()},
	}
	for p, node := range tree {
		cp := path.Clean("/" + p)
		if node.ModTime.IsZero() {
			node.ModTime = time.Unix(0, 0).UTC()
		}
		out[cp] = node
		parent := path.Dir(cp)
		for parent != "." && parent != "/" {
			if _, ok := out[parent]; !ok {
				out[parent] = Node{IsDir: true, ModTime: node.ModTime}
			}
			parent = path.Dir(parent)
		}
	}
	return out
}

func typeName(isDir bool) string {
	if isDir {
		return "dir"
	}
	return "file"
}

func FTPPort(addr string) int {
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		panic(err)
	}
	return port
}

func ReadAll(rc io.ReadCloser) []byte {
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		panic(err)
	}
	return data
}

