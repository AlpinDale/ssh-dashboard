package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/alpindale/ssh-dashboard/internal"
	"github.com/charmbracelet/lipgloss"
)

func renderProgressBar(percent float64, width int, color lipgloss.Color) string {
	if percent > 100 {
		percent = 100
	}
	if percent < 0 {
		percent = 0
	}

	filled := int(float64(width) * percent / 100.0)
	empty := width - filled

	filledStyle := lipgloss.NewStyle().Foreground(color)
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("240"))

	bar := filledStyle.Render(strings.Repeat("█", filled)) +
		emptyStyle.Render(strings.Repeat("░", empty))

	return bar
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func effectiveWidth(width int) int {
	if width <= 0 {
		return 120
	}
	return width
}

func gridDimensions(totalWidth, itemCount, minColumnWidth, maxColumns int) (int, int) {
	if itemCount <= 0 {
		return 1, effectiveWidth(totalWidth)
	}

	totalWidth = effectiveWidth(totalWidth)
	if maxColumns < 1 {
		maxColumns = 1
	}

	const gapWidth = 4

	bestColumns := 1
	bestWidth := totalWidth
	bestScore := int(^uint(0) >> 1)

	limit := clampInt(itemCount, 1, maxColumns)
	for columns := 1; columns <= limit; columns++ {
		columnWidth := (totalWidth - gapWidth*(columns-1)) / columns
		if columnWidth < minColumnWidth {
			continue
		}

		rows := (itemCount + columns - 1) / columns
		emptySlots := rows*columns - itemCount
		balance := rows - columns
		if balance < 0 {
			balance = -balance
		}

		score := rows*100 + balance*10 + emptySlots*5 - columns
		if score < bestScore {
			bestScore = score
			bestColumns = columns
			bestWidth = columnWidth
		}
	}

	return bestColumns, bestWidth
}

func coreGridDimensions(totalWidth, totalHeight, itemCount int) (int, int) {
	if itemCount <= 0 {
		return 1, effectiveWidth(totalWidth)
	}

	totalWidth = effectiveWidth(totalWidth)
	const (
		minColumnWidth = 33
		maxColumns     = 4
		gapWidth       = 2
		reservedLines  = 22
	)

	maxFitByWidth := (totalWidth + gapWidth) / (minColumnWidth + gapWidth)
	columns := clampInt(maxFitByWidth, 1, maxColumns)
	if columns > itemCount {
		columns = itemCount
	}

	availableRows := totalHeight - reservedLines
	if availableRows < 1 {
		availableRows = 1
	}
	for columns < maxColumns && columns < itemCount {
		rows := (itemCount + columns - 1) / columns
		if rows <= availableRows {
			break
		}
		next := columns + 1
		nextWidth := (totalWidth - gapWidth*(next-1)) / next
		if nextWidth < minColumnWidth {
			break
		}
		columns = next
	}

	columnWidth := (totalWidth - gapWidth*(columns-1)) / columns
	if columnWidth < minColumnWidth {
		columnWidth = minColumnWidth
	}

	return columns, columnWidth
}

func coreUsageColor(usage float64) lipgloss.Color {
	if usage < 45 {
		return lipgloss.Color("10")
	}
	if usage < 80 {
		return lipgloss.Color("208")
	}
	return lipgloss.Color("196")
}

func renderCoreMeters(coreGroups []internal.CPUCoreGroup, totalWidth, totalHeight int) string {
	if len(coreGroups) == 0 {
		return ""
	}

	contentWidth := effectiveWidth(totalWidth) - 4
	columns, cellWidth := coreGridDimensions(contentWidth, totalHeight, len(coreGroups))
	barWidth := clampInt((cellWidth-29)/2, 2, 10)

	var b strings.Builder
	for i := 0; i < len(coreGroups); i += columns {
		cells := make([]string, 0, columns)
		for j := i; j < i+columns && j < len(coreGroups); j++ {
			group := coreGroups[j]
			coreLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render(fmt.Sprintf("C%02d", j+1))

			threadSegment := func(threadIdx int) string {
				if threadIdx >= len(group.Threads) {
					return strings.Repeat(" ", 12+barWidth)
				}
				thread := group.Threads[threadIdx]
				usage := thread.Usage
				if usage < 0 {
					usage = 0
				}
				if usage > 100 {
					usage = 100
				}

				role := "Core"
				if threadIdx > 0 {
					role = "Thread"
				}
				label := fmt.Sprintf("%-6s %3.0f%% ", role, usage)
				bar := renderProgressBar(usage, barWidth, coreUsageColor(usage))
				return label + bar
			}

			cellContent := coreLabel + " " + threadSegment(0) + " " + threadSegment(1)
			cell := lipgloss.NewStyle().Width(cellWidth).Align(lipgloss.Center).Render(cellContent)
			cells = append(cells, cell)
		}
		b.WriteString(joinColumns(cells))
		b.WriteString("\n")
	}

	return b.String()
}

