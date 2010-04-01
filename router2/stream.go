//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"reflect"
	"io"
	"os"
	"fmt"
	"sync"
)

type stream struct {
	//imported chan to recv ctrl msgs from peer
	importCtrlChan chan *genericMsg
	//exported chan to send ctrl msgs to peer
	exportCtrlChan chan *genericMsg
	//use peerIntf.sendAppData() to forward app msgs to peer(proxy)
	peer peerIntf
	//
	rwc        io.ReadWriteCloser
	mar        Marshaler
	demar      Demarshaler
	sync.Mutex //lock for sharing sending socket-end between ctrl msgs and app msgs
	//
	proxy *proxyImpl
	//others
	Logger
	FaultRaiser
	Closed bool
}

func newStream(rwc io.ReadWriteCloser, mp MarshallingPolicy, p *proxyImpl) *stream {
	s := new(stream)
	s.proxy = p
	//create export chans
	s.exportCtrlChan = make(chan *genericMsg, DefCmdChanBufSize)
	//
	s.rwc = rwc
	s.mar = mp.NewMarshaler(rwc)
	s.demar = mp.NewDemarshaler(rwc)
	//
	ln := ""
	if len(p.router.name) > 0 {
		if len(p.name) > 0 {
			ln = p.router.name + p.name
		} else {
			ln = p.router.name + "_proxy"
		}
		ln += "_stream"
	}
	s.Logger.Init(p.router.SysID(RouterLogId), p.router, ln)
	s.FaultRaiser.Init(p.router.SysID(RouterFaultId), p.router, ln)
	return s
}

func (s *stream) start() {
	go s.outputMainLoop()
	go s.inputMainLoop()
}

func (s *stream) Close() {
	s.Log(LOG_INFO, "Close() is called")
	close(s.importCtrlChan)
}

func (s *stream) closeImpl() {
	if !s.Closed {
		s.Log(LOG_INFO, "closeImpl called")
		s.Closed = true
		//shutdown inputMainLoop
		s.rwc.Close()
		//close logger
		s.FaultRaiser.Close()
		s.Logger.Close()
	}
}

func (s *stream) sendCtrlData(m *genericMsg) (err os.Error) {
	s.Lock()
	defer s.Unlock()
	if err = s.mar.Marshal(m.Id); err != nil {
		s.LogError(err)
		//s.Raise(err)
		return
	}
	if err := s.mar.Marshal(m.Data); err != nil {
		s.LogError(err)
		//s.Raise(err)
		return
	}
	s.Log(LOG_INFO, fmt.Sprintf("output send one msg for id %v", m.Id))
	return
}

func (s *stream) sendAppData(id Id, data interface{}) (err os.Error) {
	_, ok := data.(chanCloseMsg)
	if ok {
		id, _ = id.Clone(NumScope, NumMembership) //special id to mark chan close
	}
	s.Lock()
	defer s.Unlock()
	if err = s.mar.Marshal(id); err != nil {
		s.LogError(err)
		//s.Raise(err)
		return
	}
	if !ok {
		if err = s.mar.Marshal(data); err != nil {
			s.LogError(err)
			//s.Raise(err)
			return
		}
	}
	return
}

//read data from importCtrlChan/importAppDataChan and send them to io.Writer
func (s *stream) outputMainLoop() {
	s.Log(LOG_INFO, "outputMainLoop start")
	//
	proxyDisconn := false
	cont := true
	for cont {
		m := <-s.importCtrlChan
		if !closed(s.importCtrlChan) {
			if err := s.sendCtrlData(m); err != nil {
				cont = false
			}
			if m.Id.Match(s.proxy.router.SysID(RouterDisconnId)) {
				proxyDisconn = true
				cont = false
			}
		}
	}
	if !proxyDisconn {
		//must be io conn fail or marshal fail
		//notify proxy disconn
		s.exportCtrlChan <- &genericMsg{Id: s.proxy.router.SysID(RouterDisconnId), Data: &ConnInfoMsg{}}
	}
	s.closeImpl()
	s.Log(LOG_INFO, "outputMainLoop exit")
}

//read data from io.Reader, pass ctrlMsg to exportCtrlChan and dataMsg to peer
func (s *stream) inputMainLoop() {
	s.Log(LOG_INFO, "inputMainLoop start")
	cont := true
	for cont {
		if err := s.recv(); err != nil {
			cont = false
		}
	}
	//when reach here, must be io conn fail or demarshal fail
	//notify proxy disconn
	s.exportCtrlChan <- &genericMsg{Id: s.proxy.router.SysID(RouterDisconnId), Data: &ConnInfoMsg{}}
	//s.closeImpl() only called from outputMainLoop
	s.Log(LOG_INFO, "inputMainLoop exit")
}

func (s *stream) recv() (err os.Error) {
	r := s.proxy.router
	id, _ := r.seedId.Clone()
	if err = s.demar.Demarshal(id, nil); err != nil {
		s.LogError(err)
		//s.Raise(err)
		return
	}
	switch {
	case id.Match(r.SysID(RouterConnId)):
		fallthrough
	case id.Match(r.SysID(RouterDisconnId)):
		fallthrough
	case id.Match(r.SysID(ConnErrorId)):
		fallthrough
	case id.Match(r.SysID(ConnReadyId)):
		idc, _ := r.seedId.Clone()
		cim := &ConnInfoMsg{SeedId: idc}
		if err = s.demar.Demarshal(cim, nil); err != nil {
			s.LogError(err)
			//s.Raise(err)
			return
		}
		s.exportCtrlChan <- &genericMsg{id, cim}
	case id.Match(r.SysID(PubId)):
		fallthrough
	case id.Match(r.SysID(UnPubId)):
		fallthrough
	case id.Match(r.SysID(SubId)):
		fallthrough
	case id.Match(r.SysID(UnSubId)):
		idc, _ := r.seedId.Clone()
		icm := &IdChanInfoMsg{[]*IdChanInfo{&IdChanInfo{idc, nil, nil}}}
		if err = s.demar.Demarshal(icm, nil); err != nil {
			s.LogError(err)
			//s.Raise(err)
			return
		}
		s.exportCtrlChan <- &genericMsg{id, icm}
	default: //appMsg
		if id.Scope() == NumScope && id.Member() == NumMembership { //chan is closed
			s.peer.sendAppData(id, chanCloseMsg{})
		} else {
			chanType := s.proxy.getExportRecvChanType(id)
			if chanType == nil {
				errStr := fmt.Sprintf("failed to find chanType for id %v", id)
				s.Log(LOG_ERROR, errStr)
				s.Raise(os.ErrorString(errStr))
				return
			}
			val := reflect.MakeZero(chanType.Elem())
			ptrT, ok := chanType.Elem().(*reflect.PtrType)
			if ok {
				sto := reflect.MakeZero(ptrT.Elem())
				val = reflect.MakeZero(ptrT)
				val.(*reflect.PtrValue).PointTo(sto)
			}
			if err = s.demar.Demarshal(val.Interface(), val); err != nil {
				s.LogError(err)
				//s.Raise(err)
				return
			}
			s.peer.sendAppData(id, val.Interface())
		}
	}
	s.Log(LOG_INFO, fmt.Sprintf("input recv one msg for id %v", id))
	return
}
