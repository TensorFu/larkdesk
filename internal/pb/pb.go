package pb

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"unicode/utf8"
)

type Field struct {
	Num   int
	Wire  string
	Int   uint64
	Bytes []byte
}

func uvarint(n uint64) []byte {
	out := make([]byte, 0, 10)
	for n > 0x7F {
		out = append(out, byte(n&0x7F)|0x80)
		n >>= 7
	}
	return append(out, byte(n))
}

func key(field int, wire int) []byte {
	return uvarint(uint64(field<<3 | wire))
}

func EncodeInt(field int, n uint64) []byte {
	return append(key(field, 0), uvarint(n)...)
}

func EncodeBytes(field int, data []byte) []byte {
	out := append(key(field, 2), uvarint(uint64(len(data)))...)
	return append(out, data...)
}

func EncodeString(field int, text string) []byte {
	return EncodeBytes(field, []byte(text))
}

func DecodeVarint(buf []byte, i int) (uint64, int, error) {
	var n uint64
	var shift uint
	for {
		if i >= len(buf) {
			return 0, i, fmt.Errorf("truncated varint")
		}
		b := buf[i]
		i++
		n |= uint64(b&0x7F) << shift
		if b < 0x80 {
			return n, i, nil
		}
		shift += 7
		if shift > 63 {
			return 0, i, fmt.Errorf("varint too long")
		}
	}
}

func DecodeFields(buf []byte) []Field {
	var fields []Field
	i := 0
	for i < len(buf) {
		k, ni, err := DecodeVarint(buf, i)
		if err != nil {
			break
		}
		i = ni
		field := int(k >> 3)
		wire := int(k & 7)
		if field <= 0 {
			break
		}
		switch wire {
		case 0:
			v, ni, err := DecodeVarint(buf, i)
			if err != nil {
				return fields
			}
			i = ni
			fields = append(fields, Field{Num: field, Wire: "varint", Int: v})
		case 2:
			ln, ni, err := DecodeVarint(buf, i)
			if err != nil || ni+int(ln) > len(buf) {
				return fields
			}
			i = ni
			fields = append(fields, Field{Num: field, Wire: "bytes", Bytes: buf[i : i+int(ln)]})
			i += int(ln)
		case 1:
			if i+8 > len(buf) {
				return fields
			}
			fields = append(fields, Field{Num: field, Wire: "fixed64", Bytes: buf[i : i+8]})
			i += 8
		case 5:
			if i+4 > len(buf) {
				return fields
			}
			fields = append(fields, Field{Num: field, Wire: "fixed32", Bytes: buf[i : i+4]})
			i += 4
		default:
			return fields
		}
	}
	return fields
}

type Packet struct {
	PayloadType uint64
	Cmd         uint64
	Status      uint64
	Payload     []byte
	CID         string
}

func EncodePacket(cmd uint64, payload []byte, cid string) ([]byte, string) {
	if cid == "" {
		cid = newCID()
	}
	body := EncodeInt(2, 1)
	body = append(body, EncodeInt(3, cmd)...)
	body = append(body, EncodeBytes(5, payload)...)
	body = append(body, EncodeString(6, cid)...)
	return body, cid
}

func DecodePacket(buf []byte) Packet {
	var out Packet
	for _, f := range DecodeFields(buf) {
		switch f.Num {
		case 2:
			out.PayloadType = f.Int
		case 3:
			out.Cmd = f.Int
		case 4:
			out.Status = f.Int
		case 5:
			if f.Wire == "bytes" {
				out.Payload = f.Bytes
			}
		case 6:
			if f.Wire == "bytes" {
				out.CID = string(f.Bytes)
			}
		}
	}
	return out
}

func UTF8Printable(data []byte) (string, bool) {
	if len(data) == 0 {
		return "", false
	}
	s := string(data)
	if !utf8OK(s) {
		return "", false
	}
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			continue
		}
		if r < 32 || r == 127 {
			return "", false
		}
	}
	return s, true
}

func utf8OK(s string) bool {
	return utf8.ValidString(s)
}

func NewUUID() string { return newCID() }

func newCID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "00000000-0000-0000-0000-000000000000"
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}