func gpuGridDimensions(totalWidth, totalHeight, itemCount int) (int, int) {
	if itemCount <= 0 {
		return 1, effectiveWidth(totalWidth)
	}

	totalWidth = effectiveWidth(totalWidth)
	const (
		minColumnWidth = 52
		maxColumns     = 4
		gapWidth       = 4
	)
	_ = totalHeight

	bestColumns := 1
	bestWidth := totalWidth
	bestScore := int(^uint(0) >> 1)

	limit := clampInt(itemCount, 1, maxColumns)
	for columns := 1; columns <= limit; columns++ {
		columnWidth := (totalWidth - gapWidth*(columns-1)) / columns
		if columnWidth < minColumnWidth {
			continue
		}

		rows := (itemCount + columns - 1) / columns
		emptySlots := rows*columns - itemCount
		balance := rows - columns
		if balance < 0 {
			balance = -balance
		}

		score := rows*100 + balance*10 + emptySlots*5 - columns
		if score < bestScore {
			bestScore = score
			bestColumns = columns
			bestWidth = columnWidth
		}
	}

	return bestColumns, bestWidth
}

func parseSizeToGB(size string) float64 {
	if size == "" {
		return 0
	}

	unit := size[len(size)-1]
	number := size
	multiplier := 1.0

	if unit < '0' || unit > '9' {
		number = size[:len(size)-1]
		switch unit {
		case 'T', 't':
			multiplier = 1024
		case 'G', 'g':
			multiplier = 1
		case 'M', 'm':
			multiplier = 1.0 / 1024.0
		case 'K', 'k':
			multiplier = 1.0 / (1024.0 * 1024.0)
		default:
			return 0
		}
	}

	value, err := strconv.ParseFloat(number, 64)
	if err != nil {
		return 0
	}

	return value * multiplier
}

func isSignificantDisk(disk internal.DiskInfo) bool {
	mount := strings.ToLower(strings.TrimSpace(disk.MountPoint))
	if mount == "/" || mount == "/home" {
		return true
	}

	ignoredMounts := []string{"/boot", "/boot/efi", "/efi", "[swap]"}
	for _, ignored := range ignoredMounts {
		if mount == ignored {
			return false
		}
	}

	sizeGB := parseSizeToGB(strings.TrimSpace(disk.Size))
	if sizeGB > 0 && sizeGB < 4 {
		return false
	}

	return true
}

func joinColumns(columns []string) string {
	if len(columns) == 0 {
		return ""
	}

	row := columns[0]
	for i := 1; i < len(columns); i++ {
		row = lipgloss.JoinHorizontal(lipgloss.Top, row, "    ", columns[i])
	}

	return row
}

func fillToHeight(content string, height int) string {
	if height <= 0 {
		return content
	}
	trimmed := strings.TrimRight(content, "\n")
	if trimmed == "" {
		return strings.Repeat("\n", height)
	}

	lines := strings.Split(trimmed, "\n")
	if len(lines) > height {
		return strings.Join(lines[:height], "\n")
	}

	content = strings.Join(lines, "\n")
	lineCount := lipgloss.Height(content)
	if lineCount >= height {
		return content
	}
	return content + strings.Repeat("\n", height-lineCount)
}

