//
// Copyright (c) 2010 - 2011 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"reflect"
	"os"
)

//store marshaled information about ChanType
type chanElemTypeData struct {
	//full name of elem type: pkg_path.data_type
	FullName string
	//the following should be string encoding of the elem type
	//it contains info for both names/types.
	//e.g. for struct, it could be in form of "struct{fieldName:typeName,...}"
	TypeEncoding string
}

//a message struct holding information about id and its associated ChanType
type IdChanInfo struct {
	Id       Id
	ChanType *reflect.ChanType
	ElemType *chanElemTypeData
}

//a message struct for propagating router's namespace changes (chan attachments or detachments)
type IdChanInfoMsg struct {
	Info []*IdChanInfo
}

//the generic message
type genericMsg struct {
	Id   Id
	Data interface{}
}

//a message struct containing information about remote router connection
type ConnInfoMsg struct {
	ConnInfo       string
	Error          os.Error
	Id             Id
	Type           int //async/flowControlled/raw
}

//recver-router notify sender-router which channel are ready to recv how many msgs
type ChanReadyInfo struct {
	Id     Id
	Credit int
}

type ConnReadyMsg struct {
	Info []*ChanReadyInfo
}

type BindEventType int8

const (
	PeerAttach BindEventType = iota
	PeerDetach
	EndOfData
)

//a message struct containing information for peer (sender/recver) binding/connection.
//sent by router whenever peer attached or detached.
//   Type:  the type of event just happened: PeerAttach/PeerDetach/EndOfData
//   Count: how many peers are still bound now
type BindEvent struct {
	Type  BindEventType
	Count int //total attached
}
