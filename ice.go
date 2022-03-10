// Package ice defines an encoding of Go values that tries to mirror the Go memory model itself.
// An encoded value is called a "block" and can be thought of as a mini heap dump.
//
// The goal of ice is to provide a binary format with efficient random access, for use in storage or communication between programs.
package ice

import (
	"reflect"
	"unsafe"

	"github.com/blitz-frost/conv"
)

func init() {
	scheme := conv.MakeScheme(block{})
	scheme.Load(arrayTo)
	scheme.Load(boolTo)
	scheme.Load(mapTo)
	scheme.Load(numberTo)
	scheme.Load(pointerTo)
	scheme.Load(reflectTo)
	scheme.Load(reflectSliceTo)
	scheme.Load(sliceTo)
	scheme.Load(stringTo)
	scheme.Load(structTo)
	scheme.Build(&freeze)

	inverse := conv.MakeInverse(block{})
	inverse.Load(arrayFrom)
	inverse.Load(boolFrom)
	inverse.Load(mapFrom)
	inverse.Load(numberFrom)
	inverse.Load(pointerFrom)
	inverse.Load(reflectFrom)
	inverse.Load(reflectSliceFrom)
	inverse.Load(sliceFrom)
	inverse.Load(stringFrom)
	inverse.Load(structFrom)
	inverse.Build(&thaw)
}

func Marshal(v interface{}) ([]byte, error) {
	b := newBlock(nil, nil)
	if err := freeze(b, v); err != nil {
		return nil, err
	}
	b.commit(0) // commit stack size; needed to separate from heap by reader

	return append(b.stack, b.heap...), nil
}

func Unmarshal(v interface{}, b []byte) error {
	i := metaRead(&b[0])
	return thaw(v, *newBlock(b[0:i], b[i:]))
}

func arrayFrom(dst *conv.Array, src block) error {
	elem := dst.Elem()
	if isSolid(elem) {
		p := unsafe.Pointer(src.dataPtr())
		dst.UnsafeSet(p, 0)

		size := uint64(elem.Size()) * uint64(dst.Len())
		*src.i += size
		return nil
	}

	for i, n := 0, dst.Len(); i < n; i++ {
		p := dst.New()
		if err := thaw(p, src); err != nil {
			return err
		}
		dst.SetPtr(i, p)
	}

	return nil
}

func arrayTo(dst *block, src conv.Array) error {
	return arrayishTo(dst, src)
}

func arrayishTo(dst *block, src conv.ArrayInterface) error {
	elem := src.Elem()
	if isSolid(elem) {
		size := elem.Size() * uintptr(src.Len())
		dst.stack = memAppend(dst.stack, unsafe.Pointer(src.Unsafe()), size)
		return nil
	}

	for i, n := 0, src.Len(); i < n; i++ {
		if err := freeze(dst, src.Index(i)); err != nil {
			return err
		}
	}

	return nil
}

func boolFrom(dst *bool, src block) error {
	if src.data()[0] == 0 {
		*dst = false
	} else {
		*dst = true
	}

	*src.i++
	return nil
}

func boolTo(dst *block, src bool) error {
	if src {
		dst.stack = append(dst.stack, 1)
	} else {
		dst.stack = append(dst.stack, 0)
	}
	return nil
}

func isSolid(t reflect.Type) bool {
	k := t.Kind()
	if conv.IsNumeric(k) || k == reflect.Bool {
		return true
	}

	switch k {
	case reflect.Array:
		return isSolid(t.Elem())
	case reflect.Struct:
		for i, n := 0, t.NumField(); i < n; i++ {
			if !isSolid(t.Field(i).Type) {
				return false
			}
		}
		return true
	}

	return false
}

func mapFrom(dst *conv.Map, src block) error {
	end := src.metaRead()

	for *src.i < end {
		k := dst.NewKey()
		v := dst.NewValue()
		if err := thaw(k, src); err != nil {
			return err
		}
		if err := thaw(v, src); err != nil {
			return err
		}
		dst.SetPtr(k, v)
	}

	return nil
}

func mapTo(dst *block, src conv.Map) error {
	i := dst.metaReserve() // reserve map end

	// add (key, value) pairs one by one recursively
	// internal map layout is opaque, so don't attempt optimization
	r := src.Range()
	for r.Next() {
		if err := freeze(dst, r.Key()); err != nil {
			return err
		}
		if err := freeze(dst, r.Value()); err != nil {
			return err
		}
	}

	dst.commit(i)
	return nil
}

