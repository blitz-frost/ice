package ice

import (
	. "reflect"

	"github.com/blitz-frost/errors"
	"github.com/blitz-frost/io"
)

// Type registration is not concurrent safe with Block generation or active Block usage.
type Registry struct {
	conv map[Type]byte // maps concrete types to unique ids; 0 and 1 are excluded
	inv  map[byte]Type // inverse of conv

	interfaceConv map[Type]Converter // maps interface types to custom converters
	interfaceInv  map[Type]Inverter
}

func RegistryMake() Registry {
	return Registry{
		conv:          make(map[Type]byte),
		inv:           make(map[byte]Type),
		interfaceConv: make(map[Type]Converter),
		interfaceInv:  make(map[Type]Inverter),
	}
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

// Concrete registers a non-interface type. At must 254 types can be registered.
// Repeated types are ignored.
//
// Not concurrent safe with itself or other methods.
func (x Registry) Concrete(t Type) error {
	if t.Kind() == Interface {
		return errors.Simple("type is not concrete")
	}

	n := len(x.conv)
	if n == 254 { // max byte variants - 2, since 0 and 1 are reserved
		return errors.Simple("overflow")
	}

	id := byte(n + 2) // ids start at 2

	if _, ok := x.conv[t]; ok {
		// ignore duplicates
		return nil
	}

	x.conv[t] = id
	x.inv[id] = t

	return nil
}

// InterfaceConverter registers a custom interface type conversion, which will be used if the concrete type of a value has not been registered.
// The Converter will receive a value of concrete type, not the interface being registered.
// Note that different interface types are not equal, even if they are equivalent.
//
// Not concurrent safe with itself or other methods.
//
// Panics if t is not an interface type.
func (x Registry) InterfaceConverter(t Type, fn Converter) {
	if t.Kind() != Interface {
		panic("type is not an interface")
	}
	x.interfaceConv[t] = fn
}

// See [Registry.InterfaceConverter].
//
// The Inverter should return a value of concrete type.
func (x Registry) InterfaceInverter(t Type, fn Inverter) {
	if t.Kind() != Interface {
		panic("type is not an interface")
	}
	x.interfaceInv[t] = fn
}

func RegisterConcrete[T any](x Registry) error {
	t := TypeFor[T]()
	return x.Concrete(t)
}

func RegisterInterfaceConverter[T any](x Registry, fn Converter) {
	t := TypeFor[T]()
	x.InterfaceConverter(t, fn)
}

func RegisterInterfaceInverter[T any](x Registry, fn Inverter) {
	t := TypeFor[T]()
	x.InterfaceInverter(t, fn)
}
