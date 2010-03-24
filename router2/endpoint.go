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

type Endpoint struct {
	kind       endpointType
	Id         Id
	Chan       *reflect.ChanValue //ext SendChan/RecvChan, attached by clients
	dispatcher Dispatcher         //current for push dispacher, only sender uses dispatcher
	bindChan   chan *BindEvent
	bindLock   sync.Mutex
	bindings   []*Endpoint //binding_set
	inDisp     bool        //in a dispatch loop
	opBuf      *vector.Vector
	flag       bool //a flag to mark if we close ext chan when EndOfData even if bindChan exist
}

func newEndpoint(id Id, t endpointType, ch *reflect.ChanValue, bc chan *BindEvent) *Endpoint {
	endp := &Endpoint{}
	endp.kind = t
	endp.Id = id
	endp.Chan = ch
	endp.bindChan = bc
	return endp
}

func (e *Endpoint) start(bufSize int, disp DispatchPolicy) {
	e.bindings = make([]*Endpoint, 0, DefBindingSetSize)
	if e.kind == senderType {
		e.dispatcher = disp.NewDispatcher()
		go e.senderLoop()
	}
}

func (e *Endpoint) Close() {
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
			opBuf := e.opBuf
			e.opBuf = nil
			e.bindLock.Unlock()
			if opBuf != nil {
				e.runPendingOps(opBuf)
			}
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
			e.attach(op.peer)
		case detachOp:
			e.detach(op.peer)
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

func (e *Endpoint) attach(p *Endpoint) {
	e.bindLock.Lock()
	if e.inDisp {
		if e.opBuf == nil {
			e.opBuf = new(vector.Vector)
		}
		e.opBuf.Push(oper{attachOp, p})
		e.bindLock.Unlock()
		return
	}
	len0 := len(e.bindings)
	if len0 == cap(e.bindings) {
		//expand
		newBindings := make([]*Endpoint, len0+DefBindingSetSize)
		copy(newBindings, e.bindings)
		e.bindings = newBindings
	}
	e.bindings = e.bindings[0 : len0+1]
	e.bindings[len0] = p
	e.bindLock.Unlock()
	if e.bindChan != nil {
		//KeepLatest non-blocking send
		for !(e.bindChan <- &BindEvent{PeerAttach, len0 + 1}) { //chan full
			<-e.bindChan //drop the oldest one
		}
	}
}

func (e *Endpoint) detach(p *Endpoint) {
	e.bindLock.Lock()
	if e.inDisp {
		if e.opBuf == nil {
			e.opBuf = new(vector.Vector)
		}
		e.opBuf.Push(oper{detachOp, p})
		e.bindLock.Unlock()
		return
	}
	defer e.bindLock.Unlock()
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
						//only close it when flag is set
						for !(e.bindChan <- &BindEvent{EndOfData, 0}) {
							<-e.bindChan
						}
						if e.flag {
							e.Chan.Close()
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
