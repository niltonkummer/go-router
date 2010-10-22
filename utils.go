//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"reflect"
	"os"
	"fmt"
	"sync"
)

//GenericChan wraps "chan *genericMsg" and presents the same api as reflectChanValue
type genericMsgChan struct {
	id Id
	ch chan *genericMsg
	idTranslate func(Id)Id
	sendChanCloseMsg bool
}
func (gch *genericMsgChan) Type() reflect.Type { return reflect.Typeof(gch.ch) }
func (gch *genericMsgChan) Interface() interface{} { return gch }
func (gch *genericMsgChan) Cap() int { return cap(gch.ch) }
func (gch *genericMsgChan) Close() { 
	if gch.sendChanCloseMsg {
		id1 := gch.id
		if gch.idTranslate != nil {
			id1 = gch.idTranslate(id1)  
		}
		id1, _ = id1.Clone(NumScope, NumMembership) //special id to mark chan close
		gch.ch <- &genericMsg{id1, nil} 
	}
}
func (gch *genericMsgChan) Closed() bool { return closed(gch.ch) }
func (gch *genericMsgChan) IsNil() bool { return gch.ch == nil }
func (gch *genericMsgChan) Len() int { return len(gch.ch) }
func (gch *genericMsgChan) Recv() reflect.Value { return reflect.NewValue((<-gch.ch).Data) }
func (gch *genericMsgChan) Send(v reflect.Value) { 
	id1 := gch.id
	if gch.idTranslate != nil {
		id1 = gch.idTranslate(id1)
	}
	gch.ch <- &genericMsg{id1, v.Interface()} 
}
func (gch *genericMsgChan) TryRecv() reflect.Value { 
	v, ok := <- gch.ch
	if !ok {
		return nil
	}
	return reflect.NewValue(v.Data)
}
func (gch *genericMsgChan) TrySend(v reflect.Value) bool { 
	id1 := gch.id
	if gch.idTranslate != nil {
		id1 = gch.idTranslate(id1)
	}
	return gch.ch <- &genericMsg{id1, v.Interface()} 
}

//recvChanBundle groups a set of recvChans together
//recvChanBundle has no lock protection, since it is only used from proxy mainLoop
type recverInBundle struct {
	id          Id
	ch          reflectChanValue
	endp        *Endpoint
}

type recvChanBundle struct {
	router           *routerImpl
	scope            int
	member           int
	recvChans        map[interface{}]*recverInBundle
}

func newRecvChanBundle(r Router, s int, m int) *recvChanBundle {
	rcb := new(recvChanBundle)
	rcb.router = r.(*routerImpl)
	rcb.scope = s
	rcb.member = m
	rcb.recvChans = make(map[interface{}]*recverInBundle)
	return rcb
}

func (rcb *recvChanBundle) RecverExist(id Id) bool {
	_, ok := rcb.recvChans[id.Key()]
	return ok
}

func (rcb *recvChanBundle) BindingCount(id Id) int {
	r, ok := rcb.recvChans[id.Key()]
	if !ok {
		return -1
	}
	return r.endp.NumBindings()
}

func (rcb *recvChanBundle) AllRecverInfo() []*IdChanInfo {
	info := make([]*IdChanInfo, len(rcb.recvChans))
	num := 0
	for _, v := range rcb.recvChans {
		ici := &IdChanInfo{}
		ici.Id = v.id
		ici.ChanType = v.ch.Type().(*reflect.ChanType)
		info[num] = ici
		num++
	}
	return info
}

func (rcb *recvChanBundle) AddRecver(id Id, ch reflectChanValue) (err os.Error) {
	_, ok := rcb.recvChans[id.Key()]
	if ok {
		err = os.ErrorString("router recvChanBundle: AddRecver duplicated id")
		return
	}
	r := new(recverInBundle)
	r.id, _ = id.Clone(rcb.scope, rcb.member)
	r.ch = ch
	if gch, ok := ch.(*genericMsgChan); ok {
		gch.id = r.id
	}
	rt := rcb.router
	r.endp, err = rt.AttachRecvChan(r.id, r.ch.Interface())
	if err != nil {
		return
	}
	rcb.recvChans[r.id.Key()] = r
	rcb.router.Log(LOG_INFO, fmt.Sprintf("add recver for %v", r.id))
	return
}

