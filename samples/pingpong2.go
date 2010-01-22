package main

import (
	"fmt"
	"strings"
	"strconv"
	"flag"
	"router"
)

//pinger: send to ping chan, recv from pong chan
type Pinger struct {
	//Pinger's public interface
	pingChan chan<- string
	pongChan <-chan string
	done     chan<- bool
	//Pinger's private state
	numRuns int //how many times should we ping-pong
	count   int
}

func (p *Pinger) Run() {
	p.count = 0
	for v := range p.pongChan {
		fmt.Println("Pinger recv: ", v)
		ind := strings.Index(v, ":")
		p.count, _ = strconv.Atoi(v[ind+1:])
		if p.count > p.numRuns {
			break
		}
		p.count++
		p.pingChan <- fmt.Sprintf("hello from Pinger :%d", p.count)
	}
	close(p.pingChan)
	p.done <- true
}

func newPinger(rot router.Router, done chan<- bool, numRuns int) {
	//attach chans to router
	pingChan := make(chan string)
	pongChan := make(chan string)
	rot.AttachSendChan(router.StrID("ping"), pingChan)
	rot.AttachRecvChan(router.StrID("pong"), pongChan)
	//start pinger
	ping := &Pinger{pingChan, pongChan, done, numRuns, 0}
	go ping.Run()
}

//ponger: send to pong chan, recv from ping chan
type Ponger struct {
	//Ponger's public interface
	pongChan chan<- string
	pingChan <-chan string
	done     chan<- bool
	//Ponger's private state
	count int
}

func (p *Ponger) Run() {
	p.count = 0
	p.pongChan <- fmt.Sprintf("hello from Ponger :%d", p.count)
	for v := range p.pingChan {
		fmt.Println("Ponger recv: ", v)
		ind := strings.Index(v, ":")
		p.count, _ = strconv.Atoi(v[ind+1:])
		p.count++
		p.pongChan <- fmt.Sprintf("hello from Ponger :%d", p.count)
	}
	close(p.pongChan)
	p.done <- true
}

func newPonger(rot router.Router, done chan<- bool) {
	//attach chans to router
	pingChan := make(chan string)
	pongChan := make(chan string)
	bindChan := make(chan router.BindEvent, 1)
	rot.AttachSendChan(router.StrID("pong"), pongChan, bindChan)
	rot.AttachRecvChan(router.StrID("ping"), pingChan)
	//wait for pinger connecting
	for {
		if (<-bindChan).Count > 0 {
			break
		}
	}
	//start ponger
	pong := &Ponger{pongChan, pingChan, done, 0}
	go pong.Run()
}

func main() {
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Println("Usage: pingpong2 num_runs")
		return
	}
	numRuns, _ := strconv.Atoi(flag.Arg(0))
	//alloc a router to connect Pinger and Ponger
	rot := router.New(router.StrID(), 32, router.BroadcastPolicy)
	done := make(chan bool)
	//hook up Pinger and Ponger
	newPinger(rot, done, numRuns)
	newPonger(rot, done)
	//wait for ping-pong to finish
	<-done
	<-done
}
