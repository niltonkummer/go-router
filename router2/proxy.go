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
	"io"
)

/*
 Proxy is the primary interface to connect router to its peer router.
 At both ends of a connection, there is a proxy object for its router.
 Simple router connection can be set up thru calling Router.Connect().
 Proxy.Connect() can be used to set up more complicated connections,
 such as setting IdFilter to allow only a subset of messages pass thru
 the connection, or setting IdTranslator which can "relocate" remote message
 ids into a subspace in local router's namespace.
 Proxy.Close() is called to disconnect router from its peer.
*/
type Proxy interface {
	Connect(Proxy) os.Error
	ConnectRemote(io.ReadWriteCloser, MarshallingPolicy) os.Error
	Close()
}

type peerIntf interface {
	//send app/ctrl msgs to peer
	sendAppMsg(Id, interface{}) os.Error
	sendCtrlMsg(Id, interface{}) os.Error
}

type proxyImpl struct {
	//chan for ctrl msgs from peers during connSetup
	ctrlChan chan *genericMsg
	//use peerIntf to forward app/ctrl msgs to peer (proxy or stream)
	peer peerIntf
	//my/local router
	router *routerImpl
	//chans connecting to local router
	sysChans     *sysChans
	appSendChans *sendChanBundle //send to local router
	appRecvChans *recvChanBundle //recv from local router
	//filter & translator
	filter     IdFilter
	translator IdTranslator
	//cache of export/import ids at proxy
	exportSendIds map[interface{}]*IdChanInfo //exported send ids, global publish
	exportRecvIds map[interface{}]*IdChanInfo //exported recv ids, global subscribe
	importSendIds map[interface{}]*IdChanInfo //imported send ids from peer
	importRecvIds map[interface{}]*IdChanInfo //imported recv ids from peer
	//mutex to protect access to exportRecvIds from both proxy and stream
	exportRecvLock sync.Mutex
	//for log/debug
	name string
	Logger
	FaultRaiser
	//others
	Closed  bool
	errChan chan os.Error
}

/*
 Proxy constructor. It accepts the following arguments:
    1. r:    the router which will be bound with this proxy and be owner of this proxy
    2. name: proxy's name, used in log messages if owner router's log is turned on
    3. f:    IdFilter to be installed at this proxy
    4. t:    IdTranslator to be installed at this proxy
*/
func NewProxy(r Router, name string, f IdFilter, t IdTranslator) Proxy {
	p := new(proxyImpl)
	p.router = r.(*routerImpl)
	p.name = name
	p.filter = f
	p.translator = t
	//create chan for incoming ctrl msgs during connSetup
	p.ctrlChan = make(chan *genericMsg, DefCmdChanBufSize)
	//chans to local router
	p.sysChans = newSysChans(p)
	p.appSendChans = newSendChanBundle(p.router, ScopeLocal, MemberRemote)
	if p.translator == nil {
		p.appRecvChans = newRecvChanBundle(p.router, ScopeLocal, MemberRemote, func(msg *genericMsg) { p.peer.sendAppMsg(msg.Id, msg.Data) })
	} else {
		p.appRecvChans = newRecvChanBundle(p.router, ScopeLocal, MemberRemote, func(msg *genericMsg) { p.peer.sendAppMsg(p.translator.TranslateOutward(msg.Id), msg.Data) })
	}
	//cache: only need to create import cache, since export cache are queried/returned from router
	p.importSendIds = make(map[interface{}]*IdChanInfo)
	p.importRecvIds = make(map[interface{}]*IdChanInfo)
	p.router.addProxy(p)
	ln := ""
	if len(p.router.name) > 0 {
		if len(p.name) > 0 {
			ln = p.router.name + p.name
		} else {
			ln = p.router.name + "_proxy"
		}
	}
	p.Logger.Init(p.router.SysID(RouterLogId), p.router, ln)
	p.FaultRaiser.Init(p.router.SysID(RouterFaultId), p.router, ln)
	return p
}

func (p1 *proxyImpl) Connect(pp Proxy) os.Error {
	p2 := pp.(*proxyImpl)
	p2.peer = p1
	p1.peer = p2
	p1.errChan = make(chan os.Error)
	p1.start()
	p2.start()
	return <-p1.errChan
}

func (p *proxyImpl) ConnectRemote(rwc io.ReadWriteCloser, mar MarshallingPolicy) os.Error {
	s := newStream(rwc, mar, p)
	s.peer = p
	p.peer = s
	p.errChan = make(chan os.Error)
	p.start()
	s.start()
	return <-p.errChan
}

