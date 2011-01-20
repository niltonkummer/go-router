//
// Copyright (c) 2010 - 2011 Yigong Liu
//
// Distributed under New BSD License
//

package router

import (
	"fmt"
)

type notifyChansPerScope struct {
	chans   [4]chan *IdChanInfoMsg
	routChs [4]*RoutedChan
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
		nc.routChs[i], _ = r.AttachSendChan(r.NewSysID(PubId+i, scope, member), nc.chans[i])
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

func (n notifier) notifyPub(info *IdChanInfo) {
	n.router.Log(LOG_INFO, fmt.Sprintf("notifyPub: %v", info.Id))
	nc := n.notifyChans[info.Id.Scope()]
	if nc.routChs[0].NumPeers() > 0 {
		ok := nc.chans[0] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}}
		if !ok {
			go func() { nc.chans[0] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}} }()
		}
	}
}

func (n notifier) notifyUnPub(info *IdChanInfo) {
	n.router.Log(LOG_INFO, fmt.Sprintf("notifyUnPub: %v", info.Id))
	nc := n.notifyChans[info.Id.Scope()]
	if nc.routChs[1].NumPeers() > 0 {
		ok := nc.chans[1] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}}
		if !ok {
			go func() { nc.chans[1] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}} }()
		}
	}
}

func (n notifier) notifySub(info *IdChanInfo) {
	n.router.Log(LOG_INFO, fmt.Sprintf("notifySub: %v", info.Id))
	nc := n.notifyChans[info.Id.Scope()]
	if nc.routChs[2].NumPeers() > 0 {
		ok := nc.chans[2] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}}
		if !ok {
			go func() { nc.chans[2] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}} }()
		}
	}
}

func (n notifier) notifyUnSub(info *IdChanInfo) {
	n.router.Log(LOG_INFO, fmt.Sprintf("notifyUnSub: %v", info.Id))
	nc := n.notifyChans[info.Id.Scope()]
	if nc.routChs[3].NumPeers() > 0 {
		ok := nc.chans[3] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}}
		if !ok {
			go func() { nc.chans[3] <- &IdChanInfoMsg{Info: []*IdChanInfo{info}} }()
		}
	}
}
