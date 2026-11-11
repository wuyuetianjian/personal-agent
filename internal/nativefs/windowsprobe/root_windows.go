//go:build windows

package windowsprobe

// OpenLocalRoot is under native qualification; it currently refuses acquisition.
func OpenLocalRoot(path string) (*Root, error) {
	return nil, errUnsafe
}
