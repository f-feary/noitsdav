package mounts

import (
	"errors"
	"path"
	"strings"
)

type ResolvedPath struct {
	IsRoot      bool
	MountName   string
	BackendPath string
}

func Resolve(requestPath string) (ResolvedPath, error) {
	cleaned := path.Clean("/" + requestPath)
	if cleaned == "/" {
		return ResolvedPath{IsRoot: true, BackendPath: "/"}, nil
	}
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		return ResolvedPath{}, errors.New("invalid path")
	}
	backend := "/"
	if len(parts) > 1 {
		backend = "/" + strings.Join(parts[1:], "/")
	}
	return ResolvedPath{
		MountName:   parts[0],
		BackendPath: path.Clean(backend),
	}, nil
}

