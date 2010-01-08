//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//
package router

import (
	"gob"
	"json"
	"bytes"
	"strconv"
	"strings"
	"fmt"
	"io"
	"os"
	"reflect"
)

type Marshaler interface {
	Marshal(interface{}) os.Error
}

type Demarshaler interface {
	Demarshal(interface{}, reflect.Value) os.Error
}

type MarshallingPolicy interface {
	NewMarshaler(io.Writer) Marshaler
	NewDemarshaler(io.Reader) Demarshaler
}

// marshalling policy using gob

type gobMarshallingPolicy byte
type gobMarshaler gob.Encoder
type gobDemarshaler gob.Decoder

var GobMarshaling MarshallingPolicy = gobMarshallingPolicy(0)

func (g gobMarshallingPolicy) NewMarshaler(w io.Writer) Marshaler {
	return (*gobMarshaler)(gob.NewEncoder(w))
}

func (g gobMarshallingPolicy) NewDemarshaler(r io.Reader) Demarshaler {
	return (*gobDemarshaler)(gob.NewDecoder(r))
}

func (gm *gobMarshaler) Marshal(e interface{}) os.Error {
	v := reflect.Indirect(reflect.NewValue(e))
	switch v := v.(type) {
	case *reflect.BoolValue:
		w := BoolWrapper{v.Get()}
		return ((*gob.Encoder)(gm)).Encode(w)
	case *reflect.IntValue:
		w := IntWrapper{v.Get()}
		return ((*gob.Encoder)(gm)).Encode(w)
	/*
		case *reflect.Int8Value:
		case *reflect.Int16Value:
		case *reflect.Int32Value:
		case *reflect.Int64Value:
		case *reflect.UintValue:
		case *reflect.Uint8Value:
		case *reflect.Uint16Value:
		case *reflect.Uint32Value:
		case *reflect.Uint64Value:
		case *reflect.UintptrValue:
	*/
	case *reflect.FloatValue:
		w := FloatWrapper{v.Get()}
		return ((*gob.Encoder)(gm)).Encode(w)
	/*
		case *reflect.Float32Value:
		case *reflect.Float64Value:
	*/
	case *reflect.StringValue:
		w := StrWrapper{v.Get()}
		return ((*gob.Encoder)(gm)).Encode(w)
	/*
		case *reflect.ArrayValue:
		case *reflect.SliceValue:
	*/
	case *reflect.StructValue:
		switch e1 := e.(type) {
		case ConnInfoMsg:
			if err := ((*gob.Encoder)(gm)).Encode(BoolWrapper{e1.SeedId != nil}); err != nil {
				return err
			}
			if e1.SeedId != nil {
				if err := ((*gob.Encoder)(gm)).Encode(e1.SeedId); err != nil {
					return err
				}
			}
			if err := ((*gob.Encoder)(gm)).Encode(BoolWrapper{e1.Error != nil}); err != nil {
				return err
			}
			if e1.Error != nil {
				if err := ((*gob.Encoder)(gm)).Encode(StrWrapper{e1.Error.String()}); err != nil {
					return err
				}
			}
			if err := ((*gob.Encoder)(gm)).Encode(BoolWrapper{len(e1.ConnInfo) > 0}); err != nil {
				return err
			}
			if len(e1.ConnInfo) > 0 {
				if err := ((*gob.Encoder)(gm)).Encode(StrWrapper{e1.ConnInfo}); err != nil {
					return err
				}
			}
			return nil
		case IdChanInfoMsg:
			num := len(e1.Info)
			w := &IntWrapper{num}
			if err := ((*gob.Encoder)(gm)).Encode(w); err != nil {
				return err
			}
			for _, v := range e1.Info {
				if err := ((*gob.Encoder)(gm)).Encode(v.Id); err != nil {
					return err
				}
				if v.ElemType == nil {
					v.ElemType = new(ChanElemTypeData)
					elemType := v.ChanType.Elem()
					v.ElemType.FullName = elemType.PkgPath() + "." + elemType.Name()
				}
				if err := ((*gob.Encoder)(gm)).Encode(v.ElemType); err != nil {
					return err
				}
			}
			return nil
		case Id:
			if err := ((*gob.Encoder)(gm)).Encode(e); err != nil {
				return err
			}
			return nil
		default:
			return ((*gob.Encoder)(gm)).Encode(v.Interface())
		}
	default:
		return os.ErrorString("unknown chan elem type found in gobMarshaler.Marshal")
	}
	return nil
}

