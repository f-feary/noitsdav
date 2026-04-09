package ftpfs

import (
	"fmt"
	"strconv"
	"strings"
)

func ParseRange(size int64, header string) (start int64, end int64, partial bool, err error) {
	if header == "" {
		return 0, size - 1, false, nil
	}
	if !strings.HasPrefix(header, "bytes=") {
		return 0, 0, false, ErrInvalidRange
	}
	spec := strings.TrimPrefix(header, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, 0, false, ErrInvalidRange
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, ErrInvalidRange
	}

	if parts[0] == "" {
		n, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || n <= 0 {
			return 0, 0, false, ErrInvalidRange
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, nil
	}

	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 {
		return 0, 0, false, ErrInvalidRange
	}
	if start >= size {
		return 0, 0, false, ErrUnsatisfiableRange
	}
	if parts[1] == "" {
		return start, size - 1, true, nil
	}
	end, err = strconv.ParseInt(parts[1], 10, 64)
	if err != nil || end < start {
		return 0, 0, false, ErrInvalidRange
	}
	if end >= size {
		end = size - 1
	}
	return start, end, true, nil
}

func RangeError(size int64) string {
	return fmt.Sprintf("bytes */%d", size)
}

