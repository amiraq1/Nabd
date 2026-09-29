package rtl

import "github.com/rivo/uniseg"

// Cluster is an extended grapheme cluster with absolute source ranges into
// the logical input. SrcRunes and SrcBytes always delimit whole UTF-8
// sequences and whole grapheme clusters; byte offsets are never used as rune
// offsets.
type Cluster struct {
	Text     string
	SrcRunes [2]int
	SrcBytes [2]int
	Width    int
}

// clusterize splits logical into extended grapheme clusters, tracking rune and
// byte ranges. Offsets are absolute within logical.
func clusterize(logical string) []Cluster {
	out := make([]Cluster, 0, len(logical))
	state := -1
	rest := logical
	byteOff, runeOff := 0, 0
	for len(rest) > 0 {
		cluster, newRest, width, newState := uniseg.FirstGraphemeClusterInString(rest, state)
		if cluster == "" {
			break
		}
		nr := len([]rune(cluster))
		out = append(out, Cluster{
			Text:     cluster,
			SrcRunes: [2]int{runeOff, runeOff + nr},
			SrcBytes: [2]int{byteOff, byteOff + len(cluster)},
			Width:    width,
		})
		byteOff += len(cluster)
		runeOff += nr
		rest = newRest
		state = newState
	}
	return out
}

// clusterWidth sums cluster widths.
func clusterWidths(clusters []Cluster) int {
	w := 0
	for i := range clusters {
		w += clusters[i].Width
	}
	return w
}
