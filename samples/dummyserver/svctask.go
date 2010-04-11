//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//
package main

import (
	"router"
	"rand"
	"fmt"
	"time"
	"strings"
	"os"
)

/*
ServiceTask: a dummy application service running inside server
         recv a request string and return response (request_string + "[processed: trans number]")
         a real server can have multiple ServiceTasks for diff services
ServiceTask has the following messaging interface:
   A> messages sent
      /App/ServiceName/Response
      /Fault/AppService/Exception
      /DB/Request
   B> messages recved
      /App/ServiceName/Request
      /App/ServiceName/Command
      /App/ServiceName/DB/Response
      /Sys/Command
*/
type ServiceTask struct {
	//output_intf or send chans
	dbReqChan   chan *DbReq
	svcRespChan chan string
	*router.FaultRaiser
	//input_intf or recv chans
	svcReqChan chan string
	sysCmdChan chan string
	svcCmdChan chan string
	dbRespChan chan string
	//some private state
	rot         router.Router
	name        string //unique ServiceName
	servantName string
	numTrans    int //how many requests have been handled
	role        ServantRole
	random      *rand.Rand //for generating fake db requests and fake fault report
	bindChan    chan *router.BindEvent
}

func NewServiceTask(r router.Router, sn string, n string, role ServantRole) *ServiceTask {
	st := &ServiceTask{}
	go st.Run(r, sn, n, role)
	return st
}

func (at *ServiceTask) Run(r router.Router, sn string, n string, role ServantRole) {
	//init phase
	at.init(r, sn, n, role)
	//service mainLoop
	cont := true
	for cont {
		select {
		case cmd := <-at.sysCmdChan:
			if !closed(at.sysCmdChan) {
				cont = at.handleCmd(cmd)
			} else {
				cont = false
			}
		case req := <-at.svcReqChan:
			if !closed(at.svcReqChan) {
				if at.role == Active {
					at.handleSvcReq(req)
				}
			} else {
				cont = false
			}
		case cmd := <-at.svcCmdChan:
			if !closed(at.svcCmdChan) {
				if at.role == Active {
					switch cmd {
					case "Reset":
						//right now, the only service command is "Reset"
						at.numTrans = 0
						fmt.Println("App Service [", at.name, "] at [", at.servantName, "] is reset")
					default:
					}
				}
			} else {
				cont = false
			}
		case be := <-at.bindChan:
			if be.Count == 0 { //peer exit
				cont = false
			}
		}
	}
	//shutdown phase
	at.shutdown()
	fmt.Println("App task [", at.name, "] at [", at.servantName, "] exit")
}

func (at *ServiceTask) init(r router.Router, sn string, n string, role ServantRole) {
	at.rot = r
	at.name = n
	at.servantName = sn
	at.role = role
	at.random = rand.New(rand.NewSource(time.Seconds()))
	at.svcRespChan = make(chan string)
	at.dbReqChan = make(chan *DbReq)
	at.svcReqChan = make(chan string, router.DefDataChanBufSize)
	at.sysCmdChan = make(chan string)
	at.svcCmdChan = make(chan string)
	at.dbRespChan = make(chan string)
	at.bindChan = make(chan *router.BindEvent, 1)
	svcname := "/App/" + at.name
	//output_intf or send chans
	at.FaultRaiser = router.NewFaultRaiser(router.StrID("/Fault/AppService/Exception"), at.rot, at.name)
	at.rot.AttachSendChan(router.StrID("/DB/Request"), at.dbReqChan)
	at.rot.AttachSendChan(router.StrID(svcname+"/Response"), at.svcRespChan)
	//input_intf or recv chans
	at.rot.AttachRecvChan(router.StrID("/Sys/Command"), at.sysCmdChan)
	at.rot.AttachRecvChan(router.StrID(svcname+"/Command"), at.svcCmdChan)
	at.rot.AttachRecvChan(router.StrID(svcname+"/DB/Response"), at.dbRespChan)
	at.rot.AttachRecvChan(router.StrID(svcname+"/Request"), at.svcReqChan, at.bindChan)
}

func (at *ServiceTask) shutdown() {
	svcname := "/App/" + at.name
	//output_intf or send chans
	at.FaultRaiser.Close()
	at.rot.DetachChan(router.StrID(svcname+"/Response"), at.svcRespChan)
	at.rot.DetachChan(router.StrID("/DB/Request"), at.dbReqChan)
	//input_intf or recv chans
	at.rot.DetachChan(router.StrID(svcname+"/Request"), at.svcReqChan)
	at.rot.DetachChan(router.StrID("/Sys/Command"), at.sysCmdChan)
	at.rot.DetachChan(router.StrID(svcname+"/Command"), at.svcCmdChan)
	at.rot.DetachChan(router.StrID(svcname+"/DB/Response"), at.dbRespChan)
}

func (at *ServiceTask) handleCmd(cmd string) bool {
	svcname := ""
	idx := strings.Index(cmd, ":")
	if idx > 0 {
		svcname = cmd[idx+1:]
		cmd = cmd[0:idx]
	}
	cont := true
	switch cmd {
	case "Start":
		if at.role == Standby {
			//first drain old req
			_, ok := <-at.svcReqChan
			for ok {
				if closed(at.svcReqChan) {
					cont = false
					break
				}
				_, ok = <-at.svcReqChan
			}
		}
		at.numTrans = 0
		at.role = Active
		fmt.Println("App Service [", at.name, "] at [", at.servantName, "] is activated")
	case "Stop":
		at.role = Standby
		fmt.Println("App Service [", at.name, "] at [", at.servantName, "] is stopped")
	case "Shutdown":
		cont = false
		fmt.Println("App Service [", at.name, "] at [", at.servantName, "] is shutdown")
	case "DelService":
		if svcname == at.name {
			cont = false
			fmt.Println("App Service [", at.name, "] at [", at.servantName, "] is deleted")
		}
	default:
	}
	return cont
}

func (at *ServiceTask) handleSvcReq(req string) {
	fmt.Println("App Service [", at.name, "] at [", at.servantName, "] process req: ", req)
	//fake random database req
	r := at.random.Intn(6)
	if r == 1 {
		at.dbReqChan <- &DbReq{at.name, "give me data"}
		dbResp := <-at.dbRespChan
		if len(dbResp) == 0 {
			fmt.Println("App Service [", at.name, "] at [", at.servantName, "] standby and failed req: ", req)
			return
		}
	}
	//send response
	at.svcRespChan <- fmt.Sprintf("[%s] is processed at [%s] : transaction_id [%d]", req, at.servantName, at.numTrans)
	at.numTrans++
	//fake random fault report
	if at.numTrans > 3 {
		r = at.random.Intn(1024)
		if r == 99 {
			at.Raise(os.ErrorString("app service got an error"))
		}
	}
}
