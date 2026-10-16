package msg

import (
	encmsg "github.com/blitz-frost/encoding/msg"
	"github.com/blitz-frost/errors"
	"github.com/blitz-frost/ice"
	iomsg "github.com/blitz-frost/io/msg"
	"github.com/blitz-frost/msg"
)

func ExchangeInlet(src iomsg.ExchangeInlet, reg ice.Registry) encmsg.ExchangeInlet {
	return func() (encmsg.ExchangeInput, error) {
		in, err := src()
		if err != nil {
			return encmsg.ExchangeInput{}, err
		}

		return ExchangeInput(in, reg)
	}
}

// Reads an encoding/msg.ExchangeInput from the provided input, which will be either closed or canceled before returning.
func ExchangeInput(in iomsg.ExchangeInput, reg ice.Registry) (encmsg.ExchangeInput, error) {
	b, err := reg.BlockFrom(in.Read)
	if err != nil {
		err = &errors.T{
			Message: "read Block",
			Sub:     err,
		}
		return encmsg.ExchangeInput{}, errors.Join(err, in.Cancel())
	}

	if err = in.Close(); err != nil { // no longer needed
		return encmsg.ExchangeInput{}, err
	}

	return encmsg.ExchangeInput{
		Decode: b.Decode,
		Close:  msg.Noop,
		Output: func() (encmsg.Output, error) {
			return outputMake(in.Output, reg), nil
		},
		Cancel: in.Cancel,
	}, nil
}

func ExchangeInputPipe(dst encmsg.ExchangeInputTaker, reg ice.Registry) iomsg.ExchangeInputTaker {
	return func(in iomsg.ExchangeInput) error {
		ein, err := ExchangeInput(in, reg)
		if err != nil {
			return err
		}

		return dst(ein)
	}
}

func ExchangeOutlet(dst iomsg.ExchangeOutlet, reg ice.Registry) encmsg.ExchangeOutlet {
	return func() (encmsg.ExchangeOutput, error) {
		b := reg.Block()

		out := &exchangeOutput{
			b:   &b,
			dst: dst,
			reg: reg,
		}

		return encmsg.ExchangeOutput{
			Encode: b.Encode,
			Close:  out.Close,
			Input:  out.Input,
			Cancel: msg.Noop,
		}, nil
	}
}

func Inlet(src iomsg.Inlet, reg ice.Registry) encmsg.Inlet {
	return func() (encmsg.Input, error) {
		return inputMake(src, reg)
	}
}

func Outlet(dst iomsg.Outlet, reg ice.Registry) encmsg.Outlet {
	return func() (encmsg.Output, error) {
		return outputMake(dst, reg), nil
	}
}

type exchangeOutput struct {
	b      *ice.Block
	dst    iomsg.ExchangeOutlet
	out    iomsg.ExchangeOutput
	reg    ice.Registry
	closed bool
}

func (x *exchangeOutput) Close() error {
	x.closed = true

	var err error
	if x.out, err = x.dst(); err != nil {
		return errors.TraceLine(err)
	}

	return writeAndClose(x.b, iomsg.Output{
		Write: x.out.Write,
		Close: x.out.Close,
	})
}

func (x *exchangeOutput) Input() (encmsg.Input, error) {
	if !x.closed {
		if err := x.Close(); err != nil {
			err = errors.TraceLine(err)
			return encmsg.Input{}, err
		}
	}

	return inputMake(x.out.Input, x.reg)
}

func inputMake(src iomsg.Inlet, reg ice.Registry) (encmsg.Input, error) {
	in, err := src()
	if err != nil {
		err = errors.TraceLine(err)
		return encmsg.Input{}, err
	}

	b, err := reg.BlockFrom(in.Read)
	if err != nil {
		err = errors.TraceLine(err)
		return encmsg.Input{}, errors.Join(err, in.Close())
	}

	if err = in.Close(); err != nil { // no longer needed
		err = errors.TraceLine(err)
		return encmsg.Input{}, err
	}

	return encmsg.Input{
		Decode: b.Decode,
		Close:  msg.Noop,
	}, nil
}

func outputMake(dst iomsg.Outlet, reg ice.Registry) encmsg.Output {
	b := reg.Block()

	return encmsg.Output{
		Encode: b.Encode,
		Close: func() error {
			out, err := dst()
			if err != nil {
				return err
			}

			return writeAndClose(&b, out)
		},
		Cancel: msg.Noop, // don't need to do anything
	}
}

func writeAndClose(b *ice.Block, out iomsg.Output) error {
	bytes := b.Bytes()

	var errs errors.Group

	if errIo := out.Write(bytes); errIo != nil {
		err := errors.TraceLine(errIo)
		errs = append(errs, err)
	}

	if err := out.Close(); err != nil {
		err = errors.TraceLine(err)
		errs = append(errs, err)
	}

	return errs.Resolve()
}