func (p *proxyImpl) start() { go p.ctrlMainLoop() }

func (p *proxyImpl) Close() {
	p.Log(LOG_INFO, "proxy.Close() is called")
	close(p.ctrlChan)
}

func (p *proxyImpl) closeImpl() {
	if !p.Closed {
		//notify local chan that remote peer is leaving (unpub, unsub)
		pubInfo := p.importPubInfo()
		subInfo := p.importSubInfo()
		p.Log(LOG_INFO, fmt.Sprintf("unpub [%d], unsub [%d]", len(pubInfo), len(subInfo)))
		p.sysChans.SendPubSubInfo(UnPubId, &IdChanInfoMsg{pubInfo})
		p.sysChans.SendPubSubInfo(UnSubId, &IdChanInfoMsg{subInfo})
		//start closing
		p.Log(LOG_INFO, "proxy closing")
		p.Closed = true
		p.router.delProxy(p)
		//close my chans
		p.appRecvChans.Close()
		p.appSendChans.Close()
		p.sysChans.Close()
		//close logger
		p.FaultRaiser.Close()
		p.Logger.Close()
	}
	p.Log(LOG_INFO, "proxy closed")
}

func (p *proxyImpl) connSetup() os.Error {
	r := p.router
	//1. to initiate conn setup handshaking, send my conn info to peer
	p.peer.sendCtrlMsg(r.SysID(RouterConnId), &ConnInfoMsg{SeedId: p.router.seedId})
	//2. recv connInfo from peer
	switch m := <-p.ctrlChan; {
	case m.Id.Match(r.SysID(RouterConnId)):
		//save peer conninfo & forward it
		ci := m.Data.(*ConnInfoMsg)
		p.sysChans.SendConnInfo(RouterConnId, ci)
		//check type info
		if reflect.Typeof(ci.SeedId) != reflect.Typeof(r.seedId) {
			err := os.ErrorString(errRmtIdTypeMismatch)
			ci.Error = err
			//tell local listeners about the fail
			p.sysChans.SendConnInfo(ConnErrorId, ci)
			//tell peer about fail
			p.peer.sendCtrlMsg(r.SysID(ConnErrorId), &ConnInfoMsg{Error: err})
			p.LogError(err)
			return err
		}
	default:
		err := os.ErrorString(errConnInvalidMsg)
		//tell peer about fail
		p.peer.sendCtrlMsg(r.SysID(ConnErrorId), &ConnInfoMsg{Error: err})
		p.LogError(err)
		return err
	}
	//3. send initial pub/sub info to peer
	p.peer.sendCtrlMsg(r.SysID(PubId), p.initPubInfoMsg())
	p.peer.sendCtrlMsg(r.SysID(SubId), p.initSubInfoMsg())
	//4. handle init_pub/sub msgs, send connReady to peer and wait for peer's connReady
	peerReady := false
	myReady := false
	for !(peerReady && myReady) {
		switch m := <-p.ctrlChan; {
		case m.Id.Match(r.SysID(ConnReadyId)):
			p.sysChans.SendConnInfo(ConnReadyId, m.Data.(*ConnInfoMsg))
			peerReady = true
		case m.Id.Match(r.SysID(ConnErrorId)):
			ci := m.Data.(*ConnInfoMsg)
			p.sysChans.SendConnInfo(ConnErrorId, ci)
			p.LogError(ci.Error)
			return ci.Error
		case m.Id.Match(r.SysID(PubId)):
			_, err := p.handlePeerPubMsg(m)
			p.sysChans.SendPubSubInfo(PubId, m.Data.(*IdChanInfoMsg))
			if err != nil {
				//tell peer about fail
				p.peer.sendCtrlMsg(r.SysID(ConnErrorId), &ConnInfoMsg{Error: err})
				p.LogError(err)
				return err
			}
		case m.Id.Match(r.SysID(SubId)):
			_, err := p.handlePeerSubMsg(m)
			p.sysChans.SendPubSubInfo(SubId, m.Data.(*IdChanInfoMsg))
			if err != nil {
				//tell peer about fail
				p.peer.sendCtrlMsg(r.SysID(ConnErrorId), &ConnInfoMsg{Error: err})
				p.LogError(err)
				return err
			}
			myReady = true
			//tell peer i am ready
			p.peer.sendCtrlMsg(r.SysID(ConnReadyId), &ConnInfoMsg{})
		default:
			err := os.ErrorString(errConnInvalidMsg)
			//tell peer about fail
			p.peer.sendCtrlMsg(r.SysID(ConnErrorId), &ConnInfoMsg{Error: err})
			p.LogError(err)
			return err
		}
	}
	return nil
}

