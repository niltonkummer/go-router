//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"fmt"
)

type notifyChansPerScope struct {
	chans [4]chan *IdChanInfoMsg
	flags [4]bool
}

func (n *notifyChansPerScope) Close(scope int, member int, r *routerImpl) {
	for i := 0; i < 4; i++ {
		if n.chans[i] != nil {
			close(n.chans[i])
			//r.DetachChan(r.NewSysID(PubId+i, scope, member), n.chans[i]);
		}
	}
}

func newNotifyChansPerScope(scope int, member int, r *routerImpl) *notifyChansPerScope {
	nc := new(notifyChansPerScope)
	for i := 0; i < 4; i++ {
		nc.chans[i] = make(chan *IdChanInfoMsg, r.defChanBufSize)
		r.AttachSendChan(r.NewSysID(PubId+i, scope, member), nc.chans[i])
	}
	return nc
}

type notifier struct {
	router      *routerImpl
	notifyChans [NumScope]*notifyChansPerScope
}

func newNotifier(s *routerImpl) *notifier {
	n := new(notifier)
	n.router = s
	for i := 0; i < int(NumScope); i++ {
		n.notifyChans[i] = newNotifyChansPerScope(i, MemberLocal, n.router)
	}
	return n
}

func (n *notifier) Close() {
	for i := 0; i < int(NumScope); i++ {
		n.notifyChans[i].Close(i, MemberLocal, n.router)
	}
}

//the following are only called by router mainLoop, mostly in another goroutine
//all flags (notifChansAttached, notifChans.flags[] are accessed from router mainLoop goroutine,
//there should be no memory consistency issue ??sure? should switch to using a separate goroutine for notifier?

func (n *notifier) setFlag(id Id, sysIdx int, flag bool) {
	n.notifyChans[id.Scope()].flags[sysIdx-PubId] = flag
}

func (n notifier) notifyPub(info *IdChanInfo) {
	n.router.Log(LOG_INFO, fmt.Sprintf("notifyPub: %v", info.Id))
	if n.notifyChans[info.Id.Scope()].flags[0] {
		ok := n.notifyChans[info.Id.Scope()].chans[0] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}}
		if !ok {
			go func() { n.notifyChans[info.Id.Scope()].chans[0] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}} }()
		}
	}
}

func (n notifier) notifyUnPub(info *IdChanInfo) {
	n.router.Log(LOG_INFO, fmt.Sprintf("notifyUnPub: %v", info.Id))
	if n.notifyChans[info.Id.Scope()].flags[1] {
		ok := n.notifyChans[info.Id.Scope()].chans[1] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}}
		if !ok {
			go func() { n.notifyChans[info.Id.Scope()].chans[1] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}} }()
		}
	}
}

func (n notifier) notifySub(info *IdChanInfo) {
	n.router.Log(LOG_INFO, fmt.Sprintf("notifySub: %v", info.Id))
	if n.notifyChans[info.Id.Scope()].flags[2] {
		ok := n.notifyChans[info.Id.Scope()].chans[2] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}}
		if !ok {
			go func() { n.notifyChans[info.Id.Scope()].chans[2] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}} }()
		}
	}
}

func (n notifier) notifyUnSub(info *IdChanInfo) {
	n.router.Log(LOG_INFO, fmt.Sprintf("notifySub: %v", info.Id))
	if n.notifyChans[info.Id.Scope()].flags[3] {
		ok := n.notifyChans[info.Id.Scope()].chans[3] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}}
		if !ok {
			go func() { n.notifyChans[info.Id.Scope()].chans[3] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}} }()
		}
	}
}
