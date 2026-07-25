package internal

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/alpindale/ssh-dashboard/internal/gpu"
)

type SystemInfo struct {
	CPU              CPUInfo
	GPUs             []GPUInfo
	GPUDriverVersion string
	CUDAVersion      string
	RAM              RAMInfo
	Disk             []DiskInfo
}

type CPUInfo struct {
	Model           string
	Count           string
	Usage           string
	LogicalCPUCount int
	CoreGroups      []CPUCoreGroup
}

type CPUThreadUsage struct {
	LogicalID int
	Usage     float64
}

type CPUCoreGroup struct {
	SocketID int
	CoreID   int
	Threads  []CPUThreadUsage
}

type CPUCoreIdentity struct {
	SocketID int
	CoreID   int
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
	TempLimit   int // in Celsius
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

	gpuInfo, driverVersion, cudaVersion, _ := getGPUInfo(client)
	info.GPUs = gpuInfo
	info.GPUDriverVersion = driverVersion
	info.CUDAVersion = cudaVersion

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

	perCPUUsage, usageErr := getPerCPUUsage(client)
	cpuToCore, coreMapErr := getCPUToCoreMap(client)
	if usageErr == nil && len(perCPUUsage) > 0 {
		logicalIDs := make([]int, 0, len(perCPUUsage))
		for cpuID := range perCPUUsage {
			logicalIDs = append(logicalIDs, cpuID)
		}
		sort.Ints(logicalIDs)
		info.LogicalCPUCount = len(logicalIDs)

		groupsByCore := make(map[string][]CPUThreadUsage)
		groupIdentities := make(map[string]CPUCoreIdentity)
		for _, cpuID := range logicalIDs {
			identity := CPUCoreIdentity{SocketID: -1, CoreID: cpuID}
			if coreMapErr == nil {
				if mappedIdentity, ok := cpuToCore[cpuID]; ok {
					identity = mappedIdentity
				}
			}

			key := fmt.Sprintf("%d:%d", identity.SocketID, identity.CoreID)
			groupIdentities[key] = identity

			groupsByCore[key] = append(groupsByCore[key], CPUThreadUsage{
				LogicalID: cpuID,
				Usage:     perCPUUsage[cpuID],
			})
		}

		keys := make([]string, 0, len(groupsByCore))
		for key := range groupsByCore {
			keys = append(keys, key)
		}
		sort.Slice(keys, func(i, j int) bool {
			left := groupIdentities[keys[i]]
			right := groupIdentities[keys[j]]
			if left.SocketID != right.SocketID {
				return left.SocketID < right.SocketID
			}
			return left.CoreID < right.CoreID
		})

		groups := make([]CPUCoreGroup, 0, len(keys))
		for _, key := range keys {
			threads := groupsByCore[key]
			sort.Slice(threads, func(i, j int) bool {
				return threads[i].LogicalID < threads[j].LogicalID
			})
			identity := groupIdentities[key]
			groups = append(groups, CPUCoreGroup{SocketID: identity.SocketID, CoreID: identity.CoreID, Threads: threads})
		}

		info.CoreGroups = groups
	}

	if info.LogicalCPUCount > 0 && info.Count == "" {
		info.Count = strconv.Itoa(info.LogicalCPUCount)
	}

	return info, nil
}

