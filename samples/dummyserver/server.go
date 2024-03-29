//
// Copyright (c) 2010 - 2011 Yigong Liu
//
// Distributed under New BSD License
//
package main

import (
	"router"
)

//define a filter to allow only heartbeat msgs between active and
//standby servants
type ServantFilter struct {
	allowedId string
}

func (f *ServantFilter) BlockInward(id0 router.Id) bool {
	id := id0.(*router.StrId)
	if id.Val == f.allowedId {
		return false
	}
	return true
}

func (f *ServantFilter) BlockOutward(id0 router.Id) bool {
	id := id0.(*router.StrId)
	if id.Val == f.allowedId {
		return false
	}
	return true
}

func main() {
	done := make(chan bool)
	//create active/standby servant
	activeServant := NewServant("servant1", Active, done)
	standbyServant := NewServant("servant2", Standby, done)
	//connect servants by connecting their proxies configured with filters
	filter := &ServantFilter{"/Sys/Ctrl/Heartbeat"} //only allow heartbeats between active/standby
	proxy1 := router.NewProxy(activeServant.Rot, "", filter, nil)
	proxy2 := router.NewProxy(standbyServant.Rot, "", filter, nil)
	proxy1.Connect(proxy2)
	//wait for servants to exit
	<-done
	<-done
}
