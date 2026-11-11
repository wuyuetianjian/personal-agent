//go:build windows

package windowsprobe

// sameDirectoryGuard is a test-first traversal qualification contract. It is
// deliberately refusing and not wired into any traversal until native RED.
func sameDirectoryGuard(a, b snapshot) bool { return false }
