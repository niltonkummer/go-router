//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//
package router

import (
	"fmt"
	"strconv"
	"os"
	"reflect"
)

//Scope is the scope to publish/send msgs
const (
	ScopeGlobal = iota
	ScopeRemote
	ScopeLocal
	NumScope
)

func ScopeString(s int) string {
	switch s {
	case ScopeLocal:
		return "ScopeLocal"
	case ScopeRemote:
		return "ScopeRemote"
	case ScopeGlobal:
		return "ScopeGlobal"
	}
	return "InvalidScope"
}

//Membership identifies whether communicating peers are connected to namespace of the same router
//or diff & connected routers
const (
	MemberLocal = iota
	MemberRemote
	NumMembership
)

func MemberString(m int) string {
	switch m {
	case MemberLocal:
		return "MemberLocal"
	case MemberRemote:
		return "MemberRemote"
	}
	return "InvalidMembership"
}

//MatchType describes the types of namespaces and match algorithms used for id-matching
type MatchType int

const (
	ExactMatch  MatchType = iota
	PrefixMatch           // for PathId
	AssocMatch            // for RegexId, TupleId
)

//Id defines the common interface shared by all kinds of ids: integers/strings/pathnames...
type Id interface {
	//query
	Scope() int
	Member() int
	//for store in map
	Key() interface{}
	//for id matching
	Match(Id) bool
	MatchType() MatchType
	//for generating other ids of same type
	SysID(int, ...) (Id, os.Error) //generate sys ids, also called as method of "router"
	Clone(...) (Id, os.Error)      //create a new id with same id, but possible diff scope & membership
	//Stringer
	String() string
}

//indices for sys msgs
const (
	RouterConnId = iota
	RouterDisconnId
	ConnErrorId
	ConnReadyId
	PubId
	UnPubId
	SubId
	UnSubId
	NumSysIds
)

//some system level internal ids
const (
	RouterLogId = NumSysIds + iota
	RouterFaultId
	NumSysInternalIds
)

//a function used as predicate in router.idsForSend()/idsForRecv() to find all ids in a router's
//namespace which are exported to outside
func ExportedId(id Id) bool {
	return id.Member() == MemberLocal && (id.Scope() == ScopeRemote || id.Scope() == ScopeGlobal)
}

//use integer as ids in router
type IntId struct {
	Val       int
	ScopeVal  int
	MemberVal int
}

func (id IntId) Clone(args ...) (nnid Id, err os.Error) {
	nid := &IntId{Val: id.Val, ScopeVal: id.ScopeVal, MemberVal: id.MemberVal}
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() > 0 {
		if nid.ScopeVal, err = parseIdArg(v.Field(0)); err != nil {
			return
		}
	}
	if v.NumField() > 1 {
		if nid.MemberVal, err = parseIdArg(v.Field(1)); err != nil {
			return
		}
	}
	nnid = nid
	return
}

func (id IntId) Key() interface{} { return id.Val }

func (id1 IntId) Match(id2 Id) bool {
	if id3, ok := id2.(*IntId); ok {
		return id1.Val == id3.Val
	}
	return false
}

func (id IntId) MatchType() MatchType { return ExactMatch }

func (id IntId) Scope() int  { return id.ScopeVal }
func (id IntId) Member() int { return id.MemberVal }
func (id IntId) String() string {
	return fmt.Sprintf("%d_%s_%s", id.Val, ScopeString(id.ScopeVal), MemberString(id.MemberVal))
}
//define 8 system msg ids
var IntSysIdBase int = -10101 //need better values
func (id IntId) SysID(indx int, args ...) (ssid Id, err os.Error) {
	if indx < 0 || indx >= NumSysInternalIds {
		err = os.ErrorString(errInvalidSysId)
		return
	}
	sid := &IntId{Val: (IntSysIdBase - indx)}
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() > 0 {
		if sid.ScopeVal, err = parseIdArg(v.Field(0)); err != nil {
			return
		}
	}
	if v.NumField() > 1 {
		if sid.MemberVal, err = parseIdArg(v.Field(1)); err != nil {
			return
		}
	}
	ssid = sid
	return
}

//use strings as ids in router
type StrId struct {
	Val       string
	ScopeVal  int
	MemberVal int
}

func (id StrId) Clone(args ...) (nnid Id, err os.Error) {
	nid := &StrId{Val: id.Val, ScopeVal: id.ScopeVal, MemberVal: id.MemberVal}
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() > 0 {
		if nid.ScopeVal, err = parseIdArg(v.Field(0)); err != nil {
			return
		}
	}
	if v.NumField() > 1 {
		if nid.MemberVal, err = parseIdArg(v.Field(1)); err != nil {
			return
		}
	}
	nnid = nid
	return
}

func (id StrId) Key() interface{} { return id.Val }

func (id1 StrId) Match(id2 Id) bool {
	if id3, ok := id2.(*StrId); ok {
		return id1.Val == id3.Val
	}
	return false
}

func (id StrId) MatchType() MatchType { return ExactMatch }

