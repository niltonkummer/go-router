There are two implementations of "router" package; both provide the same user API.

trunk/router1:
. strictly follow Go's idea of "Do not communicate by sharing memory; 
           instead, share memory by communicating."
. avoid directly sharing memory among goroutines (thus avoid locks/mutex); 
  each data has a owner and can only be changed by owner; 
  others have to talk to owners thru channels to access the data
. although heavily depending on channel message passing, the implementation is
  reasonably efficient (goroutines/channels are fast), see the test data.

"ping-pong" tests:
(bouncing 100K msgs between "Pinger" goroutine and "Ponger" goroutine)
test setup:
      machine: Pentium(R) Dual CPU E2160 @ 1.80GHz with 2G RAM
      GOMAXPROCS=32

. pingpong1: using direct channels between Pinger and Ponger:
real	0m7.724s
user	0m2.280s
sys	0m1.032s

. pingpong2: Pinger/Ponger using channels connected thru one router:
real	0m7.641s
user	0m3.436s
sys	0m2.348s

. pingpong3: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are directly connected:
real	0m11.497s
user	0m6.540s
sys	0m4.628s

. pingpong4: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are connected thru unix socket:
real	0m26.834s
user	0m17.841s
sys	0m8.701s


trunk/router2:
. directly share memory among various goroutines for better efficiency, shared
  memory are protected by sync.Mutex
. router's routing-table, endpoint's binding_set, and proxy's RecvChanBundle and
  SendChanBundle are directly shared and locked.
. trunk/router2 is a bit more efficient than trunk/router1.

"ping-pong" tests: 
same setup as above

. pingpong1: using direct channels between Pinger and Ponger:
real	0m7.698s
user	0m2.136s
sys	0m1.100s

. pingpong2: Pinger/Ponger using channels connected thru one router:
real	0m7.674s
user	0m2.664s
sys	0m1.472s

. pingpong3: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are directly connected:
real	0m8.540s
user	0m5.992s
sys	0m2.312s

. pingpong4: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are connected thru unix socket:
real	0m19.224s
user	0m13.073s
sys	0m5.540s
