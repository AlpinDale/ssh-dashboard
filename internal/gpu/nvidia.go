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
	output, err := runCmd("nvidia-smi --query-gpu=index,name,memory.total,memory.used,utilization.gpu,power.draw,power.limit,temperature.gpu,temperature.gpu.tlimit,driver_version --format=csv,noheader,nounits")
	if err != nil {
		output, err = runCmd("nvidia-smi --query-gpu=index,name,memory.total,memory.used,utilization.gpu,power.draw,power.limit,temperature.gpu,driver_version --format=csv,noheader,nounits")
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

			if len(parts) >= 10 {
				if val, err := strconv.Atoi(strings.TrimSpace(parts[8])); err == nil {
					device.TempLimit = val
				}
				device.Driver = strings.TrimSpace(parts[9])
			} else {
				device.Driver = strings.TrimSpace(parts[8])
			}

			devices = append(devices, device)
		}
	}

	return devices, nil
}
