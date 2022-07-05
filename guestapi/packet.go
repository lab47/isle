package guestapi

import (
	"errors"

	"google.golang.org/protobuf/encoding/protowire"
)

var (
	ErrDecode  = errors.New("error decoding")
	ErrUseSlow = errors.New("use slow decode")
)

func (p *Packet) FastUnmarshalProto(b []byte) error {
	for len(b) > 0 {
		num, typ, tagLen := protowire.ConsumeTag(b)

		if tagLen < 0 {
			return ErrDecode
		}

		var valLen int

		switch num {
		case 1: // data
			if typ != protowire.BytesType {
				return ErrDecode
			}

			val, n := protowire.ConsumeBytes(b[tagLen:])
			if n < 0 {
				return ErrDecode
			}

			if p.Data != nil {
				p.Data = append(p.Data[:0], val...)
			} else {
				p.Data = append([]byte(nil), val...)
			}

			valLen = n
		case 2: // channel
			if typ != protowire.VarintType {
				return ErrDecode
			}

			val, n := protowire.ConsumeVarint(b[tagLen:])
			if n < 0 {
				return ErrDecode
			}

			p.Channel = Packet_Channel(val)

			valLen = n
		default:
			return ErrUseSlow
		}

		b = b[tagLen+valLen:]
	}

	return nil
}

func (p *Packet) FastDataOnlyMarshal(b []byte) []byte {
	b = protowire.AppendTag(b, 1, protowire.BytesType)
	b = protowire.AppendBytes(b, p.Data)
	b = protowire.AppendTag(b, 2, protowire.VarintType)
	b = protowire.AppendVarint(b, uint64(p.Channel))

	return b
}
