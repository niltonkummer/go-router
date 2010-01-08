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

type Proxy interface {
	Connect(Proxy) os.Error
	ConnectRemote(io.ReadWriteCloser, MarshallingPolicy) os.Error
	Start()
	Close()
}

type peerCommand int

const (
	Start peerCommand = iota
	Pause
	Close
)

//common interface for all connection peers (Proxy, Stream) which should embed this
type peerBase struct {
	//imported chans from peer to allow peer send msgs to us
	importConnChan    chan GenericMsg
	importPubSubChan  chan GenericMsg
	importAppDataChan chan GenericMsg
	//exported chans for sending msgs to peer
	exportConnChan    chan GenericMsg
	exportPubSubChan  chan GenericMsg
	exportAppDataChan chan GenericMsg
}

type proxyImpl struct {
	peerBase
	myCmdChan chan peerCommand
	//my/local router
	router *routerImpl
	//chans connecting to local router
	sysChans     *sysChans
	appSendChans *SendChanBundle //send to local router
	appRecvChans *RecvChanBundle //recv from local router
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

func NewProxy(r Router /*, name string, f Filter, t Translator*/) Proxy {
	p := new(proxyImpl)
	p.router = r.(*routerImpl)
	//create import chans
	p.myCmdChan = make(chan peerCommand, DefCmdChanBufSize)
	p.exportConnChan = make(chan GenericMsg, p.router.defChanBufSize)
	p.exportPubSubChan = make(chan GenericMsg, p.router.defChanBufSize)
	p.exportAppDataChan = make(chan GenericMsg, p.router.defChanBufSize)
	//chans to local router
	p.sysChans = newSysChans(p)
	p.appSendChans = NewSendChanBundle(p.router, ScopeLocal, MemberRemote)
	p.appRecvChans = NewRecvChanBundle(p.router, ScopeLocal, MemberRemote, p.exportAppDataChan)
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
	p.Logger.Init(p.router, ln, DefLogBufSize, NumScope)
	p.FaultRaiser.Init(p.router, ln, DefCmdChanBufSize)
	return p
}

func (p1 *proxyImpl) Connect(pp Proxy) os.Error {
	p2 := pp.(*proxyImpl)
	p1.importConnChan = p2.exportConnChan
	p1.importPubSubChan = p2.exportPubSubChan
	p1.importAppDataChan = p2.exportAppDataChan
	p2.importConnChan = p1.exportConnChan
	p2.importPubSubChan = p1.exportPubSubChan
	p2.importAppDataChan = p1.exportAppDataChan
	p1.errChan = make(chan os.Error)
	p1.Start()
	p2.Start()
	return <-p1.errChan
}

func (p *proxyImpl) ConnectRemote(rwc io.ReadWriteCloser, mar MarshallingPolicy) os.Error {
	s := NewStream(rwc, mar, p)
	p.importConnChan = s.exportConnChan
	p.importPubSubChan = s.exportPubSubChan
	p.importAppDataChan = s.exportAppDataChan
	s.importConnChan = p.exportConnChan
	s.importPubSubChan = p.exportPubSubChan
	s.importAppDataChan = p.exportAppDataChan
	p.errChan = make(chan os.Error)
	p.Start()
	s.Start()
	return <-p.errChan
}

func (p *proxyImpl) Start() { go p.ctrlMainLoop() }

func (p *proxyImpl) Close() {
	p.Log(LOG_INFO, "proxy.Close() is called")
	p.myCmdChan <- Close
}

func (p *proxyImpl) closeImpl() {
	if !p.Closed {
		p.Log(LOG_INFO, "proxy closing")
		p.Closed = true
		p.router.delProxy(p)
		//notify dataMainLoop() to exit
		close(p.importAppDataChan)
		//close my chans
		p.sysChans.Close()
		p.appRecvChans.Close()
		p.appSendChans.Close()
		//close logger
		p.CloseFaultRaiser()
		p.CloseLogger()
	}
	p.Log(LOG_INFO, "proxy closed")
}

func (p *proxyImpl) connSetup() os.Error {
	r := p.router
	//1. to initiate conn setup handshaking, send my conn info to peer
	p.exportConnChan <- GenericMsg{Id: r.SysID(RouterConnId), Data: ConnInfoMsg{SeedId: p.router.seedId}}
	//2. recv connInfo from peer
	switch m := <-p.importConnChan; {
	case m.Id.Match(r.SysID(RouterConnId)):
		//save peer conninfo & forward it
		ci := m.Data.(ConnInfoMsg)
		p.sysChans.SendConnInfo(RouterConnId, ci)
		//check type info
		if reflect.Typeof(ci.SeedId) != reflect.Typeof(r.seedId) {
			err := os.ErrorString(errRmtIdTypeMismatch)
			ci.Error = err
			//tell local listeners about the fail
			p.sysChans.SendConnInfo(ConnErrorId, ci)
			//tell peer about fail
			p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ConnInfoMsg{Error: err}}
			p.LogError(err)
			return err
		}
	default:
		err := os.ErrorString(errConnInvalidMsg)
		//tell peer about fail
		p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ConnInfoMsg{Error: err}}
		p.LogError(err)
		return err
	}
	//3. send initial pub/sub info to peer
	p.exportPubSubChan <- GenericMsg{Id: r.SysID(PubId), Data: p.initPubInfoMsg()}
	p.exportPubSubChan <- GenericMsg{Id: r.SysID(SubId), Data: p.initSubInfoMsg()}
	//4. handle init_pub/sub msgs, send connReady to peer and wait for peer's connReady
	peerReady := false
	myReady := false
	for !(peerReady && myReady) {
		select {
		case m := <-p.importConnChan:
			switch {
			case m.Id.Match(r.SysID(ConnReadyId)):
				p.sysChans.SendConnInfo(ConnReadyId, m.Data.(ConnInfoMsg))
				peerReady = true
			case m.Id.Match(r.SysID(ConnErrorId)):
				ci := m.Data.(ConnInfoMsg)
				p.sysChans.SendConnInfo(ConnErrorId, ci)
				p.LogError(ci.Error)
				return ci.Error
			default:
				err := os.ErrorString(errConnInvalidMsg)
				//tell peer about fail
				p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ConnInfoMsg{Error: err}}
				p.LogError(err)
				return err //how to inform the failure?
			}
		case m := <-p.importPubSubChan:
			switch {
			case m.Id.Match(r.SysID(PubId)):
				_, err := p.handlePeerPubMsg(m)
				p.sysChans.SendPubSubInfo(PubId, m.Data.(IdChanInfoMsg))
				if err != nil {
					//tell peer about fail
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ConnInfoMsg{Error: err}}
					p.LogError(err)
					return err
				}
			case m.Id.Match(r.SysID(SubId)):
				_, err := p.handlePeerSubMsg(m)
				p.sysChans.SendPubSubInfo(SubId, m.Data.(IdChanInfoMsg))
				if err != nil {
					//tell peer about fail
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ConnInfoMsg{Error: err}}
					p.LogError(err)
					return err
				}
				myReady = true
				//tell peer i am ready
				p.exportConnChan <- GenericMsg{Id: r.SysID(ConnReadyId), Data: ConnInfoMsg{}}
			default:
				err := os.ErrorString(errConnInvalidMsg)
				//tell peer about fail
				p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ConnInfoMsg{Error: err}}
				p.LogError(err)
				return err
			}
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
	go p.dataMainLoop()
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
		case cmd := <-p.myCmdChan:
			if cmd == Close {
				//tell peer exiting
				p.Log(LOG_INFO, "proxy shutdown, send peer Disconn msg")
				p.exportConnChan <- GenericMsg{Id: r.SysID(RouterDisconnId), Data: ConnInfoMsg{}}
				cont = false
			}
		case m := <-p.sysChans.pubSubInfo:
			switch {
			case m.Id.Match(r.SysID(PubId)):
				_, err := p.handleLocalPubMsg(m)
				if err != nil {
					//tell peer about fail
					ci := ConnInfoMsg{Error: err}
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ci}
					p.sysChans.SendConnInfo(ConnErrorId, ci)
					p.LogError(err)
					cont = false
				}
			case m.Id.Match(r.SysID(UnPubId)):
				_, err := p.handleLocalUnPubMsg(m)
				if err != nil {
					//tell peer about fail
					ci := ConnInfoMsg{Error: err}
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ci}
					p.sysChans.SendConnInfo(ConnErrorId, ci)
					p.LogError(err)
					cont = false
				}
			case m.Id.Match(r.SysID(SubId)):
				_, err := p.handleLocalSubMsg(m)
				if err != nil {
					//tell peer about fail
					ci := ConnInfoMsg{Error: err}
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ci}
					p.sysChans.SendConnInfo(ConnErrorId, ci)
					p.LogError(err)
					cont = false
				}
			case m.Id.Match(r.SysID(UnSubId)):
				_, err := p.handleLocalUnSubMsg(m)
				if err != nil {
					//tell peer about fail
					ci := ConnInfoMsg{Error: err}
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ci}
					p.sysChans.SendConnInfo(ConnErrorId, ci)
					p.LogError(err)
					cont = false
				}
			}
		case m := <-p.importConnChan:
			switch {
			case m.Id.Match(r.SysID(RouterDisconnId)):
				p.sysChans.SendConnInfo(RouterDisconnId, m.Data.(ConnInfoMsg))
				p.Log(LOG_INFO, "recv Disconn msg, proxy shutdown")
				cont = false
			case m.Id.Match(r.SysID(ConnErrorId)):
				ci := m.Data.(ConnInfoMsg)
				p.sysChans.SendConnInfo(ConnErrorId, ci)
				p.LogError(ci.Error)
				cont = false
			}
		case m := <-p.importPubSubChan:
			switch {
			case m.Id.Match(r.SysID(PubId)):
				_, err := p.handlePeerPubMsg(m)
				p.sysChans.SendPubSubInfo(PubId, m.Data.(IdChanInfoMsg))
				if err != nil {
					//tell peer about fail
					ci := ConnInfoMsg{Error: err}
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ci}
					p.sysChans.SendConnInfo(ConnErrorId, ci)
					p.LogError(err)
					cont = false
				}
			case m.Id.Match(r.SysID(UnPubId)):
				_, err := p.handlePeerUnPubMsg(m)
				p.sysChans.SendPubSubInfo(UnPubId, m.Data.(IdChanInfoMsg))
				if err != nil {
					//tell peer about fail
					ci := ConnInfoMsg{Error: err}
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ci}
					p.sysChans.SendConnInfo(ConnErrorId, ci)
					p.LogError(err)
					cont = false
				}
			case m.Id.Match(r.SysID(SubId)):
				_, err := p.handlePeerSubMsg(m)
				p.sysChans.SendPubSubInfo(SubId, m.Data.(IdChanInfoMsg))
				if err != nil {
					//tell peer about fail
					ci := ConnInfoMsg{Error: err}
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ci}
					p.sysChans.SendConnInfo(ConnErrorId, ci)
					p.LogError(err)
					cont = false
				}
			case m.Id.Match(r.SysID(UnSubId)):
				_, err := p.handlePeerUnSubMsg(m)
				p.sysChans.SendPubSubInfo(UnSubId, m.Data.(IdChanInfoMsg))
				if err != nil {
					//tell peer about fail
					ci := ConnInfoMsg{Error: err}
					p.exportConnChan <- GenericMsg{Id: r.SysID(ConnErrorId), Data: ci}
					p.sysChans.SendConnInfo(ConnErrorId, ci)
					p.LogError(err)
					cont = false
				}
			}
		}
		p.Log(LOG_INFO, "proxy finish one sys ctrl msg")
	}
	p.closeImpl()
}

