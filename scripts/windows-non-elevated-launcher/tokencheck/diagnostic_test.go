package tokencheck

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestCandidateDiagnosticPreservesIntegrityAndRefusal(t *testing.T) {
	for _, il := range []string{"S-1-16-12288", "S-1-16-4096", "S-1-16-8448", "S-1-16-8192"} {
		s := Snapshot{ElevationType: 3, Integrity: il, AdminAttributes: []uint32{16}, EnabledPrivileges: []string{"SeChangeNotifyPrivilege"}}
		var out bytes.Buffer
		err := DiagnoseCandidate(&out, "restricted", s, nil)
		if (err == nil) != (il == "S-1-16-8192") {
			t.Fatalf("qualification changed for %s: %v", il, err)
		}
		var record map[string]json.RawMessage
		if e := json.Unmarshal(bytes.TrimPrefix(out.Bytes(), []byte("TOKEN_DIAGNOSTIC ")), &record); e != nil {
			t.Fatal(e)
		}
		var got string
		if e := json.Unmarshal(record["integrity"], &got); e != nil || got != il {
			t.Fatalf("lost raw integrity: %q %v", got, e)
		}
		for key := range record {
			switch key {
			case "stage", "integrity", "elevation", "elevation_type", "admin_attributes", "enabled_privileges", "inspection_error", "qualification_error":
			default:
				t.Fatalf("unexpected disclosure %q", key)
			}
		}
	}
}
func TestCandidateDiagnosticQueryAndWriterFailClosed(t *testing.T) {
	injected := errors.New("query failed")
	var out bytes.Buffer
	s := Snapshot{ElevationType: 3, Integrity: "S-1-16-8192"}
	if e := DiagnoseCandidate(&out, "current", s, injected); !errors.Is(e, injected) {
		t.Fatalf("query error lost: %v", e)
	}
	if e := DiagnoseCandidate(failedDiagnosticWriter{injected}, "current", s, nil); !errors.Is(e, injected) {
		t.Fatalf("output error lost: %v", e)
	}
}

type failedDiagnosticWriter struct{ err error }

func (w failedDiagnosticWriter) Write([]byte) (int, error) { return 0, w.err }
