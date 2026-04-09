package unit

import (
	"errors"
	"testing"

	"noitsdav/internal/ftpfs"
)

func TestParseRangeVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		header  string
		size    int64
		start   int64
		end     int64
		partial bool
		err     error
	}{
		{name: "full", size: 10, header: "", start: 0, end: 9},
		{name: "explicit", size: 10, header: "bytes=2-5", start: 2, end: 5, partial: true},
		{name: "resume", size: 10, header: "bytes=4-", start: 4, end: 9, partial: true},
		{name: "suffix", size: 10, header: "bytes=-3", start: 7, end: 9, partial: true},
		{name: "unsat", size: 10, header: "bytes=20-30", err: ftpfs.ErrUnsatisfiableRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start, end, partial, err := ftpfs.ParseRange(tt.size, tt.header)
			if tt.err != nil {
				if !errors.Is(err, tt.err) {
					t.Fatalf("expected %v got %v", tt.err, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if start != tt.start || end != tt.end || partial != tt.partial {
				t.Fatalf("unexpected range: %d-%d partial=%v", start, end, partial)
			}
		})
	}
}

