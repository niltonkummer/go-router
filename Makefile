include $(GOROOT)/src/Make.inc

# name of the package (library) being built
TARG=router

# source files in package
GOFILES=\
	router.go\
	id.go\
	chans.go\
	routedchan.go\
	dispatcher.go\
	notifier.go\
	proxy.go\
	msg.go\
	logfault.go\
	utils.go\
	marshaler.go\
	stream.go\
	filtrans.go\

include $(GOROOT)/src/Make.pkg