func (gm *gobDemarshaler) Demarshal(e interface{}, val reflect.Value) os.Error {
	if val == nil {
		val = reflect.NewValue(e)
	}
	v := reflect.Indirect(val)
	switch v := v.(type) {
	case *reflect.BoolValue:
		w := &BoolWrapper{}
		if err := ((*gob.Decoder)(gm)).Decode(w); err != nil {
			return err
		}
		v.Set(w.Val)
	case *reflect.IntValue:
		w := &IntWrapper{}
		if err := ((*gob.Decoder)(gm)).Decode(w); err != nil {
			return err
		}
		v.Set(w.Val)
	/*
		case *reflect.Int8Value:
		case *reflect.Int16Value:
		case *reflect.Int32Value:
		case *reflect.Int64Value:
		case *reflect.UintValue:
		case *reflect.Uint8Value:
		case *reflect.Uint16Value:
		case *reflect.Uint32Value:
		case *reflect.Uint64Value:
		case *reflect.UintptrValue:
	*/
	case *reflect.FloatValue:
		w := &FloatWrapper{}
		if err := ((*gob.Decoder)(gm)).Decode(w); err != nil {
			return err
		}
		v.Set(w.Val)
	/*
		case *reflect.Float32Value:
		case *reflect.Float64Value:
	*/
	case *reflect.StringValue:
		w := &StrWrapper{}
		if err := ((*gob.Decoder)(gm)).Decode(w); err != nil {
			return err
		}
		v.Set(w.Val)
	/*
		case *reflect.ArrayValue:
		case *reflect.SliceValue:
	*/
	case *reflect.StructValue:
		switch e1 := e.(type) {
		case *ConnInfoMsg:
			flag := &BoolWrapper{}
			if err := ((*gob.Decoder)(gm)).Decode(flag); err != nil {
				return err
			}
			if flag.Val {
				if err := ((*gob.Decoder)(gm)).Decode(e1.SeedId); err != nil {
					return err
				}
			}
			flag.Val = false
			if err := ((*gob.Decoder)(gm)).Decode(flag); err != nil {
				return err
			}
			if flag.Val {
				str := &StrWrapper{}
				if err := ((*gob.Decoder)(gm)).Decode(str); err != nil {
					return err
				}
				e1.Error = os.ErrorString(str.Val)
			}
			flag.Val = false
			if err := ((*gob.Decoder)(gm)).Decode(flag); err != nil {
				return err
			}
			if flag.Val {
				str := &StrWrapper{}
				if err := ((*gob.Decoder)(gm)).Decode(str); err != nil {
					return err
				}
				e1.ConnInfo = str.Val
			}
			return nil
		case *IdChanInfoMsg:
			dummyId := e1.Info[0].Id
			num := &IntWrapper{}
			if err := ((*gob.Decoder)(gm)).Decode(num); err != nil {
				return err
			}
			info := make([]*IdChanInfo, num.Val)
			for i := 0; i < num.Val; i++ {
				ici := new(IdChanInfo)
				ici.Id, _ = dummyId.Clone()
				if err := ((*gob.Decoder)(gm)).Decode(ici.Id); err != nil {
					return err
				}
				ici.ElemType = new(ChanElemTypeData)
				if err := ((*gob.Decoder)(gm)).Decode(ici.ElemType); err != nil {
					return err
				}
				info[i] = ici
			}
			e1.Info = info
			return nil
		default:
			return ((*gob.Decoder)(gm)).Decode(e)
		}

	default:
		return os.ErrorString("unknown chan elem type found in gobMarshaler.demarshal")
	}
	return nil
}

// marshalling policy using json

