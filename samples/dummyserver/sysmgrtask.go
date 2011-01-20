//
// Copyright (c) 2010 - 2011 Yigong Liu
//
// Distributed under New BSD License
//
package main

import (
	"router"
	"time"
	"fmt"
	"strings"
)

/*
 SystemManagerTask: perform overall system management
 SysteManager at standby servant monitors the heartbeat from active server, if 2 heartbeat miss in a row, standby will become active and start serving user requests
 SystemManager at active servant will send heartbeats to standby, send command msgs to subordinate tasks to control their life cycle
 SystemManager will monitor client connections and create ServiceTask on demand
 SysMgrTask has the following messaging interface:
 A> messages sent
 /Sys/Ctrl/Heartbeat
 /Sys/Command
 B> messages recved
 /Sys/Ctrl/Heartbeat
 /Sys/OutOfService
*/
type SysMgrTask struct {
	//output_intf or send chans
	htbtSendChan chan *time.Time
	sysCmdChan   chan string
	//input_intf or recv chans
	htbtRecvChan chan *time.Time
	sysOOSChan   chan string
	//private state
	role          ServantRole
	rot           router.Router
	name          string
	childBindChan chan *router.BindEvent
	stopChan      chan bool
	startChan     chan bool
	pubChan       chan *router.IdChanInfoMsg
	unpubChan     chan *router.IdChanInfoMsg
	pubBindChan   chan *router.BindEvent
	unpubBindChan chan *router.BindEvent
}

func NewSysMgrTask(r router.Router, n string, role ServantRole) *SysMgrTask {
	smt := &SysMgrTask{}
	go smt.Run(r, n, role)
	return smt
}

func (smt *SysMgrTask) Run(r router.Router, n string, role ServantRole) {
	//init phase
	smt.init(r, n, role)
	//wait for child tasks to bind, at least two (DB, Fault)
	for {
		if (<-smt.childBindChan).Count >= 2 {
			break
		}
	}
	if smt.role == Active {
		//in active servant
		//ask all child tasks coming up to service
		smt.sysCmdChan <- "Start"
		//start sending heartbeat to standby
		go smt.sendHeartbeat()
	} else {
		//in standby servant
		//start monitoring heartbeats from active servant
		go smt.monitorActiveHeartbeat()
	}
	//service mainLoop
	fmt.Println("SysMgrTask [", n, "] starts mainloop")
	cont := true
	for cont {
		select {
		case _ = <-smt.startChan:
			//at standby servant, SysMgrTask will monitor heartbeats
			//active servant stopped, change my role to active
			smt.role = Active
			//drain remaining out-of-date OOS msgs
			_, ok := <-smt.sysOOSChan
			for ok {
				_, ok = <-smt.sysOOSChan
			}
			//ask all child tasks coming up to service
			smt.sysCmdChan <- "Start"
			//start sending heartbeat to standby
			go smt.sendHeartbeat()
			fmt.Println("!!!! Servant [", smt.name, "] come up in service ...")
		case _ = <-smt.sysOOSChan:
			//at active servant, per request from FaultMgrTask, SysMgrTask can put the servant
			//out of service by stopping heartbeating and asking subordinate tasks to stop
			if !closed(smt.sysOOSChan) {
				smt.role = Standby
				smt.stopHeartbeat() //1 second later, standby will come up
				fmt.Println("xxxx Servant [", smt.name, "] will take a break and standby ...")
				//start monitoring heartbeats from active servant
				go smt.monitorActiveHeartbeat()
				smt.sysCmdChan <- "Stop" //?move into monitorActiveHeartbeat() wait for active coming up
			} else {
				fmt.Println("error: OOS chan closed")
				cont = false
			}
		case pub := <-smt.pubChan:
			if !closed(smt.pubChan) {
				for _, v := range pub.Info {
					id := v.Id.(*router.StrId)
					data := strings.Split(id.Val, "/", -1)
					if data[1] == "App" {
						smt.sysCmdChan <- fmt.Sprintf("AddService:%s", data[2])
						fmt.Printf("%s AddService:%s\n", smt.name, data[2])
						NewServiceTask(smt.rot, smt.name, data[2], smt.role)
					}
				}
			} else {
				fmt.Println("error: pubChan closed")
				cont = false
			}
		case unpub := <-smt.unpubChan:
			if !closed(smt.unpubChan) {
				for _, v := range unpub.Info {
					id := v.Id.(*router.StrId)
					data := strings.Split(id.Val, "/", -1)
					if data[1] == "App" {
						smt.sysCmdChan <- fmt.Sprintf("DelService:%s", data[2])
						fmt.Printf("%s DelService:%s\n", smt.name, data[2])
					}
				}
			} else {
				fmt.Println("error: unpubChan closed")
				cont = false
			}
		}
	}
	//shutdown phase
	smt.shutdown()
	fmt.Println("SysMgr at [", smt.name, "] exit")
}

