// Package ice defines an encoding of Go values that tries to mirror the Go memory model itself.
// An encoding is called a "block" and can be thought of as a mini heap dump.
//
// The goal of ice is to provide a binary format with efficient random access, for use in storage or communication between programs.
//
// This package imports all "reflect" identifiers explicitly.
//
// Following is a brief description of the binary format
//
// The top level form of an ice block is
//	[stack][heap]
// Where both stack and heap are of the form
//	[end][data]
// [x end] is the end index of x in the raw bytes. This is always a uint64.
//
// Values are sequentially encoded onto the stack. Values pointed at by pointers are encoded into the heap.
// The stack and heap generally follow the same rules. See pointer handling below for more information on the heap's role and usage.
//
// A type is said to be solid if all its data is guaranteed to be contained in a single memory block, whose size is knowable just by knowing the type.
// Boolean and numeric types are solid, as well as all arrays and structs that are recursively composed of these types.
//
// Solid types are encoded through direct memory reading. A consequence of this is that this package is not portable between architectures.
//
// Arrays are encoded as
//	[index_0][index_1]...
//
// Slices are encoded as
//	[end][index_0][index_1]...
//
// Strings are equivalent to byte slices.
//
// Maps are encoded as
//	[end][key_0][value_0][key_1][value_1]...
//
// Structs, including both exported and non-exported fields, are encoded as
//	[field_0][field_1]...
//
// Channels must be bidirectional, and will be drained. They are encoded as
//	[cap][data end][data_0][data_1]...[closed]
//
// Pointers are handled a bit differently. The value that is being pointed at is encoded onto the heap, while its index in the heap functions as the value of the pointer itself.
// This pointer value might end up itself on the heap, if the pointer is reached by dereferencing another pointer. When encoding or decoding, encountered pointers are remembered and reused if encountered again.
// This also guards against infinite loops in cyclical data. Unsafe pointers are currently not allowed.
//
// Function and interface types are invalid. Mechanisms for dealing with interfaces might be added in the future.
//
// Decoded values are generally views of the binary data. Therefore, a byte slice used for decoding should not be used for anything else.
//
// Using the "Freeze" and "Thaw" Block methods is equivalent to encoding or decoding strucs, one field at a time. A Block must never be used for both freezing and thawing. This restriction may be lifted in the future.
package ice

import (
	. "reflect"
	"unsafe"

	"github.com/blitz-frost/conv"
)

// to ensure a level of portability, meta values are encoded as uint64
const (
	metaSize = 8 // size of a single meta value, in bytes
)

var (
	libConv    *conv.Library[converter]
	schemeConv = conv.Scheme[converter]{}

	libInv    *conv.Library[inverter]
	schemeInv = conv.Scheme[inverter]{}
)

var typeBlock = TypeOf(Block{})

type (
	converter = func(*Block, Value) error
	inverter  = func(Block) (Value, error)
)

func init() {
	schemeConv.Use(invalidConv)
	schemeInv.Use(invalidInv)

	schemeConv.Use(solidConv)
	schemeInv.Use(solidInv)

	schemeConv.Use(stringConv)
	schemeInv.Use(stringInv)

	schemeConv.Use(arrayConv)
	schemeInv.Use(arrayInv)

	schemeConv.Use(sliceConv)
	schemeInv.Use(sliceInv)

	schemeConv.Use(mapConv)
	schemeInv.Use(mapInv)

	schemeConv.Use(structConv)
	schemeInv.Use(structInv)

	schemeConv.Use(pointerConv)
	schemeInv.Use(pointerInv)

	schemeConv.Use(chanConv)
	schemeInv.Use(chanInv)

	libConv = conv.NewLibrary[converter](schemeConv.Build, nil)
	libInv = conv.NewLibrary[inverter](schemeInv.Build, nil)
}

