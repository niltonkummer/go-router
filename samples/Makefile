# defines $(GC) (compiler), $(LD) (linker) and $(O) (architecture)
include $(GOROOT)/src/Make.inc

# build executables
all: test chatcli chatsrv pingpong1 pingpong2 pingpong3 pingpong4 pingpong5 client server

test: package
	$(GC) -I../_obj test.go
	$(LD) -L../_obj -o $@ test.$O
	@echo "Done. Executable is: $@"

chatcli: package
	$(GC) -I../_obj chatcli.go
	$(LD) -L../_obj -o $@ chatcli.$O
	@echo "Done. Executable is: $@"

chatsrv: package
	$(GC) -I../_obj chatsrv.go
	$(LD) -L../_obj -o $@ chatsrv.$O
	@echo "Done. Executable is: $@"

pingpong1: package
	$(GC) -I../_obj pingpong1.go
	$(LD) -L../_obj -o $@ pingpong1.$O
	@echo "Done. Executable is: $@"

pingpong2: package
	$(GC) -I../_obj pingpong2.go
	$(LD) -L../_obj -o $@ pingpong2.$O
	@echo "Done. Executable is: $@"

pingpong3: package
	$(GC) -I../_obj pingpong3.go
	$(LD) -L../_obj -o $@ pingpong3.$O
	@echo "Done. Executable is: $@"

pingpong4: package
	$(GC) -I../_obj pingpong4.go
	$(LD) -L../_obj -o $@ pingpong4.$O
	@echo "Done. Executable is: $@"

pingpong5: package
	$(GC) -I../_obj pingpong5.go
	$(LD) -L../_obj -o $@ pingpong5.$O
	@echo "Done. Executable is: $@"

client: package
	$(GC) -I../_obj dummyserver/client.go
	$(LD) -L../_obj -o $@ client.$O
	@echo "Done. Executable is: $@"

DummyServerFiles=\
	dummyserver/server.go\
	dummyserver/servant.go\
	dummyserver/dbtask.go\
	dummyserver/faultmgrtask.go\
	dummyserver/svctask.go\
	dummyserver/sysmgrtask.go\

server: package
	$(GC) -I../_obj -o server.$O $(DummyServerFiles)
	$(LD) -L../_obj -o $@ server.$O
	@echo "Done. Executable is: $@"

clean:
	rm -rf *.[$(OS)o] *.a [$(OS)].out _obj _test _testmain.go main test chatcli chatsrv pingpong1 pingpong2 pingpong3 pingpong4 pingpong5 client server

package: ../_obj/router.a


# compile
_go_.$O: $(GOFILES)
	$(GC) -o $@ $(GOFILES)

_dummy_.$O: $(DummyServerFiles)
	$(GC) -I../_obj -o $@ $(DummyServerFiles)
