package ice

import (
	. "reflect"
	"sync"

	"github.com/blitz-frost/errors"
	"github.com/blitz-frost/io"
)

// Used to uniquely mark custom interface conversions.
const customInterface byte = 25

// Type registration must be completed before Block generation.
type Registry struct {
	conv map[Type]byte // maps types to unique ids
	inv  map[byte]Type // inverse of conv

	interfaceConv map[Type]Converter // maps interface types to custom converters
	interfaceInv  map[Type]Inverter
}

func RegistryMake() Registry {
	// these are mostly the same values as the reflect.Kind constants
	conv := map[Type]byte{
		// Invalid is used to mark nil interfaces
		TypeFor[bool]():       1,
		TypeFor[int]():        2,
		TypeFor[int8]():       3,
		TypeFor[int16]():      4,
		TypeFor[int32]():      5,
		TypeFor[int64]():      6,
		TypeFor[uint]():       7,
		TypeFor[uint8]():      8,
		TypeFor[uint16]():     9,
		TypeFor[uint32]():     10,
		TypeFor[uint64]():     11,
		TypeFor[uintptr]():    12,
		TypeFor[float32]():    13,
		TypeFor[float64]():    14,
		TypeFor[complex64]():  15,
		TypeFor[complex128](): 16,
		TypeFor[any]():        20, // Interface value
		TypeFor[string]():     24,
		// 25 is used to mark custom interface conversions
	}

	inv := make(map[byte]Type)
	for t, b := range conv {
		inv[b] = t
	}

	x := Registry{
		conv:          conv,
		inv:           inv,
		interfaceConv: make(map[Type]Converter),
		interfaceInv:  make(map[Type]Inverter),
	}

	// caches used when dealing with "any"
	var (
		anyConv    = make(map[Type]string)
		anyConvMux sync.RWMutex
	)

	anyConverter := func(blk *Block, v Value) error {
		// to get here, this must have been a non-registered type

		convert := func(s string) error {
			if err := blk.Freeze(s); err != nil {
				return err
			}
			return blk.Encode(v)
		}

		// check cache

		t := v.Type()

		anyConvMux.RLock()
		s, ok := anyConv[t]
		anyConvMux.RUnlock()

		if ok {
			return convert(s)
		}

		// build and cache type representation

		b := make([]byte, 0, 8)

		var walk func(Type) error

		eval := func(t Type) error {
			if t.Name() != "" {
				// non-registered defined types can't be decoded
				return errors.Simple("type is defined")
			}

			k := t.Kind()
			b = append(b, byte(k))

			switch k {
			case Array:
				b = metaAppend(b, uint64(t.Len()))
				fallthrough
			case Pointer, Slice:
				elem := t.Elem()
				return walk(elem)
			case Chan:
				b = append(b, byte(t.ChanDir()))
				elem := t.Elem()
				return walk(elem)
			case Map:
				key := t.Key()
				if err := walk(key); err != nil {
					return err
				}

				elem := t.Elem()
				return walk(elem)
			}

			return errors.Simple("invalid type")
		}

		walk = func(t Type) error {
			// subtypes might be registered
			id, ok := x.conv[t]
			if ok {
				b = append(b, id)
				return nil
			}

			return eval(t)
		}

		if err := eval(t); err != nil {
			return err
		}

		s = string(b)

		anyConvMux.Lock()
		anyConv[t] = s
		anyConvMux.Unlock()

		return convert(s)
	}

	var (
		anyInv    = make(map[string]Type)
		anyInvMux sync.RWMutex
	)

	anyInverter := func(blk *Block) (Value, error) {
		s, err := Thaw[string](blk)
		if err != nil {
			return Value{}, err
		}

		// check cache

		anyInvMux.RLock()
		t, ok := anyInv[s]
		anyInvMux.RUnlock()

		if ok {
			return blk.Decode(t)
		}

		// parse type and cache it

		// keep original for caching
		ss := s

		var parse func() (Type, error)
		parse = func() (Type, error) {
			b := ss[0]
			ss = ss[1:]

			t, ok := x.inv[b]
			if ok {
				return t, nil
			}

			switch Kind(b) {
			case Array:
				bb := []byte(ss[:metaSize])
				l := metaRead(&bb[0])

				ss = ss[metaSize:]
				elem, err := parse()
				if err != nil {
					return nil, err
				}

				return ArrayOf(int(l), elem), nil
			case Chan:
				dir := ChanDir(ss[0])
				ss = ss[1:]

				elem, err := parse()
				if err != nil {
					return nil, err
				}

				return ChanOf(dir, elem), nil
			case Map:
				key, err := parse()
				if err != nil {
					return nil, err
				}

				elem, err := parse()
				if err != nil {
					return nil, err
				}

				return MapOf(key, elem), nil
			case Pointer:
				elem, err := parse()
				if err != nil {
					return nil, err
				}

				return PointerTo(elem), nil
			case Slice:
				elem, err := parse()
				if err != nil {
					return nil, err
				}

				return SliceOf(elem), nil
			}

			return nil, errors.Simple("unknown type representation")
		}

		t, err = parse()
		if err != nil {
			return Value{}, err
		}

		anyInvMux.Lock()
		anyInv[ss] = t
		anyInvMux.Unlock()

		return blk.Decode(t)
	}

	t := TypeFor[any]()
	x.interfaceConv[t] = anyConverter
	x.interfaceInv[t] = anyInverter

	return x
}

