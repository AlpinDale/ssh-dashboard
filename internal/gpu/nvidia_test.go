package gpu

import "testing"

func TestNvidiaProviderQueryFieldAlignment(t *testing.T) {
	tests := []struct {
		name          string
		line          string
		wantDriver    string
		wantTempLimit int
		wantPCIeGen   int
		wantPCIeWidth int
	}{
		{
			name:          "full response with temperature.gpu.tlimit",
			line:          "0, RTX 3090, 24576, 12000, 45, 250.00, 350.00, 68, 90, 550.90.07, 4, 16",
			wantDriver:    "550.90.07",
			wantTempLimit: 90,
			wantPCIeGen:   4,
			wantPCIeWidth: 16,
		},
		{
			name:          "fallback response without temperature.gpu.tlimit",
			line:          "0, RTX 3090, 24576, 12000, 45, 250.00, 350.00, 68, 550.90.07, 4, 16",
			wantDriver:    "550.90.07",
			wantTempLimit: 0,
			wantPCIeGen:   4,
			wantPCIeWidth: 16,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runCmd := func(cmd string) (string, error) {
				return tt.line, nil
			}

			devices, err := NvidiaProvider{}.Query(runCmd)
			if err != nil {
				t.Fatalf("Query() error = %v", err)
			}
			if len(devices) != 1 {
				t.Fatalf("Query() returned %d devices, want 1", len(devices))
			}

			got := devices[0]
			if got.Driver != tt.wantDriver {
				t.Errorf("Driver = %q, want %q", got.Driver, tt.wantDriver)
			}
			if got.TempLimit != tt.wantTempLimit {
				t.Errorf("TempLimit = %d, want %d", got.TempLimit, tt.wantTempLimit)
			}
			if got.PCIeGen != tt.wantPCIeGen {
				t.Errorf("PCIeGen = %d, want %d", got.PCIeGen, tt.wantPCIeGen)
			}
			if got.PCIeWidth != tt.wantPCIeWidth {
				t.Errorf("PCIeWidth = %d, want %d", got.PCIeWidth, tt.wantPCIeWidth)
			}
		})
	}
}
