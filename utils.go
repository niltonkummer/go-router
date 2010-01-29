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

//recvChanBundle groups a set of recvChans together
type recverInBundle struct {
	bundle      *recvChanBundle
	id          Id
	ch          *reflect.ChanValue
	bindChan    chan *BindEvent
	numBindings int
}

func (r *recverInBundle) Close() { r.ch.Close() }

func (r *recverInBundle) mainLoop() {
	r.bundle.router.Log(LOG_INFO, fmt.Sprintf("proxy forward chan for %v start", r.id))
	for {
		v := r.ch.Recv()
		if r.ch.Closed() {
			r.bundle.router.Log(LOG_INFO, fmt.Sprintf("close proxy chan for %v", r.id))
			r.bundle.OutChan <- &genericMsg{Id: r.id, Data: chanCloseMsg{}}
			break
		}
		r.bundle.OutChan <- &genericMsg{Id: r.id, Data: v.Interface()}
		//r.bundle.router.Log(LOG_INFO, fmt.Sprintf("proxy forward one msg for id %v: %v", r.id, v.Interface()))
	}
	r.bundle.router.Log(LOG_INFO, fmt.Sprintf("proxy forward chan goroutine for %v exit", r.id))
}

type recvChanBundle struct {
	router     *routerImpl
	scope      int
	member     int
	recvChans  map[interface{}]*recverInBundle
	OutChan    chan *genericMsg
	ownOutChan bool
	started    bool
}

func newRecvChanBundle(r Router, s int, m int, mc chan *genericMsg) *recvChanBundle {
	rcb := new(recvChanBundle)
	rcb.router = r.(*routerImpl)
	rcb.scope = s
	rcb.member = m
	rcb.recvChans = make(map[interface{}]*recverInBundle)
	if mc != nil {
		rcb.OutChan = mc
		rcb.ownOutChan = false
	} else {
		rcb.OutChan = make(chan *genericMsg, rcb.router.defChanBufSize)
		rcb.ownOutChan = true
	}
	return rcb
}

