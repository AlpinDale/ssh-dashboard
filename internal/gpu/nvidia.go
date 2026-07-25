package gpu

import (
	"strconv"
	"strings"

	"github.com/alpindale/ssh-dashboard/internal/gpu/base"
)

type NvidiaProvider struct{}

func (p NvidiaProvider) Name() string {
	return "nvidia"
}

func (p NvidiaProvider) Detect(runCmd base.RunCmdFunc) bool {
	_, err := runCmd("which nvidia-smi")
	return err == nil
}

func (p NvidiaProvider) Query(runCmd base.RunCmdFunc) ([]base.Device, error) {
	output, err := runCmd("nvidia-smi --query-gpu=index,name,memory.total,memory.used,utilization.gpu,power.draw,power.limit,temperature.gpu,temperature.gpu.tlimit,driver_version,pcie.link.gen.current,pcie.link.width.current --format=csv,noheader,nounits")
	if err != nil {
		output, err = runCmd("nvidia-smi --query-gpu=index,name,memory.total,memory.used,utilization.gpu,power.draw,power.limit,temperature.gpu,driver_version,pcie.link.gen.current,pcie.link.width.current --format=csv,noheader,nounits")
	}
	if err != nil {
		return nil, err
	}

	var devices []base.Device
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) >= 9 {
			device := base.Device{
				Vendor: "nvidia",
			}

			if val, err := strconv.Atoi(strings.TrimSpace(parts[0])); err == nil {
				device.Index = val
			}
			device.Name = strings.TrimSpace(parts[1])

			if val, err := strconv.Atoi(strings.TrimSpace(parts[2])); err == nil {
				device.VRAMTotal = val
			}
			if val, err := strconv.Atoi(strings.TrimSpace(parts[3])); err == nil {
				device.VRAMUsed = val
			}
			if val, err := strconv.Atoi(strings.TrimSpace(parts[4])); err == nil {
				device.Utilization = val
			}
			if val, err := strconv.ParseFloat(strings.TrimSpace(parts[5]), 64); err == nil {
				device.PowerDraw = int(val)
			}
			if val, err := strconv.ParseFloat(strings.TrimSpace(parts[6]), 64); err == nil {
				device.PowerLimit = int(val)
			}
			if val, err := strconv.Atoi(strings.TrimSpace(parts[7])); err == nil {
				device.Temperature = val
			}

			// The primary query includes temperature.gpu.tlimit (12 fields total);
			// the fallback query for older drivers omits it (11 fields total).
			// Track the next field to read explicitly so driver/PCIe fields don't
			// shift depending on which query succeeded.
			idx := 8
			if len(parts) >= 12 {
				if val, err := strconv.Atoi(strings.TrimSpace(parts[idx])); err == nil {
					device.TempLimit = val
				}
				idx++
			}
			if idx < len(parts) {
				device.Driver = strings.TrimSpace(parts[idx])
				idx++
			}
			if idx < len(parts) {
				if val, err := strconv.Atoi(strings.TrimSpace(parts[idx])); err == nil {
					device.PCIeGen = val
				}
				idx++
			}
			if idx < len(parts) {
				if val, err := strconv.Atoi(strings.TrimSpace(parts[idx])); err == nil {
					device.PCIeWidth = val
				}
			}

			devices = append(devices, device)
		}
	}

	return devices, nil
}
