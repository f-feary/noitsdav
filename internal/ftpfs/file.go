package ftpfs

import (
	"context"
	"fmt"
	"io"
)

type FileReadResult struct {
	Entry  Entry
	Reader io.ReadCloser
}

func ReadFile(ctx context.Context, client *Client, rel string, start int64) (FileReadResult, error) {
	entry, err := client.Stat(ctx, rel)
	if err != nil {
		return FileReadResult{}, err
	}
	if entry.IsDir {
		return FileReadResult{}, ErrNotAFile
	}
	reader, err := client.OpenFile(ctx, rel, start)
	if err != nil {
		return FileReadResult{}, err
	}
	return FileReadResult{Entry: entry, Reader: reader}, nil
}

func RangeHeaders(size int64, start, end int64, partial bool) map[string]string {
	headers := map[string]string{
		"Accept-Ranges":  "bytes",
		"Content-Length": fmt.Sprintf("%d", size),
	}
	if partial {
		headers["Content-Length"] = fmt.Sprintf("%d", end-start+1)
		headers["Content-Range"] = fmt.Sprintf("bytes %d-%d/%d", start, end, size)
	}
	return headers
}

