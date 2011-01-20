//
// Copyright (c) 2010 - 2011 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"sync"
	"container/vector"
)

type routedChanType int

const (
	senderType routedChanType = iota
	recverType
)

type operType int

const (
	attachOp operType = iota
	detachOp
)

type oper struct {
	kind operType
	peer *RoutedChan
}

/*
 RoutedChan represents channels which are attached to router. 
 They expose Channel's interface: Send()/TrySend()/Recv()/TryRecv()/...
 and additional info:
 1. Id - the id which channel is attached to
 2. NumPeers() - return the number of bound peers
 3. Peers() - array of bound peers RoutedChan
 4. Detach() - detach the channel from router
*/
type RoutedChan struct {
	kind         routedChanType
	Id           Id
	Channel      //external SendChan/RecvChan, attached by clients
	router       *routerImpl
	dispatcher   Dispatcher //current for push dispacher, only sender uses dispatcher
	bindChan     chan *BindEvent
	bindCond     chan bool //simulate a cond var for waiting for recver binding
	bindLock     sync.Mutex
	bindings     []*RoutedChan //binding_set
	inDisp       bool          //in a dispatch loop
	opBuf        vector.Vector
	internalChan bool
}

func newRoutedChan(id Id, t routedChanType, ch Channel, r *routerImpl, bc chan *BindEvent) *RoutedChan {
	routCh := &RoutedChan{}
	routCh.kind = t
	routCh.Id = id
	routCh.Channel = ch
	routCh.router = r
	routCh.bindChan = bc
	if t == senderType {
		routCh.bindCond = make(chan bool, 1)
	}
	return routCh
}

func (e *RoutedChan) NumPeers() int {
	e.bindLock.Lock()
	defer e.bindLock.Unlock()
	num := len(e.bindings)
	for i := 0; i < len(e.opBuf); i++ {
		op := e.opBuf[i].(oper)
		switch op.kind {
		case attachOp:
			num++
		case detachOp:
			num--
		}
	}
	return num
}

func (e *RoutedChan) Peers() (copySet []*RoutedChan) {
	e.bindLock.Lock()
	defer e.bindLock.Unlock()
	l := len(e.bindings)
	if l == 0 {
		return
	}
	copySet = make([]*RoutedChan, l)
	copy(copySet, e.bindings)
	return
}


func (e *RoutedChan) start(disp DispatchPolicy) {
	e.bindings = make([]*RoutedChan, 0, DefBindingSetSize)
	if e.kind == senderType {
		e.dispatcher = disp.NewDispatcher()
		go e.senderLoop()
	}
}

func (e *RoutedChan) close() {
	if e.bindChan != nil {
		close(e.bindChan)
	}
	e.Channel.Close()
}

func (e *RoutedChan) senderLoop() {
	cont := true
	for cont {
		v := e.Channel.Recv()
		if !e.Channel.Closed() {
			e.bindLock.Lock()
			//block here till we have recvers so that message will not be lost
			for len(e.bindings) == 0 {
				e.bindLock.Unlock()
				//wait here until some recver attach
				<-e.bindCond
				e.bindLock.Lock()
			}
			e.inDisp = true
			e.bindLock.Unlock()
			e.dispatcher.Dispatch(v, e.bindings)
			e.bindLock.Lock()
			e.inDisp = false
			if e.opBuf.Len() > 0 {
				e.runPendingOps()
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

func (e *RoutedChan) runPendingOps() {
	for i := 0; i < e.opBuf.Len(); i++ {
		op := e.opBuf[i].(oper)
		switch op.kind {
		case attachOp:
			e.attachImpl(op.peer)
		case detachOp:
			e.detachImpl(op.peer)
		}
	}
	//clean up
	//e.opBuf.Cut(0, e.opBuf.Len())
	e.opBuf = e.opBuf[0:0]
}

func (e *RoutedChan) detachAllRecvChans() {
	for i, r := range e.bindings {
		r.detach(e)
		e.bindings[i] = nil
	}
	e.bindings = e.bindings[0:0]
}

func (e *RoutedChan) Detach() {
	e.router.detach(e)
}

func (e *RoutedChan) attachImpl(p *RoutedChan) {
	len0 := len(e.bindings)
	if len0 == cap(e.bindings) {
		//expand
		newBindings := make([]*RoutedChan, len0+DefBindingSetSize)
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
	if len0 == 0 && e.kind == senderType {
		//fill e.bindCond to notify we have bindings now
		_ = e.bindCond <- true
	}
}

func (e *RoutedChan) attach(p *RoutedChan) {
	e.bindLock.Lock()
	defer e.bindLock.Unlock()
	if e.inDisp {
		e.opBuf.Push(oper{attachOp, p})
	} else {
		e.attachImpl(p)
	}
}

func (e *RoutedChan) detachImpl(p *RoutedChan) {
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
			if len(e.bindings) == 0 {
				switch e.kind {
				case recverType:
					//for recver, if all senders detached
					//send EndOfData to notify possible pending goroutine
					if e.bindChan != nil {
						//if bindChan exist, user is monitoring bind status
						//send EndOfData event and normally leave ext chan "ch" open
						for !(e.bindChan <- &BindEvent{EndOfData, 0}) {
							<-e.bindChan
						}
					} else {
						//since no bindChan, user code is not monitoring bind status
						//close ext chan to notify potential pending goroutine
						e.Channel.Close()
					}
				case senderType:
					//for sender, if all recver detached
					//drain e.bindCond so that dispatcher goroutine can wait
					_ = <-e.bindCond
				}
			}
			return
		}
	}
}

func (e *RoutedChan) detach(p *RoutedChan) {
	e.bindLock.Lock()
	defer e.bindLock.Unlock()
	if e.inDisp {
		e.opBuf.Push(oper{detachOp, p})
	} else {
		e.detachImpl(p)
	}
}
