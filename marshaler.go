//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"gob"
	"json"
	"io"
	"os"
)

//the common interface of all marshaler such as GobMarshaler and JsonMarshaler
type Marshaler interface {
	Marshal(interface{}) os.Error
}

//the common interface of all demarshaler such as GobDemarshaler and JsonDemarshaler
type Demarshaler interface {
	Demarshal(interface{}) os.Error
}

//the common interface of all Marshaling policy such as GobMarshaling and JsonMarshaling
type MarshalingPolicy interface {
	NewMarshaler(io.Writer) Marshaler
	NewDemarshaler(io.Reader) Demarshaler
}

// marshalling policy using gob

type gobMarshalingPolicy byte
type gobMarshaler gob.Encoder
type gobDemarshaler gob.Decoder

//use package "gob" for marshaling
var GobMarshaling MarshalingPolicy = gobMarshalingPolicy(0)

func (g gobMarshalingPolicy) NewMarshaler(w io.Writer) Marshaler {
	return (*gobMarshaler)(gob.NewEncoder(w))
}

func (g gobMarshalingPolicy) NewDemarshaler(r io.Reader) Demarshaler {
	return (*gobDemarshaler)(gob.NewDecoder(r))
}

func (gm *gobMarshaler) Marshal(e interface{}) (err os.Error) {
	switch e1 := e.(type) {
	case *ConnInfoMsg:
		if err = ((*gob.Encoder)(gm)).Encode(boolWrapper{e1.SeedId != nil}); err != nil {
			return
		}
		if e1.SeedId != nil {
			if err = ((*gob.Encoder)(gm)).Encode(e1.SeedId); err != nil {
				return
			}
		}
		if err = ((*gob.Encoder)(gm)).Encode(boolWrapper{e1.Error != nil}); err != nil {
			return
		}
		if e1.Error != nil {
			if err = ((*gob.Encoder)(gm)).Encode(strWrapper{e1.Error.String()}); err != nil {
				return
			}
		}
		if err = ((*gob.Encoder)(gm)).Encode(boolWrapper{len(e1.ConnInfo) > 0}); err != nil {
			return
		}
		if len(e1.ConnInfo) > 0 {
			if err = ((*gob.Encoder)(gm)).Encode(strWrapper{e1.ConnInfo}); err != nil {
				return
			}
		}
		return nil
	case *IdChanInfoMsg:
		num := len(e1.Info)
		if err = ((*gob.Encoder)(gm)).Encode(intWrapper{num}); err != nil {
			return
		}
		for _, v := range e1.Info {
			if err = ((*gob.Encoder)(gm)).Encode(v.Id); err != nil {
				return
			}
			if v.ElemType == nil {
				v.ElemType = new(chanElemTypeData)
				elemType := v.ChanType.Elem()
				v.ElemType.FullName = elemType.PkgPath() + "." + elemType.Name()
			}
			if err = ((*gob.Encoder)(gm)).Encode(v.ElemType); err != nil {
				return
			}
		}
		return nil
	case bool:
		return ((*gob.Encoder)(gm)).Encode(boolWrapper{e1})
	case int:
		return ((*gob.Encoder)(gm)).Encode(intWrapper{e1})
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
	case float:
		return ((*gob.Encoder)(gm)).Encode(floatWrapper{e1})
		/*
		 case *reflect.Float32Value:
		 case *reflect.Float64Value:
		 */
	case string:
		return ((*gob.Encoder)(gm)).Encode(strWrapper{e1})
		/*
		 case *reflect.ArrayValue:
		 case *reflect.SliceValue:
		 */
	default:
	//case Id:
		return ((*gob.Encoder)(gm)).Encode(e)
	}
	return nil
}

func (gm *gobDemarshaler) Demarshal(e interface{}) (err os.Error) {
	switch e1 := e.(type) {
	case *ConnInfoMsg:
		flag := &boolWrapper{}
		if err = ((*gob.Decoder)(gm)).Decode(flag); err != nil {
			return
		}
		if flag.Val {
			if err = ((*gob.Decoder)(gm)).Decode(e1.SeedId); err != nil {
				return
			}
		}
		flag.Val = false
		if err = ((*gob.Decoder)(gm)).Decode(flag); err != nil {
			return
		}
		if flag.Val {
			str := &strWrapper{}
			if err = ((*gob.Decoder)(gm)).Decode(str); err != nil {
				return
			}
			e1.Error = os.ErrorString(str.Val)
		}
		flag.Val = false
		if err = ((*gob.Decoder)(gm)).Decode(flag); err != nil {
			return
		}
		if flag.Val {
			str := &strWrapper{}
			if err = ((*gob.Decoder)(gm)).Decode(str); err != nil {
				return
			}
			e1.ConnInfo = str.Val
		}
		return
	case *IdChanInfoMsg:
		dummyId := e1.Info[0].Id
		num := &intWrapper{}
		if err = ((*gob.Decoder)(gm)).Decode(num); err != nil {
			return
		}
		info := make([]*IdChanInfo, num.Val)
		for i := 0; i < num.Val; i++ {
			ici := new(IdChanInfo)
			ici.Id, _ = dummyId.Clone()
			if err = ((*gob.Decoder)(gm)).Decode(ici.Id); err != nil {
				return
			}
			ici.ElemType = new(chanElemTypeData)
			if err = ((*gob.Decoder)(gm)).Decode(ici.ElemType); err != nil {
				return
			}
			info[i] = ici
		}
		e1.Info = info
		return
	case *bool:
		w := &boolWrapper{}
		if err = ((*gob.Decoder)(gm)).Decode(w); err != nil {
			return
		}
		*e1 = w.Val
	case *int:
		w := &intWrapper{}
		if err = ((*gob.Decoder)(gm)).Decode(w); err != nil {
			return
		}
		*e1 = w.Val
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
	case *float:
		w := &floatWrapper{}
		if err = ((*gob.Decoder)(gm)).Decode(w); err != nil {
			return
		}
		*e1 = w.Val
		/*
		 case *reflect.Float32Value:
		 case *reflect.Float64Value:
		 */
	case *string:
		w := &strWrapper{}
		if err = ((*gob.Decoder)(gm)).Decode(w); err != nil {
			return
		}
		*e1 = w.Val
		/*
		 case *reflect.ArrayValue:
		 case *reflect.SliceValue:
		 */
	default:
	//case Id:
		return ((*gob.Decoder)(gm)).Decode(e)
	}
	return
}

