There are two implementations of "router" package; both provide the same user API.

router1:
. strictly follow Go's idea of "Do not communicate by sharing memory; 
           instead, share memory by communicating."
. avoid sharing data among goroutines (thus avoid locks/mutex); 
  each data has a owner and can only be changed by owner; 
  others have to talk to owners thru channels to access the data
. although heavily depending on channel message passing, the implementation is
  reasonably efficient (goroutines/channels are fast), see the test data.
. there is one issue resulted from Go issue #536. 
  If a "select" waits on several channel recv/send operations, and
  msg flow rates are substantially different in diff channels, memory usage
  could climb up quickly. Currently add a kludge for this by sending dummy
  "GC" msgs to slow channels periodically.

"ping-pong" tests:
(bouncing 100K msgs between "Pinger" goroutine and "Ponger" goroutine)
test setup:
      machine: Pentium(R) Dual CPU E2160 @ 1.80GHz with 2G RAM
      GOMAXPROCS=32

. ping-pong using direct channels between Pinger and Ponger:
real	0m7.724s
user	0m2.280s
sys	0m1.032s

. Pinger/Ponger using channels connected thru one router:
real	0m7.641s
user	0m3.436s
sys	0m2.348s

. Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are directly connected:
real	0m11.497s
user	0m6.540s
sys	0m4.628s

. Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are connected thru unix socket:
real	0m26.834s
user	0m17.841s
sys	0m8.701s


router2:
. directly share data among various goroutines for better efficiency, shared
  data are protected by sync.Mutex
. router's routing-table, endpoint's binding_set, and proxy's RecvChanBundle and
  SendChanBundle are directly shared and locked.
. avoid Go issue #536.
. router2 is a bit more efficient than router1.

"ping-pong" tests: 
same setup as above

. ping-pong using direct channels between Pinger and Ponger:
real	0m7.698s
user	0m2.136s
sys	0m1.100s

. Pinger/Ponger using channels connected thru one router:
real	0m7.674s
user	0m2.664s
sys	0m1.472s

. Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are directly connected:
real	0m8.540s
user	0m5.992s
sys	0m2.312s

. Pinger's channels connected to router1 and Ponger's channels connected to
  router2 and router1/router2 are connected thru unix socket:
real	0m19.224s
user	0m13.073s
sys	0m5.540s
