package main

import (
	"fmt"
	"reflect"

	"github.com/blitz-frost/ice"
)

func test(v interface{}) interface{} {
	b, err := ice.Marshal(v)
	fmt.Println(b, err)

	t := reflect.TypeOf(v)
	pv := reflect.New(t)
	p := pv.Interface()
	err = ice.Unmarshal(p, b)
	for pv.Elem().Kind() == reflect.Ptr {
		pv = pv.Elem()
	}

	o := pv.Elem().Interface()
	fmt.Println(o, err)
	return o
}

func main() {
	test(44)
	test(true)
	test("ding")
	test([]int{1, 2, 3})
	test([][]int{
		[]int{1, 2, 3},
		[]int{4, 5, 6},
		[]int{7, 8, 9},
	})
	test([3]int{2, 3, 4})
	test([]string{"one", "two", "ding"})

	a := 43
	test(&a)

	b, c := 44, 45
	test([]*int{&a, &b, &c, &a})

	test(map[string]int{
		"ze":    66,
		"prost": 5,
		"ozaur": 135,
	})

	test(solid{
		a: 2,
		b: true,
		c: [3]byte{6, 8, 3},
	})

	test(solid2{
		a: [3][3]byte{
			[3]byte{1, 2, 3},
			[3]byte{4, 5, 6},
			[3]byte{7, 8, 9},
		},
		b: solid{
			a: 3,
			b: false,
			c: [3]byte{1, 1, 2},
		},
	})

	d := solid{
		a: 4,
		b: true,
		c: [3]byte{2, 2, 3},
	}
	r := test(fluid{
		a: &d,
		b: []int{5, 50, 500},
	})
	fmt.Println(*r.(fluid).a)

	v := reflect.ValueOf(a)
	res, err := ice.Marshal(v)
	fmt.Println(res, err)

	v = reflect.ValueOf(new(int))
	err = ice.Unmarshal(&v, res)
	fmt.Println(v.Elem().Interface(), err)

	vs := []reflect.Value{
		reflect.ValueOf(55),
		reflect.ValueOf(56),
		reflect.ValueOf(57),
	}
	res, err = ice.Marshal(vs)
	fmt.Println(res, err)

	vs = []reflect.Value{
		reflect.ValueOf(new(int)),
		reflect.ValueOf(new(int)),
		reflect.ValueOf(new(int)),
	}
	err = ice.Unmarshal(&vs, res)
	fmt.Println(vs[0].Elem())
	fmt.Println(vs[1].Elem())
	fmt.Println(vs[2].Elem())

	fmt.Println("direct sequential\n")

	// direct sequential
	block := ice.NewBlock()
	block.Freeze(10)
	block.Freeze(20)
	block.Freeze(30)
	p := block.ToBytes()

	block2 := ice.FromBytes(p)
	var anInt int
	block2.Thaw(&anInt)
	fmt.Println(anInt)
	block2.Thaw(&anInt)
	fmt.Println(anInt)
	block2.Thaw(&anInt)
	fmt.Println(anInt)
}

type solid struct {
	a int
	b bool
	c [3]byte
}

type solid2 struct {
	a [3][3]byte
	b solid
}

type fluid struct {
	a *solid
	b []int
}
