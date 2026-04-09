package server

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"noitsdav/internal/ftpfs"
	"noitsdav/internal/mounts"
)

type Handler struct {
	registry *mounts.Registry
	logger   *slog.Logger
}

func NewHandler(registry *mounts.Registry, logger *slog.Logger) http.Handler {
	return &Handler{registry: registry, logger: logger}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodOptions:
		h.handleOptions(w, r)
	case "PROPFIND":
		h.handlePropfind(w, r)
	case http.MethodHead:
		h.handleRead(w, r, true)
	case http.MethodGet:
		h.handleRead(w, r, false)
	case http.MethodPut, http.MethodDelete, "MKCOL", "COPY", "MOVE", "PROPPATCH", "LOCK", "UNLOCK":
		http.Error(w, "read-only mount", http.StatusMethodNotAllowed)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *Handler) handleOptions(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Allow", "OPTIONS, PROPFIND, HEAD, GET")
	w.Header().Set("DAV", "1")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) handlePropfind(w http.ResponseWriter, r *http.Request) {
	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "1"
	}
	resolved, err := mounts.Resolve(r.URL.Path)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var responses []davResponse
	if resolved.IsRoot {
		if depth == "0" {
			responses = append(responses, dirResponse(absoluteHref(r, "/"), ""))
		} else {
			for _, mount := range h.registry.List() {
				responses = append(responses, dirResponse(absoluteHref(r, "/"+mount.Name+"/"), mount.Name))
			}
		}
	} else {
		mount, ok := h.registry.Get(resolved.MountName)
		if !ok {
			http.NotFound(w, r)
			return
		}
		client := ftpfs.NewClient(mount)
		entry, err := client.Stat(r.Context(), resolved.BackendPath)
		if err != nil {
			h.updateHealth(mount.Name, err)
			h.writeBackendError(w, mount.Name, err)
			return
		}
		h.updateHealth(mount.Name, nil)
		if !entry.IsDir || depth == "0" {
			currentHref := r.URL.Path
			if entry.IsDir && !strings.HasSuffix(currentHref, "/") {
				currentHref += "/"
			}
			responses = append(responses, responseForPath(absoluteHref(r, currentHref), entry))
		}
		if entry.IsDir && depth != "0" {
			children, err := client.ListDir(r.Context(), resolved.BackendPath)
			if err != nil {
				h.updateHealth(mount.Name, err)
				h.writeBackendError(w, mount.Name, err)
				return
			}
			for _, child := range children {
				childPath := strings.TrimSuffix(r.URL.Path, "/") + "/" + child.Name
				if child.IsDir {
					childPath += "/"
				}
				responses = append(responses, responseForPath(absoluteHref(r, childPath), child))
			}
		}
	}

	body, err := xml.MarshalIndent(multistatus{XmlnsD: "DAV:", Responses: responses}, "", "  ")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", `application/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusMultiStatus)
	_, _ = w.Write([]byte(xml.Header))
	_, _ = w.Write(body)
}