func getPerCPUUsage(client *SSHClient) (map[int]float64, error) {
	output, err := client.ExecuteCommand("cat /proc/stat; sleep 0.2; cat /proc/stat")
	if err != nil {
		return nil, err
	}

	lines := strings.Split(strings.TrimSpace(output), "\n")
	blocks := make([]map[int][2]uint64, 0, 2)
	current := make(map[int][2]uint64)
	inCPUBlock := false

	flushBlock := func() {
		if len(current) == 0 {
			return
		}
		blockCopy := make(map[int][2]uint64, len(current))
		for k, v := range current {
			blockCopy[k] = v
		}
		blocks = append(blocks, blockCopy)
		current = make(map[int][2]uint64)
	}

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "cpu") {
			inCPUBlock = true
			fields := strings.Fields(line)
			if len(fields) < 8 || !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
				continue
			}

			cpuID, idErr := strconv.Atoi(strings.TrimPrefix(fields[0], "cpu"))
			if idErr != nil {
				continue
			}

			values := make([]uint64, 0, len(fields)-1)
			for _, field := range fields[1:] {
				val, parseErr := strconv.ParseUint(field, 10, 64)
				if parseErr != nil {
					values = nil
					break
				}
				values = append(values, val)
			}
			if len(values) < 8 {
				continue
			}

			idleAll := values[3] + values[4]
			nonIdle := values[0] + values[1] + values[2] + values[5] + values[6] + values[7]
			total := idleAll + nonIdle
			current[cpuID] = [2]uint64{total, idleAll}
			continue
		}

		if inCPUBlock && len(current) > 0 {
			flushBlock()
			inCPUBlock = false
		}
	}

	flushBlock()
	if len(blocks) < 2 {
		return nil, fmt.Errorf("insufficient /proc/stat snapshots")
	}

	first := blocks[0]
	second := blocks[len(blocks)-1]
	usageByCPU := make(map[int]float64)
	for cpuID, secondTotals := range second {
		firstTotals, ok := first[cpuID]
		if !ok {
			continue
		}

		deltaTotal := secondTotals[0] - firstTotals[0]
		deltaIdle := secondTotals[1] - firstTotals[1]
		if deltaTotal == 0 {
			usageByCPU[cpuID] = 0
			continue
		}

		usageByCPU[cpuID] = (float64(deltaTotal-deltaIdle) / float64(deltaTotal)) * 100
	}

	if len(usageByCPU) == 0 {
		return nil, fmt.Errorf("no per-CPU usage data found")
	}

	return usageByCPU, nil
}

func getCPUToCoreMap(client *SSHClient) (map[int]CPUCoreIdentity, error) {
	output, err := client.ExecuteCommand("lscpu -p=cpu,socket,core | grep -v '^#'")
	if err != nil {
		return nil, err
	}

	mapping := make(map[int]CPUCoreIdentity)
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if line == "" {
			continue
		}

		parts := strings.Split(line, ",")
		if len(parts) < 3 {
			continue
		}

		cpuID, cpuErr := strconv.Atoi(strings.TrimSpace(parts[0]))
		socketID, socketErr := strconv.Atoi(strings.TrimSpace(parts[1]))
		coreID, coreErr := strconv.Atoi(strings.TrimSpace(parts[2]))
		if cpuErr != nil || socketErr != nil || coreErr != nil {
			continue
		}

		mapping[cpuID] = CPUCoreIdentity{SocketID: socketID, CoreID: coreID}
	}

	if len(mapping) == 0 {
		return nil, fmt.Errorf("no CPU/core mapping data found")
	}

	return mapping, nil
}

func getGPUInfo(client *SSHClient) ([]GPUInfo, string, string, error) {
	runCmd := func(cmd string) (string, error) {
		return client.ExecuteCommand(cmd)
	}

	devices, err := gpu.QueryAll(runCmd)
	if err != nil {
		return []GPUInfo{}, "", "", nil
	}

	driverVersion := ""
	cudaVersion := ""
	if len(devices) > 0 {
		driverVersion = devices[0].Driver
		if devices[0].Vendor == "nvidia" {
			if output, queryErr := runCmd("nvidia-smi"); queryErr == nil {
				cudaPattern := regexp.MustCompile(`CUDA Version:\s*([0-9.]+)`)
				if matches := cudaPattern.FindStringSubmatch(output); len(matches) == 2 {
					cudaVersion = matches[1]
				}
			}
		}
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
			TempLimit:   dev.TempLimit,
		}
	}

	return gpus, driverVersion, cudaVersion, nil
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