func (p *proxyImpl) dataMainLoop() {
	p.Log(LOG_INFO, "-- dataMainLoop start")
	for {
		p.Log(LOG_INFO, "proxy wait for another app msg")
		m := <-p.importAppDataChan //put app data chan at very last
		if closed(p.importAppDataChan) {
			p.Log(LOG_INFO, "proxy importAppDataChan closed")
			break
		}
		p.Log(LOG_INFO, "proxy dataMainLoop recv/forward app msg")
		err := p.appSendChans.Send(m.Id, m.Data)
		if err != nil {
			p.LogError(err)
		}
		p.Log(LOG_INFO, "proxy finish one app msg")
	}
	p.Log(LOG_INFO, "-- dataMainLoop exit")
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
			info1.ElemType = new(ChanElemTypeData)
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
			info2.ElemType = new(ChanElemTypeData)
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
func (p *proxyImpl) handleLocalSubMsg(m GenericMsg) (num int, err os.Error) {
	msg := m.Data.(IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	flags := make([]bool, len(sInfo))
	for i, sub := range sInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalSubMsg: %v", sub.Id))
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
					p.Raise(ChanTypeMismatch, err)
				}
			}
		}
		num++
	}
	if num > 0 {
		if num == len(sInfo) {
			p.exportPubSubChan <- m
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
			p.exportPubSubChan <- GenericMsg{Id: m.Id, Data: IdChanInfoMsg{Info: sInfo2}}
		}
	}
	return
}

