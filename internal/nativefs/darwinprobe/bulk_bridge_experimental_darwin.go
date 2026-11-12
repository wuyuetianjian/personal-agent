//go:build darwin && cgo && nativeprobe_darwin_bulk

package darwinprobe

/*
#include <sys/attr.h>
#include <sys/stat.h>
#include <sys/vnode.h>
#include <unistd.h>
#include <errno.h>
#include <stdint.h>
#include <string.h>

#define PA_BULK_MASK (ATTR_CMN_RETURNED_ATTRS | ATTR_CMN_ERROR | ATTR_CMN_NAME | ATTR_CMN_OBJTYPE | ATTR_CMN_FLAGS | ATTR_CMN_FILEID)
_Static_assert(sizeof(attribute_set_t) == 20, "attribute_set_t ABI");
_Static_assert(sizeof(attrreference_t) == 8, "attrreference_t ABI");
_Static_assert(sizeof(fsobj_type_t) == 4, "fsobj_type_t ABI");
_Static_assert(PA_BULK_MASK == 0xa2040009U, "attribute bits ABI");
_Static_assert(FSOPT_PACK_INVAL_ATTRS == 8, "packing ABI");
_Static_assert(SF_FIRMLINK == 0x800000, "firmlink ABI");
_Static_assert(VREG == 1 && VDIR == 2, "vnode types ABI");

typedef struct { int count; int error; } pa_bulk_result;
// Exactly one public SDK call. No descriptor ownership, allocation, looping,
// pointer retention, private symbol, pathname resolution or leaf open occurs.
static pa_bulk_result pa_query_bulk(int fd, void *buf, size_t size) {
  struct attrlist attrs;
  memset(&attrs, 0, sizeof(attrs));
  attrs.bitmapcount = ATTR_BIT_MAP_COUNT;
  attrs.commonattr = PA_BULK_MASK;
  int n = getattrlistbulk(fd, &attrs, buf, size, FSOPT_PACK_INVAL_ATTRS);
  int e = n < 0 ? errno : 0;
  pa_bulk_result result = {n, e};
  return result;
}
*/
import "C"

import (
	"syscall"
	"unsafe"
)

type bulkABI struct {
	Mask, Firmlink                                                               uint32
	AttributeSet, Reference, ObjectType, FileID, PackInvalid, Regular, Directory int
}

func nativeBulkABI() bulkABI {
	return bulkABI{Mask: C.PA_BULK_MASK, Firmlink: C.SF_FIRMLINK, AttributeSet: C.sizeof_attribute_set_t, Reference: C.sizeof_attrreference_t, ObjectType: C.sizeof_fsobj_type_t, FileID: C.sizeof_uint64_t, PackInvalid: C.FSOPT_PACK_INVAL_ATTRS, Regular: C.VREG, Directory: C.VDIR}
}
func queryNativeBulk(fd int, b []byte) (int, error) {
	if len(b) != bulkBufferSize {
		return 0, errBulkRefused
	}
	result := C.pa_query_bulk(C.int(fd), unsafe.Pointer(&b[0]), C.size_t(len(b)))
	if result.count < 0 {
		if result.error == 0 {
			return 0, errBulkRefused
		}
		return int(result.count), syscall.Errno(result.error)
	}
	if result.error != 0 {
		return 0, syscall.Errno(result.error)
	}
	return int(result.count), nil
}
