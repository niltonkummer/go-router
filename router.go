//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//

/*
"router" is a Go package for remote channel communication, based on peer-peer pub/sub model.
Basically we attach a send channel to an id in router to send messages, and attach a recv channel to
an id to receive messages. If these 2 ids match, the messages from send channel will be "routed" to recv channel, e.g.

   rot := router.New(...)
   chan1 := make(chan string)
   chan2 := make(chan string)
   chan3 := make(chan string)
   rot.AttachSendChan(PathID("/sports/basketball"), chan1)
   rot.AttachRecvChan(PathID("/sports/basketball"), chan2)
   rot.AttachRecvChan(PathID("/sports/*"), chan3)

We can use integers, strings, pathnames, or structs as Ids in router (maybe regex ids
and tuple id in future).

we can connect two routers so that channels attached to router1 can communicate with
channels attached to router2 transparently.
*/
package router

import (
	"reflect"
	"fmt"
	"os"
	"container/vector"
	"io"
)

//Default size settings in router
const (
	DefLogBufSize      = 256
	DefDataChanBufSize = 32
	DefCmdChanBufSize  = 64
	DefBindingSetSize  = 8
)

//Router is the main access point to functionality. Applications will create an instance
//of it thru router.New(...) and attach channels to it
type Router interface {
	//---- core api ----
	//Attach chans to id in router, with an optional argument (chan *BindEvent)
	//currently only accept the following chan types: chan bool/int/float/string/*struct
	//When specified, the optional argument will serve two purposes:
	//1. used to tell when other ends connecting/disconn
	//2. in AttachRecvChan, used as a flag to ask router to keep recv chan open when all senders close
	AttachSendChan(Id, interface{}, ...) os.Error
	AttachRecvChan(Id, interface{}, ...) os.Error

	//Detach sendChan/recvChan from router
	DetachChan(Id, interface{}) os.Error

	//Shutdown router
	Close()

	//Connect this router to another router.
	//1. internally it calls Proxy.Connect(...) to do the real job
	//2. The connection can be disconnected by calling Proxy.Close() on returned proxy object
	//3. for more compilcated connection setup (such as setting IdFilter and IdTranslator), use Proxy.Connect() instead
	//Connect to a local router
	Connect(Router) (Proxy, Proxy, os.Error)

	//Connect to a remote router thru io conn
	ConnectRemote(io.ReadWriteCloser, MarshallingPolicy) (Proxy, os.Error)

	//--- other utils ---
	//return pre-created SysIds according to the router's id-type, with ScopeGlobal / MemberLocal
	SysID(idx int) Id

	//create a new SysId with "args..." specifying scope/membership
	NewSysID(idx int, args ...) Id

	//return all ids and their ChanTypes from router's namespace which satisfy predicate
	IdsForSend(predicate func(id Id) bool) map[interface{}]*IdChanInfo
	IdsForRecv(predicate func(id Id) bool) map[interface{}]*IdChanInfo
}

//The internal commands handled by router's main goroutine loop
type commandType int

const (
	attach commandType = iota
	detach
	queryIdsForSend
	queryIdsForRecv
	addProxy
	delProxy
	shutdown
	GC         //kludge for issue #536
)

type command struct {
	kind    commandType
	data    interface{}
	error   os.Error
	rspChan chan *command //response channel
}

//Major data structures for router:
//1. tblEntry: an entry for each id in router
//2. routerImpl: main data struct of router
type tblEntry struct {
	chanType *reflect.ChanType
	id       Id
	senders  map[interface{}]*endpoint
	recvers  map[interface{}]*endpoint
}

type routerImpl struct {
	defChanBufSize int
	dispPolicy     DispatchPolicy
	seedId         Id
	idType         reflect.Type
	matchType      MatchType
	routingTable   map[interface{}](*tblEntry)
	cmdChan        chan *command
	sysIds         [NumSysInternalIds]Id
	notifier       *notifier
	proxies        *vector.Vector
	//for log/debug, if name != nil, debug is enabled
	Logger
	LogSink
	FaultRaiser
	name string
}

