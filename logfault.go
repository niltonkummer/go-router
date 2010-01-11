//
// Copyright (c) 2010 Yigong Liu
//
// Distributed under New BSD License
//
package router

import (
	"os"
	"time"
	"log"
	"fmt"
	//		"strconv"
)

//some basic msgs for log and fault
var (
	errIdTypeMismatch        = "id type mismatch"
	errNotChan               = "values other than channels cannot be attached to router"
	errInvalidBindChan       = "invalid channels for notifying sender/recver attachment"
	errChanTypeMismatch      = "channels of diff type attach to matched id"
	errDetachChanNotInRouter = "try to detach a channel which is not attached to router"
	errDupAttachment         = "a channel has been attached to same id more than once"
	errInvalidId             = "invalid id"
	errInvalidSysId          = "invalid index for System Id"

	errConnFail            = "remote conn failed, possibly router type mismatch"
	errConnInvalidMsg      = "remote conn failed, invalid msg transaction"
	errRmtIdTypeMismatch   = "remote conn failed, remote router id type mismatch"
	errRmtChanTypeMismatch = "remote conn failed, remote chan type mismatch"
	//...more
)

type LogPriority int

const (
	LOG_INFO LogPriority = iota
	LOG_DEBUG
	LOG_WARN
	LOG_ERROR
)

func (lp LogPriority) String() string {
	switch lp {
	case LOG_INFO:
		return "INFO"
	case LOG_DEBUG:
		return "DEBUG"
	case LOG_WARN:
		return "WARNING"
	case LOG_ERROR:
		return "ERROR"
	}
	return "InvalidLogType"
}

type LogRecord struct {
	Pri       LogPriority
	Source    string
	Info      interface{}
	Timestamp int64
}

type logger struct {
	bindEvtChan chan BindEvent
	source      string
	logChan     chan *LogRecord
	router      Router
	id          Id
	recved      bool
}

func newlogger(id Id, r Router, src string, bufSize int) *logger {
	logger := new(logger)
	logger.id = id
	logger.router = r
	logger.source = src
	logger.bindEvtChan = make(chan BindEvent, 1) //just need the latest binding event
	logger.logChan = make(chan *LogRecord, bufSize)
	err := logger.router.AttachSendChan(id, logger.logChan, logger.bindEvtChan)
	if err != nil {
		log.Crash("failed to add logger for ", logger.source);
		return nil
	}
	return logger
}

func (l *logger) log(p LogPriority, msg interface{}) {
	if len(l.bindEvtChan) > 0 {
		be := <-l.bindEvtChan
		if be.Count > 0 {
			l.recved = true
		} else {
			l.recved = false
		}
	}

	if !l.recved {
		return
	}

	lr := &LogRecord{p, l.source, msg, time.Nanoseconds()}
	/*
		//when logChan full, drop the oldest log record, avoid block / slow down app
		for !(l.logChan <- lr) {
			<- l.logChan;
		}
	*/
	//log all msgs even if too much log recrods may block / slow down app
	l.logChan <- lr
}

func (l *logger) Close() {
	close(l.logChan)
	//l.router.DetachChan(l.id, l.logChan);
}

//a wrapper for embed
type Logger struct {
	router   *routerImpl
	logger   *logger
}

func NewLogger(id Id, r Router, src string, bufSize int) *Logger {
	return new(Logger).Init(id, r, src, bufSize)
}

func (l *Logger) Init(id Id, r Router, src string, bufSize int) *Logger {
	l.router = r.(*routerImpl)
	if len(src) > 0 {
		l.logger = newlogger(id, l.router, src, bufSize)
	}
	return l
}

func (l *Logger) Close() {
	if l.logger != nil {
		l.logger.Close()
	}
}

func (r *Logger) Log(p LogPriority, msg interface{}) {
	if r.logger != nil {
		r.logger.log(p, msg)
	}
}

func (r *Logger) LogError(err os.Error) {
	if r.logger != nil {
		r.logger.log(LOG_ERROR, err)
	}
}

//a simple log sink
type LogSink struct {
	sinkChan chan *LogRecord
	sinkExit chan bool
}

func NewLogSink(id Id, r Router) *LogSink {
	return new(LogSink).Init(id, r)
}

func (l *LogSink) Init(id Id, r Router) *LogSink {
			l.sinkExit = make(chan bool)
			l.sinkChan = make(chan *LogRecord, DefLogBufSize)
			l.runConsoleLogSink(id, r)
	return l
}

func (l *LogSink) Close() {
		if l.sinkChan != nil {
			close(l.sinkChan)
			//wait for sink gorutine to exit
			<-l.sinkExit
		}
}

