package velox

import "unsafe"

// assignments returns the assignments of set, sharing its memory: a Setter's
// only field is its Assignment.
func assignments[E any](set []Setter[E]) []Assignment {
	return unsafe.Slice((*Assignment)(unsafe.Pointer(unsafe.SliceData(set))), len(set))
}