func memAppend(dst []byte, p unsafe.Pointer, size uintptr) []byte {
	b := unsafe.Slice((*byte)(p), size)
	return append(dst, b...)
}

func memCopy(dst []byte, p unsafe.Pointer, size uintptr) {
	b := unsafe.Slice((*byte)(p), size)
	copy(dst, b)
}

func metaAppend(dst []byte, m uint64) []byte {
	return memAppend(dst, unsafe.Pointer(&m), metaSize)
}

func metaCopy(dst []byte, m uint64) {
	memCopy(dst, unsafe.Pointer(&m), metaSize)
}

func metaRead(src *byte) uint64 {
	return *(*uint64)(unsafe.Pointer(src))
}

func numberFrom(dst *conv.Number, src block) error {
	dst.UnsafeSet(unsafe.Pointer(src.dataPtr()))
	*src.i += uint64(dst.Size())
	return nil
}

func numberTo(dst *block, src conv.Number) error {
	size := src.Size()
	dst.stack = memAppend(dst.stack, unsafe.Pointer(src.Unsafe()), size)
	return nil
}

func pointerFrom(dst *conv.Pointer, src block) error {
	// check if pointer is already known
	i := src.metaRead()
	p, ok := src.thawed[i]
	if ok {
		// if known, just copy it and return
		dst.Set(p)
		return nil
	}

	// decode the pointer's value from heap
	p = dst.Value()
	tmp := block{
		stack:  src.heap,
		heap:   src.heap,
		thawed: src.thawed,
		i:      new(uint64),
		frozen: src.frozen, // not really important
	}
	*tmp.i = i // keep original i separate, to add it to the table

	if err := thaw(p, tmp); err != nil {
		return err
	}

	// register pointer for future reuse
	src.thawed[i] = p

	return nil
}

func pointerTo(dst *block, src conv.Pointer) error {
	// check if pointer is already known
	p := src.Value()
	i, ok := dst.frozen[p]
	if ok {
		// if known, just write its value and return
		dst.stack = metaAppend(dst.stack, i)
		return nil
	}

	// register pointer, adding its value to the heap

	// are we in a "heap block"?
	//
	// in a heap block, dst.heap is only used to check the block type
	// otherwise all operations are on "stack", which is actually some other block's heap
	//
	// pointer conversions are currently the only ones that need to be aware of this distinction
	//
	// the stack and heap should always be distinct in a normal block; if they start at the same address, we can conclude that we are in a heap block (see recursion below)
	// this is why the heap must always contain at least one byte
	var heap *[]byte // will point to the "stack" in a heap block
	i = uint64(len(dst.heap))
	if &dst.stack[0] == &dst.heap[0] {
		// if we're in a heap block, we add a pointer pointing after itself, where its value will be added
		// the normal approach would result in a pointer pointing to itself

		heap = &dst.stack
		i += metaSize
	} else {
		// if we're in a normal block, we add a pointer to the stack, pointing at the end of the heap, where the pointer's value will be added

		heap = &dst.heap
	}

	dst.frozen[p] = i
	dst.stack = metaAppend(dst.stack, i)

	// we want to freeze pointer values into the heap
	// use a "heap block" to use normal code
	// this will update the table automatically; we also recover its "stack"
	tmp := block{
		stack:  *heap,
		heap:   *heap,
		frozen: dst.frozen,
		thawed: dst.thawed, // not really important
		i:      dst.i,      // not really important
	}

	if err := freeze(&tmp, src.Elem()); err != nil {
		return err
	}

	*heap = tmp.stack
	return nil
}

func reflectFrom(dst *reflect.Value, src block) error {
	return thaw(dst.Interface(), src)
}

func reflectTo(dst *block, src reflect.Value) error {
	return freeze(dst, src.Interface())
}

func reflectSliceFrom(dst *[]reflect.Value, src block) error {
	for i := 0; i < len(*dst); i++ {
		if err := thaw((*dst)[i].Interface(), src); err != nil {
			return err
		}
	}
	return nil
}

func reflectSliceTo(dst *block, src []reflect.Value) error {
	for i := 0; i < len(src); i++ {
		if err := freeze(dst, src[i].Interface()); err != nil {
			return err
		}
	}
	return nil
}

func sliceFrom(dst *conv.Slice, src block) error {
	elem := dst.Elem()

	end := src.metaRead()

	if isSolid(elem) {
		n := int(end-*src.i) / int(elem.Size())
		p := unsafe.Pointer(src.dataPtr())
		dst.UnsafeSet(p, n)
		*src.i = end
		return nil
	}

	for *src.i < end {
		p := dst.New()
		if err := thaw(p, src); err != nil {
			return err
		}
		dst.AppendPtr(p)
	}

	return nil
}