// A Block is a container for encoded data.
// Values can be added or retrieved from the Block through the "Freeze" and "Thaw" methods.
// A Block can be used either for freezing or thawing, but not both.
type Block struct {
	stack []byte  // encoded data
	i     *uint64 // working index in stack

	// unsafe.Pointer could also be used for the pointer maps,
	// but the only reliable check we can make with the current Go runtime is direct pointer comparison.
	// This leaves out advanced mechanics like accounting for overlaping memory areas.
	//
	// Thus the only utility of unsafe.Pointer would be to allow superposition of different pointers on the heap.
	// This is because pointers of different types will not be equal, even if they share the same address,
	// but different types are encoded differently; it does not seem worth dealing with, as this would be exceedingly rare in normal Go programs.
	frozen map[any]uint64 // map already frozen pointers to heap index
	thawed map[uint64]any // map heap index to already thawed pointers
	heap   []byte         // encoded pointer data
}

func FromBytes(b []byte) Block {
	i := metaRead(&b[0])
	return *newBlock(b[:i], b[i:])
}

func NewBlock() *Block {
	// stack and heap start with their own lengths; initially unknown so just allocate the space
	stack := make([]byte, metaSize)
	heap := make([]byte, metaSize)
	return newBlock(stack, heap)
}

func newBlock(stack, heap []byte) *Block {
	i := new(uint64)
	*i = metaSize

	return &Block{
		stack:  stack,
		i:      i,
		frozen: make(map[any]uint64),
		thawed: make(map[uint64]any),
		heap:   heap,
	}
}

func (x *Block) Freeze(v any) error {
	f := libConv.Get(TypeOf(v))
	return f(x, ValueOf(v))
}

func (x *Block) FreezeValue(v Value) error {
	f := libConv.Get(v.Type())
	return f(x, v)
}

func Thaw[T any](x Block) (T, error) {
	t := conv.TypeEval[T]()
	f := libInv.Get(t)
	v, err := f(x)
	if err != nil {
		var o T
		return o, err
	}
	return v.Interface().(T), nil
}

func (x Block) ThawValue(t Type) (Value, error) {
	f := libInv.Get(t)
	return f(x)
}

func (x Block) ToBytes() []byte {
	x.commit(0)                               // commit stack size
	metaCopy(x.heap[0:], uint64(len(x.heap))) // commit heap size

	return append(x.stack, x.heap...)
}

// commit writes the current stack size to the given stack index.
// Used to fill in previously reserved meta bytes, registering the end of the last encoded stack value.
func (x Block) commit(i int) {
	metaCopy(x.stack[i:], uint64(len(x.stack)))
}

// dataPtr returns a pointer to the current working index in the stack.
func (x Block) dataPtr() *byte {
	return &x.stack[*x.i]
}

// dataSlice returns a slice up to the specified index of the current working stack.
func (x Block) dataSlice(end uint64) []byte {
	return x.stack[*x.i:end]
}

// metaRead returns the meta value at the current index, then advances the index past it.
func (x Block) metaRead() uint64 {
	p := x.dataPtr()
	*x.i += metaSize
	return metaRead(p)
}

// metaReserve appends an empty meta value to the stack, reserving it for later.
// Returns its index.
func (x *Block) metaReserve() int {
	i := len(x.stack)
	x.stack = append(x.stack, make([]byte, metaSize)...)
	return i
}

func Marshal(v any) ([]byte, error) {
	return MarshalValue(ValueOf(v))
}

func MarshalValue(v Value) ([]byte, error) {
	x := NewBlock()
	err := x.FreezeValue(v)
	if err != nil {
		return nil, err
	}
	return x.ToBytes(), nil
}

func Unmarshal[T any](b []byte) (T, error) {
	x := FromBytes(b)
	return Thaw[T](x)
}

func UnmarshalValue(t Type, b []byte) (Value, error) {
	x := FromBytes(b)
	return x.ThawValue(t)
}

func arrayConv(t Type) (converter, bool) {
	if t.Kind() != Array {
		return nil, false
	}

	return arrayishConv(t), true
}

func arrayInv(t Type) (inverter, bool) {
	if t.Kind() != Array {
		return nil, false
	}

	f, _ := schemeInv.Build(t.Elem())
	return func(x Block) (Value, error) {
		o := New(t).Elem()
		for i, n := 0, o.Len(); i < n; i++ {
			elem, _ := f(x)
			o.Index(i).Set(elem)
		}
		return o, nil
	}, true
}