func (p *proxyImpl) ctrlMainLoop() {
	//query router main goroutine to retrieve exported ids
	p.exportSendIds = p.router.IdsForSend(ExportedId)
	p.exportRecvIds = p.router.IdsForRecv(ExportedId)

	//start conn handshaking
	err := p.connSetup()
	if p.errChan != nil {
		p.errChan <- err
	}
	if err != nil {
		p.closeImpl()
		return
	}

	//at here, conn is ready, activate recv/forward goroutines
	p.sysChans.Start()
	p.appRecvChans.Start()

	p.Log(LOG_INFO, "-- connection ready")

	//normal msg pumping
	r := p.router
	cont := true
	for cont {
		p.Log(LOG_INFO, "proxy wait for another sys ctrl msg")
		//give conn msgs, pubSub msgs higher priority by duplicating them twice
		//put app data chan at very last
		select {
		case m := <-p.sysChans.pubSubInfo:
			if closed(p.sysChans.pubSubInfo) {
				//tell peer exiting
				p.Log(LOG_INFO, "proxy shutdown, send peer Disconn msg")
				p.peer.sendCtrlMsg(r.SysID(RouterDisconnId), &ConnInfoMsg{})
				cont = false
				break
			}
			switch {
			case m.Id.Match(r.SysID(PubId)):
				_, err = p.handleLocalPubMsg(m)
			case m.Id.Match(r.SysID(UnPubId)):
				_, err = p.handleLocalUnPubMsg(m)
			case m.Id.Match(r.SysID(SubId)):
				_, err = p.handleLocalSubMsg(m)
			case m.Id.Match(r.SysID(UnSubId)):
				_, err = p.handleLocalUnSubMsg(m)
			}
			if err != nil {
				//tell peer about fail
				ci := &ConnInfoMsg{Error: err}
				p.peer.sendCtrlMsg(r.SysID(ConnErrorId), ci)
				p.sysChans.SendConnInfo(ConnErrorId, ci)
				p.LogError(err)
				cont = false
			}
		case m := <-p.ctrlChan:
			if closed(p.ctrlChan) {
				//tell peer exiting
				p.Log(LOG_INFO, "proxy shutdown, send peer Disconn msg")
				p.peer.sendCtrlMsg(r.SysID(RouterDisconnId), &ConnInfoMsg{})
				cont = false
				break
			}
			switch {
			case m.Id.Match(r.SysID(RouterDisconnId)):
				p.sysChans.SendConnInfo(RouterDisconnId, m.Data.(*ConnInfoMsg))
				p.Log(LOG_INFO, "recv Disconn msg, proxy shutdown")
				cont = false
			case m.Id.Match(r.SysID(ConnErrorId)):
				ci := m.Data.(*ConnInfoMsg)
				p.sysChans.SendConnInfo(ConnErrorId, ci)
				p.LogError(ci.Error)
				cont = false
			case m.Id.Match(r.SysID(PubId)):
				_, err = p.handlePeerPubMsg(m)
				p.sysChans.SendPubSubInfo(PubId, m.Data.(*IdChanInfoMsg))
			case m.Id.Match(r.SysID(UnPubId)):
				_, err = p.handlePeerUnPubMsg(m)
				p.sysChans.SendPubSubInfo(UnPubId, m.Data.(*IdChanInfoMsg))
			case m.Id.Match(r.SysID(SubId)):
				_, err = p.handlePeerSubMsg(m)
				p.sysChans.SendPubSubInfo(SubId, m.Data.(*IdChanInfoMsg))
			case m.Id.Match(r.SysID(UnSubId)):
				_, err = p.handlePeerUnSubMsg(m)
				p.sysChans.SendPubSubInfo(UnSubId, m.Data.(*IdChanInfoMsg))
			}
			if err != nil {
				//tell peer about fail
				ci := &ConnInfoMsg{Error: err}
				p.peer.sendCtrlMsg(r.SysID(ConnErrorId), ci)
				p.sysChans.SendConnInfo(ConnErrorId, ci)
				p.LogError(err)
				cont = false
			}
		}
		p.Log(LOG_INFO, "proxy finish one sys ctrl msg")
	}
	p.closeImpl()
}

