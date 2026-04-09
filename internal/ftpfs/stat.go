package ftpfs

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Entry struct {
	Name    string
	Path    string
	Size    int64
	ModTime time.Time
	IsDir   bool
}

func parseFactsLine(line string) (Entry, error) {
	parts := strings.SplitN(strings.TrimSpace(line), " ", 2)
	if len(parts) != 2 {
		return Entry{}, fmt.Errorf("invalid facts line: %q", line)
	}
	facts, name := parts[0], strings.TrimSpace(parts[1])
	entry := Entry{Name: name, Path: name}
	for _, fact := range strings.Split(facts, ";") {
		if fact == "" {
			continue
		}
		kv := strings.SplitN(fact, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(kv[0])
		val := kv[1]
		switch key {
		case "type":
			entry.IsDir = val == "dir" || val == "cdir" || val == "pdir"
		case "size":
			size, _ := strconv.ParseInt(val, 10, 64)
			entry.Size = size
		case "modify":
			if ts, err := time.Parse("20060102150405", val); err == nil {
				entry.ModTime = ts.UTC()
			}
		}
	}
	return entry, nil
}

func sortEntries(entries []Entry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name == entries[j].Name {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Name < entries[j].Name
	})
}
