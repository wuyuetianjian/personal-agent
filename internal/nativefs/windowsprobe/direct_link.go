//go:build nativeprobe_sqlite_identity

package windowsprobe

import (
	"errors"
	"unicode/utf16"
)

// Private experimental resolver protocol; no production acquisition calls this.
type directLinkOps struct {
	context func() error
	open    func(string) (uintptr, error)
	qualify func(uintptr) error
	query   func(uintptr) ([]uint16, uint32, uint32, error)
	close   func(uintptr) error
}

func decodeDirectLink(b []uint16, n, returned uint32) (string, error) {
	// ReturnedLength may include a terminating WCHAR; it is not used as slice length.
	if n == 0 || n%2 != 0 || uint64(n) > uint64(len(b))*2 || returned < n || uint64(returned) > uint64(len(b))*2 || returned%2 != 0 {
		return "", errUnsafe
	}
	b = b[:n/2]
	for i := 0; i < len(b); i++ {
		v := b[i]
		if v == 0 {
			return "", errUnsafe
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+1 >= len(b) || b[i+1] < 0xdc00 || b[i+1] > 0xdfff {
				return "", errUnsafe
			}
			i++
		} else if v >= 0xdc00 && v <= 0xdfff {
			return "", errUnsafe
		}
	}
	return physicalVolumeRoot(string(utf16.Decode(b)))
}
func withDirectLinkAnchor(path string, o directLinkOps, acquire func(string, []string) error) (err error) {
	drive, parts, err := parseLocalRootPath(path)
	if err != nil {
		return err
	}
	if err = o.context(); err != nil {
		return err
	}
	h, err := o.open(`\??\` + drive)
	if h != 0 {
		defer func() {
			if h != 0 {
				owned := h
				h = 0
				err = errors.Join(err, o.close(owned))
			}
		}()
	}
	if err != nil {
		return err
	}
	if h == 0 {
		return errUnsafe
	}
	if err = o.qualify(h); err != nil {
		return err
	}
	b, n, r, err := o.query(h)
	if err != nil {
		return err
	}
	anchor, err := decodeDirectLink(b, n, r)
	if err != nil {
		return err
	}
	owned := h
	h = 0
	if err = o.close(owned); err != nil {
		return err
	}
	return acquire(anchor, parts)
}
func qualifyDirectLinkHandle(access, length, flags uint32) error {
	if access != 1 || length < 8 || length > 56 || flags&1 != 0 {
		return errUnsafe
	}
	return nil
}
