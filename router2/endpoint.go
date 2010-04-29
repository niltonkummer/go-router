//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"reflect"
	"sync"
	"container/vector"
)

/* 
* interface of reflect.ChanValue:
* used internally for all channels attached to router
* intended to integrate "chan primitives" and "chan *GenericMsg"
*/
type reflectChanValue interface {
	Type() reflect.Type
	Interface() interface{}
	Cap() int
	Close()
	Closed() bool
	IsNil() bool
	Len() int
	Recv() reflect.Value
	Send(reflect.Value)
	TryRecv() reflect.Value
	TrySend(reflect.Value) bool
}

type endpointType int

const (
	senderType endpointType = iota
	recverType
)

type operType int

const (
	attachOp operType = iota
	detachOp
)

type oper struct {
	kind operType
	peer *Endpoint
}

/*
 Endpoints are "attachment points" where channels are attached to router. They expose the following info:
    1. Id - the id which channel is attached to
    2. Chan - the attached channel
    3. NumBindings() - return the number of bound peers
*/
type Endpoint struct {
	kind       endpointType
	Id         Id
	Chan       reflectChanValue //external SendChan/RecvChan, attached by clients
	dispatcher Dispatcher         //current for push dispacher, only sender uses dispatcher
	bindChan   chan *BindEvent
	bindLock   sync.Mutex
	bindings   []*Endpoint //binding_set
	inDisp     bool        //in a dispatch loop
	opBuf      *vector.Vector
	genFlag    bool
}

func newEndpoint(id Id, t endpointType, ch reflectChanValue, bc chan *BindEvent) *Endpoint {
	endp := &Endpoint{}
	endp.kind = t
	endp.Id = id
	endp.Chan = ch
	endp.bindChan = bc
	return endp
}

func (e *Endpoint) NumBindings() int {
	e.bindLock.Lock()
	defer e.bindLock.Unlock()
	num := len(e.bindings)
	if e.opBuf != nil {
		for i := 0; i < e.opBuf.Len(); i++ {
			op := e.opBuf.At(i).(oper)
			switch op.kind {
			case attachOp:
				num++
			case detachOp:
				num--
			}
		}
	}
	return num
}

func (e *Endpoint) start(bufSize int, disp DispatchPolicy) {
	e.bindings = make([]*Endpoint, 0, DefBindingSetSize)
	if e.kind == senderType {
		e.dispatcher = disp.NewDispatcher()
		go e.senderLoop()
	}
}

func (e *Endpoint) close() {
	if e.bindChan != nil {
		close(e.bindChan)
	}
	e.Chan.Close()
}

func (e *Endpoint) senderLoop() {
	cont := true
	for cont {
		v := e.Chan.Recv()
		if !e.Chan.Closed() {
			e.bindLock.Lock()
			e.inDisp = true
			e.bindLock.Unlock()
			e.dispatcher.Dispatch(v, e.bindings)
			e.bindLock.Lock()
			e.inDisp = false
			if e.opBuf != nil {
				e.runPendingOps(e.opBuf)
				e.opBuf = nil
			}
			e.bindLock.Unlock()
		} else {
			e.bindLock.Lock()
			e.detachAllRecvChans()
			e.bindLock.Unlock()
			cont = false
		}
	}
}

func (e *Endpoint) runPendingOps(opBuf *vector.Vector) {
	for i := 0; i < opBuf.Len(); i++ {
		op := opBuf.At(i).(oper)
		switch op.kind {
		case attachOp:
			e.attachImpl(op.peer)
		case detachOp:
			e.detachImpl(op.peer)
		}
	}
}

func (e *Endpoint) detachAllRecvChans() {
	for i, r := range e.bindings {
		r.detach(e)
		e.bindings[i] = nil
	}
	e.bindings = e.bindings[0:0]
}

func (e *Endpoint) attachImpl(p *Endpoint) {
	len0 := len(e.bindings)
	if len0 == cap(e.bindings) {
		//expand
		newBindings := make([]*Endpoint, len0+DefBindingSetSize)
		copy(newBindings, e.bindings)
		e.bindings = newBindings
	}
	e.bindings = e.bindings[0 : len0+1]
	e.bindings[len0] = p
	if e.bindChan != nil {
		//KeepLatest non-blocking send
		for !(e.bindChan <- &BindEvent{PeerAttach, len0 + 1}) { //chan full
			<-e.bindChan //drop the oldest one
		}
	}
}

func (e *Endpoint) attach(p *Endpoint) {
	e.bindLock.Lock()
	defer e.bindLock.Unlock()
	if e.inDisp {
		if e.opBuf == nil {
			e.opBuf = new(vector.Vector)
		}
		e.opBuf.Push(oper{attachOp, p})
	} else {
		e.attachImpl(p)
	}
}

func (e *Endpoint) detachImpl(p *Endpoint) {
	n := len(e.bindings)
	for i, v := range e.bindings {
		if v == p {
			copy(e.bindings[i:n-1], e.bindings[i+1:n])
			e.bindings[n-1] = nil
			e.bindings = e.bindings[0 : n-1]
			if e.bindChan != nil {
				//KeepLatest non-blocking send
				for !(e.bindChan <- &BindEvent{PeerDetach, n - 1}) { //chan full
					<-e.bindChan //drop the oldest one
				}
			}
			if e.kind == recverType {
				//for recver, if all senders detached
				//send EndOfData to notify possible pending goroutine
				if len(e.bindings) == 0 {
					if e.bindChan != nil {
						//if bindChan exist, user is monitoring bind status
						//send EndOfData event and normally leave ext chan "ch" open
						for !(e.bindChan <- &BindEvent{EndOfData, 0}) {
							<-e.bindChan
						}
					} else {
						//since no bindChan, user code is not monitoring bind status
						//close ext chan to notify potential pending goroutine
						e.Chan.Close()
					}
				}
			}
			return
		}
	}
}

func (e *Endpoint) detach(p *Endpoint) {
	e.bindLock.Lock()
	defer e.bindLock.Unlock()
	if e.inDisp {
		if e.opBuf == nil {
			e.opBuf = new(vector.Vector)
		}
		e.opBuf.Push(oper{detachOp, p})
	} else {
		e.detachImpl(p)
	}
}