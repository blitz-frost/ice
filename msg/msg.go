package msg

import (
	encmsg "github.com/blitz-frost/encoding/msg"
	"github.com/blitz-frost/errors"
	"github.com/blitz-frost/ice"
	iomsg "github.com/blitz-frost/io/msg"
	"github.com/blitz-frost/msg"
)

func ExchangeInlet(src iomsg.ExchangeInlet, m ice.Mapping) encmsg.ExchangeInlet {
	return func() (encmsg.ExchangeInput, error) {
		in, err := src()
		if err != nil {
			return encmsg.ExchangeInput{}, err
		}

		return ExchangeInput(in, m)
	}
}

// Reads an encoding/msg.ExchangeInput from the provided input, which will be either closed or canceled before returning.
func ExchangeInput(in iomsg.ExchangeInput, m ice.Mapping) (encmsg.ExchangeInput, error) {
	b, err := m.BlockFrom(in.Read)
	if err != nil {
		oErr := errors.Message("read Block", err)
		oErr.Link(in.Cancel())
		return encmsg.ExchangeInput{}, oErr
	}

	if err = in.Close(); err != nil { // no longer needed
		return encmsg.ExchangeInput{}, err
	}

	return encmsg.ExchangeInput{
		Decode: b.Decode,
		Close:  msg.Noop,
		Output: func() (encmsg.Output, error) {
			return outputMake(in.Output, m), nil
		},
		Cancel: in.Cancel,
	}, nil
}

func ExchangeInputPipe(dst encmsg.ExchangeInputTaker, m ice.Mapping) iomsg.ExchangeInputTaker {
	return func(in iomsg.ExchangeInput) error {
		ein, err := ExchangeInput(in, m)
		if err != nil {
			return err
		}

		return dst(ein)
	}
}

func ExchangeOutlet(dst iomsg.ExchangeOutlet, m ice.Mapping) encmsg.ExchangeOutlet {
	return func() (encmsg.ExchangeOutput, error) {
		b := m.Block()

		out := &exchangeOutput{
			b:   b,
			dst: dst,
			m:   m,
		}

		return encmsg.ExchangeOutput{
			Encode: b.Encode,
			Close:  out.Close,
			Input:  out.Input,
			Cancel: msg.Noop,
		}, nil
	}
}

func Inlet(src iomsg.Inlet, m ice.Mapping) encmsg.Inlet {
	return func() (encmsg.Input, error) {
		return inputMake(src, m)
	}
}

func Outlet(dst iomsg.Outlet, m ice.Mapping) encmsg.Outlet {
	return func() (encmsg.Output, error) {
		return outputMake(dst, m), nil
	}
}

type exchangeOutput struct {
	b      *ice.Block
	dst    iomsg.ExchangeOutlet
	out    iomsg.ExchangeOutput
	m      ice.Mapping
	closed bool
}

func (x *exchangeOutput) Close() error {
	x.closed = true

	var err error
	if x.out, err = x.dst(); err != nil {
		return err
	}

	return writeAndClose(x.b, iomsg.Output{
		Write: x.out.Write,
		Close: x.out.Close,
	})
}

func (x *exchangeOutput) Input() (encmsg.Input, error) {
	if !x.closed {
		if err := x.Close(); err != nil {
			return encmsg.Input{}, err
		}
	}

	return inputMake(x.out.Input, x.m)
}

func inputMake(src iomsg.Inlet, m ice.Mapping) (encmsg.Input, error) {
	in, err := src()
	if err != nil {
		return encmsg.Input{}, err
	}

	b, err := m.BlockFrom(in.Read)
	if err != nil {
		oErr := errors.Message("read Block", err)
		oErr.Link(in.Close())
		return encmsg.Input{}, oErr
	}

	if err = in.Close(); err != nil { // no longer needed
		return encmsg.Input{}, err
	}

	return encmsg.Input{
		Decode: b.Decode,
		Close:  msg.Noop,
	}, nil
}

func outputMake(dst iomsg.Outlet, m ice.Mapping) encmsg.Output {
	b := m.Block()

	return encmsg.Output{
		Encode: b.Encode,
		Close: func() error {
			out, err := dst()
			if err != nil {
				return err
			}

			return writeAndClose(b, out)
		},
		Cancel: msg.Noop, // don't need to do anything
	}
}

func writeAndClose(b *ice.Block, out iomsg.Output) error {
	bytes := b.Bytes()

	var err errors.Group

	_, err1 := out.Write(bytes)
	err.Add(err1)

	err.Add(out.Close())

	return err.Wrap()
}