func (smt *SysMgrTask) init(r router.Router, n string, role ServantRole) {
	smt.rot = r
	smt.name = n
	smt.role = role
	smt.htbtSendChan = make(chan *time.Time)
	smt.htbtRecvChan = make(chan *time.Time)
	smt.sysCmdChan = make(chan string)
	smt.sysOOSChan = make(chan string)
	smt.childBindChan = make(chan *router.BindEvent, 1)
	smt.startChan = make(chan bool, 1)
	smt.stopChan = make(chan bool, 1)
	//output_intf or send chans
	smt.rot.AttachSendChan(router.StrID("/Sys/Command"), smt.sysCmdChan, smt.childBindChan)
	smt.rot.AttachSendChan(router.StrID("/Sys/Ctrl/Heartbeat", router.ScopeRemote), smt.htbtSendChan)
	//input_intf or recv chans
	smt.rot.AttachRecvChan(router.StrID("/Sys/Ctrl/Heartbeat", router.ScopeRemote), smt.htbtRecvChan)
	smt.rot.AttachRecvChan(router.StrID("/Sys/OutOfService"), smt.sysOOSChan)
	//
	smt.pubChan = make(chan *router.IdChanInfoMsg)
	smt.unpubChan = make(chan *router.IdChanInfoMsg)
	//use pubBindChan/unpubBindChan when attaching chans to PubId/UnPubId, so that they will not be
	//closed when all clients close and leave
	smt.pubBindChan = make(chan *router.BindEvent, 1)
	smt.unpubBindChan = make(chan *router.BindEvent, 1)
	smt.rot.AttachRecvChan(smt.rot.NewSysID(router.PubId, router.ScopeRemote), smt.pubChan, smt.pubBindChan)
	smt.rot.AttachRecvChan(smt.rot.NewSysID(router.UnPubId, router.ScopeRemote), smt.unpubChan, smt.unpubBindChan)
}

func (smt *SysMgrTask) shutdown() {
	//output_intf or send chans
	smt.rot.DetachChan(router.StrID("/Sys/Command"), smt.sysCmdChan)
	smt.rot.DetachChan(router.StrID("/Sys/Ctrl/Heartbeat"), smt.htbtSendChan)
	//input_intf or recv chans
	smt.rot.DetachChan(router.StrID("/Sys/Ctrl/Heartbeat"), smt.htbtRecvChan)
	smt.rot.DetachChan(router.StrID("/Sys/OutOfService"), smt.sysOOSChan)
	//
	smt.rot.DetachChan(smt.rot.NewSysID(router.PubId, router.ScopeRemote), smt.pubChan)
	smt.rot.DetachChan(smt.rot.NewSysID(router.UnPubId, router.ScopeRemote), smt.unpubChan)
}

//standby servant will monitor heartbeat from active Servant, if missing 3 in a row, come up active
func (smt *SysMgrTask) monitorActiveHeartbeat() {
	fmt.Println(smt.name, " enter monitor heartbeat")
	miss := 0
	//first block wait for active servant coming up
	<-smt.htbtRecvChan
	//drain remaining out-of-date heartbeats, if the standby coming up late
	_, ok := <-smt.htbtRecvChan
	for ok {
		_, ok = <-smt.htbtRecvChan
	}
	//start heartbeat monitoring, standby servant should recv about 4-5 heartbeats per second from
	//active servant, otherwise, it will come up as active
	for {
		time.Sleep(2e8)
		_, ok = <-smt.htbtRecvChan
		if !ok {
			miss++
			if miss > 1 {
				break
			}
		} else {
			//fmt.Println("standby [", smt.name, "] recv heartbeat from active")
			miss = 0
		}
	}
	fmt.Println(smt.name, " exit monitor heartbeat")
	smt.startChan <- true
}

func (smt *SysMgrTask) sendHeartbeat() {
	fmt.Println(smt.name, " enter send heartbeat")
	//send a heartbeat to standby servant every 1/5 second
	for {
		//check if should stop heartbeating
		_, ok := <-smt.stopChan
		if ok {
			break
		}
		//send heartbeat in non-blocking way
		for !(smt.htbtSendChan <- time.LocalTime()) {
			_, _ = <-smt.htbtSendChan
		}
		time.Sleep(2e8)
	}
	fmt.Println(smt.name, " exit send heartbeat")
}

func (smt *SysMgrTask) stopHeartbeat() { smt.stopChan <- true }
