//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//
package main

import (
	"router"
	"os"
	"fmt"
	"net"
)

type ServantRole int

const (
	Standby ServantRole = iota
	Active
)

func (s ServantRole) String() string {
	switch s {
	case Active:
		return "Active"
	case Standby:
		return "Standby"
	}
	return "InvalidRole"
}

/*
 Servant
*/
const (
	ServantAddr1 = "/tmp/dummy.servant.1"
	ServantAddr2 = "/tmp/dummy.servant.2"
)

type Servant struct {
	Rot  router.Router
	role ServantRole
	name string
}

func NewServant(n string, role ServantRole, done chan bool) *Servant {
	s := new(Servant)
	s.Rot = router.New(router.StrID(), 32, router.BroadcastPolicy)
	s.role = role
	s.name = n
	//start system tasks, ServiceTask will be created when clients connect
	NewSysMgrTask(s.Rot, n, role)
	NewDbTask(s.Rot, n, role)
	NewFaultMgrTask(s.Rot, n, role)
	//run Servant mainloop to wait for client connection
	go s.Run(done)
	return s
}

func (s *Servant) Run(done chan bool) {
	addr := ServantAddr1
	if s.role == Standby {
		addr = ServantAddr2
	}
	os.Remove(addr)
	l, _ := net.Listen("unix", addr)

	//keep accepting client conn and connect local router to it
	for {
		conn, err := l.Accept()
		if err != nil {
			fmt.Println(err)
			continue
		}
		fmt.Println(s.name, "connect one client")

		_, err = s.Rot.ConnectRemote(conn, router.JsonMarshaling)
		if err != nil {
			fmt.Println(err)
			continue
		}
	}

	//in fact never reach here
	s.Rot.Close()
	l.Close()

	done <- true
}