//the following 2 functions are external interface exposed to peers
func (p *proxyImpl) sendCtrlMsg(id Id, data interface{}) (err os.Error) {
	p.ctrlChan <- &genericMsg{id, data}
	return
}

func (p *proxyImpl) sendAppMsg(id Id, data interface{}) (err os.Error) {
	if p.translator != nil {
		err = p.appSendChans.Send(p.translator.TranslateInward(id), data)
	} else {
		err = p.appSendChans.Send(id, data)
	}
	return
}

func (p *proxyImpl) chanTypeMatch(info1, info2 *IdChanInfo) bool {
	if info1.ChanType != nil {
		if info2.ChanType != nil {
			return info1.ChanType == info2.ChanType
		}
		//at here, info2 should be marshaled data from remote
		if info2.ElemType == nil {
			p.Log(LOG_ERROR, fmt.Sprintf("IdChanInfo miss both ChanType & ElemType info for %v", info2.Id))
			return false
		}
		//1. marshal data for info1
		if info1.ElemType == nil {
			info1.ElemType = new(chanElemTypeData)
			elemType := info1.ChanType.Elem()
			info1.ElemType.FullName = elemType.PkgPath() + "." + elemType.Name()
			//do the real type encoding
		}
		//2. compare marshaled data
		if info1.ElemType.FullName == info2.ElemType.FullName {
			//3. since type match, use info1's ChanType for info2
			info2.ChanType = info1.ChanType
			return true
		} else {
			return false
		}
	} else {
		if info2.ChanType == nil {
			p.Log(LOG_ERROR, fmt.Sprintf("both pub/sub miss ChanType for %v", info2.Id))
			return false
		}
		//at here, info1 should be marshaled data from remote
		if info1.ElemType == nil {
			p.Log(LOG_ERROR, fmt.Sprintf("IdChanInfo miss both ChanType & ElemType info for %v", info1.Id))
			return false
		}
		//1. marshal data for info2
		if info2.ElemType == nil {
			info2.ElemType = new(chanElemTypeData)
			elemType := info2.ChanType.Elem()
			info2.ElemType.FullName = elemType.PkgPath() + "." + elemType.Name()
			//do the real type encoding
		}
		//2. compare marshaled data
		if info1.ElemType.FullName == info2.ElemType.FullName {
			//3. since type match, use info1's ChanType for info2
			info1.ChanType = info2.ChanType
			return true
		} else {
			return false
		}
	}
	return false
}

//for the following 8 Local/Peer Pub/Sub mehtods, the general rule is that we set up
//senders (to recv routers) before we set up recvers (from send routers), so msg will not be lost
func (p *proxyImpl) handleLocalSubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	flags := make([]bool, len(sInfo))
	for i, sub := range sInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalSubMsg: %v", sub.Id))
		if p.filter != nil && p.filter.BlockInward(sub.Id) {
			flags[i] = true
			continue
		}
		_, ok := p.exportRecvIds[sub.Id.Key()]
		if ok {
			flags[i] = true
			continue
		}
		//here we have a new global sub id
		//update exported id cache
		p.exportRecvLock.Lock()
		p.exportRecvIds[sub.Id.Key()] = sub
		p.exportRecvLock.Unlock()
		//check if peer already pubed it
		for _, pub := range p.importSendIds {
			if sub.Id.Match(pub.Id) {
				if p.chanTypeMatch(sub, pub) {
					//change scope to set flag marking sender is ready
					sub.Id, _ = sub.Id.Clone(NumScope, MemberRemote)
					p.appSendChans.AddSender(pub.Id, pub.ChanType)
				} else {
					err = os.ErrorString(errRmtChanTypeMismatch)
					p.LogError(err)
					p.Raise(err)
				}
			}
		}
		num++
	}
	if num > 0 {
		if p.translator == nil {
			if num == len(sInfo) {
				p.peer.sendCtrlMsg(m.Id, m.Data)
			} else {
				sInfo2 := make([]*IdChanInfo, num)
				cnt := 0
				for i, sub := range sInfo {
					if !flags[i] {
						sInfo2[cnt] = sub
						cnt++
					}
					if cnt == num {
						break
					}
				}
				p.peer.sendCtrlMsg(m.Id, &IdChanInfoMsg{Info: sInfo2})
			}
		} else {
			sInfo2 := make([]*IdChanInfo, num)
			cnt := 0
			for i, sub := range sInfo {
				if !flags[i] {
					sInfo2[cnt] = new(IdChanInfo)
					sInfo2[cnt].Id = p.translator.TranslateOutward(sub.Id)
					sInfo2[cnt].ChanType = sub.ChanType
					sInfo2[cnt].ElemType = sub.ElemType
					cnt++
				}
				if cnt == num {
					break
				}
			}
			p.peer.sendCtrlMsg(m.Id, &IdChanInfoMsg{Info: sInfo2})
		}
	}
	return
}

