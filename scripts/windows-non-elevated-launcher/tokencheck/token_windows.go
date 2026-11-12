package tokencheck

import (
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"unsafe"
)

func nativeQuery(t windows.Token, class uint32) informationQuery {
	return func(b []byte) (uint32, error) {
		var p *byte
		if len(b) > 0 {
			p = &b[0]
		}
		var used uint32
		e := windows.GetTokenInformation(t, class, p, uint32(len(b)), &used)
		return used, e
	}
}
func info(t windows.Token, class uint32) ([]byte, error) {
	b, e := queryVariable(nativeQuery(t, class), windows.ERROR_INSUFFICIENT_BUFFER)
	if e != nil {
		return nil, fmt.Errorf("token variable query %d: %w", class, e)
	}
	return b, nil
}
func scalar(t windows.Token, class uint32) (uint32, error) {
	value, e := queryScalar(nativeQuery(t, class))
	if e != nil {
		return 0, fmt.Errorf("token scalar query %d: %w", class, e)
	}
	return value, nil
}
func Inspect(t windows.Token) (s Snapshot, err error) {
	s.Elevation, err = scalar(t, windows.TokenElevation)
	if err != nil {
		return s, err
	}
	s.ElevationType, err = scalar(t, windows.TokenElevationType)
	if err != nil {
		return s, err
	}
	b, e := info(t, windows.TokenIntegrityLevel)
	if e != nil {
		return s, e
	}
	if len(b) < int(unsafe.Sizeof(windows.SIDAndAttributes{})) {
		return s, fmt.Errorf("short integrity record")
	}
	sid := (*windows.SIDAndAttributes)(unsafe.Pointer(&b[0])).Sid
	if sid == nil || !sid.IsValid() {
		return s, fmt.Errorf("invalid integrity SID")
	}
	s.Integrity = sid.String()
	runtime.KeepAlive(b)
	gs, e := t.GetTokenGroups()
	if e != nil {
		return s, e
	}
	for _, g := range gs.AllGroups() {
		if g.Sid.String() == "S-1-5-32-544" {
			s.AdminAttributes = append(s.AdminAttributes, g.Attributes)
		}
	}
	b, e = info(t, windows.TokenPrivileges)
	if e != nil {
		return s, e
	}
	if len(b) < 4 {
		return s, fmt.Errorf("short privileges")
	}
	ps := (*windows.Tokenprivileges)(unsafe.Pointer(&b[0]))
	if uint64(ps.PrivilegeCount)*uint64(unsafe.Sizeof(windows.LUIDAndAttributes{}))+4 > uint64(len(b)) {
		return s, fmt.Errorf("invalid privilege count")
	}
	name, _ := windows.UTF16PtrFromString("SeChangeNotifyPrivilege")
	var allowed windows.LUID
	if e = windows.LookupPrivilegeValue(nil, name, &allowed); e != nil {
		return s, e
	}
	for _, p := range ps.AllPrivileges() {
		if p.Attributes&windows.SE_PRIVILEGE_ENABLED != 0 {
			if p.Luid == allowed {
				s.EnabledPrivileges = append(s.EnabledPrivileges, "SeChangeNotifyPrivilege")
			} else {
				s.EnabledPrivileges = append(s.EnabledPrivileges, fmt.Sprintf("LUID:%d:%d", p.Luid.HighPart, p.Luid.LowPart))
			}
		}
	}
	runtime.KeepAlive(b)
	return s, nil
}
func Current() (windows.Token, error) {
	var t windows.Token
	e := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE|windows.TOKEN_ASSIGN_PRIMARY, &t)
	return t, e
}
func CheckCurrent() (Snapshot, error) {
	var t windows.Token
	if e := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &t); e != nil {
		return Snapshot{}, e
	}
	s, e := Inspect(t)
	ce := t.Close()
	if e != nil {
		return s, e
	}
	if ce != nil {
		return s, ce
	}
	return s, Validate(s)
}