func (id StrId) Scope() int  { return id.ScopeVal }
func (id StrId) Member() int { return id.MemberVal }
func (id StrId) String() string {
	return fmt.Sprintf("%s_%s_%s", id.Val, ScopeString(id.ScopeVal), MemberString(id.MemberVal))
}
//define 8 system msg ids
var StrSysIdBase string = "-10101" //need better values
func (id StrId) SysID(indx int, args ...) (ssid Id, err os.Error) {
	if indx < 0 || indx >= NumSysInternalIds {
		err = os.ErrorString(errInvalidSysId)
		return
	}
	sid := &StrId{Val: (StrSysIdBase + strconv.Itoa(indx))}
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() > 0 {
		if sid.ScopeVal, err = parseIdArg(v.Field(0)); err != nil {
			return
		}
	}
	if v.NumField() > 1 {
		if sid.MemberVal, err = parseIdArg(v.Field(1)); err != nil {
			return
		}
	}
	ssid = sid
	return
}

//use file-system like pathname as ids
//pathId has diff Match() algo from StrId
type PathId struct {
	Val       string
	ScopeVal  int
	MemberVal int
}

func (id PathId) Clone(args ...) (nnid Id, err os.Error) {
	nid := &PathId{Val: id.Val, ScopeVal: id.ScopeVal, MemberVal: id.MemberVal}
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() > 0 {
		if nid.ScopeVal, err = parseIdArg(v.Field(0)); err != nil {
			return
		}
	}
	if v.NumField() > 1 {
		if nid.MemberVal, err = parseIdArg(v.Field(1)); err != nil {
			return
		}
	}
	nnid = nid
	return
}

func (id PathId) Key() interface{} { return id.Val }

func (id1 PathId) Match(id2 Id) bool {
	if id3, ok := id2.(*PathId); ok {
		p1, p2 := id1.Val, id3.Val
		n1, n2 := len(p1), len(p2)
		//make sure both are valid path names
		if n1 == 0 || p1[0] != '/' {
			return false
		}
		if n2 == 0 || p2[0] != '/' {
			return false
		}
		//check wildcards
		w1, w2 := p1[n1-1] == '*', p2[n2-1] == '*'
		if w1 {
			n1--
		}
		if w2 {
			n2--
		}
		if (n1 < n2) && w1 {
			return p1[0:n1] == p2[0:n1]
		}
		if (n2 < n1) && w2 {
			return p1[0:n2] == p2[0:n2]
		}
		if n1 == n2 {
			return p1[0:n1] == p2[0:n1]
		}
	}
	return false
}

func (id PathId) MatchType() MatchType { return PrefixMatch }

func (id PathId) Scope() int  { return id.ScopeVal }
func (id PathId) Member() int { return id.MemberVal }
func (id PathId) String() string {
	return fmt.Sprintf("%s_%s_%s", id.Val, ScopeString(id.ScopeVal), MemberString(id.MemberVal))
}
//define 8 system msg ids
var PathSysIdBase string = "/10101" //need better values
func (id PathId) SysID(indx int, args ...) (ssid Id, err os.Error) {
	if indx < 0 || indx >= NumSysInternalIds {
		err = os.ErrorString(errInvalidSysId)
		return
	}
	sid := &PathId{Val: (PathSysIdBase + "/" + strconv.Itoa(indx))}
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() > 0 {
		if sid.ScopeVal, err = parseIdArg(v.Field(0)); err != nil {
			return
		}
	}
	if v.NumField() > 1 {
		if sid.MemberVal, err = parseIdArg(v.Field(1)); err != nil {
			return
		}
	}
	ssid = sid
	return
}

//use a common msgTag as Id
type MsgTag struct {
	Family int //divide all msgs into families: system, fault, provision,...
	Tag    int //further division inside a family
}

func (t MsgTag) String() string { return fmt.Sprintf("%d_%d", t.Family, t.Tag) }

type MsgId struct {
	Val       MsgTag
	ScopeVal  int
	MemberVal int
}

func (id MsgId) Clone(args ...) (nnid Id, err os.Error) {
	nid := &MsgId{Val: id.Val, ScopeVal: id.ScopeVal, MemberVal: id.MemberVal}
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() > 0 {
		if nid.ScopeVal, err = parseIdArg(v.Field(0)); err != nil {
			return
		}
	}
	if v.NumField() > 1 {
		if nid.MemberVal, err = parseIdArg(v.Field(1)); err != nil {
			return
		}
	}
	nnid = nid
	return
}

func (id MsgId) Key() interface{} { return id.Val.String() }

func (id1 MsgId) Match(id2 Id) bool {
	if id3, ok := id2.(*MsgId); ok {
		return id1.Val.Family == id3.Val.Family &&
			id1.Val.Tag == id3.Val.Tag
	}
	return false
}

func (id MsgId) MatchType() MatchType { return ExactMatch }

