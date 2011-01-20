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
	ConnectRemote(io.ReadWriteCloser, MarshalingPolicy, ...bool) os.Error
	Close()
	//query messaging interface with peer
	LocalPubInfo() []*IdChanInfo
	LocalSubInfo() []*IdChanInfo
	PeerPubInfo() []*IdChanInfo
	PeerSubInfo() []*IdChanInfo
}

/*
 Peers are to be connected thru forwarding channels
 Proxy and Stream are peers
*/
type peerIntf interface {
	appMsgChanForId(Id) (Channel, int)
	sendCtrlMsg(*genericMsg) os.Error
}

type proxyImpl struct {
	//chan for ctrl msgs from peers
	ctrlChan chan *genericMsg
	//use peerIntf to forward app/ctrl msgs to peer (proxy or stream)
	peer           peerIntf
	streamPeer     bool //is peer a stream
	flowControlled bool
	//my/local router
	router *routerImpl
	//chans connecting to local router on behalf of peer
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
	//mutex to protect cache
	cacheLock sync.Mutex
	//for log/debug
	name string
	Logger
	FaultRaiser
	//others
	connReady bool
	Closed    bool
	errChan   chan os.Error
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
	p.appSendChans = newSendChanBundle(p, ScopeLocal, MemberRemote)
	p.appRecvChans = newRecvChanBundle(p, ScopeLocal, MemberRemote)
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

func (p *proxyImpl) ConnectRemote(rwc io.ReadWriteCloser, mar MarshalingPolicy, flags ...bool) os.Error {
	s := newStream(rwc, mar, p)
	s.peer = p
	p.peer = s
	if len(flags) > 0 {
		p.flowControlled = flags[0]
	}
	p.streamPeer = true
	p.errChan = make(chan os.Error)
	p.start()
	s.start()
	return <-p.errChan
}

func (p *proxyImpl) start() { go p.connInitLoop() }

func (p *proxyImpl) Close() {
	p.Log(LOG_INFO, "proxy.Close() is called")
	//close(p.ctrlChan)
	p.peer.sendCtrlMsg(&genericMsg{p.router.SysID(DisconnId), &ConnInfoMsg{}})
	p.shutdown()
}

func (p *proxyImpl) shutdown() {
	//close sysChans pubSubInfo chan so that connInitLoop
	//will exit and call closeImpl()
	p.sysChans.Shutdown()
}

func (p *proxyImpl) closeImpl() {
	pubInfo := p.PeerPubInfo()
	subInfo := p.PeerSubInfo()
	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()
	if !p.Closed {
		p.Closed = true
		//notify local chan that remote peer is leaving (unpub, unsub)
		p.Log(LOG_INFO, fmt.Sprintf("unpub [%d], unsub [%d]", len(pubInfo), len(subInfo)))
		p.sysChans.SendPubSubInfo(UnPubId, &IdChanInfoMsg{pubInfo})
		p.sysChans.SendPubSubInfo(UnSubId, &IdChanInfoMsg{subInfo})
		//start closing
		p.Log(LOG_INFO, "proxy closing")
		p.sysChans.Close()
		//p.Closed = true
		p.router.delProxy(p)
		//close my chans
		p.appRecvChans.Close()
		p.appSendChans.Close()
		//close logger
		p.FaultRaiser.Close()
		p.Logger.Close()
	}
	p.Log(LOG_INFO, "proxy closed")
}

const (
	raw = iota
	async
	flowControlled
)

func (p *proxyImpl) connType() int {
	switch {
	case p.router.async:
		return async
	case p.flowControlled:
		return flowControlled
	}
	return raw
}

func (p *proxyImpl) connSetup() os.Error {
	r := p.router
	//1. to initiate conn setup handshaking, send my conn info to peer
	p.peer.sendCtrlMsg(&genericMsg{r.SysID(ConnId), &ConnInfoMsg{Id: r.seedId, Type: p.connType()}})
	//2. recv connInfo from peer
	switch m := <-p.ctrlChan; m.Id.SysIdIndex() {
	case ConnId:
		//save peer conninfo & forward it
		ci := m.Data.(*ConnInfoMsg)
		p.sysChans.SendConnInfo(ConnId, ci)
		//check type info
		if reflect.Typeof(ci.Id) != reflect.Typeof(r.seedId) || ci.Type != p.connType() {
			err := os.ErrorString(errRouterTypeMismatch)
			ci.Error = err
			//tell local listeners about the fail
			p.sysChans.SendConnInfo(ErrorId, ci)
			//tell peer about fail
			p.peer.sendCtrlMsg(&genericMsg{r.SysID(ErrorId), &ConnInfoMsg{Error: err}})
			p.LogError(err)
			return err
		}
	default:
		err := os.ErrorString(errConnInvalidMsg)
		//tell peer about fail
		p.peer.sendCtrlMsg(&genericMsg{r.SysID(ErrorId), &ConnInfoMsg{Error: err}})
		p.LogError(err)
		return err
	}
	//3. send initial pub/sub info to peer
	p.peer.sendCtrlMsg(&genericMsg{r.SysID(PubId), p.initPubInfoMsg()})
	p.peer.sendCtrlMsg(&genericMsg{r.SysID(SubId), p.initSubInfoMsg()})
	//4. handle init_pub/sub msgs, send connReady to peer and wait for peer's connReady
	peerReady := false
	myReady := false
	for !(peerReady && myReady) {
		switch m := <-p.ctrlChan; m.Id.SysIdIndex() {
		case ErrorId:
			ci := m.Data.(*ConnInfoMsg)
			p.sysChans.SendConnInfo(ErrorId, ci)
			p.LogError(ci.Error)
			return ci.Error
		case ReadyId:
			crm := m.Data.(*ConnReadyMsg)
			if crm.Info != nil {
				_, err := p.handlePeerReadyMsg(m)
				p.sysChans.SendReadyInfo(ReadyId, m.Data.(*ConnReadyMsg))
				if err != nil {
					//tell peer about fail
					p.peer.sendCtrlMsg(&genericMsg{r.SysID(ErrorId), &ConnInfoMsg{Error: err}})
					p.LogError(err)
					return err
				}
			} else {
				peerReady = true
			}
		case PubId:
			_, err := p.handlePeerPubMsg(m)
			p.sysChans.SendPubSubInfo(PubId, m.Data.(*IdChanInfoMsg))
			if err != nil {
				//tell peer about fail
				p.peer.sendCtrlMsg(&genericMsg{r.SysID(ErrorId), &ConnInfoMsg{Error: err}})
				p.LogError(err)
				return err
			}
		case SubId:
			_, err := p.handlePeerSubMsg(m)
			p.sysChans.SendPubSubInfo(SubId, m.Data.(*IdChanInfoMsg))
			if err != nil {
				//tell peer about fail
				p.peer.sendCtrlMsg(&genericMsg{r.SysID(ErrorId), &ConnInfoMsg{Error: err}})
				p.LogError(err)
				return err
			}
			myReady = true
			//tell peer i am ready by sending an empty ConnReadyMsg
			p.peer.sendCtrlMsg(&genericMsg{r.SysID(ReadyId), &ConnReadyMsg{}})
		default:
			err := os.ErrorString(errConnInvalidMsg)
			//tell peer about fail
			p.peer.sendCtrlMsg(&genericMsg{r.SysID(ErrorId), &ConnInfoMsg{Error: err}})
			p.LogError(err)
			return err
		}
	}
	return nil
}

func (p *proxyImpl) connInitLoop() {
	//query router main goroutine to retrieve exported ids
	p.exportSendIds = p.router.IdsForSend(ExportedId)
	p.exportRecvIds = p.router.IdsForRecv(ExportedId)

	//start conn handshaking
	err := p.connSetup()
	if p.errChan != nil {
		p.errChan <- err
	}
	if err != nil {
		p.Log(LOG_INFO, "-- connection error")
		p.closeImpl()
		return
	}

	p.Log(LOG_INFO, "-- connection ready")

	p.cacheLock.Lock()
	p.connReady = true
	p.cacheLock.Unlock()

	//drain the remaing peer ctrl msgs
	close(p.ctrlChan)
	for m := range p.ctrlChan {
		p.handlePeerCtrlMsg(m)
	}

	//turn into loop handling only local ctrl msgs
	cont := true
	for cont {
		m := <-p.sysChans.pubSubInfo
		if closed(p.sysChans.pubSubInfo) {
			cont = false
			break
		}
		if err = p.handleLocalCtrlMsg(m); err != nil {
			cont = false
		}
	}
	p.closeImpl()
}

func (p *proxyImpl) handleLocalCtrlMsg(m *genericMsg) (err os.Error) {
	p.Log(LOG_INFO, "enter handleLocalCtrlMsg")
	switch m.Id.SysIdIndex() {
	case PubId:
		_, err = p.handleLocalPubMsg(m)
	case UnPubId:
		_, err = p.handleLocalUnPubMsg(m)
	case SubId:
		_, err = p.handleLocalSubMsg(m)
	case UnSubId:
		_, err = p.handleLocalUnSubMsg(m)
	}
	if err != nil {
		//tell peer about fail
		ci := &ConnInfoMsg{Error: err}
		p.peer.sendCtrlMsg(&genericMsg{p.router.SysID(ErrorId), ci})
		p.sysChans.SendConnInfo(ErrorId, ci)
		p.LogError(err)
	}
	p.Log(LOG_INFO, "exit handleLocalCtrlMsg")
	return
}

func (p *proxyImpl) handlePeerCtrlMsg(m *genericMsg) (err os.Error) {
	switch m.Id.SysIdIndex() {
	case DisconnId:
		p.sysChans.SendConnInfo(DisconnId, m.Data.(*ConnInfoMsg))
		p.Log(LOG_INFO, "recv Disconn msg, proxy shutdown")
		p.shutdown()
		return
	case ErrorId:
		ci := m.Data.(*ConnInfoMsg)
		p.sysChans.SendConnInfo(ErrorId, ci)
		p.LogError(ci.Error)
		p.shutdown()
		return
	case ReadyId:
		_, err = p.handlePeerReadyMsg(m)
		p.sysChans.SendReadyInfo(ReadyId, m.Data.(*ConnReadyMsg))
	case PubId:
		_, err = p.handlePeerPubMsg(m)
		p.sysChans.SendPubSubInfo(PubId, m.Data.(*IdChanInfoMsg))
	case UnPubId:
		_, err = p.handlePeerUnPubMsg(m)
		p.sysChans.SendPubSubInfo(UnPubId, m.Data.(*IdChanInfoMsg))
	case SubId:
		_, err = p.handlePeerSubMsg(m)
		p.sysChans.SendPubSubInfo(SubId, m.Data.(*IdChanInfoMsg))
	case UnSubId:
		_, err = p.handlePeerUnSubMsg(m)
		p.sysChans.SendPubSubInfo(UnSubId, m.Data.(*IdChanInfoMsg))
	}
	if err != nil {
		//tell peer about fail
		ci := &ConnInfoMsg{Error: err}
		p.peer.sendCtrlMsg(&genericMsg{p.router.SysID(ErrorId), ci})
		p.sysChans.SendConnInfo(ErrorId, ci)
		p.LogError(err)
		p.shutdown()
	}
	return
}


//the following 2 functions are external interface exposed to peers
func (p *proxyImpl) sendCtrlMsg(m *genericMsg) (err os.Error) {
	p.cacheLock.Lock()
	if !p.connReady {
		p.cacheLock.Unlock()
		p.ctrlChan <- m
	} else {
		p.cacheLock.Unlock()
		err = p.handlePeerCtrlMsg(m)
	}
	return
}

func (p *proxyImpl) appMsgChanForId(id Id) (Channel, int) {
	id1 := id
	if p.translator != nil {
		id1 = p.translator.TranslateInward(id)
	}
	return p.appSendChans.findSender(id1)
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
	numReady := 0
	readyInfo := make([]*ChanReadyInfo, len(sInfo))
	sInfo2 := make([]*IdChanInfo, len(sInfo))
	p.cacheLock.Lock()
	for _, sub := range sInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalSubMsg: %v", sub.Id))
		if p.filter != nil && p.filter.BlockInward(sub.Id) {
			continue
		}
		_, ok := p.exportRecvIds[sub.Id.Key()]
		if ok {
			continue
		}
		//here we have a new global sub id
		//update exported id cache
		p.exportRecvIds[sub.Id.Key()] = sub
		sInfo2[num] = sub
		num++ //one more to be exported
		//check if peer already pubed it
		for _, pub := range p.importSendIds {
			if sub.Id.Match(pub.Id) {
				if p.chanTypeMatch(sub, pub) {
					readyInfo[numReady] = &ChanReadyInfo{pub.Id, p.router.recvChanBufSize(sub.Id)}
					numReady++
					p.Log(LOG_INFO, fmt.Sprintf("send ConnReady for: %v", pub.Id))
					p.appSendChans.AddSender(pub.Id, pub.ChanType)
				} else {
					err = os.ErrorString(errRmtChanTypeMismatch)
					p.sysChans.SendConnInfo(ErrorId, &ConnInfoMsg{Id: sub.Id, Error: err})
					p.LogError(err)
					p.cacheLock.Unlock()
					p.Raise(err)
				}
			}
		}
	}
	p.cacheLock.Unlock()
	//send subscriptions to peer
	if num > 0 {
		sInfo2 = sInfo2[0:num]
		if p.translator == nil {
			p.peer.sendCtrlMsg(&genericMsg{m.Id, &IdChanInfoMsg{Info: sInfo2}})
		} else {
			for i, sub := range sInfo2 {
				sInfo2[i] = new(IdChanInfo)
				sInfo2[i].Id = p.translator.TranslateOutward(sub.Id)
				sInfo2[i].ChanType = sub.ChanType
				sInfo2[i].ElemType = sub.ElemType
			}
			p.peer.sendCtrlMsg(&genericMsg{m.Id, &IdChanInfoMsg{Info: sInfo2}})
		}
	}
	//send connReadyInfo to peer
	if numReady > 0 {
		readyInfo = readyInfo[0:numReady]
		if p.translator == nil {
			p.peer.sendCtrlMsg(&genericMsg{p.router.SysID(ReadyId), &ConnReadyMsg{Info: readyInfo}})
		} else {
			for i, ready := range readyInfo {
				readyInfo[i].Id = p.translator.TranslateOutward(ready.Id)
			}
			p.peer.sendCtrlMsg(&genericMsg{p.router.SysID(ReadyId), &ConnReadyMsg{Info: readyInfo}})
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
	sInfo2 := make([]*IdChanInfo, len(sInfo))
	p.cacheLock.Lock()
	for _, sub := range sInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalUnSubMsg: %v", sub.Id))
		_, ok := p.exportRecvIds[sub.Id.Key()]
		if !ok {
			continue
		}
		if p.filter != nil && p.filter.BlockInward(sub.Id) {
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
			continue
		}

		//update exported id cache
		if delCache {
			p.exportRecvIds[sub.Id.Key()] = sub, false
			sInfo2[num] = sub
			num++
		}

		//check if peer already pubed it
		if delSender {
			pub, ok := p.importSendIds[sub.Id.Key()]
			if ok {
				p.appSendChans.DelSender(pub.Id)
			}
		}
	}
	p.cacheLock.Unlock()
	if num > 0 {
		sInfo2 = sInfo2[0:num]
		if p.translator == nil {
			p.peer.sendCtrlMsg(&genericMsg{m.Id, &IdChanInfoMsg{Info: sInfo2}})
		} else {
			for i, sub := range sInfo2 {
				sInfo2[i] = new(IdChanInfo)
				sInfo2[i].Id = p.translator.TranslateOutward(sub.Id)
				sInfo2[i].ChanType = sub.ChanType
				sInfo2[i].ElemType = sub.ElemType
			}
			p.peer.sendCtrlMsg(&genericMsg{m.Id, &IdChanInfoMsg{Info: sInfo2}})
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
	pInfo2 := make([]*IdChanInfo, len(pInfo))
	p.cacheLock.Lock()
	for _, pub := range pInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalPubMsg: %v", pub.Id))
		if p.filter != nil && p.filter.BlockOutward(pub.Id) {
			continue
		}
		_, ok := p.exportSendIds[pub.Id.Key()]
		if ok {
			continue
		}
		//update exported id cache
		p.exportSendIds[pub.Id.Key()] = pub
		pInfo2[num] = pub
		num++ //one more export
		//check if peer already subed it
		for _, sub := range p.importRecvIds {
			if pub.Id.Match(sub.Id) {
				if !p.chanTypeMatch(sub, pub) {
					err = os.ErrorString(errRmtChanTypeMismatch)
					p.sysChans.SendConnInfo(ErrorId, &ConnInfoMsg{Id: pub.Id, Error: err})
					p.LogError(err)
					p.cacheLock.Unlock()
					return
				}
			}
		}
	}
	p.cacheLock.Unlock()
	if num > 0 {
		pInfo2 = pInfo2[0:num]
		if p.translator == nil {
			p.peer.sendCtrlMsg(&genericMsg{m.Id, &IdChanInfoMsg{pInfo2}})
		} else {
			for cnt, pub := range pInfo2 {
				pInfo2[cnt] = new(IdChanInfo)
				pInfo2[cnt].Id = p.translator.TranslateOutward(pub.Id)
				pInfo2[cnt].ChanType = pub.ChanType
				pInfo2[cnt].ElemType = pub.ElemType
			}
			p.peer.sendCtrlMsg(&genericMsg{m.Id, &IdChanInfoMsg{pInfo2}})
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
	pInfo2 := make([]*IdChanInfo, len(pInfo))
	p.cacheLock.Lock()
	for _, pub := range pInfo {
		p.Log(LOG_INFO, fmt.Sprintf("handleLocalUnPubMsg: %v", pub.Id))
		_, ok := p.exportSendIds[pub.Id.Key()]
		if !ok {
			continue
		}
		if p.filter != nil && p.filter.BlockOutward(pub.Id) {
			continue
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
			continue
		}

		//update exported id cache
		if delCache {
			p.exportSendIds[pub.Id.Key()] = pub, false
			pInfo2[num] = pub
			num++
		}

		//check if peer already pubed it
		if delRecver {
			sub, ok := p.importRecvIds[pub.Id.Key()]
			if ok {
				p.appRecvChans.DelRecver(sub.Id)
			}
		}
	}
	p.cacheLock.Unlock()
	if num > 0 {
		pInfo2 = pInfo2[0:num]
		if p.translator == nil {
			p.peer.sendCtrlMsg(&genericMsg{m.Id, &IdChanInfoMsg{pInfo2}})
		} else {
			for cnt, pub := range pInfo2 {
				pInfo2[cnt] = new(IdChanInfo)
				pInfo2[cnt].Id = p.translator.TranslateOutward(pub.Id)
				pInfo2[cnt].ChanType = pub.ChanType
				pInfo2[cnt].ElemType = pub.ElemType
			}
			p.peer.sendCtrlMsg(&genericMsg{m.Id, &IdChanInfoMsg{pInfo2}})
		}
	}
	return
}

func (p *proxyImpl) handlePeerReadyMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*ConnReadyMsg)
	rInfo := msg.Info
	if len(rInfo) == 0 {
		return
	}
	for _, ready := range rInfo {
		if p.translator != nil {
			ready.Id = p.translator.TranslateInward(ready.Id)
		}
		//p.Log(LOG_INFO, fmt.Sprintf("handlePeerReadyMsg: %v, %v", ready.Id, ready.Credit))
		if p.filter != nil && p.filter.BlockOutward(ready.Id) {
			continue
		}
		//check
		if r, _ := p.appRecvChans.findRecver(ready.Id); r != nil {
			fs, ok := r.(*flowChanSender)
			if ok {
				fs.ack(ready.Credit)
				//p.Log(LOG_INFO, fmt.Sprintf("handlePeerReadyMsg: add credit %v for recver %v", ready.Credit, ready.Id))
			} else {
				p.Log(LOG_INFO, fmt.Sprintf("handlePeerReadyMsg: recver %v is not flow sender", ready.Id))
			}
			continue
		}
		//check if local already pubed it
		p.cacheLock.Lock()
		for _, pub := range p.exportSendIds {
			if pub.Id.Match(ready.Id) {
				//ready.Id, _ = ready.Id.Clone(ScopeLocal, MemberRemote)
				peerChan, _ := p.peer.appMsgChanForId(ready.Id)
				if peerChan != nil {
					p.appRecvChans.AddRecver(ready.Id, peerChan, ready.Credit)
					p.Log(LOG_INFO, fmt.Sprintf("handlePeerReadyMsg, add recver for: %v, %v", ready.Id, ready.Credit))
				}
				num++
				break
			}
		}
		p.cacheLock.Unlock()
	}
	return
}

func (p *proxyImpl) handlePeerSubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	p.cacheLock.Lock()
	for _, sub := range sInfo {
		if p.translator != nil {
			sub.Id = p.translator.TranslateInward(sub.Id)
		}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerSubMsg: %v", sub.Id))
		if p.filter != nil && p.filter.BlockOutward(sub.Id) {
			continue
		}
		_, ok := p.importRecvIds[sub.Id.Key()]
		if ok {
			p.cacheLock.Unlock()
			return
		}
		p.importRecvIds[sub.Id.Key()] = sub
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerSubMsg1: %v", sub.Id))
		//check if local already pubed it
		for _, pub := range p.exportSendIds {
			if pub.Id.Match(sub.Id) {
				if !p.chanTypeMatch(pub, sub) {
					err = os.ErrorString(errRmtChanTypeMismatch)
					p.sysChans.SendConnInfo(ErrorId, &ConnInfoMsg{Id: sub.Id, Error: err})
					p.LogError(err)
					p.cacheLock.Unlock()
					return
				}
				break
			}
		}
		num++
	}
	p.cacheLock.Unlock()
	return
}

func (p *proxyImpl) handlePeerUnSubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	sInfo := msg.Info
	if len(sInfo) == 0 {
		return
	}
	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()
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
	readyInfo := make([]*ChanReadyInfo, len(pInfo))
	p.cacheLock.Lock()
	for _, pub := range pInfo {
		if p.translator != nil {
			pub.Id = p.translator.TranslateInward(pub.Id)
		}
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerPubMsg: %v", pub.Id))
		if p.filter != nil && p.filter.BlockInward(pub.Id) {
			continue
		}
		_, ok := p.importSendIds[pub.Id.Key()]
		if ok {
			p.cacheLock.Unlock()
			return
		}
		p.importSendIds[pub.Id.Key()] = pub
		for _, sub := range p.exportRecvIds {
			if pub.Id.Match(sub.Id) {
				if !p.chanTypeMatch(pub, sub) {
					err = os.ErrorString(errRmtChanTypeMismatch)
					p.sysChans.SendConnInfo(ErrorId, &ConnInfoMsg{Id: sub.Id, Error: err})
					p.LogError(err)
					p.cacheLock.Unlock()
					return
				}
				id := pub.Id
				if p.translator != nil {
					id = p.translator.TranslateOutward(id)
				}
				p.Log(LOG_INFO, fmt.Sprintf("send ConnReady for: %v", pub.Id))
				readyInfo[num] = &ChanReadyInfo{id, p.router.recvChanBufSize(sub.Id)}
				num++
				p.appSendChans.AddSender(pub.Id, pub.ChanType)
			}
		}
	}
	p.cacheLock.Unlock()
	//send ConnReadyMsg
	if num > 0 {
		p.peer.sendCtrlMsg(&genericMsg{p.router.SysID(ReadyId), &ConnReadyMsg{Info: readyInfo[0:num]}})
		p.Log(LOG_INFO, fmt.Sprintf("handlePeerPubMsg sends ConnReadyMsg for : %v", readyInfo[0].Id))
	}
	return
}