func (m Model) renderConnectingProgress() string {
	var b strings.Builder

	title := "  System Dashboard - Connecting  "
	connectedCount := len(m.clients)
	totalCount := len(m.selectedHosts)
	subtitle := fmt.Sprintf("v%s | Connecting to %d host(s)... (%d/%d ready)", internal.ShortVersion(), totalCount, connectedCount, totalCount)

	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(subtitle))
	b.WriteString("\n\n")

	maxNameLen := 0
	for _, host := range m.selectedHosts {
		if len(host.Name) > maxNameLen {
			maxNameLen = len(host.Name)
		}
	}

	for _, host := range m.selectedHosts {
		client := m.clients[host.Name]
		sysInfo := m.sysInfos[host.Name]

		statusIcon := m.spinner.View()
		statusText := "Connecting..."
		statusColor := lipgloss.Color("240")

		if client != nil {
			if sysInfo != nil {
				statusIcon = "✓"
				statusText = "Ready"
				statusColor = lipgloss.Color("10")
			} else {
				statusIcon = m.spinner.View()
				statusText = "Gathering information..."
				statusColor = lipgloss.Color("11")
			}
		}

		paddedName := host.Name + strings.Repeat(" ", maxNameLen-len(host.Name))
		hostName := headerStyle.Render("● " + paddedName)
		status := lipgloss.NewStyle().Foreground(statusColor).Render(fmt.Sprintf("%s %s", statusIcon, statusText))

		b.WriteString(fmt.Sprintf("  %s  %s\n", hostName, status))
	}

	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Please wait..."))

	return b.String()
}

func (m Model) renderOverview() string {
	var b strings.Builder

	title := fmt.Sprintf("  Overview - All Hosts (%d)  ", len(m.selectedHosts))
	subtitle := fmt.Sprintf("v%s | Last Updated: %s | Interval: %s | 't' per-host | 'c' add hosts | 'q' quit",
		internal.ShortVersion(), time.Now().Format("15:04:05"), formatInterval(m.updateInterval))

	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(subtitle))
	b.WriteString("\n\n")

	contentWidth := effectiveWidth(m.width) - 2
	columns, cardWidth := gridDimensions(contentWidth, len(m.selectedHosts), 44, 3)
	const gapWidth = 4

	for i := 0; i < len(m.selectedHosts); i += columns {
		rowCount := columns
		if remaining := len(m.selectedHosts) - i; remaining < rowCount {
			rowCount = remaining
		}

		rowCardWidth := cardWidth
		if rowCount > 0 {
			rowCardWidth = (contentWidth - gapWidth*(rowCount-1)) / rowCount
		}

		cards := make([]string, 0, rowCount)
		for j := i; j < i+rowCount && j < len(m.selectedHosts); j++ {
			cards = append(cards, m.renderSingleHostOverview(m.selectedHosts[j], rowCardWidth))
		}
		b.WriteString(joinColumns(cards))
		b.WriteString("\n")
	}

	return fillToHeight(b.String(), m.height)
}

func (m Model) renderSingleHostOverview(host internal.SSHHost, sectionWidth int) string {
	var b strings.Builder

	sectionWidth = clampInt(sectionWidth, 44, 120)
	barWidth := clampInt(sectionWidth-4, 20, 90)

	sysInfo := m.sysInfos[host.Name]

	if sysInfo == nil {
		b.WriteString(headerStyle.Render(fmt.Sprintf("● %s", host.Name)))
		b.WriteString("  ")
		b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("Loading..."))
		b.WriteString("\n")
		return lipgloss.NewStyle().Width(sectionWidth).Render(b.String())
	}

	b.WriteString(headerStyle.Render(fmt.Sprintf("● %s", host.Name)))
	b.WriteString("\n")

	cpuUsage := sysInfo.CPU.Usage
	if cpuUsage == "" {
		cpuUsage = "N/A"
	}
	b.WriteString(fmt.Sprintf("  CPU: %s", cpuUsage))
	b.WriteString("\n")

	if sysInfo.RAM.Total > 0 {
		b.WriteString(fmt.Sprintf("  RAM: %.1f / %.1f GB (%.0f%%)",
			float64(sysInfo.RAM.Used)/1024,
			float64(sysInfo.RAM.Total)/1024,
			sysInfo.RAM.UsagePercent))
	} else {
		b.WriteString("  RAM: N/A")
	}
	b.WriteString("\n")

	if len(sysInfo.Disk) > 0 {
		disk := sysInfo.Disk[0]
		b.WriteString(fmt.Sprintf("  Disk: %s / %s (%s)", disk.Used, disk.Size, disk.UsagePercent))
	} else {
		b.WriteString("  Disk: N/A")
	}
	b.WriteString("\n")

	if len(sysInfo.GPUs) > 0 {
		var totalVRAM, usedVRAM int
		var totalUtil int
		for _, gpu := range sysInfo.GPUs {
			totalVRAM += gpu.VRAMTotal
			usedVRAM += gpu.VRAMUsed
			totalUtil += gpu.Utilization
		}

		vramPercent := 0.0
		if totalVRAM > 0 {
			vramPercent = (float64(usedVRAM) / float64(totalVRAM)) * 100
		}

		avgUtil := 0
		if len(sysInfo.GPUs) > 0 {
			avgUtil = totalUtil / len(sysInfo.GPUs)
		}

		vramBar := renderProgressBar(vramPercent, barWidth, lipgloss.Color("33"))
		utilBar := renderProgressBar(float64(avgUtil), barWidth, lipgloss.Color("208"))

		b.WriteString(fmt.Sprintf("  GPU VRAM: %.1f / %.1f GB (%.0f%%)\n", float64(usedVRAM)/1024, float64(totalVRAM)/1024, vramPercent))
		b.WriteString("  " + vramBar + "\n")
		b.WriteString(fmt.Sprintf("  GPU Util: %d%% avg\n", avgUtil))
		b.WriteString("  " + utilBar + "\n")
	} else {
		paddingLine := strings.Repeat(" ", barWidth)
		b.WriteString("  GPU: N/A\n")
		b.WriteString(paddingLine + "\n")
		b.WriteString(paddingLine + "\n")
		b.WriteString(paddingLine + "\n")
	}

	return lipgloss.NewStyle().Width(sectionWidth).Render(b.String())
}