func (h *Handler) handleRead(w http.ResponseWriter, r *http.Request, headOnly bool) {
	resolved, err := mounts.Resolve(r.URL.Path)
	if err != nil || resolved.IsRoot {
		http.NotFound(w, r)
		return
	}
	mount, ok := h.registry.Get(resolved.MountName)
	if !ok {
		http.NotFound(w, r)
		return
	}
	client := ftpfs.NewClient(mount)
	result, err := ftpfs.ReadFile(r.Context(), client, resolved.BackendPath, 0)
	if err != nil {
		h.updateHealth(mount.Name, err)
		h.writeBackendError(w, mount.Name, err)
		return
	}
	defer result.Reader.Close()
	h.updateHealth(mount.Name, nil)

	start, end, partial, err := ftpfs.ParseRange(result.Entry.Size, r.Header.Get("Range"))
	if err != nil {
		if errors.Is(err, ftpfs.ErrUnsatisfiableRange) {
			w.Header().Set("Content-Range", ftpfs.RangeError(result.Entry.Size))
			http.Error(w, "unsatisfiable range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		http.Error(w, "invalid range", http.StatusBadRequest)
		return
	}

	reader := result.Reader
	if start > 0 {
		reader.Close()
		reopened, err := ftpfs.ReadFile(r.Context(), client, resolved.BackendPath, start)
		if err != nil {
			h.updateHealth(mount.Name, err)
			h.writeBackendError(w, mount.Name, err)
			return
		}
		result = reopened
		reader = reopened.Reader
		defer reopened.Reader.Close()
	}

	headers := ftpfs.RangeHeaders(result.Entry.Size, start, end, partial)
	for key, value := range headers {
		w.Header().Set(key, value)
	}
	if !result.Entry.ModTime.IsZero() {
		w.Header().Set("Last-Modified", result.Entry.ModTime.UTC().Format(http.TimeFormat))
	}
	w.Header().Set("Content-Type", "application/octet-stream")

	status := http.StatusOK
	if partial {
		status = http.StatusPartialContent
	}
	w.WriteHeader(status)
	if headOnly {
		return
	}
	if partial {
		_, err = io.CopyN(w, reader, end-start+1)
	} else {
		_, err = io.Copy(w, reader)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		h.logReadError(mount.Name, resolved.BackendPath, err)
	}
}

func (h *Handler) updateHealth(mountName string, err error) {
	if err != nil {
		h.registry.SetHealth(mountName, mounts.StatusUnavailable, err)
		h.logger.Warn("mount operation failed", "mount", mountName, "error", err)
		return
	}
	h.registry.SetHealth(mountName, mounts.StatusAvailable, nil)
}

func (h *Handler) writeBackendError(w http.ResponseWriter, mountName string, err error) {
	switch {
	case errors.Is(err, ftpfs.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, ftpfs.ErrNotADirectory), errors.Is(err, ftpfs.ErrNotAFile):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		http.Error(w, fmt.Sprintf("mount %s unavailable", mountName), http.StatusServiceUnavailable)
	}
}

type multistatus struct {
	XMLName   xml.Name      `xml:"D:multistatus"`
	XmlnsD    string        `xml:"xmlns:D,attr"`
	Responses []davResponse `xml:"D:response"`
}

type davResponse struct {
	Href     string      `xml:"D:href"`
	Propstat davPropstat `xml:"D:propstat"`
}

type davPropstat struct {
	Prop   davProp `xml:"D:prop"`
	Status string  `xml:"D:status"`
}

type davProp struct {
	DisplayName      string          `xml:"D:displayname,omitempty"`
	ResourceType     davResourceType `xml:"D:resourcetype"`
	GetContentLength string          `xml:"D:getcontentlength,omitempty"`
	GetLastModified  string          `xml:"D:getlastmodified,omitempty"`
}

type davResourceType struct {
	Collection *struct{} `xml:"D:collection,omitempty"`
}

func dirResponse(href, name string) davResponse {
	return davResponse{
		Href: href,
		Propstat: davPropstat{
			Prop: davProp{
				DisplayName:  name,
				ResourceType: davResourceType{Collection: &struct{}{}},
			},
			Status: "HTTP/1.1 200 OK",
		},
	}
}

func responseForPath(href string, entry ftpfs.Entry) davResponse {
	prop := davProp{
		DisplayName: path.Base(strings.TrimSuffix(href, "/")),
	}
	if entry.IsDir {
		prop.ResourceType = davResourceType{Collection: &struct{}{}}
	} else {
		prop.GetContentLength = fmt.Sprintf("%d", entry.Size)
	}
	if !entry.ModTime.IsZero() {
		prop.GetLastModified = entry.ModTime.UTC().Format(http.TimeFormat)
	}
	return davResponse{
		Href: href,
		Propstat: davPropstat{
			Prop:   prop,
			Status: "HTTP/1.1 200 OK",
		},
	}
}

func _unused(_ context.Context, _ time.Time) {}

func absoluteHref(r *http.Request, p string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	u := url.URL{
		Scheme: scheme,
		Host:   r.Host,
		Path:   p,
	}
	return u.String()
}

func (h *Handler) logReadError(mountName, backendPath string, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		h.logger.Info("client canceled read", "mount", mountName, "path", backendPath, "error", err)
		return
	case isNetworkTimeout(err):
		h.logger.Info("client write timed out", "mount", mountName, "path", backendPath, "error", err)
		return
	default:
		h.logger.Warn("read failed", "mount", mountName, "path", backendPath, "error", err)
	}
}

func isNetworkTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