type jsonMarshallingPolicy byte
type jsonMarshaler struct {
	writer io.Writer
	buf    *bytes.Buffer
}
type jsonDemarshaler struct {
	reader io.Reader
	lenBuf [10]byte
}

var JsonMarshaling MarshallingPolicy = jsonMarshallingPolicy(0)

func (j jsonMarshallingPolicy) NewMarshaler(w io.Writer) Marshaler {
	return &jsonMarshaler{w, new(bytes.Buffer)}
}

func (j jsonMarshallingPolicy) NewDemarshaler(r io.Reader) Demarshaler {
	return &jsonDemarshaler{reader: r}
}

func (jm *jsonMarshaler) encodeBool(v bool) (err os.Error) {
	jm.buf.Reset()
	data := BoolWrapper{v}
	if err = json.Marshal(jm.buf, data); err != nil {
		return
	}
	dlen := fmt.Sprintf("%10d", jm.buf.Len())
	if _, err = jm.writer.Write(strings.Bytes(dlen)); err != nil {
		return
	}
	_, err = jm.writer.Write(jm.buf.Bytes())
	return
}

func (jm *jsonMarshaler) encodeInt(v int) (err os.Error) {
	jm.buf.Reset()
	data := IntWrapper{v}
	if err = json.Marshal(jm.buf, data); err != nil {
		return
	}
	dlen := fmt.Sprintf("%10d", jm.buf.Len())
	if _, err = jm.writer.Write(strings.Bytes(dlen)); err != nil {
		return
	}
	_, err = jm.writer.Write(jm.buf.Bytes())
	return
}

func (jm *jsonMarshaler) encodeFloat(v float) (err os.Error) {
	jm.buf.Reset()
	data := FloatWrapper{v}
	if err = json.Marshal(jm.buf, data); err != nil {
		return
	}
	dlen := fmt.Sprintf("%10d", jm.buf.Len())
	if _, err = jm.writer.Write(strings.Bytes(dlen)); err != nil {
		return
	}
	_, err = jm.writer.Write(jm.buf.Bytes())
	return
}

func (jm *jsonMarshaler) encodeStr(v string) (err os.Error) {
	jm.buf.Reset()
	data := StrWrapper{v}
	if err = json.Marshal(jm.buf, data); err != nil {
		return
	}
	dlen := fmt.Sprintf("%10d", jm.buf.Len())
	if _, err = jm.writer.Write(strings.Bytes(dlen)); err != nil {
		return
	}
	_, err = jm.writer.Write(jm.buf.Bytes())
	return
}

func (jm *jsonMarshaler) encodeStruct(e interface{}) (err os.Error) {
	data := reflect.Indirect(reflect.NewValue(e))
	jm.buf.Reset()
	if err = json.Marshal(jm.buf, data.Interface()); err != nil {
		return
	}
	dlen := fmt.Sprintf("%10d", jm.buf.Len())
	if _, err = jm.writer.Write(strings.Bytes(dlen)); err != nil {
		return
	}
	_, err = jm.writer.Write(jm.buf.Bytes())
	return
}

