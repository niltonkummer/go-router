//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"reflect"
)

type endpointType int

const (
	senderType endpointType = iota
	recverType
)

type Endpoint struct {
	kind       endpointType
	Id         Id
	bindChan   chan *BindEvent
	extIntf    *reflect.ChanValue //ext SendChan/RecvChan, attached by clients
	Chan       chan interface{}   //sendChan for sender, recvChan for recver, internal to router
	bindings   []*Endpoint        //binding_set
	cmdChan    chan *command
	dispatcher Dispatcher //current for push dispacher, only sender uses dispatcher
}

func newEndpoint(id Id, t endpointType, ch *reflect.ChanValue) *Endpoint {
	endp := &Endpoint{}
	endp.kind = t
	endp.Id = id
	endp.extIntf = ch
	return endp
}

func (e *Endpoint) start(bufSize int, disp DispatchPolicy) {
	if e.Chan != nil {
		return //already started
	}
	e.Chan = make(chan interface{}, bufSize)
	e.bindings = make([]*Endpoint, 0, DefBindingSetSize)
	e.cmdChan = make(chan *command, DefCmdChanBufSize)
	if e.kind == senderType {
		e.dispatcher = disp.NewDispatcher()
		go e.senderLoop()
	} else {
		go e.recverLoop()
	}
}

func (e *Endpoint) Close() {
	if e.bindChan != nil {
		close(e.bindChan)
	}
	e.extIntf.Close()
	close(e.Chan)
	close(e.cmdChan)
	//e.cmdChan <- &command{kind:shutdown}
}

func (e *Endpoint) handleCmd(cmd *command) (cont bool) {
	cont = true
	switch cmd.kind {
	case attach:
		e.attachImpl(cmd.data.(*Endpoint), cmd.rspChan)
	case detach:
		e.detachImpl(cmd.data.(*Endpoint))
	case shutdown:
		e.cleanup()
		//drain all pending commands and exit
		for {
			_, ok := <-e.cmdChan
			if !ok {
				break
			}
		}
		cont = false
	}
	return
}

func (e *Endpoint) recverLoop() {
	for cmd := range e.cmdChan {
		e.handleCmd(cmd)
	}
}

func (e *Endpoint) senderLoop() {
	cont := true
	count := 0
	for cont {
		if len(e.bindings) == 0 {
			// no recver, only wait for commands, so we can throttle sender
			cmd := <-e.cmdChan
			if !closed(e.cmdChan) {
				cont = e.handleCmd(cmd)
			} else {
				cont = false
			}
		} else {
			// there are recvers, handle both cmd and data input,
			// and give cmdChan higher priority
			select {
			case cmd := <-e.cmdChan:
				if !closed(e.cmdChan) {
					cont = e.handleCmd(cmd)
				} else {
					cont = false
				}
			default:
				select {
				case cmd := <-e.cmdChan:
					if !closed(e.cmdChan) {
						cont = e.handleCmd(cmd)
					} else {
						cont = false
					}
				case v := <-e.Chan:
					if !closed(e.Chan) {
						e.dispatcher.Dispatch(v, e.bindings)
						//kludge for issue#536
						count++
						if count > DefCountBeforeGC {
							count = 0
							//make this nonblocking since it is fine as long as something inside cmdChan
							_ = e.cmdChan <- &command{kind:GC}
						}
					} else {
						e.detachAllRecvChans()
						e.cleanup()
						cont = false
					}
				}
			}
		}
	}
}

func (e *Endpoint) cleanup() {}

func (e *Endpoint) detachAllRecvChans() {
	for i, r := range e.bindings {
		r.detach(e)
		e.bindings[i] = nil
	}
	e.bindings = e.bindings[0:0]
}

func (e *Endpoint) attachImpl(p *Endpoint, done chan *command) {
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
	done <- nil
}

func (e *Endpoint) attach(p *Endpoint, done chan *command) {
	cmd := &command{}
	cmd.kind = attach
	cmd.data = p
	cmd.rspChan = done
	e.cmdChan <- cmd
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
				//send chanCloseMsg to notify possible pending goroutine
				if len(e.bindings) == 0 {
					e.Chan <- chanCloseMsg{}
				}
			}
			return
		}
	}
}

func (e *Endpoint) detach(p *Endpoint) {
	cmd := &command{}
	cmd.kind = detach
	cmd.data = p
	e.cmdChan <- cmd
}
