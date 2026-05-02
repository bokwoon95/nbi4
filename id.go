package nbi4

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/mr-tron/base58"
)

// ID is a 16-byte UUID that is sortable by a timestamp component.
type ID [16]byte

// NewID creates a new ID.
func NewID() ID {
	uuid, err := uuid.NewV7()
	if err != nil {
		panic(err)
	}
	return ID(uuid)
}

// ParseID parses a UUID string of the format
// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx into an ID.
func ParseID(s string) (ID, error) {
	var id ID
	switch len(s) {
	case 0:
		break
	case 21, 22:
		b, err := base58.FastBase58Decoding(s)
		if err != nil {
			return ID{}, errors.New("invalid ID")
		}
		copy(id[:], b)
	case 36:
		for i, x := range [16]int{
			0, 2, 4, 6,
			9, 11,
			14, 16,
			19, 21,
			24, 26, 28, 30, 32, 34,
		} {
			v, ok := xtob(s[x], s[x+1])
			if !ok {
				return ID{}, errors.New("invalid ID")
			}
			id[i] = v
		}
	default:
		return ID{}, errors.New("invalid ID")
	}
	return id, nil
}

// IsZero reports if an ID is all zeroes, which is used to represent the null
// state.
func (id ID) IsZero() bool {
	return id == ID{}
}

// String prints the ID in UUID format xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.
func (id ID) String() string {
	if id.IsZero() {
		return ""
	}
	var b [32 + 4]byte // 32 ASCII + 4 dashes
	hex.Encode(b[:], id[:4])
	b[8] = '-'
	hex.Encode(b[9:13], id[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], id[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], id[8:10])
	b[23] = '-'
	hex.Encode(b[24:], id[10:])
	return string(b[:])
}

func (id ID) Base58String() string {
	return base58.FastBase58Encoding(id[:])
}

// MarshalJSON converts an ID to a JSON string in the format
// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx.
func (id ID) MarshalJSON() ([]byte, error) {
	if id.IsZero() {
		return []byte(`""`), nil
	}
	var array [32 + 4 + 2]byte // 32 ASCII + 4 dashes + 2 quotes
	array[0], array[len(array)-1] = '"', '"'
	b := array[1 : len(array)-1]
	hex.Encode(b[:], id[:4])
	b[8] = '-'
	hex.Encode(b[9:13], id[4:6])
	b[13] = '-'
	hex.Encode(b[14:18], id[6:8])
	b[18] = '-'
	hex.Encode(b[19:23], id[8:10])
	b[23] = '-'
	hex.Encode(b[24:], id[10:])
	return array[:], nil
}

// UnmarshalJSON converts a JSON string in the format
// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx into an ID. If the JSON value is null,
// the existing ID's value is untouched.
func (id *ID) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		return nil
	}
	*id = ID{}
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("cannot unmarshal non-string %s into an ID", string(data))
	}
	b := data[1 : len(data)-1]
	if len(b) == 0 {
		return nil
	}
	if len(b) != 36 {
		return errors.New("invalid ID")
	}
	for i, x := range [16]int{
		0, 2, 4, 6,
		9, 11,
		14, 16,
		19, 21,
		24, 26, 28, 30, 32, 34,
	} {
		v, ok := xtob(b[x], b[x+1])
		if !ok {
			return errors.New("invalid ID")
		}
		(*id)[i] = v
	}
	return nil
}

// Copyright (c) 2009,2014 Google Inc. All rights reserved.
//
// Redistribution and use in source and binary forms, with or without
// modification, are permitted provided that the following conditions are
// met:
//
//    * Redistributions of source code must retain the above copyright
// notice, this list of conditions and the following disclaimer.
//    * Redistributions in binary form must reproduce the above
// copyright notice, this list of conditions and the following disclaimer
// in the documentation and/or other materials provided with the
// distribution.
//    * Neither the name of Google Inc. nor the names of its
// contributors may be used to endorse or promote products derived from
// this software without specific prior written permission.
//
// THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
// "AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
// LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
// A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
// OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
// SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
// LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
// DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
// THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
// (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
// OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

// https://github.com/google/uuid

// xvalues returns the value of a byte as a hexadecimal digit or 255.
var xvalues = [256]byte{
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 255, 255, 255, 255, 255, 255,
	255, 10, 11, 12, 13, 14, 15, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 10, 11, 12, 13, 14, 15, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
}

// xtob converts hex characters x1 and x2 into a byte.
func xtob(x1, x2 byte) (byte, bool) {
	b1 := xvalues[x1]
	b2 := xvalues[x2]
	return (b1 << 4) | b2, b1 != 255 && b2 != 255
}
