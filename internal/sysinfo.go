package internal

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alpindale/ssh-dashboard/internal/gpu"
)

type SystemInfo struct {
	CPU       CPUInfo
	GPUs      []GPUInfo
	PrimaryIP string
	RAM       RAMInfo
	Disk      []DiskInfo
}

type CPUInfo struct {
	Model string
	Count string
	Usage string
}

type GPUInfo struct {
	Index       string
	Name        string
	VRAMTotal   int // in MB
	VRAMUsed    int // in MB
	Utilization int // percentage
	PowerDraw   int // in Watts
	PowerLimit  int // in Watts
	Temperature int // in Celsius
}

type RAMInfo struct {
	Total        int // in MB
	Used         int // in MB
	UsagePercent float64
}

type DiskInfo struct {
	Device       string
	Size         string
	Used         string
	Available    string
	UsagePercent string
	MountPoint   string
}

func GatherSystemInfo(client *SSHClient) (*SystemInfo, error) {
	info := &SystemInfo{}

	cpuInfo, err := getCPUInfo(client)
	if err != nil {
		return nil, fmt.Errorf("failed to get CPU info: %w", err)
	}
	info.CPU = cpuInfo

	gpuInfo, _ := getGPUInfo(client)
	info.GPUs = gpuInfo
	info.PrimaryIP = getPrimaryIP(client)

	ramInfo, err := getRAMInfo(client)
	if err != nil {
		return nil, fmt.Errorf("failed to get RAM info: %w", err)
	}
	info.RAM = ramInfo

	diskInfo, err := getDiskInfo(client)
	if err != nil {
		return nil, fmt.Errorf("failed to get disk info: %w", err)
	}
	info.Disk = diskInfo

	return info, nil
}

func getCPUInfo(client *SSHClient) (CPUInfo, error) {
	info := CPUInfo{}

	output, err := client.ExecuteCommand("lscpu | grep -E 'Model name|CPU\\(s\\):'")
	if err == nil {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			if strings.Contains(line, "Model name:") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					info.Model = strings.TrimSpace(parts[1])
				}
			} else if strings.HasPrefix(line, "CPU(s):") {
				parts := strings.SplitN(line, ":", 2)
				if len(parts) == 2 {
					info.Count = strings.TrimSpace(parts[1])
				}
			}
		}
	}

	output, err = client.ExecuteCommand("top -bn1 | grep 'Cpu(s)' | sed 's/.*, *\\([0-9.]*\\)%* id.*/\\1/' | awk '{print 100 - $1}'")
	if err == nil {
		usage := strings.TrimSpace(output)
		if usage != "" {
			if val, err := strconv.ParseFloat(usage, 64); err == nil {
				info.Usage = fmt.Sprintf("%.1f%%", val)
			}
		}
	}

	if info.Usage == "" {
		info.Usage = "N/A"
	}

	return info, nil
}

func getGPUInfo(client *SSHClient) ([]GPUInfo, error) {
	runCmd := func(cmd string) (string, error) {
		return client.ExecuteCommand(cmd)
	}

	devices, err := gpu.QueryAll(runCmd)
	if err != nil {
		return []GPUInfo{}, nil
	}

	gpus := make([]GPUInfo, len(devices))
	for i, dev := range devices {
		gpus[i] = GPUInfo{
			Index:       fmt.Sprintf("%d", dev.Index),
			Name:        dev.Name,
			VRAMTotal:   dev.VRAMTotal,
			VRAMUsed:    dev.VRAMUsed,
			Utilization: dev.Utilization,
			PowerDraw:   dev.PowerDraw,
			PowerLimit:  dev.PowerLimit,
			Temperature: dev.Temperature,
		}
	}

	return gpus, nil
}

func getPrimaryIP(client *SSHClient) string {
	output, err := client.ExecuteCommand("hostname -I")
	if err != nil {
		return ""
	}
	fields := strings.Fields(strings.TrimSpace(output))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func getRAMInfo(client *SSHClient) (RAMInfo, error) {
	info := RAMInfo{}

	output, err := client.ExecuteCommand("free -m | grep Mem:")
	if err != nil {
		return info, err
	}

	parts := strings.Fields(output)
	if len(parts) >= 3 {
		if val, err := strconv.Atoi(parts[1]); err == nil {
			info.Total = val
		}
		if val, err := strconv.Atoi(parts[2]); err == nil {
			info.Used = val
		}

		if info.Total > 0 {
			info.UsagePercent = (float64(info.Used) / float64(info.Total)) * 100
		}
	}

	return info, nil
}

func getDiskInfo(client *SSHClient) ([]DiskInfo, error) {
	output, err := client.ExecuteCommand("df -h | grep -E '^/dev/'")
	if err != nil {
		return nil, err
	}

	var disks []DiskInfo
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) >= 6 {
			disk := DiskInfo{
				Device:       parts[0],
				Size:         parts[1],
				Used:         parts[2],
				Available:    parts[3],
				UsagePercent: parts[4],
				MountPoint:   parts[5],
			}
			disks = append(disks, disk)
		}
	}

	return disks, nil
}
