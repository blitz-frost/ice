package ice

import (
	"errors"
	"fmt"
	. "reflect"
	"strconv"
	"sync"
)

// simple in the sense that knowing the kind is enough to identify the base type
var simpleTypes = map[Kind]Type{
	Bool:       TypeOf(false),
	Int:        TypeOf(0),
	Int8:       TypeOf(int8(0)),
	Int16:      TypeOf(int16(0)),
	Int32:      TypeOf(int32(0)),
	Int64:      TypeOf(int64(0)),
	Uint:       TypeOf(uint(0)),
	Uint8:      TypeOf(byte(0)),
	Uint16:     TypeOf(uint16(0)),
	Uint32:     TypeOf(uint32(0)),
	Uint64:     TypeOf(uint64(0)),
	Uintptr:    TypeOf(uintptr(0)),
	Float32:    TypeOf(float32(0)),
	Float64:    TypeOf(0.0),
	Complex64:  TypeOf(complex64(complex(0, 0))),
	Complex128: TypeOf(complex(0, 0)),
	String:     TypeOf(""),
	Interface:  TypeOf((*any)(nil)).Elem(),
}

var baseType = TypeOf(base{})

var (
	errBaseEmpty      = errors.New("empty base")
	errBaseIncomplete = errors.New("incomplete base")
)

// base describes a type's structure. Two types with the same base type will map to the same base.
type base []byte

type mapping struct {
	baseMap map[Type]base // known bases mapped by the types they represent
	baseMux sync.RWMutex

	typeMap map[string]Type // known types mapped by their base; uses strings since they are comparable, unlike byte slices
	typeMux sync.RWMutex
}

func mappingNew(m map[Type]byte) (*mapping, error) {
	// copy simple unnamed types
	baseMap := make(map[Type]base)
	typeMap := make(map[string]Type)
	for k, t := range simpleTypes {
		b := base{byte(k)}
		baseMap[t] = b
		typeMap[string(b)] = t
	}

	// add custom mapping
	for t, v := range m {
		if v <= byte(UnsafePointer) {
			return nil, errors.New("a type may not map to a reserved ID")
		}

		b := base{v}
		if _, ok := typeMap[string(b)]; ok {
			return nil, errors.New("mapping is not unique")
		}

		baseMap[t] = b
		typeMap[string(b)] = t
	}

	return &mapping{
		baseMap: baseMap,
		typeMap: typeMap,
	}, nil
}

func (x *mapping) baseOf(t Type) base {
	x.baseMux.RLock()
	o, ok := x.baseMap[t]
	if ok {
		// return already known base
		x.baseMux.RUnlock()
		return o
	}
	x.baseMux.RUnlock()

	k := t.Kind()
	o = base{byte(k)}
	switch k {
	case Array:
		// length + element base
		o = metaAppend(o, uint64(t.Len()))
		fallthrough
	case Pointer, Slice:
		// element base
		o = append(o, x.baseOf(t.Elem())...)
	case Chan:
		// direction + element base
		o = append(o, byte(t.ChanDir()))
		o = append(o, x.baseOf(t.Elem())...)
	case Func:
		if t.IsVariadic() {
			o = append(o, 0)
		} else {
			o = append(o, 1)
		}

		// input number + input bases, output number + output bases
		n := t.NumIn()
		o = metaAppend(o, uint64(n))
		for i := 0; i < n; i++ {
			o = append(o, x.baseOf(t.In(i))...)
		}

		n = t.NumOut()
		o = metaAppend(o, uint64(n))
		for i := 0; i < n; i++ {
			o = append(o, x.baseOf(t.Out(i))...)
		}
	case Map:
		// key base + element base
		o = append(o, x.baseOf(t.Key())...)
		o = append(o, x.baseOf(t.Elem())...)
	case Struct:
		// field number + field bases
		n := t.NumField()
		o = metaAppend(o, uint64(n))
		for i := 0; i < n; i++ {
			o = append(o, x.baseOf(t.Field(i).Type)...)
		}
	}

	// add to known before returning
	x.baseMux.Lock()
	x.baseMap[t] = o
	x.baseMux.Unlock()

	return o
}

// typeOf returns the Type corresponding to a base
func (x *mapping) typeOf(b base) (Type, error) {
	x.typeMux.RLock()
	o, ok := x.typeMap[string(b)]
	if ok {
		// return already known type
		x.typeMux.RUnlock()
		return o, nil
	}
	x.typeMux.RUnlock()

	o, n, err := x.typeOfEX(b)
	if err != nil {
		return nil, err
	}
	if n < len(b) {
		// reject cases where a base contains more than a valid type
		// this prevents saving the same type under different bases, and would indicate an error has occured somewhere anyway
		return nil, errors.New("base not of expected size")
	}

	// save resulting type for later
	// there's somewhat of a risk that multiple goroutines will save the same base if they look for it at the same time, but no biggie
	x.typeMux.Lock()
	x.typeMap[string(b)] = o
	x.typeMux.Unlock()

	return o, nil
}

