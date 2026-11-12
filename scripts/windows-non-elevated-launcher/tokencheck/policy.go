// Package tokencheck qualifies only a filtered non-elevated token, not an
// independent standard-user account or a general-purpose sandbox.
package tokencheck

import "fmt"

type Snapshot struct {
	Elevation         uint32
	ElevationType     uint32
	Integrity         string
	AdminAttributes   []uint32
	EnabledPrivileges []string
}

func Validate(s Snapshot) error {
	if s.Elevation != 0 || (s.ElevationType != 1 && s.ElevationType != 3) {
		return fmt.Errorf("elevated/full or unknown elevation type")
	}
	if s.Integrity != "S-1-16-8192" {
		return fmt.Errorf("not exactly Medium integrity")
	}
	for _, a := range s.AdminAttributes {
		if a&4 != 0 || a&16 == 0 {
			return fmt.Errorf("Administrators SID is not deny-only")
		}
	}
	for _, p := range s.EnabledPrivileges {
		if p != "SeChangeNotifyPrivilege" {
			return fmt.Errorf("unexpected enabled privilege: %s", p)
		}
	}
	return nil
}
