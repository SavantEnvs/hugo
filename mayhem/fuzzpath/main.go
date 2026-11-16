// libFuzzer entry for the fuzzpath target: built as a c-archive with
// -gcflags=all=-d=libfuzzer and linked with clang -fsanitize=fuzzer (see mayhem/build.sh).
package main

/*
#include <stddef.h>
#include <stdint.h>
*/
import "C"

import (
	"unsafe"

	"github.com/gohugoio/hugo/mayhem/fuzzing"
)

//export LLVMFuzzerTestOneInput
func LLVMFuzzerTestOneInput(data *C.uint8_t, size C.size_t) C.int {
	return C.int(fuzzing.Path(C.GoBytes(unsafe.Pointer(data), C.int(size))))
}

func main() {}