// Used by arrayConv and sliceConv. Not directly part of the conversion library. Doesn't make any type checks.
func arrayishConv(t Type) converter {
	f, _ := schemeConv.Build(t.Elem())
	return func(x *Block, v Value) error {
		n := v.Len()
		if n == 0 {
			return nil
		}

		for i := 0; i < n; i++ {
			f(x, v.Index(i))
		}

		return nil
	}
}

func chanConv(t Type) (converter, bool) {
	if t.Kind() != Chan {
		return nil, false
	}

	f, _ := schemeConv.Build(t.Elem())
	return func(x *Block, v Value) error {
		x.stack = metaAppend(x.stack, uint64(v.Cap()))

		i := x.metaReserve()
		for {
			elem, ok := v.TryRecv()
			if !ok {
				x.commit(i)

				closed := byte(0)
				if elem.IsValid() {
					closed = 1
				}
				x.stack = append(x.stack, closed)
				break
			}
			f(x, elem)
		}
		return nil
	}, true
}

func chanInv(t Type) (inverter, bool) {
	if t.Kind() != Chan {
		return nil, false
	}

	f, _ := schemeInv.Build(t.Elem())
	return func(x Block) (Value, error) {
		n := int(x.metaRead())
		o := MakeChan(t, n)

		end := x.metaRead()
		for *x.i < end {
			v, _ := f(x)
			o.Send(v)
		}

		if x.stack[*x.i] == 1 {
			o.Close()
		}
		*x.i++
		return o, nil
	}, true
}

func invalidConv(t Type) (converter, bool) {
	if !conv.Check(t, isValid) {
		return func(x *Block, v Value) error {
			return conv.ErrInvalid
		}, true
	}
	return nil, false
}

func invalidInv(t Type) (inverter, bool) {
	if !conv.Check(t, isValid) {
		return func(x Block) (Value, error) {
			return Value{}, conv.ErrInvalid
		}, true
	}
	return nil, false
}

// A solid type doesn't contain any pointers, and is stored in a single memory block.
func isSolid(t Type) bool {
	switch t.Kind() {
	case Chan, Map, Pointer, Slice, String: // Func and Interface should never pass the validity test
		return false
	}
	return true
}

// isValid returns false if the type is a function, interface or unsafe.Pointer type.
func isValid(t Type) bool {
	switch t.Kind() {
	case Chan:
		if t.ChanDir() != BothDir {
			return false
		}
	case Func, Interface, UnsafePointer:
		return false
	}
	return true
}

func mapConv(t Type) (converter, bool) {
	if t.Kind() != Map {
		return nil, false
	}

	fk, _ := schemeConv.Build(t.Key())
	fv, _ := schemeConv.Build(t.Elem())
	return func(x *Block, v Value) error {
		i := x.metaReserve()

		iter := v.MapRange()
		for iter.Next() {
			fk(x, iter.Key())
			fv(x, iter.Value())
		}

		x.commit(i)
		return nil
	}, true
}