func renderDashboard(hostName string, info *internal.SystemInfo, updateInterval time.Duration, lastUpdate time.Time, width, height int, multiHost bool) string {
	var b strings.Builder

	title := fmt.Sprintf("  System Dashboard - %s  ", hostName)
	navHint := ""
	if multiHost {
		navHint = " | 'n' next | 't' overview"
	}
	subtitle := fmt.Sprintf("v%s | Last Updated: %s | Interval: %s%s | 's' shell | 'c' add hosts | 'q' quit",
		internal.ShortVersion(), lastUpdate.Format("15:04:05"), formatInterval(updateInterval), navHint)

	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n")
	b.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(subtitle))
	b.WriteString("\n\n")

	b.WriteString(renderCPUSection(info.CPU, width, height))
	b.WriteString("\n")

	b.WriteString(renderRAMAndDiskSection(info.RAM, info.Disk, width))

	if len(info.GPUs) > 1 {
		b.WriteString("\n")
		b.WriteString(renderAggregateGPUSection(info.GPUs, width, height))
	}

	if len(info.GPUs) > 0 {
		b.WriteString("\n")
		b.WriteString(renderGPUSection(info.GPUs, width, height))
	}

	return fillToHeight(b.String(), height)
}

func renderCPUSection(cpu internal.CPUInfo, totalWidth, totalHeight int) string {
	var b strings.Builder
	var parts []string

	if cpu.Model != "" {
		parts = append(parts, cpu.Model)
	}
	if cpu.Count != "" {
		parts = append(parts, fmt.Sprintf("%s CPUs", cpu.Count))
	}
	if len(cpu.CoreGroups) > 0 {
		parts = append(parts, fmt.Sprintf("Physical Cores: %d", len(cpu.CoreGroups)))
	}
	parts = append(parts, fmt.Sprintf("Usage: %s", cpu.Usage))

	cpuInfo := strings.Join(parts, "  |  ")
	line := headerStyle.Render("● CPU") + "  " + cpuInfo
	b.WriteString(line)
	b.WriteString("\n")

	if len(cpu.CoreGroups) > 0 {
		meters := renderCoreMeters(cpu.CoreGroups, totalWidth, totalHeight)
		for _, meterLine := range strings.Split(strings.TrimSuffix(meters, "\n"), "\n") {
			if meterLine == "" {
				continue
			}
			b.WriteString("  ")
			b.WriteString(meterLine)
			b.WriteString("\n")
		}
	}

	return b.String()
}

