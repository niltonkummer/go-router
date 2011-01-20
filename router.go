//
// Copyright (c) 2010 - 2011 Yigong Liu
//
// Distributed under New BSD License
//

/*
"router" is a Go package for peer-peer pub/sub message passing. 
The basic usage is to attach a send channel to an id in router to send messages, 
and attach a recv channel to an id to receive messages. If these 2 ids match, 
the messages from send channel will be "routed" to recv channel, e.g.

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
	"sync"
)

//Default size settings in router
const (
	DefLogBufSize      = 256
	DefDataChanBufSize = 32
	DefCmdChanBufSize  = 64
	DefBindingSetSize  = 4
	UnlimitedBuffer    = -1
	FlowControl        = true
)

//Router is the main access point to functionality. Applications will create an instance
//of it thru router.New(...) and attach channels to it
type Router interface {
	//---- core api ----
	//Attach chans to id in router, with an optional argument (chan *BindEvent)
	//When specified, the optional argument will serve two purposes:
	//1. used to tell when the remote peers connecting/disconn
	//2. in AttachRecvChan, used as a flag to ask router to keep recv chan open when all senders close
	//the returned RoutedChan object can be used to find the number of bound peers: routCh.NumPeers()
	AttachSendChan(Id, interface{}, ...interface{}) (*RoutedChan, os.Error)
	//3. When attaching recv chans, an optional integer can specify the internal buffering size
	AttachRecvChan(Id, interface{}, ...interface{}) (*RoutedChan, os.Error)

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
	//1. io.ReadWriteCloser: transport connection
	//2. MarshalingPolicy: gob or json marshaling
	//3. bool flag: turn on flow control on connection
	ConnectRemote(io.ReadWriteCloser, MarshalingPolicy, ...bool) (Proxy, os.Error)

	//--- other utils ---
	//return pre-created SysIds according to the router's id-type, with ScopeGlobal / MemberLocal
	SysID(idx int) Id

	//create a new SysId with "args..." specifying scope/membership
	NewSysID(idx int, args ...int) Id

	//return all ids and their ChanTypes from router's namespace which satisfy predicate
	IdsForSend(predicate func(id Id) bool) map[interface{}]*IdChanInfo
	IdsForRecv(predicate func(id Id) bool) map[interface{}]*IdChanInfo
}

//Major data structures for router:
//1. tblEntry: an entry for each id in router
//2. routerImpl: main data struct of router
type tblEntry struct {
	chanType *reflect.ChanType
	id       Id
	senders  map[interface{}]*RoutedChan
	recvers  map[interface{}]*RoutedChan
}

type routerImpl struct {
	async          bool
	defChanBufSize int
	dispPolicy     DispatchPolicy
	seedId         Id
	idType         reflect.Type
	matchType      MatchType
	tblLock        sync.Mutex
	routingTable   map[interface{}](*tblEntry)
	sysIds         [NumSysInternalIds]Id
	notifier       *notifier
	proxLock       sync.Mutex
	proxies        vector.Vector
	bufSizeLock    sync.Mutex
	recvBufSizes   map[interface{}]int
	//for log/debug, if name != nil, debug is enabled
	Logger
	LogSink
	FaultRaiser
	name string
}

func (s *routerImpl) NewSysID(idx int, args ...int) Id {
	sid, err := s.seedId.SysID(idx, args...)
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

func (s *routerImpl) IdsForSend(predicate func(id Id) bool) map[interface{}]*IdChanInfo {
	ids := make(map[interface{}]*IdChanInfo)
	s.tblLock.Lock()
	for _, v := range s.routingTable {
		for _, e := range v.senders {
			idx := e.Id.SysIdIndex()
			if idx < 0 && predicate(e.Id) {
				ids[e.Id.Key()] = &IdChanInfo{Id: e.Id, ChanType: v.chanType}
			}
		}
	}
	s.tblLock.Unlock()
	return ids
}

func (s *routerImpl) IdsForRecv(predicate func(id Id) bool) map[interface{}]*IdChanInfo {
	ids := make(map[interface{}]*IdChanInfo)
	s.tblLock.Lock()
	for _, v := range s.routingTable {
		for _, e := range v.recvers {
			idx := e.Id.SysIdIndex()
			if idx < 0 && predicate(e.Id) {
				ids[e.Id.Key()] = &IdChanInfo{Id: e.Id, ChanType: v.chanType}
			}
		}
	}
	s.tblLock.Unlock()
	return ids
}

func (s *routerImpl) validateId(id Id) (err os.Error) {
	if id == nil || (id.Scope() < ScopeGlobal || id.Scope() > ScopeLocal) ||
		(id.Member() < MemberLocal || id.Member() > MemberRemote) {
		err = os.ErrorString(fmt.Sprintf("%s: %v", errInvalidId, id))
	}
	return
}

func (s *routerImpl) AttachSendChan(id Id, v interface{}, args ...interface{}) (routCh *RoutedChan, err os.Error) {
	if err = s.validateId(id); err != nil {
		s.LogError(err)
		s.Raise(err)
		return
	}
	var ok bool
	ch, internalChan := v.(Channel)
	if !internalChan {
		ch, ok = reflect.NewValue(v).(*reflect.ChanValue)
		if !ok {
			err = os.ErrorString(errInvalidChan)
			s.LogError(err)
			s.Raise(err)
			return
		}
	}
	l := len(args)
	var bindChan chan *BindEvent
	if l > 0 {
		switch cv := args[0].(type) {
		case chan *BindEvent:
			bindChan = cv
			if cap(bindChan) == 0 {
				err = os.ErrorString(errInvalidBindChan + ": binding bindChan is not buffered")
				s.LogError(err)
				s.Raise(err)
				return
			}
		default:
			err = os.ErrorString("invalid arguments to attach send chan")
			s.LogError(err)
			s.Raise(err)
			return
		}
	}
	routCh = newRoutedChan(id, senderType, ch, s, bindChan)
	routCh.internalChan = internalChan
	err = s.attach(routCh)
	if err != nil {
		s.LogError(err)
		s.Raise(err)
	}
	return
}

func (s *routerImpl) AttachRecvChan(id Id, v interface{}, args ...interface{}) (routCh *RoutedChan, err os.Error) {
	if err = s.validateId(id); err != nil {
		s.LogError(err)
		s.Raise(err)
		return
	}
	var ok bool
	ch, internalChan := v.(Channel)
	if !internalChan {
		ch, ok = reflect.NewValue(v).(*reflect.ChanValue)
		if !ok {
			err = os.ErrorString(errInvalidChan)
			s.LogError(err)
			s.Raise(err)
			return
		}
	}
	var bindChan chan *BindEvent
	for i := 0; i < len(args); i++ {
		switch cv := args[0].(type) {
		case chan *BindEvent:
			bindChan = cv
			if cap(bindChan) == 0 {
				err = os.ErrorString(errInvalidBindChan + ": binding bindChan is not buffered")
				s.LogError(err)
				s.Raise(err)
				return
			}
		case int:
			//set recv chan buffer size
			s.bufSizeLock.Lock()
			old, ok := s.recvBufSizes[id.Key()]
			if !ok || old < cv {
				s.recvBufSizes[id.Key()] = cv
			}
			s.bufSizeLock.Unlock()
		default:
			err = os.ErrorString("invalid arguments to attach recv chan")
			s.LogError(err)
			s.Raise(err)
			return
		}
	}
	if s.async && ch.Cap() != UnlimitedBuffer && !internalChan {
		//for async router, external recv chans must have unlimited buffering, 
		//ie. Cap()==-1, all undelivered msgs will be buffered right before ext recv chans
		ch = &asyncChan{Channel: ch}
	}
	routCh = newRoutedChan(id, recverType, ch, s, bindChan)
	routCh.internalChan = internalChan
	err = s.attach(routCh)
	if err != nil {
		s.LogError(err)
		s.Raise(err)
	}
	return
}

func (s *routerImpl) DetachChan(id Id, v interface{}) (err os.Error) {
	s.Log(LOG_INFO, "DetachChan called...")
	if err = s.validateId(id); err != nil {
		s.LogError(err)
		s.Raise(err)
		return
	}
	ch, ok := v.(Channel)
	if !ok {
		ch, ok = reflect.NewValue(v).(*reflect.ChanValue)
		if !ok {
			err = os.ErrorString(errInvalidChan)
			s.LogError(err)
			s.Raise(err)
			return
		}
	}
	routCh := &RoutedChan{}
	routCh.Id = id
	routCh.Channel = ch
	routCh.router = s
	err = s.detach(routCh)
	return
}

func (s *routerImpl) Close() {
	s.Log(LOG_INFO, "Close()/shutdown called")
	s.shutdown()
}

func (s *routerImpl) attach(routCh *RoutedChan) (err os.Error) {
	//handle id
	if reflect.Typeof(routCh.Id) != s.idType {
		err = os.ErrorString(errIdTypeMismatch + ": " + routCh.Id.String())
		s.LogError(err)
		return
	}

	s.tblLock.Lock()
	//router entry
	ent, ok := s.routingTable[routCh.Id.Key()]
	if !ok {
		if routCh.internalChan {
			err = os.ErrorString(fmt.Sprintf("%s %v", errChanGenericType, routCh.Id))
			s.LogError(err)
			s.tblLock.Unlock()
			return
		}
		//first routedChan attached to this id, add a router-entry for this id
		ent = &tblEntry{}
		s.routingTable[routCh.Id.Key()] = ent
		ent.id = routCh.Id // will only use the Val/Match() part of id
		ent.chanType = routCh.Channel.Type().(*reflect.ChanType)
		ent.senders = make(map[interface{}]*RoutedChan)
		ent.recvers = make(map[interface{}]*RoutedChan)
	} else {
		if !routCh.internalChan && routCh.Channel.Type().(*reflect.ChanType) != ent.chanType {
			err = os.ErrorString(fmt.Sprintf("%s %v", errChanTypeMismatch, routCh.Id))
			s.LogError(err)
			s.tblLock.Unlock()
			return
		}
	}

	//check for duplicate
	switch routCh.kind {
	case senderType:
		if _, ok = ent.senders[routCh.Channel.Interface()]; ok {
			err = os.ErrorString(errDupAttachment)
			s.LogError(err)
			s.tblLock.Unlock()
			return
		} else {
			ent.senders[routCh.Channel.Interface()] = routCh
		}
	case recverType:
		if _, ok = ent.recvers[routCh.Channel.Interface()]; ok {
			err = os.ErrorString(errDupAttachment)
			s.LogError(err)
			s.tblLock.Unlock()
			return
		} else {
			ent.recvers[routCh.Channel.Interface()] = routCh
		}
	}

	idx := routCh.Id.SysIdIndex()
	var matches vector.Vector

	//find bindings for routedChan
	if s.matchType == ExactMatch {
		switch routCh.kind {
		case senderType:
			for _, recver := range ent.recvers {
				if scope_match(routCh.Id, recver.Id) {
					s.Log(LOG_INFO, fmt.Sprintf("add bindings: %v -> %v", routCh.Id, recver.Id))
					matches.Push(recver)
				}
			}
		case recverType:
			for _, sender := range ent.senders {
				if scope_match(sender.Id, routCh.Id) {
					s.Log(LOG_INFO, fmt.Sprintf("add bindings: %v -> %v", sender.Id, routCh.Id))
					matches.Push(sender)
				}
			}
		}
	} else { //for PrefixMatch & AssocMatch, need to iterate thru all entries in map routingTable
		for _, ent2 := range s.routingTable {
			if routCh.Id.Match(ent2.id) {
				if routCh.Channel.Type().(*reflect.ChanType) == ent2.chanType ||
					(routCh.kind == recverType && routCh.internalChan) {
					switch routCh.kind {
					case senderType:
						for _, recver := range ent2.recvers {
							if scope_match(routCh.Id, recver.Id) {
								s.Log(LOG_INFO, fmt.Sprintf("add bindings: %v -> %v", routCh.Id, recver.Id))
								matches.Push(recver)
							}
						}
					case recverType:
						for _, sender := range ent2.senders {
							if scope_match(sender.Id, routCh.Id) {
								s.Log(LOG_INFO, fmt.Sprintf("add bindings: %v -> %v", sender.Id, routCh.Id))
								matches.Push(sender)
							}
						}
					}
				} else {
					em := os.ErrorString(fmt.Sprintf("%s : [%v, %v]", errChanTypeMismatch, routCh.Id, ent2.id))
					s.Log(LOG_ERROR, em)
					//should crash here?
					s.Raise(em)
				}
			}
		}
	}

	s.tblLock.Unlock()

	//activate
	//force broadcaster for system ids
	if idx >= 0 { //sys ids
		routCh.start(BroadcastPolicy)
	} else {
		routCh.start(s.dispPolicy)
	}

	//finished updating routing table
	//start updating routedChans's binding_set
	for i := 0; i < matches.Len(); i++ {
		peer := matches[i].(*RoutedChan)
		routCh.attach(peer)
		peer.attach(routCh)
	}

	//notifier will send in a separate goroutine, so non-blocking here
	if idx < 0 && routCh.Id.Member() == MemberLocal { //not sys ids
		switch routCh.kind {
		case senderType:
			s.notifier.notifyPub(&IdChanInfo{Id: routCh.Id, ChanType: routCh.Channel.Type().(*reflect.ChanType)})
		case recverType:
			s.notifier.notifySub(&IdChanInfo{Id: routCh.Id, ChanType: routCh.Channel.Type().(*reflect.ChanType)})
		}
	}
	return
}

func (s *routerImpl) detach(routCh *RoutedChan) (err os.Error) {
	s.Log(LOG_INFO, fmt.Sprintf("detach chan from id %v\n", routCh.Id))

	//check id
	if reflect.Typeof(routCh.Id) != s.idType {
		err = os.ErrorString(errIdTypeMismatch + ": " + routCh.Id.String())
		s.LogError(err)
		return
	}

	s.tblLock.Lock()

	//find router entry
	ent, ok := s.routingTable[routCh.Id.Key()]
	if !ok {
		err = os.ErrorString(errDetachChanNotInRouter + ": " + routCh.Id.String())
		s.LogError(err)
		s.tblLock.Unlock()
		return
	}

	//find the routedChan & remove it from tblEntry
	routCh1, ok := ent.senders[routCh.Channel.Interface()]
	if ok {
		ent.senders[routCh.Channel.Interface()] = routCh1, false
	} else if routCh1, ok = ent.recvers[routCh.Channel.Interface()]; ok {
		ent.recvers[routCh.Channel.Interface()] = routCh1, false
	} else {
		err = os.ErrorString(errDetachChanNotInRouter + ": " + routCh.Id.String())
		s.LogError(err)
		s.tblLock.Unlock()
		return
	}

	s.tblLock.Unlock()

	//remove bindings from peers. dup bindings to avoid race at shutdown
	copySet := routCh1.Peers()
	for _, v := range copySet {
		if routCh1.kind == senderType {
			s.Log(LOG_INFO, fmt.Sprintf("del bindings: %v -> %v", routCh1.Id, v.Id))
		} else {
			s.Log(LOG_INFO, fmt.Sprintf("del bindings: %v -> %v", v.Id, routCh1.Id))
		}
		v.detach(routCh1)
	}

	//close routedChan's chans, so any goroutines waiting on them will exit
	routCh1.close()

	//notifier will send in a separate goroutine, so non-blocking here
	idx := routCh1.Id.SysIdIndex()
	if idx < 0 && routCh.Id.Member() == MemberLocal { //not sys ids
		switch routCh.kind {
		case senderType:
			s.notifier.notifyUnPub(&IdChanInfo{Id: routCh1.Id, ChanType: routCh1.Channel.Type().(*reflect.ChanType)})
		case recverType:
			s.notifier.notifyUnSub(&IdChanInfo{Id: routCh1.Id, ChanType: routCh1.Channel.Type().(*reflect.ChanType)})
		}
	}

	return
}

func (s *routerImpl) shutdown() {
	s.Log(LOG_INFO, "shutdown start...")

	s.tblLock.Lock()
	defer s.tblLock.Unlock()
	s.proxLock.Lock()
	defer s.proxLock.Unlock()

	// close all peers
	for i := 0; i < s.proxies.Len(); i++ {
		s.proxies[i].(Proxy).Close()
	}
	s.Log(LOG_INFO, "all proxy closed")

	//close all enndpoint send chans
	for _, ent := range s.routingTable {
		for _, sender := range ent.senders {
			sender.close()
		}
	}

	//wait for console log goroutine to exit
	s.FaultRaiser.Close()
	s.Logger.Close()
	s.LogSink.Close()

	for _, ent := range s.routingTable {
		for _, recver := range ent.recvers {
			recver.close()
		}
	}
}

func (s *routerImpl) initSysIds() {
	for i := 0; i < NumSysInternalIds; i++ {
		s.sysIds[i], _ = s.seedId.SysID(i)
	}
}

func (s *routerImpl) recvChanBufSize(id Id) int {
	s.bufSizeLock.Lock()
	defer s.bufSizeLock.Unlock()
	v, ok := s.recvBufSizes[id.Key()]
	if ok {
		return v
	}
	return s.defChanBufSize
}

func (s *routerImpl) addProxy(p Proxy) {
	s.Log(LOG_INFO, "add proxy")
	s.proxLock.Lock()
	s.proxies.Push(p)
	s.proxLock.Unlock()
}

func (s *routerImpl) delProxy(p Proxy) {
	s.Log(LOG_INFO, "del proxy impl")
	num := -1
	s.proxLock.Lock()
	for i := 0; i < s.proxies.Len(); i++ {
		if s.proxies[i].(Proxy) == p {
			num = i
			break
		}
	}
	if num >= 0 {
		s.proxies.Delete(num)
	}
	s.proxLock.Unlock()
}

//Connect() connects this router to peer router, the real job is done inside Proxy
func (r1 *routerImpl) Connect(r2 Router) (p1, p2 Proxy, err os.Error) {
	p1 = NewProxy(r1, "", nil, nil)
	p2 = NewProxy(r2, "", nil, nil)
	err = p1.Connect(p2)
	return
}

func (r *routerImpl) ConnectRemote(rwc io.ReadWriteCloser, mar MarshalingPolicy, flags ...bool) (p Proxy, err os.Error) {
	p = NewProxy(r, "", nil, nil)
	err = p.ConnectRemote(rwc, mar, flags...)
	return
}

/*
New is router constructor. It accepts the following arguments:
    1. seedId: a dummy id to show what type of ids will be used. New ids will be type-checked against this.
    2. bufSize: the buffer size used for router's internal channels.
           if bufSize >= 0, its value will be used
           if bufSize < 0, it means unlimited buffering, so router is async and sending on 
                                attached channels will never block
    3. disp: dispatch policy for router. by default, it is BroadcastPolicy
    4. optional arguments ...:
            name:     router's name, if name is defined, router internal logging will be turned on,
                      ie LogRecord generated
            LogScope: if this is set, a console log sink is installed to show router internal log
                      if logScope == ScopeLocal, only log msgs from local router will show up
                      if logScope == ScopeGlobal, all log msgs from connected routers will show up
*/
func New(seedId Id, bufSize int, disp DispatchPolicy, args ...interface{}) Router {
	//parse optional router name and flag for enable console logging
	var name string
	consoleLogScope := -1
	l := len(args)
	if l > 0 {
		if sv, ok := args[0].(string); !ok {
			return nil
		} else {
			name = sv
		}
	}
	if l > 1 {
		if iv, ok := args[1].(int); !ok {
			return nil
		} else {
			consoleLogScope = iv
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
	if bufSize >= 0 {
		router.defChanBufSize = bufSize
	} else {
		router.async = true
	}
	router.dispPolicy = disp
	router.routingTable = make(map[interface{}](*tblEntry))
	router.recvBufSizes = make(map[interface{}]int)
	router.notifier = newNotifier(router)
	router.Logger.Init(router.SysID(RouterLogId), router, router.name)
	if consoleLogScope >= ScopeGlobal && consoleLogScope <= ScopeLocal {
		router.LogSink.Init(router.NewSysID(RouterLogId, consoleLogScope), router)
	}
	router.FaultRaiser.Init(router.SysID(RouterFaultId), router, router.name)
	return router
}