func (p *proxyImpl) handleLocalUnSubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	flags := make([]bool, len(sInfo))
	for i, sub := range sInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalUnSubMsg: %v", sub.Id))
		_, ok := p.exportRecvIds[sub.Id.Key()]
		if !ok {
			flags[i] = true
			continue
		}
		if p.filter != nil && p.filter.BlockInward(sub.Id) {
			flags[i] = true
			continue
		}

		delCache := false
		delSender := false
		switch no := p.appSendChans.BindingCount(sub.Id); {
		case no < 0: //not in
			delCache = true
		case no == 0:
			delCache = true
			delSender = true
		case no > 0:
			flags[i] = true
			continue
		}

		//update exported id cache
		if delCache {
			p.exportRecvLock.Lock()
			p.exportRecvIds[sub.Id.Key()] = sub, false
			p.exportRecvLock.Unlock()
		}

		//check if peer already pubed it
		if delSender {
			pub, ok := p.importSendIds[sub.Id.Key()]
			if ok {
				p.appSendChans.DelSender(pub.Id)
			}
		}
		num++
	}
	if num > 0 {
		if p.translator == nil {
			if num == len(sInfo) {
				p.peer.sendCtrlMsg(m.Id, m.Data)
			} else {
				sInfo2 := make([]*IdChanInfo, num)
				cnt := 0
				for i, sub := range sInfo {
					if !flags[i] {
						sInfo2[cnt] = sub
						cnt++
					}
					if cnt == num {
						break
					}
				}
				p.peer.sendCtrlMsg(m.Id, &IdChanInfoMsg{Info: sInfo2})
			}
		} else {
			sInfo2 := make([]*IdChanInfo, num)
			cnt := 0
			for i, sub := range sInfo {
				if !flags[i] {
					sInfo2[cnt] = new(IdChanInfo)
					sInfo2[cnt].Id = p.translator.TranslateOutward(sub.Id)
					sInfo2[cnt].ChanType = sub.ChanType
					sInfo2[cnt].ElemType = sub.ElemType
					cnt++
				}
				if cnt == num {
					break
				}
			}
			p.peer.sendCtrlMsg(m.Id, &IdChanInfoMsg{Info: sInfo2})
		}
	}
	return
}

func (p *proxyImpl) handleLocalPubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	pInfo := msg.Info
	if len(pInfo) == 0 {
		return
	}
	flags := make([]bool, len(pInfo))
	for i, pub := range pInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalPubMsg: %v", pub.Id))
		if p.filter != nil && p.filter.BlockOutward(pub.Id) {
			flags[i] = true
			continue
		}
		_, ok := p.exportSendIds[pub.Id.Key()]
		if ok {
			flags[i] = true
			continue
		}
		//update exported id cache
		p.exportSendIds[pub.Id.Key()] = pub
		//check if peer already subed it
		for _, sub := range p.importRecvIds {
			if pub.Id.Match(sub.Id) {
				if !p.chanTypeMatch(sub, pub) {
					err = os.ErrorString(errRmtChanTypeMismatch)
					p.LogError(err)
					return
				}
			}
		}
		num++
	}
	if num > 0 {
		if p.translator == nil {
			if num == len(pInfo) {
				p.peer.sendCtrlMsg(m.Id, m.Data)
			} else {
				pInfo2 := make([]*IdChanInfo, num)
				cnt := 0
				for i, pub := range pInfo {
					if !flags[i] {
						pInfo2[cnt] = pub
						cnt++
					}
					if cnt == num {
						break
					}
				}
				p.peer.sendCtrlMsg(m.Id, pInfo2)
			}
		} else {
			pInfo2 := make([]*IdChanInfo, num)
			cnt := 0
			for i, pub := range pInfo {
				if !flags[i] {
					pInfo2[cnt] = new(IdChanInfo)
					pInfo2[cnt].Id = p.translator.TranslateOutward(pub.Id)
					pInfo2[cnt].ChanType = pub.ChanType
					pInfo2[cnt].ElemType = pub.ElemType
					cnt++
				}
				if cnt == num {
					break
				}
			}
			p.peer.sendCtrlMsg(m.Id, pInfo2)
		}
	}
	return
}