func (s *routerImpl) NewSysID(idx int, args ...) Id {
	sid, err := s.seedId.SysID(idx, args)
	if err != nil {
		s.LogError(err)
		return nil
	}
	return sid
}

func (s *routerImpl) SysID(indx int) Id {
	if indx < 0 || indx >= NumSysInternalIds {
		return nil
	}
	return s.sysIds[indx]
}

func (s *routerImpl) idsForSendImpl(cmd *command) {
	predicate := cmd.data.(func(id Id) bool)
	ids := make(map[interface{}]*IdChanInfo)
	for _, v := range s.routingTable {
		for _, e := range v.senders {
			idx := s.getSysIdIdx(e.Id)
			if idx < 0 && predicate(e.Id) {
				ids[e.Id.Key()] = &IdChanInfo{Id: e.Id, ChanType: v.chanType}
			}
		}
	}
	cmd.data = ids
	cmd.rspChan <- cmd
	return
}

func (s *routerImpl) idsForRecvImpl(cmd *command) {
	predicate := cmd.data.(func(id Id) bool)
	ids := make(map[interface{}]*IdChanInfo)
	for _, v := range s.routingTable {
		for _, e := range v.recvers {
			idx := s.getSysIdIdx(e.Id)
			if idx < 0 && predicate(e.Id) {
				ids[e.Id.Key()] = &IdChanInfo{Id: e.Id, ChanType: v.chanType}
			}
		}
	}
	cmd.data = ids
	cmd.rspChan <- cmd
	return
}

func (s *routerImpl) IdsForSend(predicate func(id Id) bool) map[interface{}]*IdChanInfo {
	cmd := &command{}
	cmd.kind = queryIdsForSend
	cmd.data = predicate
	cmd.rspChan = make(chan *command)
	s.cmdChan <- cmd
	return (<-cmd.rspChan).data.(map[interface{}]*IdChanInfo)
}

func (s *routerImpl) IdsForRecv(predicate func(id Id) bool) map[interface{}]*IdChanInfo {
	cmd := &command{}
	cmd.kind = queryIdsForRecv
	cmd.data = predicate
	cmd.rspChan = make(chan *command)
	s.cmdChan <- cmd
	return (<-cmd.rspChan).data.(map[interface{}]*IdChanInfo)
}

func (s *routerImpl) validateId(id Id) (err os.Error) {
	if id == nil || (id.Scope() < ScopeGlobal || id.Scope() > ScopeLocal) ||
		(id.Member() < MemberLocal || id.Member() > MemberRemote) {
		err = os.ErrorString(fmt.Sprintf("%s: %v", errInvalidId, id))
	}
	return
}

func (s *routerImpl) validateChan(v interface{}) (ch *reflect.ChanValue, err os.Error) {
	ok := false
	ch, ok = reflect.NewValue(v).(*reflect.ChanValue)
	if !ok {
		err = os.ErrorString(errInvalidChan)
		return
	}
	et := ch.Type().(*reflect.ChanType).Elem()
	switch et := et.(type) {
	case *reflect.BoolType:
	case *reflect.IntType:
	case *reflect.FloatType:
	case *reflect.StringType:
	case *reflect.PtrType:
		if _, ok1 := et.Elem().(*reflect.StructType); !ok1 {
			err = os.ErrorString(errInvalidChan)
			return
		}
	default:
		err = os.ErrorString(errInvalidChan)
		return
	}
	return
}