func (rcb *recvChanBundle) DelRecver(id Id) os.Error {
	r, ok := rcb.recvChans[id.Key()]
	if !ok {
		return os.ErrorString("router recvChanBundle: DelRecver id doesnt exist")
	}
	rcb.recvChans[id.Key()] = r, false
	err := rcb.router.DetachChan(r.id, r.ch.Interface())
	if err != nil {
		rcb.router.LogError(err)
	}
	return nil
}

func (rcb *recvChanBundle) Close() {
	for k, r := range rcb.recvChans {
		rcb.recvChans[k] = r, false
		err := rcb.router.DetachChan(r.id, r.ch.Interface())
		if err != nil {
			rcb.router.LogError(err)
		}
	}
}

//sendChanBundle groups a set of sendChans together
type senderInBundle struct {
	id          Id
	ch          *reflect.ChanValue
	endp        *Endpoint
}

type sendChanBundle struct {
	router *routerImpl
	scope  int
	member int
	//for syncing modifying sendChans map from mainLoop and client access
	sync.Mutex
	sendChans map[interface{}]*senderInBundle
}

func newSendChanBundle(r Router, s int, m int) *sendChanBundle {
	scb := new(sendChanBundle)
	scb.router = r.(*routerImpl)
	scb.scope = s
	scb.member = m
	scb.sendChans = make(map[interface{}]*senderInBundle)
	return scb
}

//findSender return ChanValue and its number of bindings
func (scb *sendChanBundle) findSender(id Id) (*reflect.ChanValue, int) {
	scb.Lock() //!no need for lock, since add/delSender and SenderExist all from same proxy ctrlMainLoop
	s, ok := scb.sendChans[id.Key()]
	scb.Unlock()
	if !ok {
		return nil, 0
	}
	return s.ch, s.endp.NumBindings()
}

func (scb *sendChanBundle) AllSenderInfo() []*IdChanInfo {
	scb.Lock()
	defer scb.Unlock()
	info := make([]*IdChanInfo, len(scb.sendChans))
	num := 0
	for _, v := range scb.sendChans {
		ici := &IdChanInfo{}
		ici.Id = v.id
		ici.ChanType = v.ch.Type().(*reflect.ChanType)
		info[num] = ici
		num++
	}
	return info
}

func (scb *sendChanBundle) BindingCount(id Id) int {
	scb.Lock()
	s, ok := scb.sendChans[id.Key()]
	scb.Unlock()
	if !ok {
		return -1
	}
	return s.endp.NumBindings()
}

func (scb *sendChanBundle) AddSender(id Id, chanType *reflect.ChanType) (err os.Error) {
	scb.Lock()
	_, ok := scb.sendChans[id.Key()]
	scb.Unlock()
	if ok {
		err = os.ErrorString("router sendChanBundle: AddSender duplicated id")
		return
	}
	s := new(senderInBundle)
	s.id, _ = id.Clone(scb.scope, scb.member)
	rt := scb.router
	buflen := rt.defChanBufSize
	if id.SysIdIndex() >= 0 {
		buflen = DefCmdChanBufSize
	}
	s.ch = reflect.MakeChan(chanType, buflen)
	s.endp, err = rt.AttachSendChan(s.id, s.ch.Interface())
	if err != nil {
		return
	}
	scb.Lock()
	scb.sendChans[s.id.Key()] = s
	scb.Unlock()
	scb.router.Log(LOG_INFO, fmt.Sprintf("add sender for %v", s.id))
	return
}

func (scb *sendChanBundle) DelSender(id Id) os.Error {
	scb.Lock()
	s, ok := scb.sendChans[id.Key()]
	if ok {
		scb.sendChans[id.Key()] = s, false
	}
	scb.Unlock()
	if !ok {
		return os.ErrorString("router sendChanBundle: DelSender id doesnt exist")
	}
	err := scb.router.DetachChan(s.id, s.ch.Interface())
	if err != nil {
		scb.router.LogError(err)
	}
	s.ch.Close()
	return nil
}

func (scb *sendChanBundle) Close() {
	scb.Lock()
	for k, s := range scb.sendChans {
		scb.sendChans[k] = s, false
		err := scb.router.DetachChan(s.id, s.ch.Interface())
		if err != nil {
			scb.router.LogError(err)
		}
		s.ch.Close()
	}
	scb.Unlock()
}
