//
// Copyright (c) 2010 - 2011 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"gob"
	"json"
	"io"
	"os"
	"reflect"
	"fmt"
)

//the common interface of all marshaler such as GobMarshaler and JsonMarshaler
type Marshaler interface {
	Marshal(Id, interface{}) os.Error
}

//the common interface of all demarshaler such as GobDemarshaler and JsonDemarshaler
type Demarshaler interface {
	Demarshal() (Id, interface{}, reflect.Value, os.Error)
}

//the common interface of all Marshaling policy such as GobMarshaling and JsonMarshaling
type MarshalingPolicy interface {
	NewMarshaler(io.Writer) Marshaler
	NewDemarshaler(io.Reader, *proxyImpl) Demarshaler
}

// marshalling policy using gob

type gobMarshalingPolicy byte
type gobMarshaler struct {
	*gob.Encoder
}
type gobDemarshaler struct {
	*gob.Decoder
	proxy *proxyImpl
}

//use package "gob" for marshaling
var GobMarshaling MarshalingPolicy = gobMarshalingPolicy(0)

func (g gobMarshalingPolicy) NewMarshaler(w io.Writer) Marshaler {
	return &gobMarshaler{gob.NewEncoder(w)}
}

func (g gobMarshalingPolicy) NewDemarshaler(r io.Reader, p *proxyImpl) Demarshaler {
	return &gobDemarshaler{Decoder: gob.NewDecoder(r), proxy: p}
}

func (gm *gobMarshaler) Marshal(id Id, e interface{}) (err os.Error) {
	if err = gm.Encode(id); err != nil || e == nil {
		return
	}
	switch e1 := e.(type) {
	case *ConnInfoMsg:
		if err = gm.Encode(e1.Id != nil); err != nil {
			return
		}
		if e1.Id != nil {
			if err = gm.Encode(e1.Id); err != nil {
				return
			}
		}
		if err = gm.Encode(e1.Error != nil); err != nil {
			return
		}
		if e1.Error != nil {
			if err = gm.Encode(e1.Error.String()); err != nil {
				return
			}
		}
		if err = gm.Encode(len(e1.ConnInfo) > 0); err != nil {
			return
		}
		if len(e1.ConnInfo) > 0 {
			if err = gm.Encode(e1.ConnInfo); err != nil {
				return
			}
		}
		if err = gm.Encode(e1.Type); err != nil {
			return
		}
		return
	case *ConnReadyMsg:
		num := len(e1.Info)
		if err = gm.Encode(num); err != nil {
			return
		}
		for _, v := range e1.Info {
			if err = gm.Encode(v.Id); err != nil {
				return
			}
			if err = gm.Encode(v.Credit); err != nil {
				return
			}
		}
		return
	case *IdChanInfoMsg:
		num := len(e1.Info)
		if err = gm.Encode(num); err != nil {
			return
		}
		for _, v := range e1.Info {
			if err = gm.Encode(v.Id); err != nil {
				return
			}
			if v.ElemType == nil {
				v.ElemType = new(chanElemTypeData)
				elemType := v.ChanType.Elem()
				v.ElemType.FullName = elemType.PkgPath() + "." + elemType.Name()
			}
			if err = gm.Encode(v.ElemType); err != nil {
				return
			}
		}
		return
	default:
		return gm.Encode(e)
	}
	return
}

func (gm *gobDemarshaler) Demarshal() (id Id, ctrlMsg interface{}, appMsg reflect.Value, err os.Error) {
	seedId := gm.proxy.router.seedId
	id, _ = seedId.Clone()
	if err = gm.Decode(id); err != nil {
		return
	}
	if id.Scope() == NumScope && id.Member() == NumMembership {
		return
	}
	switch id.SysIdIndex() {
	case ConnId, DisconnId, ErrorId:
		idc, _ := seedId.Clone()
		ctrlMsg = &ConnInfoMsg{Id: idc}
		err = gm.demarshalCtrlMsg(ctrlMsg)
	case ReadyId:
		idc, _ := seedId.Clone()
		ctrlMsg = &ConnReadyMsg{[]*ChanReadyInfo{&ChanReadyInfo{Id: idc}}}
		err = gm.demarshalCtrlMsg(ctrlMsg)
	case PubId, UnPubId, SubId, UnSubId:
		idc, _ := seedId.Clone()
		ctrlMsg = &IdChanInfoMsg{[]*IdChanInfo{&IdChanInfo{idc, nil, nil}}}
		err = gm.demarshalCtrlMsg(ctrlMsg)
	default: //appMsg
		chanType := gm.proxy.getExportRecvChanType(id)
		if chanType == nil {
			err = os.ErrorString(fmt.Sprintf("failed to find chanType for id %v", id))
			return
		}
		appMsg = reflect.MakeZero(chanType.Elem())
		err = gm.DecodeValue(appMsg)
	}
	return
}


