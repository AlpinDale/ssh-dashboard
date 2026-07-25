package ui

import (
	"time"

	"github.com/alpindale/ssh-dashboard/internal"
	tea "github.com/charmbracelet/bubbletea"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			for _, client := range m.clients {
				if client != nil {
					client.Close()
				}
			}
			return m, tea.Quit
		case " ":
			if m.screen == ScreenHostList {
				if item, ok := m.list.SelectedItem().(hostItem); ok {
					host := item.host
					found := false
					for i, h := range m.selectedHosts {
						if h.Name == host.Name {
							m.selectedHosts = append(m.selectedHosts[:i], m.selectedHosts[i+1:]...)
							found = true
							break
						}
					}
					if !found {
						m.selectedHosts = append(m.selectedHosts, host)
					}
					m.updateListSelection()
				}
			}
		case "enter":
			if m.screen == ScreenHostList {
				if len(m.selectedHosts) == 0 {
					if item, ok := m.list.SelectedItem().(hostItem); ok {
						m.selectedHosts = append(m.selectedHosts, item.host)
					}
				}
				if len(m.selectedHosts) > 0 {
					m.failedHosts = make(map[string]error)

					hasConnections := len(m.clients) > 0

					if hasConnections {
						m.screen = ScreenDashboard
						cmd := m.connectNewHosts()
						if cmd != nil {
							return m, cmd
						}
					} else {
						m.screen = ScreenConnecting
						return m, m.connectToHosts()
					}
				}
			}
		case "n":
			if m.screen == ScreenDashboard && len(m.selectedHosts) > 1 {
				m.currentHostIdx = (m.currentHostIdx + 1) % len(m.selectedHosts)
				nextHost := m.selectedHosts[m.currentHostIdx]
				if m.clients[nextHost.Name] == nil {
					return m, m.connectToHost(nextHost)
				}
			}
		case "c":
			if m.screen == ScreenDashboard || m.screen == ScreenOverview {
				m.screen = ScreenHostList
				m.updateListSelection()
			}
		case "t":
			if m.screen == ScreenDashboard && len(m.selectedHosts) > 1 {
				m.screen = ScreenOverview
			} else if m.screen == ScreenOverview {
				m.screen = ScreenDashboard
			}
		case "s":
			if m.screen == ScreenDashboard {
				if len(m.selectedHosts) > 0 {
					currentHost := m.selectedHosts[m.currentHostIdx]
					m.sshOnExit = currentHost.Name
					for _, client := range m.clients {
						if client != nil {
							client.Close()
						}
					}
					return m, tea.Quit
				}
			}
		case "m":
			if m.screen == ScreenDashboard || m.screen == ScreenOverview {
				m.compactGPU = !m.compactGPU
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.list.SetSize(msg.Width, msg.Height-2)

	case ConnectedMsg:
		if msg.err != nil {
			m.failedHosts[msg.hostName] = msg.err

			for i, h := range m.selectedHosts {
				if h.Name == msg.hostName {
					m.selectedHosts = append(m.selectedHosts[:i], m.selectedHosts[i+1:]...)
					break
				}
			}

			if len(m.selectedHosts) == 0 {
				m.screen = ScreenHostList
				m.updateListSelection()
			} else {
				if m.currentHostIdx >= len(m.selectedHosts) {
					m.currentHostIdx = len(m.selectedHosts) - 1
				}
			}
			return m, nil
		}
		m.clients[msg.hostName] = msg.client

		if m.screen == ScreenConnecting {
			return m, m.gatherSysInfoForHost(msg.hostName)
		}

		if m.screen == ScreenDashboard || m.screen == ScreenOverview {
			return m, m.gatherSysInfoForHost(msg.hostName)
		}

	case SystemInfoMsg:
		if msg.err != nil {
			return m, nil
		}
		m.sysInfos[msg.hostName] = msg.info
		m.lastUpdates[msg.hostName] = time.Now()

		if m.screen == ScreenConnecting && len(m.selectedHosts) > 0 {
			firstHost := m.selectedHosts[0]
			if m.clients[firstHost.Name] != nil && m.sysInfos[firstHost.Name] != nil {
				m.screen = ScreenDashboard
				return m, m.tick()
			}
		}

	case UpdateCheckMsg:
		m.updateInfo = internal.UpdateInfo(msg)

	case TickMsg:
		// update every 10 seconds
		return m, tea.Batch(m.gatherAllSysInfo(), m.tick())
	}

	var spinnerCmd tea.Cmd
	m.spinner, spinnerCmd = m.spinner.Update(msg)

	if m.screen == ScreenHostList {
		var listCmd tea.Cmd
		m.list, listCmd = m.list.Update(msg)
		return m, tea.Batch(spinnerCmd, listCmd)
	}

	return m, spinnerCmd
}