func (s *routerImpl) AttachSendChan(id Id, v interface{}, args ...) (err os.Error) {
	if err = s.validateId(id); err != nil {
		s.LogError(err)
		s.Raise(err)
		return
	}
	ch, err1 := s.validateChan(v)
	if err1 != nil {
		s.LogError(err1)
		s.Raise(err1)
		return
	}
	av := reflect.NewValue(args).(*reflect.StructValue)
	var bindChan chan *BindEvent
	var ok bool
	if av.NumField() > 0 {
		switch cv := av.Field(0).(type) {
		case *reflect.ChanValue:
			icv := cv.Interface()
			bindChan, ok = icv.(chan *BindEvent)
			if !ok {
				err = os.ErrorString(errInvalidBindChan + ": binding bindChan is not chan *BindEvent")
				s.LogError(err)
				s.Raise(err)
				return
			}
			if cap(bindChan) == 0 {
				err = os.ErrorString(errInvalidBindChan + ": binding bindChan is not buffered")
				s.LogError(err)
				s.Raise(err)
				return
			}
		default:
			err = os.ErrorString("invalid arguments to attach chan")
			s.LogError(err)
			s.Raise(err)
			return
		}
	}
	endp := newEndpoint(id, senderType, ch)
	endp.bindChan = bindChan
	cmd := &command{}
	cmd.kind = attach
	cmd.data = endp
	cmd.rspChan = make(chan *command)
	s.cmdChan <- cmd    //send attach cmd to router
	cmd = <-cmd.rspChan //wait for response from router
	if cmd.error != nil {
		err = cmd.error
		s.Raise(err)
		s.LogError(err)
		return
	}
	//now we are attached successfully, start forwarding
	go func() {
		cont := true
		for cont {
			v := ch.Recv()
			if !ch.Closed() {
				endp.Chan <- v.Interface()
			} else {
				cont = false
			}
		}
		close(endp.Chan)
	}()
	return nil
}

func (s *routerImpl) AttachRecvChan(id Id, v interface{}, args ...) (err os.Error) {
	if err = s.validateId(id); err != nil {
		s.LogError(err)
		s.Raise(err)
		return
	}
	ch, err1 := s.validateChan(v)
	if err1 != nil {
		s.LogError(err1)
		s.Raise(err1)
		return
	}
	av := reflect.NewValue(args).(*reflect.StructValue)
	var bindChan chan *BindEvent
	var ok, flag bool //a flag to mark if we close ext chan when EndOfData even if bindChan exist
	if av.NumField() > 0 {
		for i := 0; i < av.NumField(); i++ {
			switch cv := av.Field(i).(type) {
			case *reflect.ChanValue:
				icv := cv.Interface()
				bindChan, ok = icv.(chan *BindEvent)
				if !ok {
					err = os.ErrorString(errInvalidBindChan + ": binding bindChan is not chan *BindEvent")
					s.LogError(err)
					s.Raise(err)
					return
				}
				if cap(bindChan) == 0 {
					err = os.ErrorString(errInvalidBindChan + ": binding bindChan is not buffered")
					s.LogError(err)
					s.Raise(err)
					return
				}
			case *reflect.BoolValue:
				flag = cv.Get()
			default:
				err = os.ErrorString("invalid arguments to attach recv chan")
				s.LogError(err)
				s.Raise(err)
				return
			}
		}
	}
	endp := newEndpoint(id, recverType, ch)
	endp.bindChan = bindChan
	cmd := &command{}
	cmd.kind = attach
	cmd.data = endp
	cmd.rspChan = make(chan *command)
	s.cmdChan <- cmd    //send attach cmd to router
	cmd = <-cmd.rspChan //wait for response from router
	if cmd.error != nil {
		err = cmd.error
		s.LogError(err)
		s.Raise(err)
		return
	}
	//now we are attached successfully, start forwarding
	go func() {
		cont := true
		for cont {
			v := <-endp.Chan
			if !closed(endp.Chan) {
				if _, ok1 := v.(chanCloseMsg); ok1 {
					if endp.bindChan != nil {
						//if bindChan exist, user is monitoring bind status
						//send EndOfData event and normally leave ext chan "ch" open
						//only close it when flag is set
						for !(endp.bindChan <- &BindEvent{EndOfData, 0}) {
							<-endp.bindChan
						}
						if flag {
							ch.Close()
						}
					} else {
						//since no bindChan, user code is not monitoring bind status
						//close ext chan to notify potential pending goroutine
						ch.Close()
					}
				} else {
					ch.Send(reflect.NewValue(v))
				}
			} else {
				cont = false
			}
		}
		ch.Close()
	}()
	return nil
}

