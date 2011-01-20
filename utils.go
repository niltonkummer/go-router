//
// Copyright (c) 2010 - 2011 Yigong Liu
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
//recvChanBundle has no lock protection, since it is only used from proxy mainLoop
type recverInBundle struct {
	id     Id
	ch     Channel
	routCh *RoutedChan
}

type recvChanBundle struct {
	router    *routerImpl
	proxy     *proxyImpl
	scope     int
	member    int
	recvChans map[interface{}]*recverInBundle
}

func newRecvChanBundle(p *proxyImpl, s int, m int) *recvChanBundle {
	rcb := new(recvChanBundle)
	rcb.router = p.router
	rcb.proxy = p
	rcb.scope = s
	rcb.member = m
	rcb.recvChans = make(map[interface{}]*recverInBundle)
	return rcb
}

//findRecver return ChanValue and its number of bindings
func (rcb *recvChanBundle) findRecver(id Id) (Channel, int) {
	r, ok := rcb.recvChans[id.Key()]
	if !ok {
		return nil, -1
	}
	return r.ch, r.routCh.NumPeers()
}

func (rcb *recvChanBundle) BindingCount(id Id) int {
	r, ok := rcb.recvChans[id.Key()]
	if !ok {
		return -1
	}
	return r.routCh.NumPeers()
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

func (rcb *recvChanBundle) AddRecver(id Id, ch Channel, credit int) (err os.Error) {
	_, ok := rcb.recvChans[id.Key()]
	if ok {
		err = os.ErrorString("router recvChanBundle: AddRecver duplicated id")
		return
	}
	rt := rcb.router
	r := new(recverInBundle)
	r.id, _ = id.Clone(rcb.scope, rcb.member)
	r.ch = ch
	if rcb.proxy.streamPeer {
		gch, _ := ch.(*genericMsgChan)
		gch.id = r.id
		if !rcb.router.async && rcb.proxy.flowControlled /* && id.SysIdIndex() < 0*/ {
			//attach flow control adapter to stream chan recver
			r.ch, _ = newFlowChanSender(ch, credit)
			rcb.router.Log(LOG_INFO, fmt.Sprintf("add flow sender: %v %v", r.id, credit))
		}
	}
	r.routCh, err = rt.AttachRecvChan(r.id, r.ch.Interface())
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
	id     Id
	ch     Channel
	routCh *RoutedChan
}

type sendChanBundle struct {
	router *routerImpl
	proxy  *proxyImpl
	scope  int
	member int
	//for syncing modifying sendChans map from mainLoop and client access
	sync.Mutex
	sendChans map[interface{}]*senderInBundle
}

func newSendChanBundle(p *proxyImpl, s int, m int) *sendChanBundle {
	scb := new(sendChanBundle)
	scb.proxy = p
	scb.router = p.router
	scb.scope = s
	scb.member = m
	scb.sendChans = make(map[interface{}]*senderInBundle)
	return scb
}

//findSender return ChanValue and its number of bindings
func (scb *sendChanBundle) findSender(id Id) (Channel, int) {
	scb.Lock() //!no need for lock, since add/delSender and SenderExist all from same proxy ctrlMainLoop
	s, ok := scb.sendChans[id.Key()]
	scb.Unlock()
	if !ok {
		return nil, 0
	}
	return s.ch, s.routCh.NumPeers()
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
	return s.routCh.NumPeers()
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
	buflen := rt.recvChanBufSize(id)
	if id.SysIdIndex() >= 0 {
		buflen = DefCmdChanBufSize
	}
	s.ch = reflect.MakeChan(chanType, buflen)
	//attach flow control adapters for stream send chans
	if !scb.router.async && scb.proxy.flowControlled && scb.proxy.streamPeer /* && id.SysIdIndex() < 0*/ {
		//flow controlled
		s.ch = &flowChanRecver{
			s.ch,
			func(n int) {
				scb.proxy.peer.sendCtrlMsg(&genericMsg{rt.SysID(ReadyId), &ConnReadyMsg{[]*ChanReadyInfo{&ChanReadyInfo{s.id, n}}}})
			},
		}
		scb.router.Log(LOG_INFO, fmt.Sprintf("add flow recver: %v", s.id))

	}
	s.routCh, err = rt.AttachSendChan(s.id, s.ch.Interface())
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
