package service

// Runtime capacity is acknowledged by gateway protocol v3. A nil value denotes
// an older gateway; it must never be presented as an applied unlimited policy.
type BridgePoolConfig struct {
	Global          int `json:"global"`
	PerAccount      int `json:"per_account"`
	Queue           int `json:"queue"`
	PerAccountQueue int `json:"per_account_queue"`
	WaitMS          int `json:"wait_ms"`
}
type BridgeCapacityConfig struct {
	Upload                    BridgePoolConfig `json:"upload"`
	Download                  BridgePoolConfig `json:"download"`
	BandwidthBytesPerSecond   int64            `json:"bandwidth_bytes_per_second"`
	APIReservedBytesPerSecond int64            `json:"api_reserved_bytes_per_second"`
}

func (p BridgePoolConfig) valid() bool {
	return p.Global >= 1 && p.Global <= 32 && p.PerAccount >= 1 && p.PerAccount <= p.Global &&
		p.Queue >= 0 && p.Queue <= 64 && p.PerAccountQueue >= 0 && p.PerAccountQueue <= p.Queue &&
		p.WaitMS >= 0 && p.WaitMS <= 3000 && ((p.Queue == 0 && p.PerAccountQueue == 0 && p.WaitMS == 0) ||
		(p.Queue > 0 && p.PerAccountQueue > 0 && p.WaitMS > 0))
}
func (c BridgeCapacityConfig) valid() bool {
	return c.Upload.valid() && c.Download.valid() && c.Upload.Global+c.Download.Global <= 48 &&
		c.BandwidthBytesPerSecond >= 128<<10 && c.BandwidthBytesPerSecond <= 1<<30 &&
		c.APIReservedBytesPerSecond >= 64<<10 && c.APIReservedBytesPerSecond <= c.BandwidthBytesPerSecond-(64<<10)
}

type BridgePoolStatus struct {
	Active    int    `json:"active"`
	Queued    int    `json:"queued"`
	Rejected  uint64 `json:"rejected"`
	TimedOut  uint64 `json:"timed_out"`
	Cancelled uint64 `json:"cancelled"`
	Draining  bool   `json:"draining"`
}
type BridgeCapacityStatus struct {
	Upload   BridgePoolStatus `json:"upload"`
	Download BridgePoolStatus `json:"download"`
}
