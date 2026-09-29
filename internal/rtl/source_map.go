package rtl

import (
	"errors"
	"sort"
)

// SourceText returns the source bytes of c from the logical input, and
// whether the range is valid. The result is empty for out-of-range clusters.
func SourceText(logical string, c Cluster) (string, bool) {
	if c.SrcBytes[0] < 0 || c.SrcBytes[1] > len(logical) || c.SrcBytes[0] > c.SrcBytes[1] {
		return "", false
	}
	return logical[c.SrcBytes[0]:c.SrcBytes[1]], true
}

// LogicalClusters returns a copy of clusters sorted by source position.
func LogicalClusters(clusters []Cluster) []Cluster {
	out := append([]Cluster(nil), clusters...)
	sort.Slice(out, func(i, j int) bool { return out[i].SrcRunes[0] < out[j].SrcRunes[0] })
	return out
}

// RestoreFromSource reconstructs the logical text of clusters by slicing the
// original input at the clusters' source ranges. This is the only sanctioned
// copy path: display text (Cluster.Text) may have been mirrored by rule L4,
// so it must never be used as a copy source.
func RestoreFromSource(logical string, clusters []Cluster) (string, error) {
	ordered := LogicalClusters(clusters)
	var b []byte
	for i := range ordered {
		src, ok := SourceText(logical, ordered[i])
		if !ok {
			return "", errors.New("rtl: cluster source range outside logical input")
		}
		b = append(b, src...)
	}
	return string(b), nil
}
