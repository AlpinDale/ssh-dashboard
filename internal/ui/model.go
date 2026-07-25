package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/alpindale/ssh-dashboard/internal"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type Screen int

const (
	ScreenHostList Screen = iota
	ScreenConnecting
	ScreenDashboard
	ScreenOverview
)

type Model struct {
	screen         Screen
	hosts          []internal.SSHHost
	selectedHosts  []internal.SSHHost
	currentHostIdx int
	compactGPU     bool
	list           list.Model
	spinner        spinner.Model
	clients        map[string]*internal.SSHClient
	sysInfos       map[string]*internal.SystemInfo
	lastUpdates    map[string]time.Time
	updateInterval time.Duration
	failedHosts    map[string]error
	width          int
	height         int
	sshOnExit      string
	updateInfo     internal.UpdateInfo
}

type TickMsg time.Time

type UpdateCheckMsg internal.UpdateInfo

type SystemInfoMsg struct {
	hostName string
	info     *internal.SystemInfo
	err      error
}

type ConnectedMsg struct {
	hostName string
	client   *internal.SSHClient
	err      error
}

type hostItem struct {
	host     internal.SSHHost
	selected bool
}

func (h hostItem) FilterValue() string { return h.host.Name }
func (h hostItem) Title() string {
	prefix := "  "
	if h.selected {
		prefix = "✓ "
	}
	return prefix + h.host.Name
}
func (h hostItem) Description() string {
	if h.host.Hostname != "" {
		return fmt.Sprintf("  %s@%s:%s", h.host.User, censorHostname(h.host.Hostname), h.host.Port)
	}
	return ""
}

func checkForUpdates() tea.Msg {
	updateInfo := internal.CheckForUpdates()
	return UpdateCheckMsg(updateInfo)
}

func censorHostname(hostname string) string {
	if hostname == "" {
		return ""
	}

	if strings.Contains(hostname, ".") {
		parts := strings.Split(hostname, ".")
		if len(parts) >= 4 {
			lastOctet := parts[len(parts)-1]
			lastPart := lastOctet
			if len(lastOctet) > 2 {
				lastPart = lastOctet[len(lastOctet)-2:]
			}
			return fmt.Sprintf("%s.***.***%s", parts[0], lastPart)
		}
	}

	if len(hostname) <= 8 {
		if len(hostname) <= 3 {
			return hostname
		}
		return hostname[:2] + strings.Repeat("*", len(hostname)-2)
	}

	return hostname[:3] + strings.Repeat("*", 5) + hostname[len(hostname)-3:]
}

func formatInterval(interval time.Duration) string {
	seconds := interval.Seconds()
	if seconds < 1 {
		return fmt.Sprintf("%.2fs", seconds)
	} else if seconds < 10 {
		return fmt.Sprintf("%.1fs", seconds)
	}
	return fmt.Sprintf("%.0fs", seconds)
}

func InitialModel(hosts []internal.SSHHost, updateInterval time.Duration) Model {
	items := make([]list.Item, len(hosts))
	for i, h := range hosts {
		items[i] = hostItem{host: h, selected: false}
	}

	delegate := list.NewDefaultDelegate()
	l := list.New(items, delegate, 0, 0)
	l.Title = "Select SSH Hosts to Monitor (Space to select, Enter to confirm)"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return Model{
		screen:         ScreenHostList,
		hosts:          hosts,
		list:           l,
		spinner:        s,
		clients:        make(map[string]*internal.SSHClient),
		sysInfos:       make(map[string]*internal.SystemInfo),
		lastUpdates:    make(map[string]time.Time),
		failedHosts:    make(map[string]error),
		updateInterval: updateInterval,
	}
}

func InitialModelWithHost(host internal.SSHHost, updateInterval time.Duration) Model {
	return InitialModelWithHosts([]internal.SSHHost{host}, []internal.SSHHost{host}, updateInterval)
}

func InitialModelWithHosts(allHosts []internal.SSHHost, selectedHosts []internal.SSHHost, updateInterval time.Duration) Model {
	items := make([]list.Item, len(allHosts))
	selectedMap := make(map[string]bool)
	for _, h := range selectedHosts {
		selectedMap[h.Name] = true
	}

	for i, h := range allHosts {
		items[i] = hostItem{host: h, selected: selectedMap[h.Name]}
	}

	delegate := list.NewDefaultDelegate()
	l := list.New(items, delegate, 0, 0)
	l.Title = "Select SSH Hosts to Monitor (Space to select, Enter to confirm)"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	return Model{
		screen:         ScreenConnecting,
		hosts:          allHosts,
		selectedHosts:  selectedHosts,
		currentHostIdx: 0,
		list:           l,
		spinner:        s,
		clients:        make(map[string]*internal.SSHClient),
		sysInfos:       make(map[string]*internal.SystemInfo),
		lastUpdates:    make(map[string]time.Time),
		failedHosts:    make(map[string]error),
		updateInterval: updateInterval,
	}
}

func (m Model) GetSSHOnExit() string {
	return m.sshOnExit
}

func (m *Model) updateListSelection() {
	items := m.list.Items()
	selectedMap := make(map[string]bool)
	for _, h := range m.selectedHosts {
		selectedMap[h.Name] = true
	}

	newItems := make([]list.Item, len(items))
	for i, item := range items {
		if hi, ok := item.(hostItem); ok {
			hi.selected = selectedMap[hi.host.Name]
			newItems[i] = hi
		}
	}
	m.list.SetItems(newItems)
}

func (m Model) Init() tea.Cmd {
	if m.screen == ScreenConnecting && len(m.selectedHosts) > 0 {
		return tea.Batch(m.spinner.Tick, m.connectToHosts(), checkForUpdates)
	}
	return tea.Batch(m.spinner.Tick, checkForUpdates)
}
