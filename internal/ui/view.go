package ui

import (
	"fmt"
	"strings"

	"github.com/alpindale/ssh-dashboard/internal"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) renderUpdateNotification() string {
	if !m.updateInfo.Available {
		return ""
	}

	updateStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("226")).
		Bold(true)

	currentVer := m.updateInfo.CurrentVersion
	if !strings.HasPrefix(currentVer, "v") {
		currentVer = "v" + currentVer
	}

	return updateStyle.Render(fmt.Sprintf("\n\n⬆  Update available! %s → %s",
		currentVer, m.updateInfo.LatestVersion))
}

func (m Model) renderConnectionFailures() string {
	if len(m.failedHosts) == 0 {
		return ""
	}

	failedDetails := make([]string, 0, len(m.failedHosts))
	for hostName, err := range m.failedHosts {
		failedDetails = append(failedDetails, fmt.Sprintf("%s (%v)", hostName, err))
	}
	warning := fmt.Sprintf("\n⚠ Failed to connect: %s", strings.Join(failedDetails, ", "))
	return lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Render(warning)
}

func (m Model) View() string {
	switch m.screen {
	case ScreenHostList:
		listView := m.list.View()
		listView += m.renderConnectionFailures()
		if len(m.selectedHosts) > 0 {
			selectedNames := make([]string, len(m.selectedHosts))
			for i, h := range m.selectedHosts {
				selectedNames[i] = h.Name
			}
			footer := fmt.Sprintf("\nSelected (%d): %s", len(m.selectedHosts), strings.Join(selectedNames, ", "))
			listView += lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Render(footer)
		}
		versionFooter := fmt.Sprintf("\nv%s", internal.ShortVersion())
		listView += lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render(versionFooter)
		listView += m.renderUpdateNotification()
		return listView

	case ScreenConnecting:
		return m.renderConnectingProgress()

	case ScreenDashboard:
		if len(m.selectedHosts) > 0 && m.currentHostIdx < len(m.selectedHosts) {
			currentHost := m.selectedHosts[m.currentHostIdx]

			if m.clients[currentHost.Name] == nil || m.sysInfos[currentHost.Name] == nil {
				return m.renderConnectingProgress()
			}

			sysInfo := m.sysInfos[currentHost.Name]
			lastUpdate := m.lastUpdates[currentHost.Name]

			hostIndicator := ""
			if len(m.selectedHosts) > 1 {
				hostIndicator = fmt.Sprintf(" [%d/%d]", m.currentHostIdx+1, len(m.selectedHosts))
			}
			dashboardView := renderDashboard(currentHost.Name+hostIndicator, sysInfo, m.updateInterval, lastUpdate, m.width, m.height, len(m.selectedHosts) > 1)
			return dashboardView + m.renderConnectionFailures() + m.renderUpdateNotification()
		}
		return m.renderConnectingProgress()

	case ScreenOverview:
		overviewView := m.renderOverview()
		return overviewView + m.renderConnectionFailures() + m.renderUpdateNotification()
	}

	return ""
}
