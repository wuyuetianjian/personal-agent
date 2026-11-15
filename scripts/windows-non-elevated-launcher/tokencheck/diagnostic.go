package tokencheck

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// DiagnoseCandidate emits only qualification metadata, never a token handle,
// account/user SID, environment or credentials. Integrity is the mandatory-label
// SID already queried by Inspect, not an account identifier. It does not alter
// the token or relax Validate. Partial snapshots remain inspection failures.
func DiagnoseCandidate(w io.Writer, stage string, s Snapshot, inspectionErr error) error {
	result := inspectionErr
	record := struct {
		Stage              string   `json:"stage"`
		Integrity          string   `json:"integrity"`
		Elevation          uint32   `json:"elevation"`
		ElevationType      uint32   `json:"elevation_type"`
		AdminAttributes    []uint32 `json:"admin_attributes"`
		EnabledPrivileges  []string `json:"enabled_privileges"`
		InspectionError    string   `json:"inspection_error,omitempty"`
		QualificationError string   `json:"qualification_error,omitempty"`
	}{Stage: stage, Integrity: s.Integrity, Elevation: s.Elevation, ElevationType: s.ElevationType, AdminAttributes: s.AdminAttributes, EnabledPrivileges: s.EnabledPrivileges}
	if inspectionErr != nil {
		record.InspectionError = inspectionErr.Error()
	} else {
		result = Validate(s)
		if result != nil {
			record.QualificationError = result.Error()
		}
	}
	raw, err := json.Marshal(record)
	if err != nil {
		return errors.Join(result, err)
	}
	_, err = fmt.Fprintf(w, "TOKEN_DIAGNOSTIC %s\n", raw)
	return errors.Join(result, err)
}
