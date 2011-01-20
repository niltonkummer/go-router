//
// Copyright (c) 2010 - 2011 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"reflect"
	"sync"
	"container/list"
	"os"
)

/* 
 Channel interface defines functional api of Go's chan:
 based on reflect.ChanValue's method set
 allow programming "generic" channels with reflect.Value as msgs
 add some utility Channel types
*/
type Channel interface {
	chanState
	Sender
	Recver
}

//common interface for msg senders
type Sender interface {
	Send(reflect.Value)
	TrySend(reflect.Value) bool
}

//common interface for msg recvers
type Recver interface {
	Recv() reflect.Value
	TryRecv() reflect.Value
}

//basic chan state
type chanState interface {
	Type() reflect.Type
	Interface() interface{}
	IsNil() bool
	Close()
	Closed() bool
	Cap() int
	Len() int
}

//SendChan: sending end of Channel (chan<-)
type SendChan interface {
	chanState
	Sender
}

//RecvChan: recving end of Channel (<-chan)
type RecvChan interface {
	chanState
	Recver
}

//GenericMsgChan: 
//mux msgs with diff ids into a common "chan *genericMsg"
//and expose Channel api
type genericMsgChan struct {
	id               Id
	Channel          //must have element type: *genericMsg
	sendChanCloseMsg bool
}

func newGenMsgChan(id Id, ch Channel, sendClose bool) *genericMsgChan {
	//add check to make sure Channel of type: chan *genericMsg
	return &genericMsgChan{id, ch, sendClose}
}

func newGenericMsgChan(id Id, ch chan *genericMsg, sendClose bool) *genericMsgChan {
	return &genericMsgChan{id, reflect.NewValue(ch).(*reflect.ChanValue), sendClose}
}

/* delegate following calls to embeded Channel's api
func (gch *genericMsgChan) Type() reflect.Type
func (gch *genericMsgChan) Closed() bool
func (gch *genericMsgChan) IsNil() bool
func (gch *genericMsgChan) Len() int
func (gch *genericMsgChan) Cap() int
func (gch *genericMsgChan) Recv() reflect.Value
func (gch *genericMsgChan) TryRecv() reflect.Value
*/

//overwrite the following calls for new behaviour
func (gch *genericMsgChan) Interface() interface{} { return gch }

func (gch *genericMsgChan) Close() {
	if gch.sendChanCloseMsg {
		id1, _ := gch.id.Clone(NumScope, NumMembership) //special id to mark chan close
		gch.Channel.Send(reflect.NewValue(&genericMsg{id1, nil}))
	}
}

func (gch *genericMsgChan) Send(v reflect.Value) {
	gch.Channel.Send(reflect.NewValue(&genericMsg{gch.id, v.Interface()}))
}

func (gch *genericMsgChan) TrySend(v reflect.Value) bool {
	return gch.Channel.TrySend(reflect.NewValue(&genericMsg{gch.id, v.Interface()}))
}

/*
 asyncChan:
 . unlimited internal buffering
 . senders never block
*/

//a trivial async chan
type asyncChan struct {
	Channel
	sync.Mutex
	buffer *list.List //when buffer!=nil, background forwarding active
	closed bool
}

func (ac *asyncChan) Close() {
	ac.Lock()
	defer ac.Unlock()
	ac.closed = true
	if ac.buffer == nil { //no background forwarder running
		ac.Channel.Close()
	}
}

func (ac *asyncChan) Interface() interface{} {
	return ac
}

func (ac *asyncChan) Cap() int {
	return UnlimitedBuffer //unlimited
}

/*how to count the items being forwarded at background?
func (ac *asyncChan) Len() int {
return UnlimitedBuffer
}
*/

//for async chan, Send() never block because of unlimited buffering
func (ac *asyncChan) Send(v reflect.Value) {
	ac.Lock()
	defer ac.Unlock()
	if ac.closed {
		return //sliently dropped
	}
	if ac.buffer == nil {
		if ac.Channel.TrySend(v) {
			return
		}
		ac.buffer = new(list.List)
		ac.buffer.PushBack(v)
		//spawn forwarder
		go func() {
			for {
				ac.Lock()
				l := ac.buffer
				if l.Len() == 0 {
					ac.buffer = nil
					if ac.closed {
						ac.Channel.Close()
					}
					ac.Unlock()
					return
				}
				ac.buffer = new(list.List)
				ac.Unlock()
				for e := l.Front(); e != nil; e = l.Front() {
					ac.Channel.Send(e.Value.(reflect.Value))
					l.Remove(e)
				}
			}
		}()
	} else {
		ac.buffer.PushBack(v)
	}
}

