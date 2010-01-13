//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//
package router

import (
	"reflect"
	"os"
)

type ChanElemTypeData struct {
	//full name of elem type: pkg_path.data_type
	FullName string
	//the following should be string encoding of the elem type
	//it contains info for both names/types.
	//e.g. for struct, it could be in form of "struct{fieldName:typeName,...}"
	TypeEncoding string
}

type IdChanInfo struct {
	Id       Id
	ChanType *reflect.ChanType
	ElemType *ChanElemTypeData
}

type IdChanInfoMsg struct {
	Info []*IdChanInfo
}

type GenericMsg struct {
	Id   Id
	Data interface{}
}

type ConnInfoMsg struct {
	ConnInfo string
	Error    os.Error
	SeedId   Id
}

type BindEventType int8

const (
	PeerAttach BindEventType = iota
	PeerDetach
	EndOfData
)

type BindEvent struct {
	Type  BindEventType
	Count int //total attached
}

type ChanCloseMsg struct{}
