package tokencheck

import (
	"encoding/binary"
	"errors"
	"testing"
)

func TestFixedTokenDWORDUsesOneExactBuffer(t *testing.T) {
	for _, value := range []uint32{0, 1, 3} {
		calls := 0
		got, e := queryScalar(func(b []byte) (uint32, error) {
			calls++
			if len(b) != 4 {
				return 4, errors.New("ERROR_BAD_LENGTH: fixed class rejects NULL probe")
			}
			binary.LittleEndian.PutUint32(b, value)
			return 4, nil
		})
		if e != nil || got != value || calls != 1 {
			t.Fatalf("fixed DWORD value=%d got=%d calls=%d err=%v", value, got, calls, e)
		}
	}
}
func TestFixedTokenDWORDRejectsErrorAndWrongReturnedLength(t *testing.T) {
	for _, n := range []uint32{0, 1, 3, 5, 1 << 20} {
		calls := 0
		got, e := queryScalar(func(b []byte) (uint32, error) {
			calls++
			if len(b) == 4 {
				binary.LittleEndian.PutUint32(b, 1)
			}
			return n, nil
		})
		if e == nil || got != 0 || calls != 1 {
			t.Fatalf("bad scalar length %d accepted/retried: %d %d %v", n, got, calls, e)
		}
	}
	fault := errors.New("native query failed")
	calls := 0
	got, e := queryScalar(func(b []byte) (uint32, error) { calls++; return 4, fault })
	if !errors.Is(e, fault) || got != 0 || calls != 1 {
		t.Fatal("query error converted to false/non-elevated")
	}
}

func TestVariableTokenQueryUsesBoundedReturnedLength(t *testing.T) {
	insufficient := errors.New("insufficient buffer")
	calls := 0
	b, e := queryVariable(func(b []byte) (uint32, error) {
		calls++
		if b == nil {
			return 16, insufficient
		}
		if len(b) != 16 {
			t.Fatal("wrong allocation")
		}
		b[0] = 7
		return 8, nil
	}, insufficient)
	if e != nil || len(b) != 8 || b[0] != 7 || calls != 2 {
		t.Fatalf("returned length not honored: len=%d calls=%d err=%v", len(b), calls, e)
	}
}
func TestVariableTokenQueryRejectsInvalidLengths(t *testing.T) {
	insufficient := errors.New("insufficient buffer")
	for _, n := range []uint32{0, 17, 1 << 21} {
		calls := 0
		b, e := queryVariable(func(b []byte) (uint32, error) {
			calls++
			if b == nil {
				return 16, insufficient
			}
			return n, nil
		}, insufficient)
		if e == nil || b != nil || calls != 2 {
			t.Fatalf("invalid second length %d accepted", n)
		}
	}
	for _, n := range []uint32{0, 1 << 21} {
		calls := 0
		b, e := queryVariable(func([]byte) (uint32, error) { calls++; return n, insufficient }, insufficient)
		if e == nil || b != nil || calls != 1 {
			t.Fatalf("invalid first length %d accepted", n)
		}
	}
	badLength := errors.New("bad length")
	for _, probeErr := range []error{nil, badLength} {
		calls := 0
		b, e := queryVariable(func([]byte) (uint32, error) { calls++; return 16, probeErr }, insufficient)
		if e == nil || b != nil || calls != 1 {
			t.Fatal("unexpected sizing result accepted")
		}
	}
}

func TestVariableTokenQueryPreservesNativeFailure(t *testing.T) {
	insufficient := errors.New("insufficient buffer")
	fault := errors.New("native failure")
	calls := 0
	b, e := queryVariable(func(b []byte) (uint32, error) {
		calls++
		if b == nil {
			return 16, insufficient
		}
		b[0] = 42
		return 8, fault
	}, insufficient)
	if b != nil || !errors.Is(e, fault) || calls != 2 {
		t.Fatal("partial result exposed or error lost")
	}
	b, e = queryVariable(func([]byte) (uint32, error) { return 16, fault }, insufficient)
	if b != nil || !errors.Is(e, fault) {
		t.Fatal("sizing native failure lost")
	}
}
