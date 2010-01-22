package main

import (
	"fmt"
	"strings"
	"strconv"
	"flag"
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

func newPinger(pingChan chan<- string, pongChan <-chan string, done chan<- bool, numRuns int) {
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

func newPonger(pongChan chan<- string, pingChan <-chan string, done chan<- bool) {
	//start ponger
	pong := &Ponger{pongChan, pingChan, done, 0}
	go pong.Run()
}

func main() {
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Println("Usage: pingpong1 num_runs")
		return
	}
	numRuns, _ := strconv.Atoi(flag.Arg(0))
	//alloc comm chans between Pinger and Ponger
	pingChan := make(chan string)
	pongChan := make(chan string)
	done := make(chan bool)
	//hook up Pinger and Ponger
	newPinger(pingChan, pongChan, done, numRuns)
	newPonger(pongChan, pingChan, done)
	//wait for ping-pong to finish
	<-done
	<-done
}
