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
)

type Stream struct {
	peerBase
	myCmdChan chan peerCommand
	//
	rwc   io.ReadWriteCloser
	mar   Marshaler
	demar Demarshaler
	//
	proxy *proxyImpl
	//others
	Logger
	FaultRaiser
	Closed bool
}

func NewStream(rwc io.ReadWriteCloser, mp MarshallingPolicy, p *proxyImpl) *Stream {
	s := new(Stream)
	s.proxy = p
	//create export chans
	s.myCmdChan = make(chan peerCommand, DefCmdChanBufSize)
	s.exportConnChan = make(chan GenericMsg, p.router.defChanBufSize)
	s.exportPubSubChan = make(chan GenericMsg, p.router.defChanBufSize)
	s.exportAppDataChan = make(chan GenericMsg, p.router.defChanBufSize)
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
	s.Logger.Init(p.router.SysID(RouterLogId), p.router, ln, DefLogBufSize)
	s.FaultRaiser.Init(p.router.SysID(RouterFaultId), p.router, ln, DefCmdChanBufSize, faultTypeString)
	return s
}

func (s *Stream) Start() {
	go s.outputMainLoop()
	go s.inputMainLoop()
}

func (s *Stream) Close() {
	s.Log(LOG_INFO, "Close() is called")
	s.myCmdChan <- Close
}

func (s *Stream) closeImpl() {
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

func (s *Stream) send(m GenericMsg, appMsg bool) (err os.Error) {
	if appMsg {
		_, ok := m.Data.(ChanCloseMsg)
		id := m.Id
		if ok {
			id, _ = m.Id.Clone(NumScope, NumMembership) //special id to mark chan close
		}
		if err = s.mar.Marshal(id); err != nil {
			s.LogError(err)
			//s.Raise(MarshalFailure, err)
			return
		}
		if !ok {
			if err := s.mar.Marshal(m.Data); err != nil {
				s.LogError(err)
				//s.Raise(MarshalFailure, err)
				return
			}
		}
	} else {
		if err = s.mar.Marshal(m.Id); err != nil {
			s.LogError(err)
			//s.Raise(MarshalFailure, err)
			return
		}
		if err := s.mar.Marshal(m.Data); err != nil {
			s.LogError(err)
			//s.Raise(MarshalFailure, err)
			return
		}
	}
	s.Log(LOG_INFO, fmt.Sprintf("output send one msg for id %v", m.Id))
	return
}

//read data from importConnChan/importPubSubChan/importAppDataChan and send them to io.Writer
func (s *Stream) outputMainLoop() {
	s.Log(LOG_INFO, "outputMainLoop start")
	proxyDisconn := false
	cont := true
	for cont {
		select {
		case cmd := <-s.myCmdChan:
			if cmd == Close {
				cont = false
			}
		case m := <-s.importConnChan:
			if !closed(s.importConnChan) {
				if err := s.send(m, false); err != nil {
					cont = false
				}
				if m.Id.Match(s.proxy.router.SysID(RouterDisconnId)) {
					proxyDisconn = true
					cont = false
				}
			}
		case m := <-s.importPubSubChan:
			if !closed(s.importPubSubChan) {
				if err := s.send(m, false); err != nil {
					cont = false
				}
			}
		case m := <-s.importAppDataChan:
			if !closed(s.importAppDataChan) {
				if err := s.send(m, true); err != nil {
					cont = false
				}
			}
		}
	}
	if !proxyDisconn {
		//must be io conn fail or marshal fail
		//notify proxy disconn
		s.exportConnChan <- GenericMsg{Id: s.proxy.router.SysID(RouterDisconnId), Data: ConnInfoMsg{}}
	}
	s.closeImpl()
	s.Log(LOG_INFO, "outputMainLoop exit")
}

//read data from io.Reader and pass them to exportConnChan/exportPubSubChan/exportAppDataChan
func (s *Stream) inputMainLoop() {
	s.Log(LOG_INFO, "inputMainLoop start")
	cont := true
	for cont {
		if err := s.recv(); err != nil {
			cont = false
		}
	}
	//when reach here, must be io conn fail or demarshal fail
	//notify proxy disconn
	s.exportConnChan <- GenericMsg{Id: s.proxy.router.SysID(RouterDisconnId), Data: ConnInfoMsg{}}
	//s.closeImpl() only called from outputMainLoop
	s.Log(LOG_INFO, "inputMainLoop exit")
}

func (s *Stream) recv() (err os.Error) {
	r := s.proxy.router
	id, _ := r.seedId.Clone()
	if err = s.demar.Demarshal(id, nil); err != nil {
		s.LogError(err)
		//s.Raise(DemarshalFailure, err)
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
		cim := ConnInfoMsg{SeedId: idc}
		if err = s.demar.Demarshal(&cim, nil); err != nil {
			s.LogError(err)
			//s.Raise(DemarshalFailure, err)
			return
		}
		s.exportConnChan <- GenericMsg{id, cim}
	case id.Match(r.SysID(PubId)):
		fallthrough
	case id.Match(r.SysID(UnPubId)):
		fallthrough
	case id.Match(r.SysID(SubId)):
		fallthrough
	case id.Match(r.SysID(UnSubId)):
		idc, _ := r.seedId.Clone()
		icm := IdChanInfoMsg{[]*IdChanInfo{&IdChanInfo{idc, nil, nil}}}
		if err = s.demar.Demarshal(&icm, nil); err != nil {
			s.LogError(err)
			//s.Raise(DemarshalFailure, err)
			return
		}
		s.exportPubSubChan <- GenericMsg{id, icm}
	default: //appMsg
		if id.Scope() == NumScope && id.Member() == NumMembership { //chan is closed
			s.exportAppDataChan <- GenericMsg{id, ChanCloseMsg{}}
		} else {
			chanType := s.proxy.getExportRecvChanType(id)
			if chanType == nil {
				errStr := fmt.Sprintf("failed to find chanType for id %v", id)
				s.Log(LOG_ERROR, errStr)
				//s.Raise(DemarshalFailure, os.ErrorString(errStr))
				return
			}
			val := reflect.MakeZero(chanType.Elem())
			if err = s.demar.Demarshal(val.Interface(), val); err != nil {
				s.LogError(err)
				//s.Raise(DemarshalFailure, err)
				return
			}
			s.exportAppDataChan <- GenericMsg{id, val.Interface()}
		}
	}
	s.Log(LOG_INFO, fmt.Sprintf("input recv one msg for id %v", id))
	return
}