func (gm *gobDemarshaler) demarshalCtrlMsg(e interface{}) (err os.Error) {
	switch e1 := e.(type) {
	case *ConnInfoMsg:
		flag := false
		if err = gm.Decode(&flag); err != nil {
			return
		}
		if flag {
			if err = gm.Decode(e1.Id); err != nil {
				return
			}
		}
		flag = false
		if err = gm.Decode(&flag); err != nil {
			return
		}
		if flag {
			str := ""
			if err = gm.Decode(&str); err != nil {
				return
			}
			e1.Error = os.ErrorString(str)
		}
		flag = false
		if err = gm.Decode(&flag); err != nil {
			return
		}
		if flag {
			str := ""
			if err = gm.Decode(&str); err != nil {
				return
			}
			e1.ConnInfo = str
		}
		if err = gm.Decode(&e1.Type); err != nil {
			return
		}
		return
	case *ConnReadyMsg:
		dummyId := e1.Info[0].Id
		num := 0
		if err = gm.Decode(&num); err != nil {
			return
		}
		if num > 0 {
			info := make([]*ChanReadyInfo, num)
			for i := 0; i < num; i++ {
				ici := new(ChanReadyInfo)
				ici.Id, _ = dummyId.Clone()
				if err = gm.Decode(ici.Id); err != nil {
					return
				}
				if err = gm.Decode(&ici.Credit); err != nil {
					return
				}
				info[i] = ici
			}
			e1.Info = info
		} else {
			e1.Info = nil
		}
		return
	case *IdChanInfoMsg:
		dummyId := e1.Info[0].Id
		num := 0
		if err = gm.Decode(&num); err != nil {
			return
		}
		if num > 0 {
			info := make([]*IdChanInfo, num)
			for i := 0; i < num; i++ {
				ici := new(IdChanInfo)
				ici.Id, _ = dummyId.Clone()
				if err = gm.Decode(ici.Id); err != nil {
					return
				}
				ici.ElemType = new(chanElemTypeData)
				if err = gm.Decode(ici.ElemType); err != nil {
					return
				}
				info[i] = ici
			}
			e1.Info = info
		} else {
			e1.Info = nil
		}
		return
	default:
		err = os.ErrorString("gobDemarshaler: Invalid Sys Msg Type")
	}
	return
}

// marshalling policy using json

type jsonMarshalingPolicy byte
type jsonMarshaler struct {
	*json.Encoder
}
type jsonDemarshaler struct {
	*json.Decoder
	proxy     *proxyImpl
	ptrBool   *reflect.PtrValue
	ptrInt    *reflect.PtrValue
	ptrFloat  *reflect.PtrValue
	ptrString *reflect.PtrValue
}

//use package "json" for marshaling
var JsonMarshaling MarshalingPolicy = jsonMarshalingPolicy(1)

func (j jsonMarshalingPolicy) NewMarshaler(w io.Writer) Marshaler {
	return &jsonMarshaler{json.NewEncoder(w)}
}

func (j jsonMarshalingPolicy) NewDemarshaler(r io.Reader, p *proxyImpl) Demarshaler {
	jm := &jsonDemarshaler{Decoder: json.NewDecoder(r), proxy: p}
	var xb *bool = nil
	jm.ptrBool = reflect.NewValue(xb).(*reflect.PtrValue)
	var xi *int = nil
	jm.ptrInt = reflect.NewValue(xi).(*reflect.PtrValue)
	var xf *float64 = nil
	jm.ptrFloat = reflect.NewValue(xf).(*reflect.PtrValue)
	var xs *string = nil
	jm.ptrString = reflect.NewValue(xs).(*reflect.PtrValue)
	return jm
}

func (jm *jsonMarshaler) Marshal(id Id, e interface{}) (err os.Error) {
	if err = jm.Encode(id); err != nil || e == nil {
		return
	}
	switch e1 := e.(type) {
	case *ConnInfoMsg:
		if err = jm.Encode(e1.Id != nil); err != nil {
			return
		}
		if e1.Id != nil {
			if err = jm.Encode(e1.Id); err != nil {
				return
			}
		}
		if err = jm.Encode(e1.Error != nil); err != nil {
			return
		}
		if e1.Error != nil {
			if err = jm.Encode(e1.Error.String()); err != nil {
				return
			}
		}
		if err = jm.Encode(len(e1.ConnInfo) > 0); err != nil {
			return
		}
		if len(e1.ConnInfo) > 0 {
			if err = jm.Encode(e1.ConnInfo); err != nil {
				return
			}
		}
		if err = jm.Encode(e1.Type); err != nil {
			return
		}
		return
	case *ConnReadyMsg:
		num := len(e1.Info)
		if err = jm.Encode(num); err != nil {
			return
		}
		for _, v := range e1.Info {
			if err = jm.Encode(v.Id); err != nil {
				return
			}
			if err = jm.Encode(v.Credit); err != nil {
				return
			}
		}
		return
	case *IdChanInfoMsg:
		num := len(e1.Info)
		if err = jm.Encode(num); err != nil {
			return
		}
		for _, v := range e1.Info {
			if err = jm.Encode(v.Id); err != nil {
				return
			}
			if v.ElemType == nil {
				v.ElemType = new(chanElemTypeData)
				elemType := v.ChanType.Elem()
				v.ElemType.FullName = elemType.PkgPath() + "." + elemType.Name()
			}
			if err = jm.Encode(v.ElemType); err != nil {
				return
			}
		}
		return
	default:
		return jm.Encode(e)
	}
	return
}

