# prerequisite: GOROOT and GOARCH must be defined

# defines $(GC) (compiler), $(LD) (linker) and $(O) (architecture)
include $(GOROOT)/src/Make.$(GOARCH)

# name of the package (library) being built
TARG=router

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

# test files for this package
GOTESTFILES=


# build executables
all: test1 chatcli chatsrv pingpong1 pingpong2 pingpong3 pingpong4 client server
test1: package
	$(GC) -I_obj test1.go
	$(LD) -L_obj -o $@ test1.$O
	@echo "Done. Executable is: $@"

chatcli: package
	$(GC) -I_obj samples/chatcli.go
	$(LD) -L_obj -o $@ chatcli.$O
	@echo "Done. Executable is: $@"

chatsrv: package
	$(GC) -I_obj samples/chatsrv.go
	$(LD) -L_obj -o $@ chatsrv.$O
	@echo "Done. Executable is: $@"

pingpong1: package
	$(GC) -I_obj samples/pingpong1.go
	$(LD) -L_obj -o $@ pingpong1.$O
	@echo "Done. Executable is: $@"

pingpong2: package
	$(GC) -I_obj samples/pingpong2.go
	$(LD) -L_obj -o $@ pingpong2.$O
	@echo "Done. Executable is: $@"

pingpong3: package
	$(GC) -I_obj samples/pingpong3.go
	$(LD) -L_obj -o $@ pingpong3.$O
	@echo "Done. Executable is: $@"

pingpong4: package
	$(GC) -I_obj samples/pingpong4.go
	$(LD) -L_obj -o $@ pingpong4.$O
	@echo "Done. Executable is: $@"

client: package
	$(GC) -I_obj samples/dummyserver/client.go
	$(LD) -L_obj -o $@ client.$O
	@echo "Done. Executable is: $@"

DummyServerFiles=\
	samples/dummyserver/server.go\
	samples/dummyserver/servant.go\
	samples/dummyserver/dbtask.go\
	samples/dummyserver/faultmgrtask.go\
	samples/dummyserver/svctask.go\
	samples/dummyserver/sysmgrtask.go\

server: package
	$(GC) -I_obj -o server.$O $(DummyServerFiles)
	$(LD) -L_obj -o $@ server.$O
	@echo "Done. Executable is: $@"

clean:
	rm -rf *.[$(OS)o] *.a [$(OS)].out _obj _test _testmain.go main test1 chatcli chatsrv pingpong1 pingpong2 pingpong3 pingpong4 client server

package: _obj/$(TARG).a


# create a Go package file (.a)
_obj/$(TARG).a: _go_.$O
	@mkdir -p _obj/$(dir)
	rm -f _obj/$(TARG).a
	gopack grc $@ _go_.$O

# create Go package for for tests
_test/$(TARG).a: _gotest_.$O
	@mkdir -p _test/$(dir)
	rm -f _test/$(TARG).a
	gopack grc $@ _gotest_.$O

# compile
_go_.$O: $(GOFILES)
	$(GC) -o $@ $(GOFILES)

_dummy_.$O: $(DummyServerFiles)
	$(GC) -I_obj -o $@ $(DummyServerFiles)

# compile tests
_gotest_.$O: $(GOFILES) $(GOTESTFILES)
	$(GC) -o $@ $(GOFILES) $(GOTESTFILES)


# targets needed by gotest

importpath:
	@echo $(TARG)

testpackage: _test/$(TARG).a

testpackage-clean:
	rm -f _test/$(TARG).a _gotest_.$O
