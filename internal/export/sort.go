package export

import (
	"sort"
	"strings"
)

func sortPartners(p []Partner) {
	sort.SliceStable(p, func(i, j int) bool { return strings.ToLower(p[i].Name) < strings.ToLower(p[j].Name) })
}