func (jm *jsonDemarshaler) Demarshal() (id Id, ctrlMsg interface{}, appMsg reflect.Value, err os.Error) {
	seedId := jm.proxy.router.seedId
	id, _ = seedId.Clone()
	if err = jm.Decode(id); err != nil {
		return
	}
	if id.Scope() == NumScope && id.Member() == NumMembership {
		return
	}
	switch id.SysIdIndex() {
	case ConnId, DisconnId, ErrorId:
		idc, _ := seedId.Clone()
		ctrlMsg = &ConnInfoMsg{Id: idc}
		err = jm.demarshalCtrlMsg(ctrlMsg)
	case ReadyId:
		idc, _ := seedId.Clone()
		ctrlMsg = &ConnReadyMsg{[]*ChanReadyInfo{&ChanReadyInfo{Id: idc}}}
		err = jm.demarshalCtrlMsg(ctrlMsg)
	case PubId, UnPubId, SubId, UnSubId:
		idc, _ := seedId.Clone()
		ctrlMsg = &IdChanInfoMsg{[]*IdChanInfo{&IdChanInfo{idc, nil, nil}}}
		err = jm.demarshalCtrlMsg(ctrlMsg)
	default: //appMsg
		chanType := jm.proxy.getExportRecvChanType(id)
		if chanType == nil {
			err = os.ErrorString(fmt.Sprintf("failed to find chanType for id %v", id))
			return
		}
		et := chanType.Elem()
		appMsg = reflect.MakeZero(et)
		var ptrT *reflect.PtrValue
		switch et := et.(type) {
		case *reflect.BoolType:
			ptrT = jm.ptrBool
			ptrT.PointTo(appMsg)
		case *reflect.IntType:
			ptrT = jm.ptrInt
			ptrT.PointTo(appMsg)
		case *reflect.FloatType:
			ptrT = jm.ptrFloat
			ptrT.PointTo(appMsg)
		case *reflect.StringType:
			ptrT = jm.ptrString
			ptrT.PointTo(appMsg)
		case *reflect.PtrType:
			sto := reflect.MakeZero(et.Elem())
			ptrT = appMsg.(*reflect.PtrValue)
			ptrT.PointTo(sto)
		default:
			err = os.ErrorString(fmt.Sprintf("invalid chanType for id %v", id))
			return
		}
		err = jm.Decode(ptrT.Interface())
	}
	return
}

func (jm *jsonDemarshaler) demarshalCtrlMsg(e interface{}) (err os.Error) {
	switch e1 := e.(type) {
	case *ConnInfoMsg:
		flag := false
		if err = jm.Decode(&flag); err != nil {
			return
		}
		if flag {
			if err = jm.Decode(e1.Id); err != nil {
				return
			}
		}
		flag = false
		if err = jm.Decode(&flag); err != nil {
			return
		}
		if flag {
			str := ""
			if err = jm.Decode(&str); err != nil {
				return
			}
			e1.Error = os.ErrorString(str)
		}
		flag = false
		if err = jm.Decode(&flag); err != nil {
			return
		}
		if flag {
			str := ""
			if err = jm.Decode(&str); err != nil {
				return
			}
			e1.ConnInfo = str
		}
		if err = jm.Decode(&e1.Type); err != nil {
			return
		}
		return
	case *ConnReadyMsg:
		dummyId := e1.Info[0].Id
		num := 0
		if err = jm.Decode(&num); err != nil {
			return
		}
		if num > 0 {
			info := make([]*ChanReadyInfo, num)
			for i := 0; i < num; i++ {
				ici := new(ChanReadyInfo)
				ici.Id, _ = dummyId.Clone()
				if err = jm.Decode(ici.Id); err != nil {
					return
				}
				if err = jm.Decode(&ici.Credit); err != nil {
					return
				}
				info[i] = ici
			}
			e1.Info = info
		} else {
			e1.Info = nil
		}
		return
	case *IdChanInfoMsg:
		dummyId := e1.Info[0].Id
		num := 0
		if err = jm.Decode(&num); err != nil {
			return
		}
		if num > 0 {
			info := make([]*IdChanInfo, num)
			for i := 0; i < num; i++ {
				ici := new(IdChanInfo)
				ici.Id, _ = dummyId.Clone()
				if err = jm.Decode(ici.Id); err != nil {
					return
				}
				ici.ElemType = new(chanElemTypeData)
				if err = jm.Decode(ici.ElemType); err != nil {
					return
				}
				info[i] = ici
			}
			e1.Info = info
		} else {
			e1.Info = nil
		}
		return
	default:
		err = os.ErrorString("jsonDemarshaler: Invalid Sys Msg Type")
	}
	return
}