func (s *routerImpl) DetachChan(id Id, v interface{}) (err os.Error) {
	s.Log(LOG_INFO, "DetachChan called...")
	if err = s.validateId(id); err != nil {
		s.LogError(err)
		s.Raise(err)
		return
	}
	cv, err1 := s.validateChan(v)
	if err1 != nil {
		s.LogError(err1)
		s.Raise(err1)
		return
	}
	endp := &endpoint{}
	endp.Id = id
	endp.extIntf = cv
	cmd := &command{}
	cmd.kind = detach
	cmd.data = endp
	cmd.rspChan = make(chan *command)
	s.cmdChan <- cmd             //send close cmd to router
	return (<-cmd.rspChan).error //wait for response from router
}

func (s *routerImpl) Close() {
	s.Log(LOG_INFO, "Close()/shutdown called")
	cmd := &command{}
	cmd.kind = shutdown
	cmd.rspChan = make(chan *command)
	s.cmdChan <- cmd
	<-cmd.rspChan //do we need to block wait?
}

//the main loop of router
func (s *routerImpl) mainLoop() {
	cont := true
	for cont {
		cmd := <-s.cmdChan
		switch cmd.kind {
		case attach:
			s.attach(cmd)
		case detach:
			s.detach(cmd)
		case queryIdsForSend:
			s.idsForSendImpl(cmd)
		case queryIdsForRecv:
			s.idsForRecvImpl(cmd)
		case addProxy:
			s.addProxyImpl(cmd.data.(Proxy))
			cmd.rspChan <- nil
		case delProxy:
			s.delProxyImpl(cmd.data.(Proxy))
			cmd.rspChan <- nil
		case shutdown:
			s.shutdown()
			cmd.rspChan <- nil //inform requester that we are done
			cont = false
			//drain cmdChan to unlock remaining commands
			//close(s.cmdChan)
			for {
				cmd1, ok := <-s.cmdChan
				if !ok {
					break
				}
				cmd1.error = os.ErrorString("router closed")
				cmd1.rspChan <- cmd1
			}
		}
	}
}