// typeOfEX does the actual work of interpreting a base, also returning the number of bytes used
// only makes rudimentary checks, and does not save into the map, since subelement types might receive a base that contains more than themselves
func (x *mapping) typeOfEX(b base) (Type, int, error) {
	if len(b) == 0 {
		return nil, 0, errBaseEmpty
	}

	x.typeMux.RLock()
	// simple and registered map as a single byte
	if t, ok := x.typeMap[string(b[0:1])]; ok {
		x.typeMux.RUnlock()
		return t, 1, nil
	}
	x.typeMux.RUnlock()

	var (
		o Type
		n int
	)
	switch Kind(b[0]) {
	case Array:
		n = metaSize + 1
		if len(b) < n {
			return nil, 0, errBaseIncomplete
		}
		elem, m, err := x.typeOfEX(b[n:])
		if err != nil {
			return nil, 0, fmt.Errorf("array element: %w", err)
		}
		n += m
		l := metaRead(&b[1])
		o = ArrayOf(int(l), elem)
	case Chan:
		n = 2
		if len(b) < n {
			return nil, 0, errBaseIncomplete
		}
		elem, m, err := x.typeOfEX(b[n:])
		if err != nil {
			return nil, 0, fmt.Errorf("chan element: %w", err)
		}
		n += m
		o = ChanOf(ChanDir(b[1]), elem)
	case Func:
		n = metaSize + 2
		if len(b) < n {
			return nil, 0, errBaseIncomplete
		}

		isVar := false
		if b[1] == 1 {
			isVar = true
		}

		// parse inputs
		num := metaRead(&b[2])
		in := make([]Type, num)
		for i := range in {
			t, m, err := x.typeOfEX(b[n:])
			if err != nil {
				return nil, 0, fmt.Errorf("func input %v: %w", i, err)
			}
			in[i] = t
			n += m
		}

		if len(b) < n+metaSize {
			return nil, 0, errBaseIncomplete
		}

		// parse outputs
		num = metaRead(&b[n])
		n += metaSize
		out := make([]Type, num)
		for i := range out {
			t, m, err := x.typeOfEX(b[n:])
			if err != nil {
				return nil, 0, fmt.Errorf("func output %v: %w", i, err)
			}
			out[i] = t
			n += m
		}

		o = FuncOf(in, out, isVar)
	case Map:
		n = 1
		key, m, err := x.typeOfEX(b[n:])
		if err != nil {
			return nil, 0, fmt.Errorf("map key: %w", err)
		}
		n += m

		elem, m, err := x.typeOfEX(b[n:])
		if err != nil {
			return nil, 0, fmt.Errorf("map element: %w", err)
		}
		n += m

		o = MapOf(key, elem)
	case Pointer:
		n = 1
		elem, m, err := x.typeOfEX(b[n:])
		if err != nil {
			return nil, 0, fmt.Errorf("pointer element: %w", err)
		}
		n += m
		o = PointerTo(elem)
	case Slice:
		n = 1
		elem, m, err := x.typeOfEX(b[n:])
		if err != nil {
			return nil, 0, fmt.Errorf("slice element: %w", err)
		}
		n += m
		o = SliceOf(elem)
	case Struct:
		n = metaSize + 1
		if len(b) < n {
			return nil, 0, errBaseIncomplete
		}

		num := metaRead(&b[1])
		fields := make([]StructField, num)
		for i := range fields {
			t, m, err := x.typeOfEX(b[n:])
			if err != nil {
				return nil, 0, fmt.Errorf("struct field %v: %w", i, err)
			}
			fields[i].Name = "F" + strconv.Itoa(i)
			fields[i].Type = t
			n += m
		}

		o = StructOf(fields)
	default:
		return nil, 0, errors.New("invalid base")
	}

	return o, n, nil
}

// GenerateMapping takes a list of values, and returns an id mapping for the encountered.
// The mapping is guaranteed to be always be the same for the same input value types.
func GenerateMapping(v ...any) (map[Type]byte, error) {
	o := make(map[Type]byte)
	i := byte(UnsafePointer + 1) // skip reserved ids
	for _, val := range v {
		if i == 0 {
			// reached overflow
			return nil, errors.New("too many types")
		}
		t := TypeOf(val)
		if _, ok := o[t]; ok {
			// skip duplicate types
			continue
		}

		o[t] = i
		i++
	}

	return o, nil
}