func (p *proxyImpl) handleLocalUnPubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	pInfo := msg.Info
	if len(pInfo) == 0 {
		return
	}
	flags := make([]bool, len(pInfo))
	for i, pub := range pInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalUnPubMsg: %v", pub.Id))
		_, ok := p.exportSendIds[pub.Id.Key()]
		if !ok {
			flags[i] = true
			continue
		}
		if p.filter != nil && p.filter.BlockOutward(pub.Id) {
			flags[i] = true
			continue
		}
		//update exported id cache
		p.exportSendIds[pub.Id.Key()] = pub, false
		//check if peer already subed it
		sub, ok := p.importRecvIds[pub.Id.Key()]
		if ok {
			if !p.chanTypeMatch(sub, pub) {
				err = os.ErrorString(errRmtChanTypeMismatch)
				p.LogError(err)
			} else {
				p.appRecvChans.DelRecver(sub.Id)
			}
		}

		delCache := false
		delRecver := false
		switch no := p.appRecvChans.BindingCount(pub.Id); {
		case no < 0: //not in
			delCache = true
		case no == 0:
			delCache = true
			delRecver = true
		case no > 0:
			flags[i] = true
			continue
		}

		//update exported id cache
		if delCache {
			p.exportSendIds[pub.Id.Key()] = pub, false
		}

		//check if peer already pubed it
		if delRecver {
			sub, ok := p.importRecvIds[pub.Id.Key()]
			if ok {
				p.appRecvChans.DelRecver(sub.Id)
			}
		}
		num++
	}
	if num > 0 {
		if p.translator == nil {
			if num == len(pInfo) {
				p.peer.sendCtrlMsg(m.Id, m.Data)
			} else {
				pInfo2 := make([]*IdChanInfo, num)
				cnt := 0
				for i, pub := range pInfo {
					if !flags[i] {
						pInfo2[cnt] = pub
						cnt++
					}
					if cnt == num {
						break
					}
				}
				p.peer.sendCtrlMsg(m.Id, pInfo2)
			}
		} else {
			pInfo2 := make([]*IdChanInfo, num)
			cnt := 0
			for i, pub := range pInfo {
				if !flags[i] {
					pInfo2[cnt] = new(IdChanInfo)
					pInfo2[cnt].Id = p.translator.TranslateOutward(pub.Id)
					pInfo2[cnt].ChanType = pub.ChanType
					pInfo2[cnt].ElemType = pub.ElemType
					cnt++
				}
				if cnt == num {
					break
				}
			}
			p.peer.sendCtrlMsg(m.Id, pInfo2)
		}
	}
	return
}

func (p *proxyImpl) handlePeerSubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	pInfo := make([]*IdChanInfo, len(sInfo))
	for _, sub := range sInfo {
		if p.translator != nil {
			sub.Id = p.translator.TranslateInward(sub.Id)
		}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerSubMsg: %v", sub.Id))
		if p.filter != nil && p.filter.BlockOutward(sub.Id) {
			continue
		}
		//for peerSub, one special case is for localPub round-trip,
		//in this case, importRecvIds may already have it
		//update import cache
		if p.appRecvChans.RecverExist(sub.Id) {
			continue
		}
		_, ok := p.importRecvIds[sub.Id.Key()]
		if !ok {
			p.importRecvIds[sub.Id.Key()] = sub
		}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerSubMsg1: %v", sub.Id))
		//check if local already pubed it
		for _, pub := range p.exportSendIds {
			if pub.Id.Match(sub.Id) {
				if !p.chanTypeMatch(pub, sub) {
					err = os.ErrorString(errRmtChanTypeMismatch)
					p.LogError(err)
					return
				}
				if sub.Id.Scope() == NumScope { //remote sender is ready
					sub.Id, _ = sub.Id.Clone(ScopeLocal, MemberRemote) //correct it back
					p.appRecvChans.AddRecver(sub.Id, sub.ChanType)
					p.Log(LOG_INFO, fmt.Sprintf("handlePeerSubMsg, add recver for: %v", sub.Id))
				} else {
					if p.translator == nil {
						pInfo[num] = pub
					} else {
						pInfo[num] = new(IdChanInfo)
						pInfo[num].Id = p.translator.TranslateOutward(pub.Id)
						pInfo[num].ChanType = pub.ChanType
						pInfo[num].ElemType = pub.ElemType
					}
					num++
				}
				break
			}
		}
	}
	if num > 0 {
		p.peer.sendCtrlMsg(p.router.SysID(PubId), &IdChanInfoMsg{Info: pInfo[0:num]})
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerSubMsg, return PubMsg for : %v", pInfo[0].Id))
	}
	return
}

