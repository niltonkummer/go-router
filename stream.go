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

type stream struct {
	peer peerIntf
	outputChan chan *genericMsg
	//
	rwc        io.ReadWriteCloser
	mar        Marshaler
	demar      Demarshaler
	//
	proxy *proxyImpl
	//others
	Logger
	FaultRaiser
	Closed bool
	ptrBool *reflect.PtrValue
	ptrInt *reflect.PtrValue
	ptrFloat *reflect.PtrValue
	ptrString *reflect.PtrValue
}

func newStream(rwc io.ReadWriteCloser, mp MarshalingPolicy, p *proxyImpl) *stream {
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
	var xb *bool = nil
	s.ptrBool = reflect.NewValue(xb).(*reflect.PtrValue)
	var xi *int = nil
	s.ptrInt = reflect.NewValue(xi).(*reflect.PtrValue)
	var xf *float = nil
	s.ptrFloat = reflect.NewValue(xf).(*reflect.PtrValue)
	var xs *string = nil
	s.ptrString = reflect.NewValue(xs).(*reflect.PtrValue)
	return s
}

func (s *stream) start() {
	go s.outputMainLoop()
	go s.inputMainLoop()
}

func (s *stream) Close() {
	s.Log(LOG_INFO, "Close() is called")
	//shutdown outputMainLoop
	close(s.outputChan) 
	//notify peer
	s.peer.sendCtrlMsg(&genericMsg{s.proxy.router.SysID(RouterDisconnId), &ConnInfoMsg{}})
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
	s.outputChan <- m
	if m.Id.Match(s.proxy.router.SysID(RouterDisconnId)) {
		close(s.outputChan)
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
	s.closeImpl() 
	s.Log(LOG_INFO, "inputMainLoop exit")
}

func (s *stream) recv() (err os.Error) {
	r := s.proxy.router
	id, _ := r.seedId.Clone()
	if err = s.demar.Demarshal(id); err != nil {
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
		if err = s.demar.Demarshal(cim); err != nil {
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
		if err = s.demar.Demarshal(icm); err != nil {
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
			et := chanType.Elem()
			val := reflect.MakeZero(et)
			var ptrT *reflect.PtrValue
			switch et := et.(type) {
			case *reflect.BoolType:
				ptrT = s.ptrBool
				ptrT.PointTo(val)
			case *reflect.IntType:
				ptrT = s.ptrInt
				ptrT.PointTo(val)
			case *reflect.FloatType:
				ptrT = s.ptrFloat
				ptrT.PointTo(val)
			case *reflect.StringType:
				ptrT = s.ptrString
				ptrT.PointTo(val)
			case *reflect.PtrType:
				sto := reflect.MakeZero(et.Elem())
				ptrT = val.(*reflect.PtrValue)
				ptrT.PointTo(sto)
			default:
				errStr := fmt.Sprintf("invalid chanType for id %v", id)
				s.Log(LOG_ERROR, errStr)
				s.Raise(os.ErrorString(errStr))
				return
			}
			if err = s.demar.Demarshal(ptrT.Interface()); err != nil {
				s.LogError(err)
				//s.Raise(err)
				return
			}
			peerChan, num := s.peer.appMsgChanForId(id)
			if peerChan != nil && num > 0 {
				peerChan.Send(val)
				//s.Log(LOG_INFO, fmt.Sprintf("send appMsg for %v", id))
			}
		}
	}
	//s.Log(LOG_INFO, fmt.Sprintf("input recv one msg for id %v", id))
	return
}