func (jm *jsonMarshaler) Marshal(e interface{}) (err os.Error) {
	v := reflect.Indirect(reflect.NewValue(e))
	switch v := v.(type) {
	case *reflect.BoolValue:
		return jm.encodeBool(v.Get())

	case *reflect.IntValue:
		return jm.encodeInt(v.Get())
	/*
		case *reflect.Int8Value:
		case *reflect.Int16Value:
		case *reflect.Int32Value:
		case *reflect.Int64Value:
		case *reflect.UintValue:
		case *reflect.Uint8Value:
		case *reflect.Uint16Value:
		case *reflect.Uint32Value:
		case *reflect.Uint64Value:
		case *reflect.UintptrValue:
	*/
	case *reflect.FloatValue:
		return jm.encodeFloat(v.Get())
	/*
		case *reflect.Float32Value:
		case *reflect.Float64Value:
	*/
	case *reflect.StringValue:
		err = jm.encodeStr(v.Get())
		return
	/*
		case *reflect.ArrayValue:
		case *reflect.SliceValue:
	*/
	case *reflect.StructValue:
		switch e1 := e.(type) {
		case ConnInfoMsg:
			if err = jm.encodeBool(e1.SeedId != nil); err != nil {
				return
			}
			if e1.SeedId != nil {
				if err = jm.encodeStruct(e1.SeedId); err != nil {
					return
				}
			}
			if err = jm.encodeBool(e1.Error != nil); err != nil {
				return
			}
			if e1.Error != nil {
				if err = jm.encodeStr(e1.Error.String()); err != nil {
					return
				}
			}
			if err = jm.encodeBool(len(e1.ConnInfo) > 0); err != nil {
				return
			}
			if len(e1.ConnInfo) > 0 {
				if err = jm.encodeStr(e1.ConnInfo); err != nil {
					return
				}
			}
			return
		case IdChanInfoMsg:
			num := len(e1.Info)
			if err = jm.encodeInt(num); err != nil {
				return
			}
			for _, v := range e1.Info {
				if err = jm.encodeStruct(v.Id); err != nil {
					return
				}
				if v.ElemType == nil {
					v.ElemType = new(ChanElemTypeData)
					elemType := v.ChanType.Elem()
					v.ElemType.FullName = elemType.PkgPath() + "." + elemType.Name()
				}
				if err = jm.encodeStruct(v.ElemType); err != nil {
					return
				}
			}
			return nil
		default:
			err = jm.encodeStruct(v.Interface())
			return
		}
	default:
		return os.ErrorString("unknown chan elem type found in jsonMarshaler.Marshal")
	}
	return nil
}

func atoi(buf []byte) (num int, err os.Error) {
	i := bytes.LastIndex(buf, []byte{' '})
	num, err = strconv.Atoi(string(buf[i+1:]))
	return
}

func (jm *jsonDemarshaler) decodeBool() (val bool, err os.Error) {
	var n, num int
	if n, err = jm.reader.Read(&jm.lenBuf); err != nil {
		return
	}
	if n, err = atoi(&jm.lenBuf); err != nil {
		return
	}
	buf := make([]byte, n)
	if num, err = jm.reader.Read(buf); num != n || err != nil {
		return
	}
	data := &BoolWrapper{}
	if ok, errtok := json.Unmarshal(string(buf), data); !ok {
		err = os.ErrorString(errtok)
		return
	}
	val = data.Val
	return
}

func (jm *jsonDemarshaler) decodeInt() (val int, err os.Error) {
	var n, num int
	if n, err = jm.reader.Read(&jm.lenBuf); err != nil {
		return
	}
	if n, err = atoi(&jm.lenBuf); err != nil {
		return
	}
	buf := make([]byte, n)
	if num, err = jm.reader.Read(buf); num != n || err != nil {
		return
	}
	data := &IntWrapper{}
	if ok, errtok := json.Unmarshal(string(buf), data); !ok {
		err = os.ErrorString(errtok)
		return
	}
	val = data.Val
	return
}

func (jm *jsonDemarshaler) decodeFloat() (val float, err os.Error) {
	var n, num int
	if n, err = jm.reader.Read(&jm.lenBuf); err != nil {
		return
	}
	if n, err = atoi(&jm.lenBuf); err != nil {
		return
	}
	buf := make([]byte, n)
	if num, err = jm.reader.Read(buf); num != n || err != nil {
		return
	}
	data := &FloatWrapper{}
	if ok, errtok := json.Unmarshal(string(buf), data); !ok {
		err = os.ErrorString(errtok)
		return
	}
	val = data.Val
	return
}

func (jm *jsonDemarshaler) decodeStr() (val string, err os.Error) {
	var n, num int
	if n, err = jm.reader.Read(&jm.lenBuf); err != nil {
		return
	}
	if n, err = atoi(&jm.lenBuf); err != nil {
		return
	}
	buf := make([]byte, n)
	if num, err = jm.reader.Read(buf); num != n || err != nil {
		return
	}
	data := &StrWrapper{}
	if ok, errtok := json.Unmarshal(string(buf), data); !ok {
		err = os.ErrorString(errtok)
		return
	}
	val = data.Val
	return
}