func (p *proxyImpl) handlePeerUnSubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	for _, sub := range sInfo {
		if p.translator != nil {
			sub.Id = p.translator.TranslateInward(sub.Id)
		}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerUnSubMsg: %v", sub.Id))
		//update import cache
		p.importRecvIds[sub.Id.Key()] = sub, false
		//check if local already pubed it
		p.appRecvChans.DelRecver(sub.Id)
		num++
	}
	return
}

func (p *proxyImpl) handlePeerPubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	pInfo := msg.Info
	if len(pInfo) == 0 {
		return
	}
	sInfo := make([]*IdChanInfo, len(pInfo))
	for _, pub := range pInfo {
		if p.translator != nil {
			pub.Id = p.translator.TranslateInward(pub.Id)
		}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerPubMsg: %v", pub.Id))
		if p.filter != nil && p.filter.BlockInward(pub.Id) {
			continue
		}
		//update import cache
		if p.appSendChans.SenderExist(pub.Id) {
			continue
		}
		_, ok := p.importSendIds[pub.Id.Key()]
		if !ok {
			p.importSendIds[pub.Id.Key()] = pub
		}
		for _, sub := range p.exportRecvIds {
			if pub.Id.Match(sub.Id) {
				if !p.chanTypeMatch(pub, sub) {
					err = os.ErrorString(errRmtChanTypeMismatch)
					p.LogError(err)
					return
				}
				sInfo[num] = new(IdChanInfo)
				if p.translator != nil {
					sInfo[num].Id = p.translator.TranslateOutward(sub.Id)
				} else {
					sInfo[num].Id = sub.Id
				}
				sInfo[num].ChanType = sub.ChanType
				sInfo[num].ElemType = sub.ElemType
				//set special scope to mark sender is ready
				sInfo[num].Id, _ = sInfo[num].Id.Clone(NumScope, MemberLocal)
				num++
				p.appSendChans.AddSender(pub.Id, pub.ChanType)
			}
		}
	}
	//return a SubMsg to peer so that a recver can be added at peer side
	if num > 0 {
		p.peer.sendCtrlMsg(p.router.SysID(SubId), &IdChanInfoMsg{Info: sInfo[0:num]})
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerPubMsg, return SubMsg for : %v", sInfo[0].Id))
	}
	return
}

func (p *proxyImpl) handlePeerUnPubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	pInfo := msg.Info
	if len(pInfo) == 0 {
		return
	}
	for _, pub := range pInfo {
		if p.translator != nil {
			pub.Id = p.translator.TranslateInward(pub.Id)
		}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerUnPubMsg: %v", pub.Id))
		//update import cache
		p.importSendIds[pub.Id.Key()] = pub, false
		p.appSendChans.DelSender(pub.Id)
		num++
	}
	return
}

//called from stream, so need lock protection
func (p *proxyImpl) getExportRecvChanType(id Id) *reflect.ChanType {
	p.exportRecvLock.Lock()
	sub, ok := p.exportRecvIds[id.Key()]
	p.exportRecvLock.Unlock()
	if ok {
		return sub.ChanType
	}
	return nil
}

func (p *proxyImpl) initSubInfoMsg() *IdChanInfoMsg {
	num := len(p.exportRecvIds)
	info := make([]*IdChanInfo, num)
	idx := 0
	for _, v := range p.exportRecvIds {
		if p.filter != nil && p.filter.BlockInward(v.Id) {
			continue
		}
		info[idx] = new(IdChanInfo)
		if p.translator != nil {
			info[idx].Id = p.translator.TranslateOutward(v.Id)
		} else {
			info[idx].Id = v.Id
		}
		info[idx].ChanType = v.ChanType
		idx++
	}
	return &IdChanInfoMsg{Info: info[0:idx]}
}

