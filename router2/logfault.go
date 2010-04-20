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
	//	"fmt"
	//		"strconv"
)

//Some basic error msgs for log and fault
var (
	errIdTypeMismatch        = "id type mismatch"
	errInvalidChan           = "invalid channels be attached to router; valid channel types: chan bool/int/float/string/*struct"
	errInvalidBindChan       = "invalid channels for notifying sender/recver attachment"
	errChanTypeMismatch      = "channels of diff type attach to matched id"
	errChanGenericType       = "channels of gnericMsg attached as first"
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

//LogRecord stores the log information
type LogRecord struct {
	Pri       LogPriority
	Source    string
	Info      interface{}
	Timestamp int64
}

type logger struct {
	endp        *Endpoint
	source      string
	logChan     chan *LogRecord
	router      Router
	id          Id
}

func newlogger(id Id, r Router, src string, bufSize int) *logger {
	logger := new(logger)
	logger.id = id
	logger.router = r
	logger.source = src
	logger.logChan = make(chan *LogRecord, bufSize)
	var err os.Error
	logger.endp, err = logger.router.AttachSendChan(id, logger.logChan)
	if err != nil {
		log.Crash("failed to add logger for ", logger.source)
		return nil
	}
	return logger
}

func (l *logger) log(p LogPriority, msg interface{}) {
	if l.endp.NumBindings() == 0 {
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

//Logger can be embedded into user structs / types, which then can use Log() / LogError() directly
type Logger struct {
	router *routerImpl
	logger *logger
}

//NewLogger will create a Logger object which sends log messages thru id in router "r"
func NewLogger(id Id, r Router, src string) *Logger {
	return new(Logger).Init(id, r, src)
}

func (l *Logger) Init(id Id, r Router, src string) *Logger {
	l.router = r.(*routerImpl)
	if len(src) > 0 {
		l.logger = newlogger(id, l.router, src, DefLogBufSize)
	}
	return l
}

func (l *Logger) Close() {
	if l.logger != nil {
		l.logger.Close()
	}
}

//send a log record to log id in router
func (r *Logger) Log(p LogPriority, msg interface{}) {
	if r.logger != nil {
		r.logger.log(p, msg)
	}
}

//send a log record and store error info in it
func (r *Logger) LogError(err os.Error) {
	if r.logger != nil {
		r.logger.log(LOG_ERROR, err)
	}
}

//A simple log sink, showing log messages in console.
type LogSink struct {
	sinkChan chan *LogRecord
	sinkExit chan bool
}

//create a new log sink, which receives log messages from id in router "r"
func NewLogSink(id Id, r Router) *LogSink { return new(LogSink).Init(id, r) }

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
	_, err := r.AttachRecvChan(logId, l.sinkChan)
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

//FaultRecord records some details about fault
type FaultRecord struct {
	Source    string
	Info      os.Error
	Timestamp int64
}

type faultRaiser struct {
	endp        *Endpoint
	source      string
	faultChan   chan *FaultRecord
	router      *routerImpl
	id          Id
	caught      bool
}

func newfaultRaiser(id Id, r Router, src string, bufSize int) *faultRaiser {
	faultRaiser := new(faultRaiser)
	faultRaiser.id = id
	faultRaiser.router = r.(*routerImpl)
	faultRaiser.source = src
	faultRaiser.faultChan = make(chan *FaultRecord, bufSize)
	var err os.Error
	faultRaiser.endp, err = faultRaiser.router.AttachSendChan(id, faultRaiser.faultChan)
	if err != nil {
		log.Stderr("failed to add faultRaiser for [%v, %v]", faultRaiser.source, id)
		return nil
	}
	//log.Stdout("add faultRaiser for ", faultRaiser.source);
	return faultRaiser
}

func (l *faultRaiser) raise(msg os.Error) {
	if l.endp.NumBindings() == 0 {
		//l.router.Log(LOG_ERROR, fmt.Sprintf("Crash at %v", msg))
		log.Crashf("Crash at %v", msg)
		return
	}

	lr := &FaultRecord{l.source, msg, time.Nanoseconds()}

	//send all msgs which are too important to lose
	l.faultChan <- lr
}

func (l *faultRaiser) Close() {
	close(l.faultChan)
	//l.router.DetachChan(l.id, l.faultChan);
}

//FaultRaiser can be embedded into user structs/ types, which then can call Raise() directly
type FaultRaiser struct {
	router      *routerImpl
	faultRaiser *faultRaiser
}

//create a new FaultRaiser to send FaultRecords to id in router "r"
func NewFaultRaiser(id Id, r Router, src string) *FaultRaiser {
	return new(FaultRaiser).Init(id, r, src)
}

func (l *FaultRaiser) Init(id Id, r Router, src string) *FaultRaiser {
	l.router = r.(*routerImpl)
	if len(src) > 0 {
		l.faultRaiser = newfaultRaiser(id, r, src, DefCmdChanBufSize)
	}
	return l
}

func (l *FaultRaiser) Close() {
	if l.faultRaiser != nil {
		l.faultRaiser.Close()
	}
}

//raise a fault - send a FaultRecord to faultId in router
func (r *FaultRaiser) Raise(msg os.Error) {
	if r.faultRaiser != nil {
		r.faultRaiser.raise(msg)
	} else {
		//r.router.Log(LOG_ERROR, fmt.Sprintf("Crash at %v", msg))
		log.Crashf("Crash at %v", msg)
	}
}