func (jm *jsonDemarshaler) decodeStruct(data interface{}) (err os.Error) {
	var n, num int
	if n, err = jm.reader.Read(&jm.lenBuf); err != nil {
		return
	}
	if n, err = atoi(&jm.lenBuf); err != nil {
		return
	}
	buf := make([]byte, n)
	if num, err = jm.reader.Read(buf); num != n || err != nil {
		return
	}
	if ok, errtok := json.Unmarshal(string(buf), data); !ok {
		err = os.ErrorString(errtok)
		return
	}
	return
}

func (jm *jsonDemarshaler) Demarshal(e interface{}, val reflect.Value) os.Error {
	if val == nil {
		val = reflect.NewValue(e)
	}
	v := reflect.Indirect(val)
	switch v := v.(type) {
	case *reflect.BoolValue:
		if w, err := jm.decodeBool(); err != nil {
			return err
		} else {
			v.Set(w)
		}
	case *reflect.IntValue:
		if w, err := jm.decodeInt(); err != nil {
			return err
		} else {
			v.Set(w)
		}
	/*
		case *reflect.Int8Value:
		case *reflect.Int16Value:
		case *reflect.Int32Value:
		case *reflect.Int64Value:
		case *reflect.UintValue:
		case *reflect.Uint8Value:
		case *reflect.Uint16Value:
		case *reflect.Uint32Value:
		case *reflect.Uint64Value:
		case *reflect.UintptrValue:
	*/
	case *reflect.FloatValue:
		if w, err := jm.decodeFloat(); err != nil {
			return err
		} else {
			v.Set(w)
		}
	/*
		case *reflect.Float32Value:
		case *reflect.Float64Value:
	*/
	case *reflect.StringValue:
		if w, err := jm.decodeStr(); err != nil {
			return err
		} else {
			v.Set(w)
		}
	/*
		case *reflect.ArrayValue:
		case *reflect.SliceValue:
	*/
	case *reflect.StructValue:
		switch e1 := e.(type) {
		case *ConnInfoMsg:
			var flag bool
			var err os.Error
			if flag, err = jm.decodeBool(); err != nil {
				return err
			}
			if flag {
				if err = jm.decodeStruct(e1.SeedId); err != nil {
					return err
				}
			}
			flag = false
			if flag, err = jm.decodeBool(); err != nil {
				return err
			}
			if flag {
				var str string
				if str, err = jm.decodeStr(); err != nil {
					return err
				}
				e1.Error = os.ErrorString(str)
			}
			flag = false
			if flag, err = jm.decodeBool(); err != nil {
				return err
			}
			if flag {
				var str string
				if str, err = jm.decodeStr(); err != nil {
					return err
				}
				e1.ConnInfo = str
			}
		case *IdChanInfoMsg:
			dummyId := e1.Info[0].Id
			var num int
			var err os.Error
			if num, err = jm.decodeInt(); err != nil {
				return err
			}
			info := make([]*IdChanInfo, num)
			for i := 0; i < num; i++ {
				ici := new(IdChanInfo)
				ici.Id, _ = dummyId.Clone()
				if err = jm.decodeStruct(ici.Id); err != nil {
					return err
				}
				ici.ElemType = new(ChanElemTypeData)
				if err = jm.decodeStruct(ici.ElemType); err != nil {
					return err
				}
				info[i] = ici
			}
			e1.Info = info
		default:
			return jm.decodeStruct(e)
		}

	default:
		return os.ErrorString("unknown chan elem type found in jsonDemarshaler.demarshal")
	}
	return nil
}

//since gob/json only supports marshalling/demarshalling of structs as top-level Value
//add a struct wrapper for basic types, such as int/float/string/array/slice
type BoolWrapper struct {
	Val bool
}

type IntWrapper struct {
	Val int
}

type StrWrapper struct {
	Val string
}

type ByteArrayWrapper struct {
	Val []byte
}

type FloatWrapper struct {
	Val float
}