func (id MsgId) Scope() int  { return id.ScopeVal }
func (id MsgId) Member() int { return id.MemberVal }
func (id MsgId) String() string {
	return fmt.Sprintf("%v_%s_%s", id.Val, ScopeString(id.ScopeVal), MemberString(id.MemberVal))
}
//define 8 system msg ids
var MsgSysIdBase MsgTag = MsgTag{-10101, -10101} //need better values
func (id MsgId) SysID(indx int, args ...) (ssid Id, err os.Error) {
	if indx < 0 || indx >= NumSysInternalIds {
		err = os.ErrorString(errInvalidSysId)
		return
	}
	msgTag := MsgSysIdBase
	msgTag.Tag -= indx
	sid := &MsgId{Val: msgTag}
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() > 0 {
		if sid.ScopeVal, err = parseIdArg(v.Field(0)); err != nil {
			return
		}
	}
	if v.NumField() > 1 {
		if sid.MemberVal, err = parseIdArg(v.Field(1)); err != nil {
			return
		}
	}
	ssid = sid
	return
}

//various Id constructors

var (
	DummyIntId  Id = &IntId{Val: -10201}
	DummyStrId  Id = &StrId{Val: "-10201"}
	DummyPathId Id = &PathId{Val: "-10201"}
	DummyMsgId  Id = &MsgId{Val: MsgTag{-10201, -10201}}
)

func IntID(args ...) Id {
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() == 0 {
		return DummyIntId
	}
	id := &IntId{}
	var err os.Error
	iv, ok := v.Field(0).(*reflect.IntValue)
	if !ok {
		return nil
	}
	id.Val = iv.Get()
	if v.NumField() > 1 {
		if id.ScopeVal, err = parseIdArg(v.Field(1)); err != nil {
			return nil
		}
	}
	if v.NumField() > 2 {
		if id.MemberVal, err = parseIdArg(v.Field(2)); err != nil {
			return nil
		}
	}
	return id
}

func StrID(args ...) Id {
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() == 0 {
		return DummyStrId
	}
	id := &StrId{}
	sv, ok := v.Field(0).(*reflect.StringValue)
	if !ok {
		return nil
	}
	id.Val = sv.Get()
	if len(id.Val) == 0 {
		return nil
	}
	var err os.Error
	if v.NumField() > 1 {
		if id.ScopeVal, err = parseIdArg(v.Field(1)); err != nil {
			return nil
		}
	}
	if v.NumField() > 2 {
		if id.MemberVal, err = parseIdArg(v.Field(2)); err != nil {
			return nil
		}
	}
	return id
}

func PathID(args ...) Id {
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() == 0 {
		return DummyPathId
	}
	id := &PathId{}
	sv, ok := v.Field(0).(*reflect.StringValue)
	if !ok {
		return nil
	}
	id.Val = sv.Get()
	if len(id.Val) == 0 || id.Val[0] != '/' {
		return nil
	}
	var err os.Error
	if v.NumField() > 1 {
		if id.ScopeVal, err = parseIdArg(v.Field(1)); err != nil {
			return nil
		}
	}
	if v.NumField() > 2 {
		if id.MemberVal, err = parseIdArg(v.Field(2)); err != nil {
			return nil
		}
	}
	return id
}

func MsgID(args ...) Id {
	v := reflect.NewValue(args).(*reflect.StructValue)
	if v.NumField() == 0 {
		return DummyMsgId
	}
	if v.NumField() == 1 {
		//should have at least 2 ints for family & tag
		return nil
	}
	id := &MsgId{}
	iv, ok := v.Field(0).(*reflect.IntValue)
	if !ok {
		return nil
	}
	id.Val.Family = iv.Get()
	iv, ok = v.Field(1).(*reflect.IntValue)
	if !ok {
		return nil
	}
	id.Val.Tag = iv.Get()
	var err os.Error
	if v.NumField() > 2 {
		if id.ScopeVal, err = parseIdArg(v.Field(2)); err != nil {
			return nil
		}
	}
	if v.NumField() > 3 {
		if id.MemberVal, err = parseIdArg(v.Field(3)); err != nil {
			return nil
		}
	}
	return id
}

func parseIdArg(v reflect.Value) (s int, err os.Error) {
	if iv, ok := v.(*reflect.IntValue); ok {
		s = iv.Get()
	} else {
		err = os.ErrorString(errInvalidId)
	}
	return
}


// for scope checking

var scope_check_table [][]int = [][]int{
	[]int{1, 0, 1, 0, 0, 1},
	[]int{0, 0, 0, 0, 0, 1},
	[]int{1, 0, 1, 0, 0, 0},
	[]int{0, 0, 0, 0, 0, 0},
	[]int{0, 0, 0, 0, 0, 0},
	[]int{1, 1, 0, 0, 0, 0},
}

//validate the src and dest scope matches
func scope_match(src, dst Id) bool {
	src_row := int(src.Member())*int(NumScope) + int(src.Scope())
	dst_col := int(dst.Member())*int(NumScope) + int(dst.Scope())
	return scope_check_table[src_row][dst_col] == 1
}
