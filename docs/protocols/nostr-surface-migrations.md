# Nostr surface migration notes

## NIP-90

Treat NIP-90 as a compatibility surface rather than the default abstraction for new work. Migrate individual data-vending-machine use cases to their applicable microstandards, preserving legacy event ingestion during a documented transition window. Do not introduce a single replacement RPC-style layer.

## NIP-96

Make Blossom the default upload and media-storage path for new configuration. Keep NIP-96 as an explicitly selected compatibility path while existing deployments migrate server discovery, authorization, upload, and deletion workflows. Advertised capability metadata must distinguish the configured default from compatibility support.