func (s *routerImpl) attach(cmd *command) {
	endp := cmd.data.(*endpoint)

	//handle id
	if reflect.Typeof(endp.Id) != s.idType {
		cmd.error = os.ErrorString(errIdTypeMismatch + ": " + endp.Id.String())
		s.LogError(cmd.error)
		cmd.rspChan <- cmd
		return
	}

	//router entry
	ent, ok := s.routingTable[endp.Id.Key()]
	if !ok {
		//first endpoint attached to this id, add a router-entry for this id
		ent = &tblEntry{}
		s.routingTable[endp.Id.Key()] = ent
		ent.id = endp.Id // will only use the Val/Match() part of id
		ent.chanType = endp.extIntf.Type().(*reflect.ChanType)
		ent.senders = make(map[interface{}]*endpoint)
		ent.recvers = make(map[interface{}]*endpoint)
	} else {
		if endp.extIntf.Type().(*reflect.ChanType) != ent.chanType {
			cmd.error = os.ErrorString(fmt.Sprintf("%s %v", errChanTypeMismatch, endp.Id))
			s.LogError(cmd.error)
			cmd.rspChan <- cmd
			return
		}
	}

	//check for duplicate
	switch endp.kind {
	case senderType:
		if _, ok := ent.senders[endp.extIntf.Interface()]; ok {
			cmd.error = os.ErrorString(errDupAttachment)
			s.LogError(cmd.error)
			cmd.rspChan <- cmd
			return
		} else {
			ent.senders[endp.extIntf.Interface()] = endp
		}
	case recverType:
		if _, ok := ent.recvers[endp.extIntf.Interface()]; ok {
			cmd.error = os.ErrorString(errDupAttachment)
			s.LogError(cmd.error)
			cmd.rspChan <- cmd
			return
		} else {
			ent.recvers[endp.extIntf.Interface()] = endp
		}
	}

	idx := s.getSysIdIdx(endp.Id)
	matches := new(vector.Vector)

	//find bindings for endpoint
	if s.matchType == ExactMatch {
		switch endp.kind {
		case senderType:
			for _, recver := range ent.recvers {
				if scope_match(endp.Id, recver.Id) {
					s.Log(LOG_INFO, fmt.Sprintf("add bindings: %v -> %v", endp.Id, recver.Id))
					matches.Push(recver)
				}
			}
		case recverType:
			for _, sender := range ent.senders {
				if scope_match(sender.Id, endp.Id) {
					s.Log(LOG_INFO, fmt.Sprintf("add bindings: %v -> %v", sender.Id, endp.Id))
					if idx >= PubId && idx < NumSysIds && len(sender.bindings) == 0 { //sys Pub/Sub ids
						//s.LogError("enable for ", sender.Id);
						s.notifier.setFlag(sender.Id, idx, true)
					}
					matches.Push(sender)
				}
			}
		}
	} else { //for PrefixMatch & AssocMatch, need to iterate thru all entries in map routingTable
		for _, ent2 := range s.routingTable {
			if endp.Id.Match(ent2.id) {
				if endp.extIntf.Type().(*reflect.ChanType) == ent2.chanType {
					switch endp.kind {
					case senderType:
						for _, recver := range ent2.recvers {
							if scope_match(endp.Id, recver.Id) {
								s.Log(LOG_INFO, fmt.Sprintf("add bindings: %v -> %v", endp.Id, recver.Id))
								matches.Push(recver)
							}
						}
					case recverType:
						for _, sender := range ent2.senders {
							if scope_match(sender.Id, endp.Id) {
								if idx >= PubId && idx < NumSysIds && len(sender.bindings) == 0 { //sys Pub/Sub ids
									//s.LogError("enable for ", sender.Id);
									s.notifier.setFlag(sender.Id, idx, true)
								}
								s.Log(LOG_INFO, fmt.Sprintf("add bindings: %v -> %v", sender.Id, endp.Id))
								matches.Push(sender)
							}
						}
					}
				} else {
					em := os.ErrorString(fmt.Sprintf("%s : [%v, %v]", errChanTypeMismatch, endp.Id, ent2.id))
					s.Log(LOG_ERROR, em)
					//should crash here?
					s.Raise(em)
				}
			}
		}
	}

	//activate
	//force broadcaster for system ids
	if idx >= 0 { //sys ids
		endp.start(s.defChanBufSize, BroadcastPolicy)
	} else {
		endp.start(s.defChanBufSize, s.dispPolicy)
	}

	//finished updating routing table, spawn remaining work
	//in another goroutine to avoid blocking router main goroutine
	go func() {
		//create a chan *command to allow router mainLoop to wait for all bindings of the new endpoint to set up
		done := make(chan *command, DefCmdChanBufSize)
		count := 0 //count how many outstanding

		for i := 0; i < matches.Len(); i++ {
			peer := matches.At(i).(*endpoint)
			endp.attach(peer, done)
			peer.attach(endp, done)
			count += 2
		}

		//wait for all bindings to set up
		count1 := count
		for count > 0 {
			<-done
			count--
		}
		s.Log(LOG_INFO, fmt.Sprintf("router.attach all %v bindings for %v are done", count1, endp.Id))

		//notifier will send in a separate goroutine, so non-blocking here
		if idx < 0 && endp.Id.Member() == MemberLocal { //not sys ids
			switch endp.kind {
			case senderType:
				s.notifier.notifyPub(&IdChanInfo{Id: endp.Id, ChanType: endp.extIntf.Type().(*reflect.ChanType)})
			case recverType:
				s.notifier.notifySub(&IdChanInfo{Id: endp.Id, ChanType: endp.extIntf.Type().(*reflect.ChanType)})
			}
		}

		//release client
		cmd.rspChan <- cmd
	}()
}