func renderAggregateGPUSection(gpus []internal.GPUInfo, totalWidth, totalHeight int) string {
	var b strings.Builder

	var totalVRAM, usedVRAM int
	var totalUtil int
	for _, gpu := range gpus {
		totalVRAM += gpu.VRAMTotal
		usedVRAM += gpu.VRAMUsed
		totalUtil += gpu.Utilization
	}

	vramPercent := 0.0
	if totalVRAM > 0 {
		vramPercent = (float64(usedVRAM) / float64(totalVRAM)) * 100
	}
	avgUtil := 0.0
	if len(gpus) > 0 {
		avgUtil = float64(totalUtil) / float64(len(gpus))
	}

	totalVRAMGB := float64(totalVRAM) / 1024.0
	usedVRAMGB := float64(usedVRAM) / 1024.0

	b.WriteString(headerStyle.Render("● Total GPU Pressure"))
	b.WriteString("\n\n")

	contentWidth := effectiveWidth(totalWidth) - 2
	columns, cardWidth := gpuGridDimensions(contentWidth, totalHeight, len(gpus))
	rowWidth := columns*cardWidth + (columns-1)*4
	fullBarWidth := clampInt(rowWidth-2, 24, 400)

	vramLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("VRAM")
	b.WriteString(fmt.Sprintf("  %s %.1f/%.1f GB (%.1f%%) across %d GPUs\n", vramLabel, usedVRAMGB, totalVRAMGB, vramPercent, len(gpus)))
	b.WriteString("  ")
	b.WriteString(renderProgressBar(vramPercent, fullBarWidth, lipgloss.Color("39")))
	b.WriteString("\n")

	utilLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render("Util")
	b.WriteString(fmt.Sprintf("  %s %.1f%% average\n", utilLabel, avgUtil))
	b.WriteString("  ")
	b.WriteString(renderProgressBar(avgUtil, fullBarWidth, lipgloss.Color("208")))
	b.WriteString("\n")

	return b.String()
}

func renderGPUSection(gpus []internal.GPUInfo, totalWidth, totalHeight int) string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("● GPU Information"))
	b.WriteString("\n\n")

	contentWidth := effectiveWidth(totalWidth) - 2
	columns, cardWidth := gpuGridDimensions(contentWidth, totalHeight, len(gpus))
	const gapWidth = 4

	for i := 0; i < len(gpus); i += columns {
		rowCount := columns
		if remaining := len(gpus) - i; remaining < rowCount {
			rowCount = remaining
		}

		rowCardWidth := cardWidth
		if rowCount > 0 {
			rowCardWidth = (contentWidth - gapWidth*(rowCount-1)) / rowCount
		}

		cards := make([]string, 0, rowCount)
		for j := i; j < i+rowCount && j < len(gpus); j++ {
			cards = append(cards, renderSingleGPU(gpus[j], rowCardWidth))
		}
		b.WriteString(joinColumns(cards))
		b.WriteString("\n")
	}

	return b.String()
}

func renderSingleGPU(gpu internal.GPUInfo, cardWidth int) string {
	var b strings.Builder

	cardWidth = clampInt(cardWidth, 52, 240)
	barWidth := clampInt(cardWidth-4, 20, 220)

	vramTotalGB := float64(gpu.VRAMTotal) / 1024.0
	vramUsedGB := float64(gpu.VRAMUsed) / 1024.0
	vramPercent := 0.0
	if gpu.VRAMTotal > 0 {
		vramPercent = (float64(gpu.VRAMUsed) / float64(gpu.VRAMTotal)) * 100
	}

	gpuIndex := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Render(gpu.Index)

	gpuName := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		Render(gpu.Name)

	powerPercent := 0.0
	if gpu.PowerLimit > 0 {
		powerPercent = (float64(gpu.PowerDraw) / float64(gpu.PowerLimit)) * 100
	}
	var powerColor lipgloss.Color
	if powerPercent < 70 {
		powerColor = lipgloss.Color("10") // Green
	} else if powerPercent < 90 {
		powerColor = lipgloss.Color("11") // Yellow
	} else {
		powerColor = lipgloss.Color("196") // Red
	}
	powerText := lipgloss.NewStyle().
		Foreground(powerColor).
		Render(fmt.Sprintf("%dW", gpu.PowerDraw))

	var tempColor lipgloss.Color
	if gpu.Temperature < 70 {
		tempColor = lipgloss.Color("10") // Green
	} else if gpu.Temperature < 80 {
		tempColor = lipgloss.Color("11") // Yellow
	} else if gpu.Temperature < 85 {
		tempColor = lipgloss.Color("208") // Orange
	} else {
		tempColor = lipgloss.Color("196") // Red
	}
	tempText := lipgloss.NewStyle().
		Foreground(tempColor).
		Render(fmt.Sprintf("%d°C", gpu.Temperature))

	b.WriteString(fmt.Sprintf("  %s  %s  %s  %s\n", gpuIndex, gpuName, powerText, tempText))

	vramLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("39")).Render("VRAM")
	b.WriteString(fmt.Sprintf("  %s %.1f/%.1f GB (%.1f%%)\n", vramLabel, vramUsedGB, vramTotalGB, vramPercent))
	b.WriteString("  ")
	b.WriteString(renderProgressBar(vramPercent, barWidth, lipgloss.Color("39")))
	b.WriteString("\n")

	utilLabel := lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render("Util")
	b.WriteString(fmt.Sprintf("  %s %d%%\n", utilLabel, gpu.Utilization))
	b.WriteString("  ")
	b.WriteString(renderProgressBar(float64(gpu.Utilization), barWidth, lipgloss.Color("208")))
	b.WriteString("\n")

	return lipgloss.NewStyle().Width(cardWidth).Render(b.String())
}

