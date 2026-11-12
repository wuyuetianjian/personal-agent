package tokencheck

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Private per-call injection isolates API buffer mechanics from token policy.
// Native callers always bind the actual GetTokenInformation implementation.
type informationQuery func([]byte) (uint32, error)

// TokenElevation and TokenElevationType are exactly one DWORD. Unlike variable
// records, their API contract does not require a successful NULL sizing probe.
// Query failures never turn into the legitimate numeric value zero.
func queryScalar(query informationQuery) (uint32, error) {
	b := make([]byte, 4)
	n, e := query(b)
	if e != nil {
		return 0, e
	}
	if n != 4 {
		return 0, fmt.Errorf("invalid scalar token length %d (want 4)", n)
	}
	return binary.LittleEndian.Uint32(b), nil
}

// Variable token records still require the documented insufficient-buffer
// sizing result. Never reinterpret BAD_LENGTH as a general success condition,
// retry indefinitely, or expose unwritten bytes beyond the returned size.
func queryVariable(query informationQuery, insufficient error) ([]byte, error) {
	n, e := query(nil)
	if !errors.Is(e, insufficient) {
		if e != nil {
			return nil, fmt.Errorf("unexpected token sizing result: %w", e)
		}
		return nil, fmt.Errorf("unexpected successful NULL token sizing query")
	}
	if n == 0 || n > 1<<20 {
		return nil, fmt.Errorf("invalid token allocation size %d", n)
	}
	b := make([]byte, n)
	used, e := query(b)
	if e != nil {
		return nil, e
	}
	if used == 0 || used > uint32(len(b)) {
		return nil, fmt.Errorf("invalid returned token length %d (capacity %d)", used, len(b))
	}
	return b[:used], nil
}
