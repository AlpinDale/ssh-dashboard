package base

// a single GPU/accelerator in vendor-neutral terms
type Device struct {
	Index       int
	Name        string
	VRAMTotal   int // MB
	VRAMUsed    int // MB
	Utilization int // percentage
	PowerDraw   int // watts
	PowerLimit  int // watts
	Temperature int // celsius
	TempLimit   int // celsius (throttle/limit)
	Driver      string
	Vendor      string
}

type RunCmdFunc func(string) (string, error)

type Provider interface {
	// returns the vendor name (e.g., "nvidia", "amd")
	Name() string

	// returns true if the required tooling exists on the host
	Detect(runCmd RunCmdFunc) bool

	// returns a slice of GPU devices or an error
	Query(runCmd RunCmdFunc) ([]Device, error)
}
