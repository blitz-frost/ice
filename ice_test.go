package ice

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/blitz-frost/errors"
)

type T struct {
	a int
	b any
}

type T2 struct {
	a any
	b error
}

var reg = RegistryMake()

func init() {
	RegisterConcrete[T](reg)

	RegisterInterface[error](reg, func(blk *Block, v reflect.Value) error {
		vv := v.Interface().(error)

		s := vv.Error()
		return blk.Freeze(s)
	}, func(blk *Block) (reflect.Value, error) {
		s, err := Thaw[string](blk)
		if err != nil {
			return reflect.Value{}, err
		}

		v := errors.Simple(s)
		return reflect.ValueOf(v), nil
	})
}

// perform an encode + decode loop
// returns the decoded value, in case the default printed messages aren't adequate, particularly for pointers and channels
func test(v any) any {
	block := reg.Block()
	t := reflect.TypeOf(v)

	err := block.Freeze(v)
	if err != nil {
		fmt.Println(t, "freeze error:", err)
		return nil
	}
	b := block.Bytes()
	fmt.Println(t, "->", b)

	block = reg.BlockOf(b)
	oVal, err := block.Decode(t)
	if err != nil {
		fmt.Println(t, "thaw error:", err)
		return nil
	}
	o := oVal.Interface()
	fmt.Println(t, "<-", oVal.Interface())
	return o
}

func TestArray(t *testing.T) {
	fmt.Println("array")

	test([4]int{1, 2, 3, 4})
	test([2][]int{{1, 2}, {3, 4}})

	fmt.Println()
}

func TestChan(t *testing.T) {
	fmt.Println("chan")

	a := make(chan int, 3)
	a <- 43
	a <- 44

	b := test(a).(chan int)
	fmt.Println(<-b, <-b)

	fmt.Println()
}

func TestInt(t *testing.T) {
	fmt.Println("int")

	test(44)

	fmt.Println()
}

func TestInterface(t *testing.T) {
	fmt.Println("interface")

	test(T{1, T{2, true}})
	test(T{3, "something"})

	err := errors.Simple("some error")
	test(T2{3, err})

	fmt.Println()
}

func TestMap(t *testing.T) {
	fmt.Println("map")

	test(map[int]int{1: 2, 3: 4})

	fmt.Println()
}

func TestPointer(t *testing.T) {
	fmt.Println("pointer")

	a := new(int)
	*a = 45
	a2 := new(int)
	*a2 = 46

	b := test(a).(*int)
	fmt.Println(*b)
	c := test([]*int{a, a, a2}).([]*int)
	fmt.Println(*c[0], *c[1], *c[2])

	fmt.Println()
}

func TestSlice(t *testing.T) {
	fmt.Println("slice")

	test([]int{2, 3, 4})
	test([]string{"one", "two"})

	fmt.Println()
}

func TestString(t *testing.T) {
	fmt.Println("string")

	test("asdf")

	fmt.Println()
}

func TestStruct(t *testing.T) {
	fmt.Println("struct")

	type aStruct struct {
		a int
		B []int
	}

	test(aStruct{1, []int{2, 3}})

	fmt.Println()
}
