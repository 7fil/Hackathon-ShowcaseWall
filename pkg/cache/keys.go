package cache

// Redis keys and channels shared by the modules that invalidate the public
// cache or publish realtime events. The realtime hub subscribes to WSChannel.
const (
	// WorksVersionKey is the counter that versions every public gallery key.
	WorksVersionKey = "works:version"
	// WorkKeyPrefix prefixes the cached public detail of one work ("work:{id}").
	WorkKeyPrefix = "work:"
	// WSSeqKey is the counter that numbers realtime events.
	WSSeqKey = "ws:seq"
	// WSChannel is the pub/sub channel carrying serialized realtime events.
	WSChannel = "ws:wall:events"
)