func (l *LogSink) runConsoleLogSink(logId Id, r Router) {
	err := r.AttachRecvChan(logId, l.sinkChan)
	if err != nil {
		log.Stderr("*** failed to enable router's console log sink ***")
		return
	}
	go func() {
		for {
			lr := <-l.sinkChan
			if closed(l.sinkChan) {
				break
			}
			//convert timestamp, following format/code of package "log" for consistency
			/*
				ts := ""
					itoa := strconv.Itoa;
					t := time.SecondsToLocalTime(lr.Timestamp / 1e9)
					ts += itoa(int(t.Year)) + "/" + itoa(t.Month) + "/" + itoa(t.Day) + " "
					ts += itoa(t.Hour) + ":" + itoa(t.Minute) + ":" + itoa(t.Second)
			*/

			switch lr.Pri {
			case LOG_ERROR:
				fallthrough
			case LOG_WARN:
				//log.Stderrf("[%s %v %v] %v", lr.Source, lr.Pri, ts, lr.Info);
				log.Stderrf("[%s %v] %v", lr.Source, lr.Pri, lr.Info)
			case LOG_DEBUG:
				fallthrough
			case LOG_INFO:
				//log.Stdoutf("[%s %v %v] %v", lr.Source, lr.Pri, ts, lr.Info);
				log.Stdoutf("[%s %v] %v", lr.Source, lr.Pri, lr.Info)
			}
		}
		l.sinkExit <- true
		log.Stderr("console log goroutine exits")
	}()
	//err = r.DetachChan(logId, l.sinkChan);
}


//Fault management

//Fault types: faults under the same fault id can be further divided into diff types
//here are fault types for router internal implementation. 
//apps can have their own types, and app should provide its own func to map fault to string
const (
	IdTypeMismatch = iota
	ChanTypeMismatch
	AttachSendFailure
	AttachRecvFailure
	DetachFailure
	MarshalFailure
	DemarshalFailure
	//more ...
)

func faultTypeString(f int) string {
	switch f {
	case IdTypeMismatch:
		return "IdTypeMismatch"
	case ChanTypeMismatch:
		return "ChanTypeMismatch"
	case AttachSendFailure:
		return "AttachSendFailure"
	case AttachRecvFailure:
		return "AttachRecvFailure"
	case DetachFailure:
		return "DetachFailure"
	case MarshalFailure:
		return "MarshalFailure"
	case DemarshalFailure:
		return "DemarshalFailure"
	}
	return fmt.Sprintf("_FaultType_%d_", f)
}

type FaultRecord struct {
	Type      int
	Source    string
	Info      interface{}
	Timestamp int64
}

type faultRaiser struct {
	bindEvtChan chan BindEvent
	source      string
	typeString  func(int)string
	faultChan   chan *FaultRecord
	router      *routerImpl
	id          Id
	caught      bool
}

func newfaultRaiser(id Id, r Router, src string, bufSize int, ts func(int)string) *faultRaiser {
	faultRaiser := new(faultRaiser)
	faultRaiser.id = id
	faultRaiser.router = r.(*routerImpl)
	faultRaiser.source = src
	faultRaiser.typeString = ts
	faultRaiser.bindEvtChan = make(chan BindEvent, 1) //just need the latest binding event
	faultRaiser.faultChan = make(chan *FaultRecord, bufSize)
	err := faultRaiser.router.AttachSendChan(id, faultRaiser.faultChan, faultRaiser.bindEvtChan)
	if err != nil {
		log.Stderr("failed to add faultRaiser for [%v, %v]", faultRaiser.source, id)
		return nil
	}
	//log.Stdout("add faultRaiser for ", faultRaiser.source);
	return faultRaiser
}

func (l *faultRaiser) raise(p int, msg interface{}) {
	if len(l.bindEvtChan) > 0 {
		be := <-l.bindEvtChan
		if be.Count > 0 {
			l.caught = true
		} else {
			l.caught = false
		}
	}

	if !l.caught {
		//l.router.Log(LOG_ERROR, fmt.Sprintf("Crash at %v, %v", p, msg))
		log.Crashf("Crash at %v, %v", l.typeString(p), msg)
		return
	}

	lr := &FaultRecord{p, l.source, msg, time.Nanoseconds()}

	//send all msgs which are too important to lose
	l.faultChan <- lr
}

func (l *faultRaiser) Close() {
	close(l.faultChan)
	//l.router.DetachChan(l.id, l.faultChan);
}

//a wrapper for embed
type FaultRaiser struct {
	router      *routerImpl
	faultRaiser *faultRaiser
}

func NewFaultRaiser(id Id, r Router, src string, bufSize int, ts func(int)string) *FaultRaiser {
	return new(FaultRaiser).Init(id, r, src, bufSize, ts)
}

func (l *FaultRaiser) Init(id Id, r Router, src string, bufSize int, ts func(int)string) *FaultRaiser {
	l.router = r.(*routerImpl)
	if len(src) > 0 {
		l.faultRaiser = newfaultRaiser(id, r, src, bufSize, ts)
	}
	return l
}

func (l *FaultRaiser) Close() {
	if l.faultRaiser != nil {
		l.faultRaiser.Close()
	}
}

func (r *FaultRaiser) Raise(p int, msg interface{}) {
	if r.faultRaiser != nil {
		r.faultRaiser.raise(p, msg)
	} else {
		//r.router.Log(LOG_ERROR, fmt.Sprintf("Crash at %v, %v", p, msg))
		log.Crashf("Crash at %v, %v", faultTypeString(p), msg)
	}
}