func (p *proxyImpl) handleLocalUnSubMsg(m GenericMsg) (num int, err os.Error) {
	msg := m.Data.(IdChanInfoMsg)
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
		if num == len(sInfo) {
			p.exportPubSubChan <- m
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
			p.exportPubSubChan <- GenericMsg{Id: m.Id, Data: IdChanInfoMsg{Info: sInfo2}}
		}
	}
	return
}

func (p *proxyImpl) handleLocalPubMsg(m GenericMsg) (num int, err os.Error) {
	msg := m.Data.(IdChanInfoMsg)
	pInfo := msg.Info
	if len(pInfo) == 0 {
		return
	}
	flags := make([]bool, len(pInfo))
	for i, pub := range pInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalPubMsg: %v", pub.Id))
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
		if num == len(pInfo) {
			p.exportPubSubChan <- m
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
			p.exportPubSubChan <- GenericMsg{Id: m.Id, Data: pInfo2}
		}
		/* !!! dont do this now, wait for peerSub to set it up
		//need to send out Pub info to peer first before the following AddRecver, so that at peer
		//the Send forward bindings is set up before data comes
		for i, pub := range pInfo {
			if !flags[i] {
				for _, sub := range p.importRecvIds {
					if pub.Id.Match(sub.Id) {
						p.appRecvChans.AddRecver(sub.Id, sub.ChanType)
					}
				}
			}
		}
		*/
	}
	return
}