func (p *proxyImpl) initPubInfoMsg() *IdChanInfoMsg {
	num := len(p.exportSendIds)
	info := make([]*IdChanInfo, num)
	idx := 0
	for _, v := range p.exportSendIds {
		if p.filter != nil && p.filter.BlockOutward(v.Id) {
			continue
		}
		info[idx] = new(IdChanInfo)
		if p.translator != nil {
			info[idx].Id = p.translator.TranslateOutward(v.Id)
		} else {
			info[idx].Id = v.Id
		}
		info[idx].Id = v.Id
		info[idx].ChanType = v.ChanType
		idx++
	}
	return &IdChanInfoMsg{Info: info[0:idx]}
}

func (p *proxyImpl) importPubInfo() []*IdChanInfo {
	num := len(p.importSendIds)
	info := make([]*IdChanInfo, num)
	idx := 0
	for _, v := range p.importSendIds {
		info[idx] = new(IdChanInfo)
		info[idx].Id = v.Id
		info[idx].ChanType = v.ChanType
		idx++
	}
	return info
}

func (p *proxyImpl) importSubInfo() []*IdChanInfo {
	num := len(p.importRecvIds)
	info := make([]*IdChanInfo, num)
	idx := 0
	for _, v := range p.importRecvIds {
		info[idx] = new(IdChanInfo)
		info[idx].Id = v.Id
		info[idx].ChanType = v.ChanType
		idx++
	}
	return info
}

//utils:
//sysChans: wrapper of sys chans
type sysChans struct {
	proxy      *proxyImpl
	pubSubInfo chan *genericMsg
	//chans to talk to local router
	sysRecvChans *recvChanBundle //recv from local router
	sysSendChans *sendChanBundle //send to local router
}

func (sc *sysChans) Close() {
	sc.proxy.Log(LOG_INFO, "proxy sysChan closing start")
	sc.sysRecvChans.Close()
	sc.sysSendChans.Close()
	sc.proxy.Log(LOG_INFO, "proxy sysChan closed")
}

func (sc *sysChans) SendConnInfo(idx int, data *ConnInfoMsg) {
	sc.sysSendChans.Send(sc.proxy.router.SysID(idx), data)
}

func (sc *sysChans) SendPubSubInfo(idx int, data *IdChanInfoMsg) {
	if idx >= PubId || idx <= UnSubId {
		//filter out sys internal ids
		info := make([]*IdChanInfo, len(data.Info))
		num := 0
		for i := 0; i < len(data.Info); i++ {
			if sc.proxy.router.getSysInternalIdIdx(data.Info[i].Id) < 0 {
				info[num] = data.Info[i]
				num++
			}
		}
		sc.sysSendChans.Send(sc.proxy.router.SysID(idx), &IdChanInfoMsg{info[0:num]})
	}
}

func (sc *sysChans) Start() { sc.sysRecvChans.Start() }

func newSysChans(p *proxyImpl) *sysChans {
	sc := new(sysChans)
	sc.proxy = p
	r := p.router
	sc.pubSubInfo = make(chan *genericMsg, DefCmdChanBufSize)
	sc.sysRecvChans = newRecvChanBundle(r, ScopeLocal, MemberRemote, func(m *genericMsg) { sc.pubSubInfo <- m })
	sc.sysRecvChans.dropChanCloseMsg = true
	sc.sysSendChans = newSendChanBundle(r, ScopeLocal, MemberRemote)
	//
	pubSubChanType := reflect.Typeof(make(chan *IdChanInfoMsg)).(*reflect.ChanType)
	connChanType := reflect.Typeof(make(chan *ConnInfoMsg)).(*reflect.ChanType)

	sc.sysRecvChans.AddRecver(r.SysID(PubId), pubSubChanType)
	sc.sysRecvChans.AddRecver(r.SysID(UnPubId), pubSubChanType)
	sc.sysRecvChans.AddRecver(r.SysID(SubId), pubSubChanType)
	sc.sysRecvChans.AddRecver(r.SysID(UnSubId), pubSubChanType)

	sc.sysSendChans.AddSender(r.SysID(RouterConnId), connChanType)
	sc.sysSendChans.AddSender(r.SysID(RouterDisconnId), connChanType)
	sc.sysSendChans.AddSender(r.SysID(ConnErrorId), connChanType)
	sc.sysSendChans.AddSender(r.SysID(ConnReadyId), connChanType)
	sc.sysSendChans.AddSender(r.SysID(PubId), pubSubChanType)
	sc.sysSendChans.AddSender(r.SysID(UnPubId), pubSubChanType)
	sc.sysSendChans.AddSender(r.SysID(SubId), pubSubChanType)
	sc.sysSendChans.AddSender(r.SysID(UnSubId), pubSubChanType)
	return sc
}
