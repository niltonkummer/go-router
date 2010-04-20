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
	peer peerIntf
	outputChan chan *genericMsg
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
	//
	s.outputChan = make(chan *genericMsg, s.proxy.router.defChanBufSize + DefCmdChanBufSize)
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
	s.closeImpl()
}

func (s *stream) closeImpl() {
	if !s.Closed {
		s.Log(LOG_INFO, "closeImpl called")
		s.Closed = true
		//shutdown outputMainLoop
		close(s.outputChan) 
		//shutdown inputMainLoop
		s.rwc.Close()
		//close logger
		s.FaultRaiser.Close()
		s.Logger.Close()
	}
}

func (s *stream) appMsgChanForId(id Id) (reflectChanValue, int) {
	if s.proxy.translator != nil {
		return &genericMsgChan{id, s.outputChan, func(id Id) Id { return s.proxy.translator.TranslateOutward(id) }, true}, 1
	}
	return &genericMsgChan{id, s.outputChan, nil, true}, 1
}

//send ctrl data to io.Writer
func (s *stream) sendCtrlMsg(m *genericMsg) (err os.Error) {
	s.Lock()
	if err = s.mar.Marshal(m.Id); err != nil {
		s.LogError(err)
		//s.Raise(err)
	} else {
		if err = s.mar.Marshal(m.Data); err != nil {
			s.LogError(err)
			//s.Raise(err)
		}
	}
	s.Unlock()
	s.Log(LOG_INFO, fmt.Sprintf("output send one msg for id %v", m.Id))
	if err != nil {
		//must be io conn fail or marshal fail
		//notify proxy disconn
		s.peer.sendCtrlMsg(&genericMsg{s.proxy.router.SysID(RouterDisconnId), &ConnInfoMsg{}})
	}
	if m.Id.Match(s.proxy.router.SysID(RouterDisconnId)) || err != nil {
		s.closeImpl()
		s.Log(LOG_INFO, "stream exit at output")
	}
	return
}

func (s *stream) outputMainLoop() {
	s.Log(LOG_INFO, "outputMainLoop start")
	//
	var err os.Error
	cont := true
	for cont {
		m := <-s.outputChan
		if closed(s.outputChan) {
			cont = false
		} else {
			s.Lock()
			if err = s.mar.Marshal(m.Id); err != nil {
				s.LogError(err)
				//s.Raise(err)
				cont = false
			} else if !(m.Id.Scope() == NumScope && m.Id.Member() == NumMembership) {
				if err = s.mar.Marshal(m.Data); err != nil {
					s.LogError(err)
					//s.Raise(err)
					cont = false
				}
			}
			s.Unlock()
		}
	}
	if err != nil {
		//must be io conn fail or marshal fail
		//notify proxy disconn
		s.peer.sendCtrlMsg(&genericMsg{s.proxy.router.SysID(RouterDisconnId), &ConnInfoMsg{}})
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
	s.peer.sendCtrlMsg(&genericMsg{s.proxy.router.SysID(RouterDisconnId), &ConnInfoMsg{}})
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
		s.peer.sendCtrlMsg(&genericMsg{id, cim})
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
		s.peer.sendCtrlMsg(&genericMsg{id, icm})
	default: //appMsg
		if id.Scope() == NumScope && id.Member() == NumMembership { //chan is closed
			peerChan, _ := s.peer.appMsgChanForId(id)
			if peerChan != nil {
				peerChan.Close()
				s.Log(LOG_INFO, fmt.Sprintf("close proxy forwarding chan for %v", id))
			}
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
			peerChan, num := s.peer.appMsgChanForId(id)
			if peerChan != nil && num > 0 {
				peerChan.Send(val)
				s.Log(LOG_INFO, fmt.Sprintf("send appMsg for %v", id))
			}
		}
	}
	s.Log(LOG_INFO, fmt.Sprintf("input recv one msg for id %v", id))
	return
}
