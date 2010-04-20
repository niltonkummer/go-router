There are two implementations of "router" package; both provide the same user API.

trunk/router1:
. strictly follow Go's idea of "Do not communicate by sharing memory; 
           instead, share memory by communicating."
. avoid directly sharing memory among goroutines (thus avoid locks/mutex); 
  each data has a owner and can only be changed by owner; 
  others have to talk to owners thru channels to access the data

"ping-pong" tests:
(bouncing 300K msgs between "Pinger" goroutine and "Ponger" goroutine)
test setup:
      machine: Pentium(R) Dual CPU E2160 @ 1.80GHz with 2G RAM
      GOMAXPROCS=32

. pingpong1: using direct channels between Pinger and Ponger:
real	0m1.747s
user	0m0.576s
sys	0m1.192s

. pingpong2: Pinger/Ponger using channels connected thru one router:
real	0m9.029s
user	0m3.780s
sys	0m5.192s

. pingpong3: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are directly connected:
real	0m27.252s
user	0m15.165s
sys	0m11.909s

. pingpong4: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are connected thru unix socket:
real	1m11.944s
user	0m49.243s
sys	0m23.149s


trunk/router2:
. directly share memory among various goroutines for better efficiency, shared
  memory are protected by sync.Mutex
. router's routing-table, endpoint's binding_set, and proxy's RecvChanBundle and
  SendChanBundle are directly shared and locked.
. trunk/router2 is a bit more efficient than trunk/router1.

"ping-pong" tests: 
same setup as above

. pingpong1: using direct channels between Pinger and Ponger:
real	0m1.731s
user	0m0.464s
sys	0m1.260s

. pingpong2: Pinger/Ponger using channels connected thru one router:
real	0m4.257s
user	0m1.564s
sys	0m2.652s

. pingpong3: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are directly connected:
real	0m6.608s
user	0m2.660s
sys	0m3.908s

. pingpong4: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are connected thru unix socket:
real	0m44.092s
user	0m31.618s
sys	0m15.197s

