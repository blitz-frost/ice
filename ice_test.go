package ice

import (
	"fmt"
	"testing"
)

func TestArray(t *testing.T) {
	b, err := Marshal([4]int{1, 2, 3, 4})
	fmt.Println("[4]int ->", b, err)
	a, err := Unmarshal[[4]int](b)
	fmt.Println("[4]int <-", a, err)

	b, err = Marshal([2][]int{[]int{1, 2}, []int{3, 4}})
	fmt.Println("[2][]int ->", b, err)
	a2, err := Unmarshal[[2][]int](b)
	fmt.Println("[2][]int <-", a2, err)
}

func TestChan(t *testing.T) {
	a := make(chan int, 3)
	a <- 43
	a <- 44

	b, err := Marshal(a)
	fmt.Println("chan int 3 ->", b, err)
	a, err = Unmarshal[chan int](b)
	fmt.Println("chan int 3 <-", <-a, <-a, err)
}

func TestMap(t *testing.T) {
	b, err := Marshal(map[int]int{1: 2, 3: 4})
	fmt.Println("map[int]int ->", b, err)
	a, err := Unmarshal[map[int]int](b)
	fmt.Println("map[int]int <-", a, err)
}

func TestInt(t *testing.T) {
	b, err := Marshal(44)
	fmt.Println("int ->", b, err)
	a, err := Unmarshal[int](b)
	fmt.Println("int <-", a, err)
}

func TestPointer(t *testing.T) {
	a := new(int)
	*a = 45
	a2 := new(int)
	*a2 = 46

	b, err := Marshal(a)
	fmt.Println("*int ->", b, err)
	a, err = Unmarshal[*int](b)
	fmt.Println("*int <-", *a, err)

	b, err = Marshal([]*int{a, a, a2})
	fmt.Println("[]*int ->", b, err)
	a3, err := Unmarshal[[]*int](b)
	fmt.Println("[]*int <-", *a3[0], *a3[1], *a3[2], err)
}

func TestSlice(t *testing.T) {
	b, err := Marshal([]int{2, 3, 4})
	fmt.Println("[]int ->", b, err)
	a, err := Unmarshal[[]int](b)
	fmt.Println("[]int <-", a, err)

	b, err = Marshal([]string{"one", "two"})
	fmt.Println("[]string ->", b, err)
	a2, err := Unmarshal[[]string](b)
	fmt.Println("[]string <-", a2, err)
}

func TestString(t *testing.T) {
	b, err := Marshal("asdf")
	fmt.Println("string ->", b, err)
	a, err := Unmarshal[string](b)
	fmt.Println("string <-", a, err)
}

func TestStruct(t *testing.T) {
	type aStruct struct {
		a int
		B []int
	}

	b, err := Marshal(aStruct{1, []int{2, 3}})
	fmt.Println("struct{int, []int} ->", b, err)
	a, err := Unmarshal[aStruct](b)
	fmt.Println("struct{int, []int} <-", a, err)
}