func mapInv(t Type) (inverter, bool) {
	if t.Kind() != Map {
		return nil, false
	}

	fk, _ := schemeInv.Build(t.Key())
	fv, _ := schemeInv.Build(t.Elem())
	return func(x Block) (Value, error) {
		end := x.metaRead()
		o := MakeMap(t)

		for *x.i < end {
			k, _ := fk(x)
			v, _ := fv(x)
			o.SetMapIndex(k, v)
		}

		return o, nil
	}, true
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

func pointerConv(t Type) (converter, bool) {
	if t.Kind() != Pointer {
		return nil, false
	}

	f, _ := schemeConv.Build(t.Elem())
	return func(x *Block, v Value) error {
		// check if pointer is already known
		p := v.Interface()
		i, ok := x.frozen[p]
		if ok {
			// if known, just write its value and return
			x.stack = metaAppend(x.stack, i)
			return nil
		}

		// register pointer, adding its value to the heap

		// are we in a "heap block"?
		//
		// in a heap block, the "heap" memeber is only used to check the block type
		// otherwise all operations are on "stack", which is actually some other block's heap
		//
		// pointer conversions are currently the only ones that need to be aware of this distinction
		//
		// the stack and heap should always be distinct in a normal block; if they start at the same address, we can conclude that we are in a heap block (see recursion below)
		// this is why the heap must always contain at least one byte
		var heap *[]byte // will point to the "stack" in a heap block
		i = uint64(len(x.heap))
		if &x.stack[0] == &x.heap[0] {
			// if we're in a heap block, we add a pointer pointing after itself, where its value will be added
			// the normal approach would result in a pointer pointing to itself

			heap = &x.stack
			i += metaSize
		} else {
			// if we're in a normal block, we add a pointer to the stack, pointing at the end of the heap, where the pointer's value will be added

			heap = &x.heap
		}

		x.frozen[p] = i
		x.stack = metaAppend(x.stack, i)

		// we want to freeze pointer values into the heap
		// use a "heap block" to use normal code
		// this will update the table automatically; we also recover its "stack"
		tmp := Block{
			stack:  *heap,
			heap:   *heap,
			frozen: x.frozen,
			thawed: x.thawed, // not really important
			i:      x.i,      // not really important
		}

		f(&tmp, v.Elem())

		*heap = tmp.stack
		return nil
	}, true
}

func pointerInv(t Type) (inverter, bool) {
	if t.Kind() != Pointer {
		return nil, false
	}

	elem := t.Elem()
	f, _ := schemeInv.Build(elem)
	return func(x Block) (Value, error) {
		// check if pointer is already known
		i := x.metaRead()
		p, ok := x.thawed[i]
		if ok {
			// if known, just return it
			return ValueOf(p), nil
		}

		// decode the pointer's value from heap
		tmp := Block{
			stack:  x.heap,
			heap:   x.heap,
			thawed: x.thawed,
			i:      new(uint64),
			frozen: x.frozen, // not really important
		}
		*tmp.i = i // keep original i separate, to add it to the table

		ov, _ := f(tmp)
		o := New(elem)
		o.Elem().Set(ov)
		return o, nil
	}, true
}

func sliceConv(t Type) (converter, bool) {
	if t.Kind() != Slice {
		return nil, false
	}

	if elem := t.Elem(); conv.Check(elem, isSolid) {
		return func(x *Block, v Value) error {
			i := x.metaReserve()
			n := uintptr(v.Len())
			if n == 0 {
				x.commit(i)
				return nil
			}
			size := elem.Size() * n
			x.stack = memAppend(x.stack, v.UnsafePointer(), size)
			x.commit(i)
			return nil
		}, true
	}

	f := arrayishConv(t)
	return func(x *Block, v Value) error {
		i := x.metaReserve()
		f(x, v)
		x.commit(i)
		return nil
	}, true
}

func sliceInv(t Type) (inverter, bool) {
	if t.Kind() != Slice {
		return nil, false
	}

	elem := t.Elem()
	if conv.Check(elem, isSolid) {
		return func(x Block) (Value, error) {
			end := x.metaRead()
			n := int(end-*x.i) / int(elem.Size())
			if n == 0 {
				return MakeSlice(t, 0, 0), nil
			}
			p := unsafe.Pointer(x.dataPtr())
			*x.i = end

			arrayType := ArrayOf(n, elem)
			array := NewAt(arrayType, p).Elem()
			slice := array.Slice(0, n)
			return slice.Convert(t), nil // t might be a named slice type
		}, true
	}

	f, _ := schemeInv.Build(elem)
	return func(x Block) (Value, error) {
		end := x.metaRead()
		o := MakeSlice(t, 0, 0)
		for *x.i < end {
			v, _ := f(x)
			o = Append(o, v)
		}
		return o, nil
	}, true
}

func solidConv(t Type) (converter, bool) {
	if !conv.Check(t, isSolid) {
		return nil, false
	}

	return func(x *Block, v Value) error {
		var ptr Value
		if v.CanAddr() {
			ptr = v.Addr()
		} else {
			ptr = New(t)
			ptr.Elem().Set(v)
		}
		x.stack = memAppend(x.stack, ptr.UnsafePointer(), t.Size())
		return nil
	}, true
}

func solidInv(t Type) (inverter, bool) {
	if !conv.Check(t, isSolid) {
		return nil, false
	}

	return func(x Block) (Value, error) {
		ptr := unsafe.Pointer((x.dataPtr()))
		o := NewAt(t, ptr)
		*x.i += uint64(t.Size())
		return o.Elem(), nil
	}, true
}

func stringConv(t Type) (converter, bool) {
	if t.Kind() != String {
		return nil, false
	}

	return func(x *Block, v Value) error {
		n := v.Len()
		end := uint64(len(x.stack)+n) + metaSize
		x.stack = metaAppend(x.stack, end)

		if v.CanAddr() {
			// get pointer to slice start, without copying
			h := (*StringHeader)(v.Addr().UnsafePointer())
			p := unsafe.Pointer(h.Data)
			x.stack = memAppend(x.stack, p, uintptr(n))
			return nil
		}

		s := v.String()
		x.stack = append(x.stack, s...)
		return nil
	}, true
}

func stringInv(t Type) (inverter, bool) {
	if t.Kind() != String {
		return nil, false
	}

	return func(x Block) (Value, error) {
		end := x.metaRead()
		b := x.dataSlice(end)
		*x.i = end

		p := unsafe.Pointer(&b)
		return NewAt(t, p).Elem(), nil
	}, true
}

func structConv(t Type) (converter, bool) {
	if t.Kind() != Struct {
		return nil, false
	}

	type fieldConv struct {
		fn         converter
		t          Type
		isExported bool
		offset     uintptr
	}

	n := t.NumField()
	haveUnexported := false
	f := make([]fieldConv, n)
	for i := range f {
		field := t.Field(i)
		isExported := field.IsExported()
		haveUnexported = haveUnexported || !isExported
		ff, _ := schemeConv.Build(field.Type)
		f[i] = fieldConv{
			fn:         ff,
			t:          field.Type,
			isExported: isExported,
			offset:     field.Offset,
		}
	}

	if !haveUnexported {
		return func(x *Block, v Value) error {
			for i := range f {
				f[i].fn(x, v.Field(i))
			}
			return nil
		}, true
	}

	return func(x *Block, v Value) error {
		// need pointer to access unexported fields
		var p unsafe.Pointer
		if v.CanAddr() {
			p = v.Addr().UnsafePointer()
		} else {
			tmp := New(t)
			tmp.Elem().Set(v)
			p = tmp.UnsafePointer()
		}

		for i, field := range f {
			if field.isExported {
				field.fn(x, v.Field(i))
				continue
			}

			pf := unsafe.Add(p, field.offset)
			vf := NewAt(field.t, pf).Elem()
			field.fn(x, vf)
		}
		return nil
	}, true
}

func structInv(t Type) (inverter, bool) {
	if t.Kind() != Struct {
		return nil, false
	}

	type fieldInv struct {
		fn         inverter
		t          Type
		isExported bool
		offset     uintptr
	}

	n := t.NumField()
	haveUnexported := false
	f := make([]fieldInv, n)
	for i := range f {
		field := t.Field(i)
		isExported := field.IsExported()
		haveUnexported = haveUnexported || !isExported
		ff, _ := schemeInv.Build(field.Type)
		f[i] = fieldInv{
			fn:         ff,
			t:          field.Type,
			isExported: isExported,
			offset:     field.Offset,
		}
	}

	if !haveUnexported {
		return func(x Block) (Value, error) {
			o := New(t).Elem()
			for i := range f {
				vf, _ := f[i].fn(x)
				o.Field(i).Set(vf)
			}
			return o, nil
		}, true
	}

	return func(x Block) (Value, error) {
		oPtr := New(t)
		p := oPtr.UnsafePointer()
		o := oPtr.Elem()
		for i, field := range f {
			vf, _ := field.fn(x)
			if field.isExported {
				o.Field(i).Set(vf)
				continue
			}

			pf := unsafe.Add(p, field.offset)
			of := NewAt(field.t, pf).Elem()
			of.Set(vf)
		}
		return o, nil
	}, true
}