func renderRAMAndDiskSection(ram internal.RAMInfo, disks []internal.DiskInfo, totalWidth int) string {
	contentWidth := effectiveWidth(totalWidth) - 2

	if contentWidth < 96 {
		diskSection := renderDiskSection(disks, contentWidth)
		ramSection := renderRAMSection(ram, contentWidth)
		return diskSection + "\n" + ramSection + "\n"
	}

	sectionWidth := (contentWidth - 4) / 2
	diskSection := renderDiskSection(disks, sectionWidth)
	ramSection := renderRAMSection(ram, sectionWidth)

	return lipgloss.JoinHorizontal(lipgloss.Top, diskSection, "    ", ramSection) + "\n"
}

func renderRAMSection(ram internal.RAMInfo, sectionWidth int) string {
	var b strings.Builder
	sectionWidth = clampInt(sectionWidth, 40, 240)
	barWidth := clampInt(sectionWidth-4, 20, 220)

	b.WriteString(headerStyle.Render("● RAM Information"))
	b.WriteString("\n")

	totalGB := float64(ram.Total) / 1024.0
	usedGB := float64(ram.Used) / 1024.0

	b.WriteString(fmt.Sprintf("  %.1f GB / %.1f GB (%.1f%%)\n", usedGB, totalGB, ram.UsagePercent))
	b.WriteString("  ")
	b.WriteString(renderProgressBar(ram.UsagePercent, barWidth, lipgloss.Color("10")))
	b.WriteString("\n")

	return lipgloss.NewStyle().Width(sectionWidth).Render(b.String())
}

func renderDiskSection(disks []internal.DiskInfo, sectionWidth int) string {
	var b strings.Builder
	sectionWidth = clampInt(sectionWidth, 40, 240)
	barWidth := clampInt(sectionWidth-4, 20, 220)

	b.WriteString(headerStyle.Render("● Disk Usage"))
	b.WriteString("\n")

	filteredDisks := make([]internal.DiskInfo, 0, len(disks))
	for _, disk := range disks {
		if isSignificantDisk(disk) {
			filteredDisks = append(filteredDisks, disk)
		}
	}

	if len(filteredDisks) == 0 {
		b.WriteString("  No significant partitions\n")
		b.WriteString("  " + strings.Repeat(" ", barWidth) + "\n")
		return lipgloss.NewStyle().Width(sectionWidth).Render(b.String())
	}

	for i, disk := range filteredDisks {
		usageStr := strings.TrimSuffix(disk.UsagePercent, "%")
		usagePercent := 0.0
		if val, err := strconv.ParseFloat(usageStr, 64); err == nil {
			usagePercent = val
		}

		b.WriteString(fmt.Sprintf("  %s  %s  %s / %s (%s)\n",
			disk.Device, disk.MountPoint, disk.Used, disk.Size, disk.UsagePercent))

		b.WriteString("  ")

		var barColor lipgloss.Color
		if usagePercent >= 90 {
			barColor = lipgloss.Color("196") // Red
		} else if usagePercent >= 75 {
			barColor = lipgloss.Color("208") // Orange
		} else {
			barColor = lipgloss.Color("10") // Green
		}

		b.WriteString(renderProgressBar(usagePercent, barWidth, barColor))
		b.WriteString("\n")
		if i < len(filteredDisks)-1 {
			b.WriteString("\n")
		}
	}

	return lipgloss.NewStyle().Width(sectionWidth).Render(b.String())
}
