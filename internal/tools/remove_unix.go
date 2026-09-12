//go:build unix

package tools

import "nabd/internal/safefs"

// removeFromRoot deletes relative beneath root through a descriptor-relative
// unlinkat (safefs.RemoveFile). relative is the filesystem authority; absolute
// is reporting metadata and is never used for a filesystem operation.
func removeFromRoot(root *Root, relative, absolute string) error {
	return safefs.RemoveFile(root.Dir(), relative)
}
