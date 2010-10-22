There are two implementations of "router" package; both provide the same user API.

branches/router1:
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
real	0m1.763s
user	0m0.540s
sys	0m1.220s

. pingpong2: Pinger/Ponger using channels connected thru one router:
real	0m8.851s
user	0m3.868s
sys	0m4.972s

. pingpong3: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are directly connected:
real	0m21.079s
user	0m9.765s
sys	0m11.169s

. pingpong4: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are connected thru unix socket:
real	0m49.062s
user	0m28.570s
sys	0m21.261s


trunk (router2):
. directly share memory among various goroutines for better efficiency, shared
  memory are protected by sync.Mutex
. router's routing-table, endpoint's binding_set, and proxy's RecvChanBundle and
  SendChanBundle are directly shared and locked.
. trunk/router2 is a bit more efficient than trunk/router1.

"ping-pong" tests: 
same setup as above

. pingpong1: using direct channels between Pinger and Ponger:
real	0m1.769s
user	0m0.584s
sys	0m1.204s

. pingpong2: Pinger/Ponger using channels connected thru one router:
real	0m4.243s
user	0m1.660s
sys	0m2.580s

. pingpong3: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are directly connected:
real	0m6.622s
user	0m2.704s
sys	0m3.876s

. pingpong4: Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are connected thru unix socket:
real	0m34.004s
user	0m20.505s
sys	0m14.929s