func (s *routerImpl) detach(cmd *command) {
	endp := cmd.data.(*endpoint)
	s.Log(LOG_INFO, fmt.Sprintf("detach chan from id %v\n", endp.Id))

	//check id
	if reflect.Typeof(endp.Id) != s.idType {
		cmd.error = os.ErrorString(errIdTypeMismatch + ": " + endp.Id.String())
		s.LogError(cmd.error)
		cmd.rspChan <- cmd
		return
	}

	//find router entry
	ent, ok := s.routingTable[endp.Id.Key()]
	if !ok {
		cmd.error = os.ErrorString(errDetachChanNotInRouter + ": " + endp.Id.String())
		s.LogError(cmd.error)
		cmd.rspChan <- cmd
		return
	}

	//find the endpoint & remove it from tblEntry
	endp1, ok := ent.senders[endp.extIntf.Interface()]
	if ok {
		ent.senders[endp.extIntf.Interface()] = endp1, false
	} else if endp1, ok = ent.recvers[endp.extIntf.Interface()]; ok {
		ent.recvers[endp.extIntf.Interface()] = endp1, false
	} else {
		cmd.error = os.ErrorString(errDetachChanNotInRouter + ": " + endp.Id.String())
		s.LogError(cmd.error)
		cmd.rspChan <- cmd
		return
	}

	//remove bindings from peers
	for _, v := range endp1.bindings {
		if endp1.kind == senderType {
			s.Log(LOG_INFO, fmt.Sprintf("del bindings: %v -> %v", endp1.Id, v.Id))
		} else {
			s.Log(LOG_INFO, fmt.Sprintf("del bindings: %v -> %v", v.Id, endp1.Id))
		}
		v.detach(endp1)
	}

	//close endpoint's chans, so any goroutines waiting on them will exit
	endp1.Close()

	//notifier will send in a separate goroutine, so non-blocking here
	idx := s.getSysIdIdx(endp1.Id)
	if idx < 0 && endp.Id.Member() == MemberLocal { //not sys ids
		switch endp.kind {
		case senderType:
			s.notifier.notifyUnPub(&IdChanInfo{Id: endp1.Id, ChanType: endp1.extIntf.Type().(*reflect.ChanType)})
		case recverType:
			s.notifier.notifyUnSub(&IdChanInfo{Id: endp1.Id, ChanType: endp1.extIntf.Type().(*reflect.ChanType)})
		}
	}

	cmd.rspChan <- cmd
}

func (s *routerImpl) shutdown() {
	s.Log(LOG_INFO, "shutdown start...")

	// close all peers
	for i := 0; i < s.proxies.Len(); i++ {
		s.proxies.At(i).(Proxy).Close()
	}
	s.Log(LOG_INFO, "all proxy closed")

	//close all enndpoint send chans
	for _, ent2 := range s.routingTable {
		for _, sender := range ent2.senders {
			sender.Close()
		}
	}

	//wait for console log goroutine to exit
	s.FaultRaiser.Close()
	s.Logger.Close()
	s.LogSink.Close()

	for _, ent2 := range s.routingTable {
		for _, recver := range ent2.recvers {
			recver.Close()
		}
	}
}

func (s *routerImpl) initSysIds() {
	for i := 0; i < NumSysInternalIds; i++ {
		s.sysIds[i], _ = s.seedId.SysID(i)
	}
}

func (s *routerImpl) getSysIdIdx(id Id) int {
	for i := 0; i < NumSysIds; i++ {
		if id.Match(s.sysIds[i]) {
			return i
		}
	}
	return -1
}

func (s *routerImpl) getSysInternalIdIdx(id Id) int {
	for i := 0; i < NumSysInternalIds; i++ {
		if id.Match(s.sysIds[i]) {
			return i
		}
	}
	return -1
}