// Block returns a valid empty Block.
func (x Registry) Block() Block {
	// stack and heap start with their own lengths; initially unknown so just allocate the space
	stack := make([]byte, metaSize)
	heap := make([]byte, metaSize)
	return blockMake(stack, heap, x)
}

// BlockOf initializes a Block value on top of what is assumed to be a valid raw binary encoding.
func (x Registry) BlockOf(b []byte) Block {
	i := metaRead(&b[0])
	return blockMake(b[:i], b[i:], x)
}

// BlockFrom reads an entire Block from r.
func (x Registry) BlockFrom(r io.Reader) (Block, error) {
	size := make([]byte, metaSize)

	// read stack size
	if err := r.Read(size); err != nil {
		return Block{}, err
	}

	// allocate and read stack
	stack := make([]byte, metaRead(&size[0]))
	copy(stack, size)
	if err := r.Read(stack[metaSize:]); err != nil {
		return Block{}, err
	}

	// read heap size
	if err := r.Read(size); err != nil {
		return Block{}, err
	}

	// allocate and read heap
	heap := make([]byte, metaRead(&size[0]))
	copy(heap, size)
	if err := r.Read(heap[metaSize:]); err != nil {
		return Block{}, err
	}

	return blockMake(stack, heap, x), nil
}

// Concrete registers a non-interface type. At must 230 types can be registered, including interfaces.
// Repeated types are ignored.
//
// Not concurrent safe with itself or other methods.
func (x Registry) Concrete(t Type) error {
	if t.Kind() == Interface {
		return errors.Simple("type is not concrete")
	}

	return x._register(t)
}

// Interface registers a custom interface type conversion, which will be used if the concrete type of a value has not been registered.
// The Converter will receive a value of concrete type, not the interface being registered.
// The Inverter should return a value of concrete type.
// Note that different interface types are not equal, even if they are equivalent.
//
// t may not be the built-in "any". Repeated calls for the same type will replace the old one.
//
// Not concurrent safe with itself or other methods.
func (x Registry) Interface(t Type, conv Converter, inv Inverter) error {
	if t.Kind() != Interface {
		return errors.Simple("type is not an interface")
	}
	if t == TypeFor[any]() {
		return errors.Simple("may not register built-in any")
	}

	if err := x._register(t); err != nil {
		return err
	}

	x.interfaceConv[t] = conv
	x.interfaceInv[t] = inv

	return nil
}

func (x Registry) _register(t Type) error {
	if _, ok := x.conv[t]; ok {
		// ignore duplicates
		return nil
	}

	const reserved = 8 // gaps in the map

	n := len(x.conv)
	if n == 256-reserved {
		return errors.Simple("overflow")
	}

	id := byte(n + reserved)

	x.conv[t] = id
	x.inv[id] = t

	return nil
}

func RegisterConcrete[T any](x Registry) error {
	t := TypeFor[T]()
	return x.Concrete(t)
}

func RegisterInterface[T any](x Registry, conv Converter, inv Inverter) error {
	t := TypeFor[T]()
	return x.Interface(t, conv, inv)
}
