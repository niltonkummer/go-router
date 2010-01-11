//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//
package main

import (
	"net"
	"fmt"
	"router"
)

type Subject struct {
	sendChan chan string
	recvChan chan string
}

func newSubject() *Subject {
	s := new(Subject)
	s.sendChan = make(chan string)
	s.recvChan = make(chan string)
	return s
}

func main() {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(l.Addr().String())

	subjMap := make(map[interface{}]*Subject)

	rot := router.New(router.StrID(), 32, router.BroadcastPolicy /*, "chatsrv", router.ScopeLocal*/ )

	//start server mainloop in a separate goroutine, and recv client conn in main goroutine
	go func() {
		//subscribe to remote publications, so learn what subjects are created
		pubChan := make(chan router.IdChanInfoMsg)
		//attach a recv chan with flag true, so that we know when senders detach and
		//this recv chan will not be closed when all senders detach
		rot.AttachRecvChan(rot.NewSysID(router.PubId, router.ScopeRemote), pubChan, true)
		//stopChan to notify when all people leave a subject
		stopChan := make(chan router.Id, 36)

		for {
			select {
			case id := <-stopChan:
				subjMap[id.Key()] = nil, false
			case pub := <-pubChan:
				//process recved client publication of subjects
				for _, v := range pub.Info {
					subj, ok := subjMap[v.Id.Key()]
					if ok {
						continue
					}
					fmt.Printf("add subject: %v\n", v.Id)
					//add a new subject with ScopeRemote, so that msgs are forwarded
					//to peers in connected routers
					id, _ := v.Id.Clone(router.ScopeRemote, router.MemberLocal)
					subj = newSubject()
					subjMap[id.Key()] = subj
					//subscribe to new subjects, forward recved msgs to other
					rot.AttachSendChan(id, subj.sendChan)
					rot.AttachRecvChan(id, subj.recvChan)
					//start forwarding
					go func(id router.Id) {
						for val := range subj.recvChan {
							fmt.Printf("chatsrv forward: subject[%v], msg[%s]\n", id, val)
							subj.sendChan <- val
						}
						stopChan <- id
						fmt.Printf("chatsrv stop forwarding for : %v\n", id)
					}(id)
				}
			}
		}
	}()

	//keep accepting client conn and connect local router to it
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println("one client connect")

		_, err = rot.ConnectRemote(conn, router.JsonMarshaling)
		if err != nil {
			fmt.Println(err)
			return
		}
	}

	//in fact never reach here
	rot.Close()
	l.Close()
}