// marshalling policy using json

type jsonMarshalingPolicy byte
type jsonMarshaler json.Encoder
type jsonDemarshaler json.Decoder

//use package "json" for marshaling
var JsonMarshaling MarshalingPolicy = jsonMarshalingPolicy(1)

func (j jsonMarshalingPolicy) NewMarshaler(w io.Writer) Marshaler {
	return (*jsonMarshaler)(json.NewEncoder(w))
}

func (j jsonMarshalingPolicy) NewDemarshaler(r io.Reader) Demarshaler {
	return (*jsonDemarshaler)(json.NewDecoder(r))
}

func (jm *jsonMarshaler) Marshal(e interface{}) (err os.Error) {
	switch e1 := e.(type) {
	case *ConnInfoMsg:
		if err = ((*json.Encoder)(jm)).Encode(e1.SeedId != nil); err != nil {
			return
		}
		if e1.SeedId != nil {
			if err = ((*json.Encoder)(jm)).Encode(e1.SeedId); err != nil {
				return
			}
		}
		if err = ((*json.Encoder)(jm)).Encode(e1.Error != nil); err != nil {
			return
		}
		if e1.Error != nil {
			if err = ((*json.Encoder)(jm)).Encode(e1.Error.String()); err != nil {
				return
			}
		}
		if err = ((*json.Encoder)(jm)).Encode(len(e1.ConnInfo) > 0); err != nil {
			return
		}
		if len(e1.ConnInfo) > 0 {
			if err = ((*json.Encoder)(jm)).Encode(e1.ConnInfo); err != nil {
				return
			}
		}
		return nil
	case *IdChanInfoMsg:
		num := len(e1.Info)
		if err = ((*json.Encoder)(jm)).Encode(num); err != nil {
			return
		}
		for _, v := range e1.Info {
			if err = ((*json.Encoder)(jm)).Encode(v.Id); err != nil {
				return
			}
			if v.ElemType == nil {
				v.ElemType = new(chanElemTypeData)
				elemType := v.ChanType.Elem()
				v.ElemType.FullName = elemType.PkgPath() + "." + elemType.Name()
			}
			if err = ((*json.Encoder)(jm)).Encode(v.ElemType); err != nil {
				return
			}
		}
		return nil
	default:
		return ((*json.Encoder)(jm)).Encode(e)
	}
	return
}

func (jm *jsonDemarshaler) Demarshal(e interface{}) (err os.Error) {
	switch e1 := e.(type) {
	case *ConnInfoMsg:
		flag := false
		str := ""
		if err = ((*json.Decoder)(jm)).Decode(&flag); err != nil {
			return
		}
		if flag {
			if err = ((*json.Decoder)(jm)).Decode(e1.SeedId); err != nil {
				return
			}
		}
		flag = false
		if err = ((*json.Decoder)(jm)).Decode(&flag); err != nil {
			return
		}
		if flag {
			if err = ((*json.Decoder)(jm)).Decode(&str); err != nil {
				return
			}
			e1.Error = os.ErrorString(str)
		}
		flag = false
		if err = ((*json.Decoder)(jm)).Decode(&flag); err != nil {
			return
		}
		if flag {
			str  = ""
			if err = ((*json.Decoder)(jm)).Decode(&str); err != nil {
				return
			}
			e1.ConnInfo = str
		}
		return
	case *IdChanInfoMsg:
		dummyId := e1.Info[0].Id
		num := 0
		if err = ((*json.Decoder)(jm)).Decode(&num); err != nil {
			return
		}
		info := make([]*IdChanInfo, num)
		for i := 0; i < num; i++ {
			ici := new(IdChanInfo)
			ici.Id, _ = dummyId.Clone()
			if err = ((*json.Decoder)(jm)).Decode(ici.Id); err != nil {
				return
			}
			ici.ElemType = new(chanElemTypeData)
			if err = ((*json.Decoder)(jm)).Decode(ici.ElemType); err != nil {
				return
			}
			info[i] = ici
		}
		e1.Info = info
		return
	default:
		return ((*json.Decoder)(jm)).Decode(e)
	}
	return
}

//since json/json only supports marshalling/demarshalling of structs as top-level Value
//add a struct wrapper for basic types, such as int/float/string/array/slice
type boolWrapper struct {
	Val bool
}

type intWrapper struct {
	Val int
}

type strWrapper struct {
	Val string
}

type byteArrayWrapper struct {
	Val []byte
}

type floatWrapper struct {
	Val float
}
