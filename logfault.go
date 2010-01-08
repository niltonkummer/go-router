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
	recved      bool
}

func newlogger(r Router, src string, bufSize int) *logger {
	logger := new(logger)
	logger.router = r
	logger.source = src
	logger.bindEvtChan = make(chan BindEvent, 1) //just need the latest binding event
	logger.logChan = make(chan *LogRecord, bufSize)
	err := logger.router.AttachSendChan(logger.router.SysID(SysLogId), logger.logChan, logger.bindEvtChan)
	if err != nil {
		//log.Stderr("failed to add logger for ", logger.source);
		return nil
	}
	//log.Stdout("add logger for ", logger.source);
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
	//l.router.DetachChan(l.router.SysID(SysLogId), l.logChan);
}

type Logger struct {
	router   *routerImpl
	logger   *logger
	sinkChan chan *LogRecord
	sinkExit chan bool
}

func (l *Logger) Init(r Router, src string, bufSize int, scope int) {
	l.router = r.(*routerImpl)
	if len(src) > 0 {
		l.logger = newlogger(l.router, src, bufSize)
		if scope >= ScopeGlobal && scope <= ScopeLocal {
			l.sinkExit = make(chan bool)
			l.sinkChan = make(chan *LogRecord, DefLogBufSize)
			l.runConsoleLogSink(scope)
		}
	}
}

func (l *Logger) CloseLogger() {
	if l.logger != nil {
		l.logger.Close()
		if l.sinkChan != nil {
			close(l.sinkChan)
			//wait for sink gorutine to exit
			<-l.sinkExit
		}
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

func (l *Logger) runConsoleLogSink(s int) {
	logId := l.router.NewSysID(SysLogId, s)
	err := l.router.AttachRecvChan(logId, l.sinkChan)
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

//Fault types
type FaultType int

const (
	IdTypeMismatch FaultType = iota
	ChanTypeMismatch
	AttachSendFailure
	AttachRecvFailure
	DetachFailure
	MarshalFailure
	DemarshalFailure
	//more ...
)

func (f FaultType) String() string {
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
	return "InvalidFaultType"
}

type FaultRecord struct {
	Type      FaultType
	Source    string
	Info      interface{}
	Timestamp int64
}

type faultRaiser struct {
	bindEvtChan chan BindEvent
	source      string
	faultChan   chan *FaultRecord
	router      *routerImpl
	caught      bool
}

func newfaultRaiser(r Router, src string, bufSize int) *faultRaiser {
	faultRaiser := new(faultRaiser)
	faultRaiser.router = r.(*routerImpl)
	faultRaiser.source = src
	faultRaiser.bindEvtChan = make(chan BindEvent, 1) //just need the latest binding event
	faultRaiser.faultChan = make(chan *FaultRecord, bufSize)
	err := faultRaiser.router.AttachSendChan(faultRaiser.router.SysID(SysFaultId), faultRaiser.faultChan, faultRaiser.bindEvtChan)
	if err != nil {
		log.Stderr("failed to add faultRaiser for ", faultRaiser.source)
		return nil
	}
	//log.Stdout("add faultRaiser for ", faultRaiser.source);
	return faultRaiser
}

func (l *faultRaiser) raise(p FaultType, msg interface{}) {
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
		log.Crashf("Crash at %v, %v", p, msg)
		return
	}

	lr := &FaultRecord{p, l.source, msg, time.Nanoseconds()}
	/*
		//when faultChan full, drop the oldest log record, avoid block / slow down app
		for !(l.faultChan <- lr) {
			<- l.faultChan;
		}
	*/
	//log all msgs even if too much log recrods may block / slow down app
	l.faultChan <- lr
}

func (l *faultRaiser) Close() {
	close(l.faultChan)
	//l.router.DetachChan(l.router.SysID(SysFaultId), l.faultChan);
}

type FaultRaiser struct {
	router      *routerImpl
	faultRaiser *faultRaiser
}

func (l *FaultRaiser) Init(r Router, src string, bufSize int) {
	l.router = r.(*routerImpl)
	if len(src) > 0 {
		l.faultRaiser = newfaultRaiser(l.router, l.router.name, bufSize)
	}
}

func (l *FaultRaiser) CloseFaultRaiser() {
	if l.faultRaiser != nil {
		l.faultRaiser.Close()
	}
}

func (r *FaultRaiser) Raise(p FaultType, msg interface{}) {
	if r.faultRaiser != nil {
		r.faultRaiser.raise(p, msg)
	} else {
		//r.router.Log(LOG_ERROR, fmt.Sprintf("Crash at %v, %v", p, msg))
		log.Crashf("Crash at %v, %v", p, msg)
	}
}