func (rcb *recvChanBundle) RecverExist(id Id) bool {
	_, ok := rcb.recvChans[id.Key()]
	if ok {
		return true
	}
	return false
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

func (rcb *recvChanBundle) BindingCount(id Id) int {
	s, ok := rcb.recvChans[id.Key()]
	if !ok {
		return -1
	}
	for {
		bv, ok := <-s.bindChan
		if !ok {
			break
		}
		s.numBindings = bv.Count
	}
	return s.numBindings
}

func (rcb *recvChanBundle) AddRecver(id Id, chanType *reflect.ChanType) os.Error {
	_, ok := rcb.recvChans[id.Key()]
	if ok {
		return os.ErrorString("router recvChanBundle: AddRecver duplicated id")
	}
	r := new(recverInBundle)
	r.bundle = rcb
	r.id, _ = id.Clone(rcb.scope, rcb.member)
	rt := rcb.router
	r.ch = reflect.MakeChan(chanType, rt.defChanBufSize)
	r.bindChan = make(chan *BindEvent, 1)
	err := rt.AttachRecvChan(r.id, r.ch.Interface(), r.bindChan, true)
	if err != nil {
		return err
	}
	rcb.recvChans[r.id.Key()] = r
	rcb.router.Log(LOG_INFO, fmt.Sprintf("add recver for %v", r.id))
	if rcb.started {
		go r.mainLoop()
	}
	return nil
}

func (rcb *recvChanBundle) DelRecver(id Id) os.Error {
	r, ok := rcb.recvChans[id.Key()]
	if !ok {
		return os.ErrorString("router recvChanBundle: DelRecver id doesnt exist")
	}
	err := rcb.router.DetachChan(r.id, r.ch.Interface())
	if err != nil {
		rcb.router.LogError(err)
	}
	r.Close()
	rcb.recvChans[id.Key()] = r, false
	return nil
}

func (rcb *recvChanBundle) Start() {
	if !rcb.started {
		rcb.started = true
		for _, r := range rcb.recvChans {
			go r.mainLoop()
		}
	}
}

func (rcb *recvChanBundle) Close() {
	for k, r := range rcb.recvChans {
		rcb.recvChans[k] = r, false
		err := rcb.router.DetachChan(r.id, r.ch.Interface())
		if err != nil {
			rcb.router.LogError(err)
		}
		r.Close()
	}
	if rcb.ownOutChan {
		close(rcb.OutChan)
	}
}

//sendChanBundle groups a set of sendChans together
type senderInBundle struct {
	id          Id
	ch          *reflect.ChanValue
	bindChan    chan *BindEvent
	numBindings int
}

type sendChanBundle struct {
	router *routerImpl
	scope  int
	member int
	//solely for syncing modifying sendChans map from mainLoop and client access
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

//the following 2 methods are called from the same goroutine which call Add/DelSender()
func (scb *sendChanBundle) SenderExist(id Id) bool {
	//scb.Lock() !no need for lock, since add/delSender and SenderExist all from same proxy ctrlMainLoop
	_, ok := scb.sendChans[id.Key()]
	//scb.Unlock()
	if ok {
		return true
	}
	return false
}

func (scb *sendChanBundle) AllSenderInfo() []*IdChanInfo {
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
	s, ok := scb.sendChans[id.Key()]
	if !ok {
		return -1
	}
	for {
		bv, ok := <-s.bindChan
		if !ok {
			break
		}
		s.numBindings = bv.Count
	}
	return s.numBindings
}

func (scb *sendChanBundle) AddSender(id Id, chanType *reflect.ChanType) os.Error {
	scb.router.Log(LOG_INFO, fmt.Sprintf("start 2..add sender for %v", id))
	_, ok := scb.sendChans[id.Key()]
	if ok {
		return os.ErrorString("router sendChanBundle: AddSender duplicated id")
	}
	s := new(senderInBundle)
	s.id, _ = id.Clone(scb.scope, scb.member)
	rt := scb.router
	s.ch = reflect.MakeChan(chanType, rt.defChanBufSize)
	s.bindChan = make(chan *BindEvent, 1)
	err := rt.AttachSendChan(s.id, s.ch.Interface(), s.bindChan)
	if err != nil {
		return err
	}
	scb.Lock()
	scb.sendChans[s.id.Key()] = s
	scb.Unlock()
	scb.router.Log(LOG_INFO, fmt.Sprintf("add sender for %v", s.id))
	return nil
}

func (scb *sendChanBundle) DelSender(id Id) os.Error {
	s, ok := scb.sendChans[id.Key()]
	if !ok {
		return os.ErrorString("router sendChanBundle: DelSender id doesnt exist")
	}
	scb.Lock()
	scb.sendChans[id.Key()] = s, false
	scb.Unlock()
	s.ch.Close()
	err := scb.router.DetachChan(s.id, s.ch.Interface())
	if err != nil {
		scb.router.LogError(err)
	}
	return nil
}

func (scb *sendChanBundle) Close() {
	for k, s := range scb.sendChans {
		s.ch.Close()
		err := scb.router.DetachChan(s.id, s.ch.Interface())
		if err != nil {
			scb.router.LogError(err)
		}
		scb.Lock()
		scb.sendChans[k] = s, false
		scb.Unlock()
	}
}

func (scb *sendChanBundle) Send(id Id, data interface{}) os.Error {
	//need lock here since Send() is called from proxy.dataMainLoop while sendChans map is modified from ctrlMainLoop
	scb.Lock()
	s, ok := scb.sendChans[id.Key()]
	scb.Unlock()
	if !ok {
		return os.ErrorString(fmt.Sprintf("router sendChanBundle: cannot find Send id [%v]", id))
	}
	if _, ok1 := data.(chanCloseMsg); ok1 {
		s.ch.Close()
		scb.router.Log(LOG_INFO, fmt.Sprintf("close proxy forwarding chan for %v", id))
	} else {
		s.ch.Send(reflect.NewValue(data))
		scb.router.Log(LOG_INFO, fmt.Sprintf("send appMsg for %v", id))
	}
	return nil
}