func (s *routerImpl) addProxy(p Proxy) {
	cmd := &command{}
	cmd.kind = addProxy
	cmd.data = p
	cmd.rspChan = make(chan *command)
	s.cmdChan <- cmd
	<-cmd.rspChan
}

func (s *routerImpl) addProxyImpl(p Proxy) {
	s.Log(LOG_INFO, "add proxy")
	s.proxies.Push(p)
}

func (s *routerImpl) delProxy(p Proxy) {
	s.Log(LOG_INFO, "del proxy called")
	cmd := &command{}
	cmd.kind = delProxy
	cmd.data = p
	cmd.rspChan = make(chan *command)
	s.cmdChan <- cmd
	<-cmd.rspChan
}

func (s *routerImpl) delProxyImpl(p Proxy) {
	s.Log(LOG_INFO, "del proxy impl")
	num := -1
	for i := 0; i < s.proxies.Len(); i++ {
		if s.proxies.At(i).(Proxy) == p {
			num = i
			break
		}
	}
	if num >= 0 {
		s.proxies.Delete(num)
	}
}

//Connect() connects this router to peer router, the real job is done inside Proxy
func (r1 *routerImpl) Connect(r2 Router) (p1, p2 Proxy, err os.Error) {
	p1 = NewProxy(r1, "", nil, nil)
	p2 = NewProxy(r2, "", nil, nil)
	err = p1.Connect(p2)
	return
}

func (r *routerImpl) ConnectRemote(rwc io.ReadWriteCloser, mar MarshallingPolicy) (p Proxy, err os.Error) {
	p = NewProxy(r, "", nil, nil)
	err = p.ConnectRemote(rwc, mar)
	return
}

/*
New is router constructor. It accepts the following arguments:
    1. seedId: a dummy id to show what type of ids will be used. New ids will be type-checked against this.
    2. bufSize: by default, 32 is the default size for router's internal channels.
                if bufSize > 0, its value will be used.
    3. disp: dispatch policy for router. by default, it is BroadcastPolicy
    4. optional arguments ...:
            name:     router's name, if name is defined, router internal logging will be turned on,
                      ie LogRecord generated
            LogScope: if this is set, a console log sink is installed to show router internal log
                      if logScope == ScopeLocal, only log msgs from local router will show up
                      if logScope == ScopeGlobal, all log msgs from connected routers will show up
*/
func New(seedId Id, bufSize int, disp DispatchPolicy, args ...) Router {
	//parse optional router name and flag for enable console logging
	var name string
	consoleLogScope := -1
	av := reflect.NewValue(args).(*reflect.StructValue)
	if av.NumField() > 0 {
		if sv, ok := av.Field(0).(*reflect.StringValue); !ok {
			return nil
		} else {
			name = sv.Get()
		}
	}
	if av.NumField() > 1 {
		if iv, ok := av.Field(1).(*reflect.IntValue); !ok {
			return nil
		} else {
			consoleLogScope = iv.Get()
			if consoleLogScope < ScopeGlobal || consoleLogScope > ScopeLocal {
				return nil
			}
		}
	}
	//create a new router
	router := &routerImpl{}
	router.name = name
	router.seedId = seedId
	router.idType = reflect.Typeof(router.seedId)
	router.matchType = router.seedId.MatchType()
	router.initSysIds()
	router.defChanBufSize = DefDataChanBufSize
	if bufSize > 0 {
		router.defChanBufSize = bufSize
	}
	router.dispPolicy = disp
	router.routingTable = make(map[interface{}](*tblEntry))
	router.cmdChan = make(chan *command, DefCmdChanBufSize)
	router.proxies = new(vector.Vector)
	go router.mainLoop()
	router.notifier = newNotifier(router)
	router.Logger.Init(router.SysID(RouterLogId), router, router.name)
	if consoleLogScope >= ScopeGlobal && consoleLogScope <= ScopeLocal {
		router.LogSink.Init(router.NewSysID(RouterLogId, consoleLogScope), router)
	}
	router.FaultRaiser.Init(router.SysID(RouterFaultId), router, router.name)
	return router
}