func (p *proxyImpl) handleLocalUnPubMsg(m GenericMsg) (num int, err os.Error) {
	msg := m.Data.(IdChanInfoMsg)
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
		if num == len(pInfo) {
			p.exportPubSubChan <- m
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
			p.exportPubSubChan <- GenericMsg{Id: m.Id, Data: pInfo2}
		}
	}
	return
}

func (p *proxyImpl) handlePeerSubMsg(m GenericMsg) (num int, err os.Error) {
	msg := m.Data.(IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	pInfo := make([]*IdChanInfo, len(sInfo))
	for _, sub := range sInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerSubMsg: %v", sub.Id))
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
					pInfo[num] = pub
					num++
				}

			}
		}
	}
	if num > 0 {
		p.exportPubSubChan <- GenericMsg{Id: p.router.SysID(PubId), Data: IdChanInfoMsg{Info: pInfo[0:num]}}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerSubMsg, return PubMsg for : %v", pInfo[0].Id))
	}
	return
}

func (p *proxyImpl) handlePeerUnSubMsg(m GenericMsg) (num int, err os.Error) {
	msg := m.Data.(IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	for _, sub := range sInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerUnSubMsg: %v", sub.Id))
		//update import cache
		p.importRecvIds[sub.Id.Key()] = sub, false
		//check if local already pubed it
		p.appRecvChans.DelRecver(sub.Id)
		num++
	}
	return
}