func (ac *asyncChan) TrySend(v reflect.Value) bool {
	ac.Send(v)
	return true
}

/*
//asyncChan2 - another async chan for pooling
type asyncChan2 struct {
	Channel
	sync.Mutex
	buffer     list.List
	forwarding bool //is background forwarding goroutine active
	closed     bool
}

func (ac *asyncChan2) Interface() interface{} {
	return ac
}

func (ac *asyncChan2) Close() {
	ac.Lock()
	defer ac.Unlock()
	ac.closed = true
	if !ac.forwarding {
		ac.Channel.Close()
	}
}

func (ac *asyncChan2) Cap() int {
	return UnlimitedBuffer //unlimited
}

func (ac *asyncChan2) Len() int {
	ac.Lock()
	defer ac.Unlock()
	return ac.buffer.Len() + ac.Channel.Len()
}

//for async chan, Send() never block assyming unlimited buffering
func (ac *asyncChan2) Send(v reflect.Value) {
	ac.Lock()
	defer ac.Unlock()
	if ac.closed {
		return //sliently dropped
	}
	if !ac.forwarding {
		if ac.Channel.TrySend(v) {
			return
		}
		ac.forwarding = true
		ac.buffer.PushBack(v)
		//spawn forwarder
		go func() {
			var e *list.Element
			for {
				ac.Lock()
				if e != nil {
					ac.buffer.Remove(e)
				}
				if ac.buffer.Len() == 0 {
					ac.forwarding = false
					if ac.closed {
						ac.Channel.Close()
					}
					ac.Unlock()
					return
				}
				e = ac.buffer.Front()
				ac.Unlock()
				ac.Channel.Send(e.Value.(reflect.Value))
			}
		}()
	} else {
		ac.buffer.PushBack(v)
	}
}

func (ac *asyncChan2) TrySend(v reflect.Value) bool {
	ac.Send(v)
	return true
}

 asyncChanPool:
 a pool of async chans share a few forwarder goroutines
*/
/*
 type asyncChanPool struct {
 }

 func (acp *asyncChanPool) newAsyncChan(ch Channel) Channel {
 }

*/

/*
 flowChan: flow controlled channel
 . a pair of <Sender, Recver>
 . flow control between <Sender, Recver>: simple window protocol for lossless transport
 . the transport Channel between Sender, Recver should have capacity >= expected credit
*/

type flowChanSender struct {
	Channel
	creditChan chan bool
}

func newFlowChanSender(ch Channel, credit int) (*flowChanSender, os.Error) {
	fc := new(flowChanSender)
	fc.Channel = ch
	//for unlimited buffer, ch.Cap() return UnlimitedBuffer(-1)
	if ch.Cap() != UnlimitedBuffer && ch.Cap() < credit {
		return nil, os.ErrorString("Flow Controlled Chan do not have enough buffering")
	}
	fc.creditChan = make(chan bool, credit)
	for i := 0; i < credit; i++ {
		fc.creditChan <- true
	}
	return fc, nil
}

func (fc *flowChanSender) Send(v reflect.Value) {
	<-fc.creditChan //use one credit 
	fc.Channel.Send(v)
}

func (fc *flowChanSender) TrySend(v reflect.Value) bool {
	_, ok := <-fc.creditChan
	if !ok {
		return false
	}
	fc.Channel.Send(v) //should not block here since credit is granted
	return true
}

func (fc *flowChanSender) Len() int {
	return cap(fc.creditChan) - len(fc.creditChan)
}

func (fc *flowChanSender) Cap() int {
	return cap(fc.creditChan)
}

func (fc *flowChanSender) ack(n int) {
	for i := 0; i < n; i++ {
		_ = fc.creditChan <- true
	}
}

func (fc *flowChanSender) Interface() interface{} {
	return fc
}

type flowChanRecver struct {
	Channel
	ack func(int)
}

func (fc *flowChanRecver) Recv() reflect.Value {
	v := fc.Channel.Recv()
	fc.ack(1)
	return v
}

func (fc *flowChanRecver) TryRecv() reflect.Value {
	v := fc.Channel.TryRecv()
	if v == nil {
		return nil
	}
	fc.ack(1)
	return v
}

func (fc *flowChanRecver) Interface() interface{} {
	return fc
}
