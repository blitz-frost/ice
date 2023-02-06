package ice

import (
	"fmt"
	"reflect"
	"testing"
)

type T struct {
	a int
	b any
}

var m, _ = GenerateMapping(T{})
var c, _ = CodecMake(m)

// perform an encode + decode loop
// returns the decoded value, in case the default printed messages aren't adequate, particularly for pointers and channels
func test(v any) any {
	block := c.Block()
	t := reflect.TypeOf(v)

	err := block.Freeze(v)
	if err != nil {
		fmt.Println(t, "freeze error:", err)
		return nil
	}
	b := block.Bytes()
	fmt.Println(t, "->", b)

	block = c.BlockOf(b)
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
	test([4]int{1, 2, 3, 4})
	test([2][]int{[]int{1, 2}, []int{3, 4}})
}

func TestChan(t *testing.T) {
	a := make(chan int, 3)
	a <- 43
	a <- 44

	b := test(a).(chan int)
	fmt.Println(<-b, <-b)
}

func TestInt(t *testing.T) {
	test(44)
}

func TestInterface(t *testing.T) {
	b := test(T{1, T{2, 3}}).(T)
	fmt.Println(reflect.TypeOf(b.b))
}

func TestMap(t *testing.T) {
	test(map[int]int{1: 2, 3: 4})
}

func TestPointer(t *testing.T) {
	a := new(int)
	*a = 45
	a2 := new(int)
	*a2 = 46

	b := test(a).(*int)
	fmt.Println(*b)
	c := test([]*int{a, a, a2}).([]*int)
	fmt.Println(*c[0], *c[1], *c[2])
}

func TestSlice(t *testing.T) {
	test([]int{2, 3, 4})
	test([]string{"one", "two"})
}

func TestString(t *testing.T) {
	test("asdf")
}

func TestStruct(t *testing.T) {
	type aStruct struct {
		a int
		B []int
	}

	test(aStruct{1, []int{2, 3}})
}
