include $(GOROOT)/src/Make.inc

# name of the package (library) being built
TARG=go-router.googlecode.com/svn/branches/router1/router

# source files in package
GOFILES=\
	router.go\
	id.go\
	endpoint.go\
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
