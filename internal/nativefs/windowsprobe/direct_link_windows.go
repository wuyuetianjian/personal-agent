//go:build windows && nativeprobe_sqlite_identity

package windowsprobe

import (
	"errors"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

// openLocalRootDirectExperiment is not a production fallback. No impersonation,
// elevation, explicit global namespace lookup or target-chain resolution occurs.
func openLocalRootDirectExperiment(path string) (root *Root, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	dll := windows.NewLazySystemDLL("ntdll.dll")
	open := dll.NewProc("NtOpenSymbolicLinkObject")
	queryLink := dll.NewProc("NtQuerySymbolicLinkObject")
	queryObject := dll.NewProc("NtQueryObject")
	ops := directLinkOps{
		context: func() error {
			var token windows.Token
			e := windows.OpenThreadToken(windows.CurrentThread(), windows.TOKEN_QUERY, true, &token)
			if e == nil {
				return errors.Join(errUnsafe, token.Close())
			}
			if e != windows.ERROR_NO_TOKEN {
				return e
			}
			for _, p := range []*windows.LazyProc{open, queryLink, queryObject} {
				if e = p.Find(); e != nil {
					return e
				}
			}
			return nil
		},
		open: func(path string) (uintptr, error) {
			name, e := windows.NewNTUnicodeString(path)
			if e != nil {
				return 0, e
			}
			oa := windows.OBJECT_ATTRIBUTES{Length: uint32(unsafe.Sizeof(windows.OBJECT_ATTRIBUTES{})), ObjectName: name, Attributes: windows.OBJ_CASE_INSENSITIVE}
			var h windows.Handle
			s, _, _ := open.Call(uintptr(unsafe.Pointer(&h)), 1, uintptr(unsafe.Pointer(&oa)))
			runtime.KeepAlive(name)
			if uint32(s) != 0 {
				return uintptr(h), windows.NTStatus(uint32(s))
			}
			return uintptr(h), nil
		},
		qualify: func(h uintptr) error {
			var basic [14]uint32
			var n uint32
			s, _, _ := queryObject.Call(h, 0, uintptr(unsafe.Pointer(&basic[0])), unsafe.Sizeof(basic), uintptr(unsafe.Pointer(&n)))
			if uint32(s) != 0 {
				return windows.NTStatus(uint32(s))
			}
			if n < 8 || n > uint32(unsafe.Sizeof(basic)) || basic[1] != 1 {
				return errUnsafe
			}
			var flags uint32
			proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetHandleInformation")
			if e := proc.Find(); e != nil {
				return e
			}
			ok, _, e := proc.Call(h, uintptr(unsafe.Pointer(&flags)))
			if ok == 0 {
				return e
			}
			return qualifyDirectLinkHandle(basic[1], n, flags)
		},
		query: func(h uintptr) ([]uint16, uint32, uint32, error) {
			b := make([]uint16, 32767)
			u := windows.NTUnicodeString{MaximumLength: uint16(len(b) * 2), Buffer: &b[0]}
			var n uint32
			s, _, _ := queryLink.Call(h, uintptr(unsafe.Pointer(&u)), uintptr(unsafe.Pointer(&n)))
			runtime.KeepAlive(b)
			if uint32(s) != 0 {
				return nil, 0, 0, windows.NTStatus(uint32(s))
			}
			if u.Buffer != &b[0] || u.MaximumLength != uint16(len(b)*2) {
				return nil, 0, 0, errUnsafe
			}
			return b, uint32(u.Length), n, nil
		}, close: func(h uintptr) error { return windows.CloseHandle(windows.Handle(h)) },
	}
	err = withDirectLinkAnchor(path, ops, func(anchor string, parts []string) error {
		var e error
		root, e = openQualifiedAnchor(anchor, parts)
		return e
	})
	return
}
