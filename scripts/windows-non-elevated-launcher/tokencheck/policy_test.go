package tokencheck

import "testing"

func TestQualificationPolicy(t *testing.T) {
	good := Snapshot{ElevationType: 3, Integrity: "S-1-16-8192", AdminAttributes: []uint32{16}, EnabledPrivileges: []string{"SeChangeNotifyPrivilege"}}
	if e := Validate(good); e != nil {
		t.Fatal(e)
	}
	absent := good
	absent.AdminAttributes = nil
	absent.ElevationType = 1
	if e := Validate(absent); e != nil {
		t.Fatal(e)
	}
	cases := []Snapshot{good, good, good, good, good, good, good}
	cases[0].Elevation = 1
	cases[1].ElevationType = 2
	cases[2].Integrity = "S-1-16-12288"
	cases[3].AdminAttributes = []uint32{20}
	cases[4].AdminAttributes = []uint32{0}
	cases[5].EnabledPrivileges = []string{"SeDebugPrivilege"}
	cases[6].ElevationType = 0
	for i, s := range cases {
		if Validate(s) == nil {
			t.Fatalf("unsafe case %d accepted", i)
		}
	}
}
