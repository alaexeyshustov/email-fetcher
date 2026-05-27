package fanout

// Fanout fans requests out to multiple provider.Provider instances concurrently,
// merges results, and surfaces partial failures in gRPC trailing metadata
// (x-failed-providers, x-provider-errors) rather than aborting the stream.
type Fanout struct{}
