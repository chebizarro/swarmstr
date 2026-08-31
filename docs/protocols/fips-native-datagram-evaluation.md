# FIPS native pubkey-datagram evaluation

Status: evaluated, not adopted.

The native pubkey-datagram API is a useful future transport surface because it avoids assigning application TCP ports and addresses peers directly by Nostr public key. It is not suitable for ACP transport today.

ACP depends on an ordered, reliable byte stream: request framing must not be reordered or silently dropped, delivery failures must be observable, and reconnects must not turn partial frames into successful task completion. The native API currently exposes datagram semantics and does not provide the versioned acknowledgement, retransmission, ordering, flow-control, and replay rules needed to preserve that contract.

Swarmstr therefore keeps ACP on TCP over the FIPS TUN interface. Daemon status and diagnostic probes may influence transport diagnosis and selection, but they never signal ACP task completion. Reconsider native datagrams only after a versioned reliability layer specifies ordering, acknowledgement/retry, duplicate suppression, backpressure, maximum message size/fragmentation, and reconnect behavior.