func (p *proxyImpl) handlePeerPubMsg(m GenericMsg) (num int, err os.Error) {
	msg := m.Data.(IdChanInfoMsg)
	pInfo := msg.Info
	if len(pInfo) == 0 {
		return
	}
	sInfo := make([]*IdChanInfo, len(pInfo))
	for _, pub := range pInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerPubMsg: %v", pub.Id))
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
				sInfo[num] = sub
				//set special scope to mark sender is ready
				sub.Id, _ = sub.Id.Clone(NumScope, MemberLocal)
				num++
				p.appSendChans.AddSender(pub.Id, pub.ChanType)
			}
		}
	}
	//return a SubMsg to peer so that a recver can be added at peer side
	if num > 0 {
		p.exportPubSubChan <- GenericMsg{Id: p.router.SysID(SubId), Data: IdChanInfoMsg{Info: sInfo[0:num]}}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerPubMsg, return SubMsg for : %v", sInfo[0].Id))
	}
	return
}

func (p *proxyImpl) handlePeerUnPubMsg(m GenericMsg) (num int, err os.Error) {
	msg := m.Data.(IdChanInfoMsg)
	pInfo := msg.Info
	if len(pInfo) == 0 {
		return
	}
	for _, pub := range pInfo {
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

func (p *proxyImpl) initSubInfoMsg() IdChanInfoMsg {
	num := len(p.exportRecvIds)
	info := make([]*IdChanInfo, num)
	idx := 0
	for _, v := range p.exportRecvIds {
		info[idx] = new(IdChanInfo)
		info[idx].Id = v.Id
		info[idx].ChanType = v.ChanType
		idx++
	}
	return IdChanInfoMsg{Info: info}
}

func (p *proxyImpl) initPubInfoMsg() IdChanInfoMsg {
	num := len(p.exportSendIds)
	info := make([]*IdChanInfo, num)
	idx := 0
	for _, v := range p.exportSendIds {
		info[idx] = new(IdChanInfo)
		info[idx].Id = v.Id
		info[idx].ChanType = v.ChanType
		idx++
	}
	return IdChanInfoMsg{Info: info}
}

//utils:
//sysChans: wrapper of sys chans
type sysChans struct {
	proxy *proxyImpl
	//chans to subscribe to local router name-space changes
	nsRecvChans [4]chan IdChanInfoMsg
	//pubsubInfo chan will aggregate msgs from all the above chans to forward to proxy mainloop
	pubSubInfo chan GenericMsg
	//chans to publish conn related events to local router
	//at most we can only have 4 of them: conn/disconn/ready/connError
	connSendChans   [4]chan ConnInfoMsg
	connBindChans   [4]chan BindEvent
	connSendEnabled [4]bool
	//chans to publish remote pub/sub events to local router
	pubSubSendChans [4]chan IdChanInfoMsg
	psBindChans     [4]chan BindEvent
	psSendEnabled   [4]bool
	//
	sysIds  [NumSysIds]Id
	done    chan bool
	started bool
}

func (sc *sysChans) Close() {
	sc.done <- true
	r := sc.proxy.router
	r.DetachChan(sc.sysIds[PubId], sc.nsRecvChans[0])
	r.DetachChan(sc.sysIds[UnPubId], sc.nsRecvChans[1])
	r.DetachChan(sc.sysIds[SubId], sc.nsRecvChans[2])
	r.DetachChan(sc.sysIds[UnSubId], sc.nsRecvChans[3])
	r.DetachChan(sc.sysIds[RouterConnId], sc.connSendChans[0])
	r.DetachChan(sc.sysIds[RouterDisconnId], sc.connSendChans[1])
	r.DetachChan(sc.sysIds[ConnErrorId], sc.connSendChans[2])
	r.DetachChan(sc.sysIds[ConnReadyId], sc.connSendChans[3])
	r.DetachChan(sc.sysIds[PubId], sc.pubSubSendChans[0])
	r.DetachChan(sc.sysIds[UnPubId], sc.pubSubSendChans[1])
	r.DetachChan(sc.sysIds[SubId], sc.pubSubSendChans[2])
	r.DetachChan(sc.sysIds[UnSubId], sc.pubSubSendChans[3])
	sc.proxy.Log(LOG_INFO, "proxy sysChan closed")
}

func (sc *sysChans) SendConnInfo(idx int, data ConnInfoMsg) {
	if idx < RouterConnId || idx > ConnReadyId {
		sc.proxy.Log(LOG_ERROR, "proxy: Invalid sys id passed to SendConnInfo")
	} else {
		if len(sc.connBindChans[idx]) > 0 {
			for {
				v, ok := <-sc.connBindChans[idx]
				if !ok {
					break
				}
				if v.Count > 0 {
					sc.connSendEnabled[idx] = true
				} else {
					sc.connSendEnabled[idx] = false
				}
			}
		}
		if sc.connSendEnabled[idx] {
			sc.connSendChans[idx] <- data
		}
	}
}

func (sc *sysChans) SendPubSubInfo(idx int, data IdChanInfoMsg) {
	if idx < PubId || idx > UnSubId {
		sc.proxy.Log(LOG_ERROR, "proxy: Invalid sys id passed to SendPubSubInfo")
	} else {
		if len(sc.psBindChans[idx-PubId]) > 0 {
			for {
				v, ok := <-sc.psBindChans[idx-PubId]
				if !ok {
					break
				}
				if v.Count > 0 {
					sc.psSendEnabled[idx-PubId] = true
				} else {
					sc.psSendEnabled[idx-PubId] = false
				}
			}
		}
		if sc.psSendEnabled[idx-PubId] {
			//filter out sys internal ids
			info := make([]*IdChanInfo, len(data.Info))
			num := 0
			for i := 0; i < len(data.Info); i++ {
				if sc.proxy.router.getSysInternalIdIdx(data.Info[i].Id) < 0 {
					info[num] = data.Info[i]
					num++
				}
			}
			sc.pubSubSendChans[idx-PubId] <- IdChanInfoMsg{info[0:num]}
			//sc.pubSubSendChans[idx-PubId] <- data
		}
	}
}

func (sc *sysChans) mainLoop() {
	sc.proxy.Log(LOG_INFO, "sysChan recver mainloop started")
	r := sc.proxy.router
	cont := true
	for cont {
		select {
		case <-sc.done:
			cont = false
		case v := <-sc.nsRecvChans[0]:
			if !closed(sc.nsRecvChans[0]) {
				sc.proxy.Log(LOG_INFO, "forward PubId msg")
				sc.pubSubInfo <- GenericMsg{Id: r.SysID(PubId), Data: v}
			} else {
				cont = false
			}
		case v := <-sc.nsRecvChans[1]:
			if !closed(sc.nsRecvChans[1]) {
				sc.proxy.Log(LOG_INFO, "forward UnPubId msg")
				sc.pubSubInfo <- GenericMsg{Id: r.SysID(UnPubId), Data: v}
			} else {
				cont = false
			}
		case v := <-sc.nsRecvChans[2]:
			if !closed(sc.nsRecvChans[2]) {
				sc.proxy.Log(LOG_INFO, "forward SubId msg")
				sc.pubSubInfo <- GenericMsg{Id: r.SysID(SubId), Data: v}
			} else {
				cont = false
			}
		case v := <-sc.nsRecvChans[3]:
			if !closed(sc.nsRecvChans[3]) {
				sc.proxy.Log(LOG_INFO, "forward UnSubId msg")
				sc.pubSubInfo <- GenericMsg{Id: r.SysID(UnSubId), Data: v}
			} else {
				cont = false
			}
		}
	}
}

func (sc *sysChans) Start() {
	if !sc.started {
		sc.started = true
		go sc.mainLoop()
	}
}

func newSysChans(p *proxyImpl) *sysChans {
	sc := new(sysChans)
	sc.proxy = p
	r := sc.proxy.router
	sc.pubSubInfo = make(chan GenericMsg, r.defChanBufSize)
	for i := 0; i < 4; i++ {
		sc.nsRecvChans[i] = make(chan IdChanInfoMsg, r.defChanBufSize)
		sc.connSendChans[i] = make(chan ConnInfoMsg, 4) //at most 1 msg for each of 4 conn msg types
		sc.connBindChans[i] = make(chan BindEvent, 1)
		sc.pubSubSendChans[i] = make(chan IdChanInfoMsg, r.defChanBufSize)
		sc.psBindChans[i] = make(chan BindEvent, 1)
	}
	sc.done = make(chan bool)
	sc.sysIds[RouterConnId] = r.NewSysID(RouterConnId, ScopeLocal, MemberRemote)
	sc.sysIds[RouterDisconnId] = r.NewSysID(RouterDisconnId, ScopeLocal, MemberRemote)
	sc.sysIds[ConnErrorId] = r.NewSysID(ConnErrorId, ScopeLocal, MemberRemote)
	sc.sysIds[ConnReadyId] = r.NewSysID(ConnReadyId, ScopeLocal, MemberRemote)
	sc.sysIds[PubId] = r.NewSysID(PubId, ScopeLocal, MemberRemote)
	sc.sysIds[UnPubId] = r.NewSysID(UnPubId, ScopeLocal, MemberRemote)
	sc.sysIds[SubId] = r.NewSysID(SubId, ScopeLocal, MemberRemote)
	sc.sysIds[UnSubId] = r.NewSysID(UnSubId, ScopeLocal, MemberRemote)
	r.AttachRecvChan(sc.sysIds[PubId], sc.nsRecvChans[0])
	r.AttachRecvChan(sc.sysIds[UnPubId], sc.nsRecvChans[1])
	r.AttachRecvChan(sc.sysIds[SubId], sc.nsRecvChans[2])
	r.AttachRecvChan(sc.sysIds[UnSubId], sc.nsRecvChans[3])
	r.AttachSendChan(sc.sysIds[RouterConnId], sc.connSendChans[0], sc.connBindChans[0])
	r.AttachSendChan(sc.sysIds[RouterDisconnId], sc.connSendChans[1], sc.connBindChans[1])
	r.AttachSendChan(sc.sysIds[ConnErrorId], sc.connSendChans[2], sc.connBindChans[2])
	r.AttachSendChan(sc.sysIds[ConnReadyId], sc.connSendChans[3], sc.connBindChans[3])
	r.AttachSendChan(sc.sysIds[PubId], sc.pubSubSendChans[0], sc.psBindChans[0])
	r.AttachSendChan(sc.sysIds[UnPubId], sc.pubSubSendChans[1], sc.psBindChans[1])
	r.AttachSendChan(sc.sysIds[SubId], sc.pubSubSendChans[2], sc.psBindChans[2])
	r.AttachSendChan(sc.sysIds[UnSubId], sc.pubSubSendChans[3], sc.psBindChans[3])
	return sc
}