func sliceTo(dst *block, src conv.Slice) error {
	i := dst.metaReserve() // save current index
	defer func() {
		dst.commit(i)
	}()
	return arrayishTo(dst, src)
}

func stringFrom(dst *string, src block) error {
	end := src.metaRead()
	*dst = string(src.dataSlice(end))
	*src.i = end
	return nil
}

func stringTo(dst *block, src string) error {
	end := uint64(len(dst.stack)+len(src)) + metaSize
	dst.stack = metaAppend(dst.stack, end)
	dst.stack = append(dst.stack, src...)
	return nil
}

func structFrom(dst *conv.Struct, src block) error {
	if t := dst.Type(); isSolid(t) {
		p := unsafe.Pointer(src.dataPtr())
		dst.UnsafeSet(p)
		size := uint64(t.Size())
		*src.i += size
		return nil
	}

	iter := dst.RangeEx()
	for iter.Next() {
		p := iter.New()
		if err := thaw(p, src); err != nil {
			return err
		}
		iter.SetPtr(p)
	}

	return nil
}

func structTo(dst *block, src conv.Struct) error {
	// note: since the goal of this package is ultimately to facilitate communication between programs, we opt for complete struct encoding/decoding, including unexported fields

	// a solid struct can be directly memory copied
	if t := src.Type(); isSolid(t) {
		p := unsafe.Pointer(src.Unsafe())
		dst.stack = memAppend(dst.stack, p, t.Size())
		return nil
	}

	// a fluid struct needs to be frozen field by field
	iter := src.RangeEx()
	for iter.Next() {
		if err := freeze(dst, iter.Value()); err != nil {
			return err
		}
	}

	return nil
}

type block struct {
	stack []byte  // encoded data
	i     *uint64 // working index in stack

	// unsafe.Pointer could also be used for the pointer maps,
	// but the only reliable check we can make with the current Go runtime is direct pointer comparison.
	// This leaves out advanced mechanics like accounting for overlaping memory areas.
	//
	// Thus the only utility of unsafe.Pointer would be to allow overlap of different pointers on the heap.
	// This is because pointers of different types will not be equal, even if they share the same address,
	// but different types are encoded differently; it does not seem worth dealing with, as this would be exceedingly rare in normal Go programs.
	frozen map[interface{}]uint64 // map already frozen pointers to heap index
	thawed map[uint64]interface{} // map heap index to already thawed pointers
	heap   []byte                 // encoded pointer data
}

func newBlock(stack, heap []byte) *block {
	// the heap must always contain an unused first byte
	// this allows always being able to take its address -> check if heap and stack are equal
	// important in pointer freezing
	if len(heap) == 0 {
		heap = make([]byte, 1)
	}

	i := new(uint64)
	*i = metaSize // stack always starts with its own length; skipped for actual data
	if len(stack) == 0 {
		stack = make([]byte, metaSize)
	}
	return &block{
		stack:  stack,
		i:      i,
		frozen: make(map[interface{}]uint64),
		thawed: make(map[uint64]interface{}),
		heap:   heap,
	}
}

// commit writes the current stack size to the given stack index.
// Used to fill in previously reserved meta bytes, registering the end of the last encoded stack value.
func (x block) commit(i int) {
	metaCopy(x.stack[i:], uint64(len(x.stack)))
}

// data returns a slice of the stack starting at the current working index.
func (x block) data() []byte {
	return x.stack[*x.i:]
}

// dataPtr returns a pointer to the current working index in the stack.
func (x block) dataPtr() *byte {
	return &x.stack[*x.i]
}

// dataSlice returns a slice up to the specified index of the current working stack.
func (x block) dataSlice(end uint64) []byte {
	return x.stack[*x.i:end]
}

// metaRead returns the meta value at the current index, then advances the index past it.
func (x block) metaRead() uint64 {
	p := x.dataPtr()
	*x.i += metaSize
	return metaRead(p)
}

// metaReserve appends an empty meta value to the stack, reserving it for later.
// Returns its index.
func (x *block) metaReserve() int {
	i := len(x.stack)
	x.stack = append(x.stack, make([]byte, metaSize)...)
	return i
}

var (
	freeze func(*block, interface{}) error
	thaw   func(interface{}, block) error
)

// to ensure a level of portability, meta values are encoded as uint64
const (
	metaSize = 8 // size of a single meta value, in bytes
)
