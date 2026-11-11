//go:build windows

package windowsprobe

import "golang.org/x/sys/windows"

// sameDirectoryGuard compares traversal authority, not directory contents.
// Callers still inspect both handles (including case/volume qualification) and
// check exact names. Sibling mutations can change times/allocation without
// changing the held directory object. This is not an immutability or ABA proof.
func sameDirectoryGuard(a, b snapshot) bool {
	const forbidden = windows.FILE_ATTRIBUTE_REPARSE_POINT | windows.FILE_ATTRIBUTE_OFFLINE | windows.FILE_ATTRIBUTE_DEVICE | windows.FILE_ATTRIBUTE_RECALL_ON_OPEN | windows.FILE_ATTRIBUTE_RECALL_ON_DATA_ACCESS
	for _, s := range []snapshot{a, b} {
		if s.Standard.Directory == 0 || s.Standard.DeletePending != 0 || s.Standard.Size < 0 || s.Basic.Attributes&forbidden != 0 || s.Tag.Attributes&forbidden != 0 || s.Tag.Tag != 0 {
			return false
		}
	}
	return a.ID == b.ID && a.Basic.Creation == b.Basic.Creation && a.Basic.Attributes == b.Basic.Attributes && a.Tag == b.Tag
}

// Select only from the expected type. A directory replaced with a file must
// fail directory qualification, never fall through to a file-only comparison.
func sameQualifiedGuard(expected, actual snapshot) bool {
	if expected.Standard.Directory != 0 {
		return sameDirectoryGuard(expected, actual)
	}
	return unchanged(expected, actual)
}