func (p *proxyImpl) handlePeerUnPubMsg(m *genericMsg) (num int, err os.Error) {
	msg := m.Data.(*IdChanInfoMsg)
	pInfo := msg.Info
	if len(pInfo) == 0 {
		return
	}
	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()
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
	p.cacheLock.Lock()
	sub, ok := p.exportRecvIds[id.Key()]
	p.cacheLock.Unlock()
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

func (p *proxyImpl) PeerPubInfo() []*IdChanInfo {
	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()
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

func (p *proxyImpl) PeerSubInfo() []*IdChanInfo {
	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()
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

func (p *proxyImpl) LocalPubInfo() []*IdChanInfo {
	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()
	num := len(p.exportSendIds)
	info := make([]*IdChanInfo, num)
	idx := 0
	for _, v := range p.exportSendIds {
		info[idx] = new(IdChanInfo)
		info[idx].Id = v.Id
		info[idx].ChanType = v.ChanType
		idx++
	}
	return info
}

func (p *proxyImpl) LocalSubInfo() []*IdChanInfo {
	p.cacheLock.Lock()
	defer p.cacheLock.Unlock()
	num := len(p.exportRecvIds)
	info := make([]*IdChanInfo, num)
	idx := 0
	for _, v := range p.exportRecvIds {
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

func (sc *sysChans) Shutdown() {
	sc.proxy.Log(LOG_INFO, "proxy sysChan closing start")
	close(sc.pubSubInfo)
}

func (sc *sysChans) Close() {
	sc.proxy.Log(LOG_INFO, "proxy sysChan closing start")
	sc.sysRecvChans.Close()
	sc.sysSendChans.Close()
	sc.proxy.Log(LOG_INFO, "proxy sysChan closed")
}

func (sc *sysChans) SendConnInfo(idx int, data *ConnInfoMsg) {
	sch, nb := sc.sysSendChans.findSender(sc.proxy.router.SysID(idx))
	if sch != nil && nb > 0 {
		sch.Send(reflect.NewValue(data))
	}
}

func (sc *sysChans) SendReadyInfo(idx int, data *ConnReadyMsg) {
	sch, nb := sc.sysSendChans.findSender(sc.proxy.router.SysID(idx))
	if sch != nil && nb > 0 {
		sch.Send(reflect.NewValue(data))
	}
}

func (sc *sysChans) SendPubSubInfo(idx int, data *IdChanInfoMsg) {
	if idx >= PubId || idx <= UnSubId {
		sch, nb := sc.sysSendChans.findSender(sc.proxy.router.SysID(idx))
		if sch != nil && nb > 0 {
			//filter out sys internal ids
			info := make([]*IdChanInfo, len(data.Info))
			num := 0
			for i := 0; i < len(data.Info); i++ {
				if data.Info[i].Id.SysIdIndex() < 0 {
					info[num] = data.Info[i]
					num++
				}
			}
			sch.Send(reflect.NewValue(&IdChanInfoMsg{info[0:num]}))
		}
	}
}

func newSysChans(p *proxyImpl) *sysChans {
	sc := new(sysChans)
	sc.proxy = p
	r := p.router
	sc.pubSubInfo = make(chan *genericMsg, DefCmdChanBufSize)
	sc.sysRecvChans = newRecvChanBundle(p, ScopeLocal, MemberRemote)
	sc.sysSendChans = newSendChanBundle(p, ScopeLocal, MemberRemote)
	//
	pubSubChanType := reflect.Typeof(make(chan *IdChanInfoMsg)).(*reflect.ChanType)
	connChanType := reflect.Typeof(make(chan *ConnInfoMsg)).(*reflect.ChanType)
	readyChanType := reflect.Typeof(make(chan *ConnReadyMsg)).(*reflect.ChanType)

	sc.sysRecvChans.AddRecver(r.SysID(PubId), newGenericMsgChan(r.SysID(PubId), sc.pubSubInfo, false), -1 /*no flow control*/ )
	sc.sysRecvChans.AddRecver(r.SysID(UnPubId), newGenericMsgChan(r.SysID(UnPubId), sc.pubSubInfo, false), -1 /*no flow control*/ )
	sc.sysRecvChans.AddRecver(r.SysID(SubId), newGenericMsgChan(r.SysID(SubId), sc.pubSubInfo, false), -1 /*no flow control*/ )
	sc.sysRecvChans.AddRecver(r.SysID(UnSubId), newGenericMsgChan(r.SysID(UnSubId), sc.pubSubInfo, false), -1 /*no flow control*/ )

	sc.sysSendChans.AddSender(r.SysID(ConnId), connChanType)
	sc.sysSendChans.AddSender(r.SysID(DisconnId), connChanType)
	sc.sysSendChans.AddSender(r.SysID(ErrorId), connChanType)
	sc.sysSendChans.AddSender(r.SysID(ReadyId), readyChanType)
	sc.sysSendChans.AddSender(r.SysID(PubId), pubSubChanType)
	sc.sysSendChans.AddSender(r.SysID(UnPubId), pubSubChanType)
	sc.sysSendChans.AddSender(r.SysID(SubId), pubSubChanType)
	sc.sysSendChans.AddSender(r.SysID(UnSubId), pubSubChanType)
	return sc
}
